/*
Copyright 2024. projectsveltos.io. All rights reserved.

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

package fv_test

import (
	"context"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	libsveltosv1beta1 "github.com/projectsveltos/libsveltos/api/v1beta1"
)

var _ = Describe("ReadinessCheck", func() {
	It("SveltosCluster readinessChecks", Label("FV"), func() {
		namespace := randomString()

		By("Create a SveltosCluster with a ReadinessChecks asking for namespace bar to exist")
		sveltosCluster := &libsveltosv1beta1.SveltosCluster{
			ObjectMeta: metav1.ObjectMeta{
				Name:      randomString(),
				Namespace: kindWorkloadCluster.Namespace,
			},
			Spec: libsveltosv1beta1.SveltosClusterSpec{
				// fv-test creates a SveltosCluster. Here we simply reuse the Secret with Kubeconfig
				KubeconfigName: "clusterapi-workload-sveltos-kubeconfig",
				ReadinessChecks: []libsveltosv1beta1.ClusterCheck{
					{
						Name: "failing-check",
						ResourceSelectors: []libsveltosv1beta1.ResourceSelector{
							{
								Kind:    "Namespace",
								Group:   "",
								Version: "v1",
								Name:    namespace,
							},
						},
						Condition: `function evaluate()
  hs = {}
  hs.pass = false
  if #resources == 1 then
    -- The namespace selected in ResourceSelector does not exist, so this test fails
    hs.pass = true
  end
  return hs
end`,
					},
				},
			},
		}

		Expect(k8sClient.Create(context.TODO(), sveltosCluster)).To(Succeed())

		By("Verify SveltosCluster is not ready")
		// Verify SveltosCluster status never moves to Ready
		Eventually(func() bool {
			currentSveltosCluster := &libsveltosv1beta1.SveltosCluster{}
			err := k8sClient.Get(context.TODO(),
				types.NamespacedName{Namespace: sveltosCluster.Namespace, Name: sveltosCluster.Name},
				currentSveltosCluster)
			if err != nil {
				return false
			}
			if currentSveltosCluster.Status.Ready {
				return false
			}
			return currentSveltosCluster.Status.FailureMessage != nil &&
				strings.Contains(*currentSveltosCluster.Status.FailureMessage, "cluster check failing-check failed")
		}, timeout, pollingInterval).Should(BeTrue())

		By("Create namespace in the managed cluster")
		ns := &corev1.Namespace{
			ObjectMeta: metav1.ObjectMeta{
				Name: namespace,
			},
		}

		remoteClient, err := getKindWorkloadClusterClient()
		Expect(err).To(BeNil())
		Expect(remoteClient.Create(context.TODO(), ns)).To(Succeed())

		By("Verify SveltosCluster is ready")
		// Verify SveltosCluster status moves to Ready
		Eventually(func() bool {
			currentSveltosCluster := &libsveltosv1beta1.SveltosCluster{}
			err := k8sClient.Get(context.TODO(),
				types.NamespacedName{Namespace: sveltosCluster.Namespace, Name: sveltosCluster.Name},
				currentSveltosCluster)
			if err != nil {
				return false
			}
			return currentSveltosCluster.Status.Ready
		}, timeout, pollingInterval).Should(BeTrue())

		By("Delete SveltosCluster")
		currentSveltosCluster := &libsveltosv1beta1.SveltosCluster{}
		Expect(k8sClient.Get(context.TODO(),
			types.NamespacedName{Namespace: sveltosCluster.Namespace, Name: sveltosCluster.Name},
			currentSveltosCluster)).To(Succeed())
		Expect(k8sClient.Delete(context.TODO(), currentSveltosCluster)).To(Succeed())
	})
})
