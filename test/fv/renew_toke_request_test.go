/*
Copyright 2023. projectsveltos.io. All rights reserved.

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
	"reflect"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"

	libsveltosv1beta1 "github.com/projectsveltos/libsveltos/api/v1beta1"
)

var _ = Describe("Renew TokenRequest", func() {
	It("SveltosCluster controller renews TokenRequest", Label("FV"), func() {
		// BeforeSuite has registered management cluster as a SveltosCluster
		// generating a Kubeconfig based on a TokenRequest
		// SveltosCluster is configured to renew the TokenRequest (and so update
		// secret with Kubeconfig) every minute

		By("Verify SveltosCluster")
		currentSveltosCluster := &libsveltosv1beta1.SveltosCluster{}
		Expect(k8sClient.Get(context.TODO(),
			types.NamespacedName{Namespace: kindWorkloadCluster.Namespace, Name: kindWorkloadCluster.Name},
			currentSveltosCluster)).To(Succeed())

		// Verify SveltosCluster is indeed configured to renew TokenRequest every minute
		Expect(currentSveltosCluster.Spec.TokenRequestRenewalOption).ToNot(BeNil())
		interval := currentSveltosCluster.Spec.TokenRequestRenewalOption.RenewTokenRequestInterval
		Expect(interval.Duration).To(Equal(time.Minute))

		// Verify SveltosCluster status is set to Ready
		Eventually(func() bool {
			err := k8sClient.Get(context.TODO(),
				types.NamespacedName{Namespace: kindWorkloadCluster.Namespace, Name: kindWorkloadCluster.Name},
				currentSveltosCluster)
			if err != nil {
				return false
			}
			return currentSveltosCluster.Status.Ready &&
				currentSveltosCluster.Status.ConnectionStatus == libsveltosv1beta1.ConnectionHealthy
		}, timeout, pollingInterval).Should(BeTrue())

		// Get Secret with SveltosCluster kubeconfig
		By("Verify Secret with SveltosCluster kubeconfig")
		currentSecret := &corev1.Secret{}
		Expect(k8sClient.Get(context.TODO(),
			types.NamespacedName{
				Namespace: kindWorkloadCluster.Namespace,
				Name:      kindWorkloadCluster.Name + secretPostfix,
			},
			currentSecret,
		)).To(Succeed())

		By("Get current kubeconfig")
		var currentKubeconfig []byte
		Expect(currentSecret.Data).ToNot(BeNil())
		for k := range currentSecret.Data {
			currentKubeconfig = currentSecret.Data[k]
			break
		}
		Expect(currentKubeconfig).ToNot(BeNil())

		By("Verify TokenRequest is renewed by verifying Secret content changes")
		Eventually(func() bool {
			err := k8sClient.Get(context.TODO(),
				types.NamespacedName{
					Namespace: kindWorkloadCluster.Namespace,
					Name:      kindWorkloadCluster.Name + secretPostfix,
				},
				currentSecret,
			)
			if err != nil {
				return false
			}
			if currentSecret.Data == nil {
				return false
			}
			for k := range currentSecret.Data {
				if currentSecret.Data[k] == nil {
					continue
				}
				if !reflect.DeepEqual(currentSecret.Data[k], currentKubeconfig) {
					return true
				}
			}

			return false
		}, timeout, pollingInterval).Should(BeTrue())
	})
})
