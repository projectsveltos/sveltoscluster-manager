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
	"context"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/klog/v2/textlogger"
	"sigs.k8s.io/controller-runtime/pkg/client"

	libsveltosv1beta1 "github.com/projectsveltos/libsveltos/api/v1beta1"
	"github.com/projectsveltos/sveltoscluster-manager/controllers"
)

var _ = Describe("cluster checks", func() {
	It("getResourcesMatchinResourceSelector excludes a deleting resource unless ResourceSelector.IncludeDeletingResources is set",
		func() {
			namespace := randomString()
			ns := &corev1.Namespace{
				ObjectMeta: metav1.ObjectMeta{
					Name: namespace,
				},
			}
			Expect(testEnv.Create(ctx, ns)).To(Succeed())
			Expect(waitForObject(ctx, testEnv.Client, ns)).To(Succeed())

			serviceAccount := &corev1.ServiceAccount{
				ObjectMeta: metav1.ObjectMeta{
					Namespace:  namespace,
					Name:       randomString(),
					Finalizers: []string{"projectsveltos.io/test-finalizer"},
				},
			}
			Expect(testEnv.Create(ctx, serviceAccount)).To(Succeed())
			Expect(waitForObject(ctx, testEnv.Client, serviceAccount)).To(Succeed())

			// The finalizer keeps the ServiceAccount around with a deletionTimestamp set,
			// instead of removing it, mimicking a resource that is still being deleted.
			Expect(testEnv.Delete(ctx, serviceAccount)).To(Succeed())

			logger := textlogger.NewLogger(textlogger.NewConfig(textlogger.Verbosity(1)))

			rs := &libsveltosv1beta1.ResourceSelector{
				Group:     "",
				Version:   "v1",
				Kind:      "ServiceAccount",
				Namespace: namespace,
				Name:      serviceAccount.Name,
			}

			Eventually(func() bool {
				resources, err := controllers.GetResourcesMatchinResourceSelector(context.TODO(), testEnv.Config, rs, logger)
				if err != nil {
					return false
				}
				return len(resources) == 0
			}, timeout, pollingInterval).Should(BeTrue())

			rs.IncludeDeletingResources = true

			Eventually(func() bool {
				resources, err := controllers.GetResourcesMatchinResourceSelector(context.TODO(), testEnv.Config, rs, logger)
				if err != nil {
					return false
				}
				return len(resources) == 1 && resources[0].GetName() == serviceAccount.Name
			}, timeout, pollingInterval).Should(BeTrue())

			// Clean up: remove the finalizer so envtest can actually delete the object.
			Expect(testEnv.Get(ctx, client.ObjectKeyFromObject(serviceAccount), serviceAccount)).To(Succeed())
			serviceAccount.Finalizers = nil
			Expect(testEnv.Update(ctx, serviceAccount)).To(Succeed())
		})
})
