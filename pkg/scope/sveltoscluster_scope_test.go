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

package scope_test

import (
	"context"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/klog/v2/klogr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	libsveltosv1alpha1 "github.com/projectsveltos/libsveltos/api/v1alpha1"
	"github.com/projectsveltos/sveltoscluster-manager/pkg/scope"
)

const sveltosClusterNamePrefix = "scope-"

var _ = Describe("SveltosClusterScope", func() {
	var sveltosCluster *libsveltosv1alpha1.SveltosCluster
	var c client.Client

	BeforeEach(func() {
		sveltosCluster = &libsveltosv1alpha1.SveltosCluster{
			ObjectMeta: metav1.ObjectMeta{
				Name:      sveltosClusterNamePrefix + randomString(),
				Namespace: sveltosClusterNamePrefix + randomString(),
			},
		}
		scheme := setupScheme()
		initObjects := []client.Object{sveltosCluster}
		c = fake.NewClientBuilder().WithScheme(scheme).WithObjects(initObjects...).Build()
	})

	It("Return nil,error if SveltosCluster is not specified", func() {
		params := scope.SveltosClusterScopeParams{
			Client: c,
			Logger: klogr.New(),
		}

		scope, err := scope.NewSveltosClusterScope(params)
		Expect(err).To(HaveOccurred())
		Expect(scope).To(BeNil())
	})

	It("Return nil,error if client is not specified", func() {
		params := scope.SveltosClusterScopeParams{
			SveltosCluster: sveltosCluster,
			Logger:         klogr.New(),
		}

		scope, err := scope.NewSveltosClusterScope(params)
		Expect(err).To(HaveOccurred())
		Expect(scope).To(BeNil())
	})

	It("Close updates SveltosCluster", func() {
		params := scope.SveltosClusterScopeParams{
			Client:         c,
			SveltosCluster: sveltosCluster,
			Logger:         klogr.New(),
		}

		scope, err := scope.NewSveltosClusterScope(params)
		Expect(err).ToNot(HaveOccurred())
		Expect(scope).ToNot(BeNil())

		sveltosCluster.Status.Ready = true

		currentSveltosCluster := &libsveltosv1alpha1.SveltosCluster{}
		Expect(c.Get(context.TODO(), types.NamespacedName{Name: sveltosCluster.Name, Namespace: sveltosCluster.Namespace},
			currentSveltosCluster)).To(Succeed())
		Expect(currentSveltosCluster.Status.Ready).ToNot(BeTrue())
	})
})
