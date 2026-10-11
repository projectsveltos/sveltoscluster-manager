/*
Copyright 2026. projectsveltos.io. All rights reserved.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package controllers_test

import (
	"bytes"
	"context"
	"time"

	"github.com/go-logr/logr"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/events"
	"k8s.io/klog/v2/textlogger"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	libsveltosv1beta1 "github.com/projectsveltos/libsveltos/api/v1beta1"
	"github.com/projectsveltos/sveltoscluster-manager/controllers"
	"github.com/projectsveltos/sveltoscluster-manager/pkg/scope"
)

const eventBufferSize = 10

var _ = Describe("SveltosCluster events", func() {
	var sveltosCluster *libsveltosv1beta1.SveltosCluster
	var recorder *events.FakeRecorder
	var reconciler *controllers.SveltosClusterReconciler
	var logger logr.Logger

	BeforeEach(func() {
		sveltosCluster = getSveltosClusterInstance(randomString(), randomString())
		recorder = events.NewFakeRecorder(eventBufferSize)
		reconciler = getClusterProfileReconciler(testEnv.Client)
		reconciler.EventRecorder = recorder
		logger = textlogger.NewLogger(textlogger.NewConfig(textlogger.Verbosity(1)))
	})

	createNamespace := func() {
		ns := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: sveltosCluster.Namespace}}
		Expect(testEnv.Create(context.TODO(), ns)).To(Succeed())
		Expect(waitForObject(context.TODO(), testEnv.Client, ns)).To(Succeed())
	}

	Context("pull mode", func() {
		// newPullModeScope creates a pull mode SveltosCluster and returns a scope on an in-memory copy,
		// ready for reconcilePullModeCluster, with the given agent heartbeat and connection state.
		newPullModeScope := func(lastReport time.Time, connection libsveltosv1beta1.ConnectionStatus,
			failures int) *scope.SveltosClusterScope {

			createNamespace()
			sveltosCluster.Spec.PullMode = true
			Expect(testEnv.Create(context.TODO(), sveltosCluster)).To(Succeed())
			Expect(waitForObject(context.TODO(), testEnv.Client, sveltosCluster)).To(Succeed())

			current := &libsveltosv1beta1.SveltosCluster{}
			Expect(testEnv.Get(context.TODO(),
				types.NamespacedName{Namespace: sveltosCluster.Namespace, Name: sveltosCluster.Name}, current)).To(Succeed())
			Expect(current.Spec.ConsecutiveFailureThreshold).To(BeNumerically(">", 0))

			reportTime := metav1.Time{Time: lastReport}
			current.Status.AgentLastReportTime = &reportTime
			current.Status.Ready = true
			current.Status.ConnectionStatus = connection
			current.Status.ConnectionFailures = failures

			clusterScope, err := scope.NewSveltosClusterScope(scope.SveltosClusterScopeParams{
				Client:         testEnv.Client,
				SveltosCluster: current,
				ControllerName: randomString(),
				Logger:         logger,
			})
			Expect(err).To(BeNil())
			return clusterScope
		}

		It("records AgentUnhealthy once when the agent stops reporting, and ClusterRecovered once when it reports again", func() {
			// One more failure reaches the threshold
			clusterScope := newPullModeScope(time.Now().Add(-time.Hour), libsveltosv1beta1.ConnectionHealthy, 0)
			clusterScope.SveltosCluster.Status.ConnectionFailures = clusterScope.SveltosCluster.Spec.ConsecutiveFailureThreshold - 1

			controllers.ReconcilePullModeCluster(reconciler, context.TODO(), clusterScope, logger)
			Expect(clusterScope.SveltosCluster.Status.ConnectionStatus).To(Equal(libsveltosv1beta1.ConnectionDown))
			Expect(recorder.Events).To(Receive(Equal("Warning AgentUnhealthy cluster " + sveltosCluster.Namespace + "/" +
				sveltosCluster.Name + ": agent has not reported for longer than the maximum allowed time")))

			// Still down: nothing new is recorded
			controllers.ReconcilePullModeCluster(reconciler, context.TODO(), clusterScope, logger)
			controllers.ReconcilePullModeCluster(reconciler, context.TODO(), clusterScope, logger)
			Expect(recorder.Events).To(BeEmpty())

			// The agent reports again
			now := metav1.Now()
			clusterScope.SveltosCluster.Status.AgentLastReportTime = &now
			controllers.ReconcilePullModeCluster(reconciler, context.TODO(), clusterScope, logger)
			Expect(clusterScope.SveltosCluster.Status.ConnectionStatus).To(Equal(libsveltosv1beta1.ConnectionHealthy))
			Expect(recorder.Events).To(Receive(Equal("Normal ClusterRecovered cluster " + sveltosCluster.Namespace + "/" +
				sveltosCluster.Name + ": agent is reporting again")))

			// Still healthy: nothing new is recorded
			controllers.ReconcilePullModeCluster(reconciler, context.TODO(), clusterScope, logger)
			Expect(recorder.Events).To(BeEmpty())
		})

		It("does not record an event before the failure threshold is reached", func() {
			clusterScope := newPullModeScope(time.Now().Add(-time.Hour), libsveltosv1beta1.ConnectionHealthy, 0)

			controllers.ReconcilePullModeCluster(reconciler, context.TODO(), clusterScope, logger)
			Expect(clusterScope.SveltosCluster.Status.ConnectionStatus).To(Equal(libsveltosv1beta1.ConnectionHealthy))
			Expect(recorder.Events).To(BeEmpty())
		})

		It("does not record an event for a healthy agent, including a cluster with no connection status yet", func() {
			clusterScope := newPullModeScope(time.Now(), "", 0)

			controllers.ReconcilePullModeCluster(reconciler, context.TODO(), clusterScope, logger)
			Expect(clusterScope.SveltosCluster.Status.ConnectionStatus).To(Equal(libsveltosv1beta1.ConnectionHealthy))
			Expect(recorder.Events).To(BeEmpty())
		})
	})

	Context("push mode", func() {
		It("records ClusterUnreachable once when the connection goes down, and ClusterRecovered once when it is back", func() {
			createNamespace()

			sveltosSecret := corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: sveltosCluster.Namespace,
					Name:      sveltosCluster.Name + "-sveltos-kubeconfig",
				},
				Data: map[string][]byte{secretDataKey: []byte("not a valid kubeconfig")},
			}
			Expect(testEnv.Create(context.TODO(), &sveltosSecret)).To(Succeed())
			Expect(waitForObject(context.TODO(), testEnv.Client, &sveltosSecret)).To(Succeed())

			Expect(testEnv.Create(context.TODO(), sveltosCluster)).To(Succeed())
			Expect(waitForObject(context.TODO(), testEnv.Client, sveltosCluster)).To(Succeed())

			clusterName := client.ObjectKey{Namespace: sveltosCluster.Namespace, Name: sveltosCluster.Name}
			reconcileAndWaitForFailures := func(failures int) {
				_, err := reconciler.Reconcile(context.TODO(), ctrl.Request{NamespacedName: clusterName})
				Expect(err).ToNot(HaveOccurred())

				Eventually(func() bool {
					current := &libsveltosv1beta1.SveltosCluster{}
					return testEnv.Get(context.TODO(), clusterName, current) == nil &&
						current.Status.ConnectionFailures == failures
				}, timeout, pollingInterval).Should(BeTrue())
			}

			// Below the threshold: no event
			reconcileAndWaitForFailures(1)
			reconcileAndWaitForFailures(2)
			Expect(recorder.Events).To(BeEmpty())

			// Threshold reached: the connection is down, one event
			reconcileAndWaitForFailures(3)
			var recorded string
			Expect(recorder.Events).To(Receive(&recorded))
			Expect(recorded).To(HavePrefix("Warning ClusterUnreachable cluster " + sveltosCluster.Namespace + "/" + sveltosCluster.Name + ": "))

			// Still down: no new event
			reconcileAndWaitForFailures(4)
			reconcileAndWaitForFailures(5)
			Expect(recorder.Events).To(BeEmpty())

			// The cluster is reachable again
			current := &corev1.Secret{}
			Expect(testEnv.Get(context.TODO(), client.ObjectKeyFromObject(&sveltosSecret), current)).To(Succeed())
			current.Data[secretDataKey] = testEnv.Kubeconfig
			Expect(testEnv.Update(context.TODO(), current)).To(Succeed())
			Eventually(func() bool {
				cached := &corev1.Secret{}
				return testEnv.Get(context.TODO(), client.ObjectKeyFromObject(&sveltosSecret), cached) == nil &&
					bytes.Equal(cached.Data[secretDataKey], testEnv.Kubeconfig)
			}, timeout, pollingInterval).Should(BeTrue())

			_, err := reconciler.Reconcile(context.TODO(), ctrl.Request{NamespacedName: clusterName})
			Expect(err).ToNot(HaveOccurred())
			Expect(recorder.Events).To(Receive(Equal("Normal ClusterRecovered cluster " + sveltosCluster.Namespace + "/" +
				sveltosCluster.Name + ": connection is healthy again")))

			Eventually(func() bool {
				cluster := &libsveltosv1beta1.SveltosCluster{}
				return testEnv.Get(context.TODO(), clusterName, cluster) == nil &&
					cluster.Status.ConnectionStatus == libsveltosv1beta1.ConnectionHealthy
			}, timeout, pollingInterval).Should(BeTrue())

			// Still healthy: no new event
			_, err = reconciler.Reconcile(context.TODO(), ctrl.Request{NamespacedName: clusterName})
			Expect(err).ToNot(HaveOccurred())
			Expect(recorder.Events).To(BeEmpty())
		})
	})
})
