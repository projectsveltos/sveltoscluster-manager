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

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	libsveltosv1alpha1 "github.com/projectsveltos/libsveltos/api/v1alpha1"
)

var _ = Describe("SveltosCluster: Reconciler", func() {
	var sveltosCluster *libsveltosv1alpha1.SveltosCluster

	BeforeEach(func() {
		sveltosCluster = getSveltosClusterInstance(randomString(), randomString())
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
			currentSveltosCluster := &libsveltosv1alpha1.SveltosCluster{}
			err := testEnv.Get(context.TODO(), sveltosClusterName, currentSveltosCluster)
			return err == nil &&
				currentSveltosCluster.Status.Ready
		}, timeout, pollingInterval).Should(BeTrue())
	})
})

func getSveltosClusterInstance(namespace, name string) *libsveltosv1alpha1.SveltosCluster {
	return &libsveltosv1alpha1.SveltosCluster{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
	}
}
