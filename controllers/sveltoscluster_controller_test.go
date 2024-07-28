/*
Copyright 2022. projectsveltos.io. All rights reserved.

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
	"context"
	"time"

	"github.com/go-logr/logr"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/klog/v2/textlogger"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	libsveltosv1beta1 "github.com/projectsveltos/libsveltos/api/v1beta1"
	"github.com/projectsveltos/sveltoscluster-manager/controllers"
	"github.com/projectsveltos/sveltoscluster-manager/pkg/scope"
)

var _ = Describe("SveltosCluster: Reconciler", func() {
	var sveltosCluster *libsveltosv1beta1.SveltosCluster
	var logger logr.Logger

	BeforeEach(func() {
		sveltosCluster = getSveltosClusterInstance(randomString(), randomString())
		logger = textlogger.NewLogger(textlogger.NewConfig(textlogger.Verbosity(1)))
	})

	It("reconcile set status to ready", func() {
		ns := &corev1.Namespace{
			ObjectMeta: metav1.ObjectMeta{
				Name: sveltosCluster.Namespace,
			},
		}

		Expect(testEnv.Create(context.TODO(), ns)).To(Succeed())
		Expect(waitForObject(context.TODO(), testEnv.Client, ns)).To(Succeed())

		// Create Secret containing Kubeconfig to access SveltosCluster

		sveltosSecret := corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Namespace: sveltosCluster.Namespace,
				Name:      sveltosCluster.Name + "-sveltos-kubeconfig",
			},
			Data: map[string][]byte{
				"data": testEnv.Kubeconfig,
			},
		}

		Expect(testEnv.Create(context.TODO(), &sveltosSecret)).To(Succeed())
		Expect(waitForObject(context.TODO(), testEnv.Client, &sveltosSecret)).To(Succeed())

		Expect(testEnv.Create(context.TODO(), sveltosCluster)).To(Succeed())
		Expect(waitForObject(context.TODO(), testEnv.Client, sveltosCluster)).To(Succeed())

		reconciler := getClusterProfileReconciler(testEnv.Client)

		sveltosClusterName := client.ObjectKey{
			Name:      sveltosCluster.Name,
			Namespace: sveltosCluster.Namespace,
		}
		_, err := reconciler.Reconcile(context.TODO(), ctrl.Request{
			NamespacedName: sveltosClusterName,
		})
		Expect(err).ToNot(HaveOccurred())

		Eventually(func() bool {
			currentSveltosCluster := &libsveltosv1beta1.SveltosCluster{}
			err := testEnv.Get(context.TODO(), sveltosClusterName, currentSveltosCluster)
			return err == nil &&
				currentSveltosCluster.Status.Ready
		}, timeout, pollingInterval).Should(BeTrue())
	})

	It("shouldRenewTokenRequest returns true when enough time has passed since last TokenRequest renewal", func() {
		sveltosCluster.Spec.TokenRequestRenewalOption = &libsveltosv1beta1.TokenRequestRenewalOption{
			RenewTokenRequestInterval: metav1.Duration{Duration: time.Minute},
		}

		initObjects := []client.Object{
			sveltosCluster,
		}

		c := fake.NewClientBuilder().WithScheme(scheme).WithStatusSubresource(initObjects...).WithObjects(initObjects...).Build()

		reconciler := getClusterProfileReconciler(c)

		sveltosClusterName := client.ObjectKey{
			Name:      sveltosCluster.Name,
			Namespace: sveltosCluster.Namespace,
		}

		now := time.Now()
		now = now.Add(-time.Hour)

		currentSveltosCluster := &libsveltosv1beta1.SveltosCluster{}
		Expect(c.Get(context.TODO(), sveltosClusterName, currentSveltosCluster)).To(Succeed())
		currentSveltosCluster.Status.LastReconciledTokenRequestAt = now.Format(time.RFC3339)
		Expect(c.Status().Update(context.TODO(), currentSveltosCluster)).To(Succeed())

		sveltosClusterScope, err := scope.NewSveltosClusterScope(scope.SveltosClusterScopeParams{
			Client:         testEnv.Client,
			SveltosCluster: currentSveltosCluster,
			ControllerName: randomString(),
			Logger:         logger,
		})
		Expect(err).To(BeNil())

		// last renewal time was set by test to an hour ago. Because RenewTokenRequestInterval is set to a minute
		// expect a renewal is needed
		Expect(controllers.ShouldRenewTokenRequest(reconciler, sveltosClusterScope, logger)).To(BeTrue())

		now = time.Now()
		Expect(c.Get(context.TODO(), sveltosClusterName, currentSveltosCluster)).To(Succeed())
		currentSveltosCluster.Status.LastReconciledTokenRequestAt = now.Format(time.RFC3339)
		Expect(c.Status().Update(context.TODO(), currentSveltosCluster)).To(Succeed())

		sveltosClusterScope.SveltosCluster = currentSveltosCluster

		// last renewal time was set by test to just now. Because RenewTokenRequestInterval is set to a minute
		// expect a renewal is not needed
		Expect(controllers.ShouldRenewTokenRequest(reconciler, sveltosClusterScope, logger)).To(BeFalse())
	})

	It("handleAutomaticPauseUnPause updates Spec.Paused based on Spec.Schedule", func() {
		sveltosCluster.Spec.ActiveWindow = &libsveltosv1beta1.ActiveWindow{
			From: "0 20 * * 5", // every friday 8PM
			To:   "0 7 * * 1",  // every monday 7AM
		}
		loc, err := time.LoadLocation("Europe/Rome") // Replace with your desired time zone
		Expect(err).To(BeNil())
		tuesday8AM := time.Date(2024, time.July, 30, 8, 0, 0, 0, loc)
		sveltosCluster.CreationTimestamp = metav1.Time{Time: tuesday8AM}

		controllers.HandleAutomaticPauseUnPause(sveltosCluster, tuesday8AM.Add(time.Hour), logger)

		// Next unpause coming friday at 8PM
		Expect(sveltosCluster.Status.NextUnpause).ToNot(BeNil())
		expectedUnpause := time.Date(2024, time.August, 2, 20, 0, 0, 0, loc)
		Expect(sveltosCluster.Status.NextUnpause.Time).To(Equal(expectedUnpause))

		// Next pause coming monday at 7AM
		Expect(sveltosCluster.Status.NextPause).ToNot(BeNil())
		expectedPause := time.Date(2024, time.August, 5, 7, 0, 0, 0, loc)
		Expect(sveltosCluster.Status.NextPause.Time).To(Equal(expectedPause))

		Expect(sveltosCluster.Spec.Paused).To(BeTrue())

		// when time is before unpause, Paused will remain set to false and NextPause
		// NextUnpause will not be updated
		thursday8AM := time.Date(2024, time.August, 1, 8, 0, 0, 0, loc)
		controllers.HandleAutomaticPauseUnPause(sveltosCluster, thursday8AM, logger)
		Expect(sveltosCluster.Spec.Paused).To(BeTrue())
		Expect(sveltosCluster.Status.NextPause.Time).To(Equal(expectedPause))
		Expect(sveltosCluster.Status.NextUnpause.Time).To(Equal(expectedUnpause))

		// when time is past unpause but before pause, Paused will be set to false and NextPause
		// NextUnpause will not be updated
		saturday8AM := time.Date(2024, time.August, 3, 8, 0, 0, 0, loc)
		controllers.HandleAutomaticPauseUnPause(sveltosCluster, saturday8AM, logger)
		Expect(sveltosCluster.Spec.Paused).To(BeFalse())
		Expect(sveltosCluster.Status.NextPause.Time).To(Equal(expectedPause))
		Expect(sveltosCluster.Status.NextUnpause.Time).To(Equal(expectedUnpause))

		// when time is past next pause, Paused will be set to true and NextPause
		// NextUnpause updated
		monday8AM := time.Date(2024, time.August, 5, 8, 0, 0, 0, loc)
		controllers.HandleAutomaticPauseUnPause(sveltosCluster, monday8AM, logger)
		Expect(sveltosCluster.Spec.Paused).To(BeTrue())
		expectedUnpause = time.Date(2024, time.August, 9, 20, 0, 0, 0, loc)
		expectedPause = time.Date(2024, time.August, 12, 7, 0, 0, 0, loc)
		Expect(sveltosCluster.Status.NextPause.Time).To(Equal(expectedPause))
		Expect(sveltosCluster.Status.NextUnpause.Time).To(Equal(expectedUnpause))
	})
})

func getSveltosClusterInstance(namespace, name string) *libsveltosv1beta1.SveltosCluster {
	return &libsveltosv1beta1.SveltosCluster{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
	}
}
