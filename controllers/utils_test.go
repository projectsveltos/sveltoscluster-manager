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
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	corev1 "k8s.io/api/core/v1"

	libsveltosv1beta1 "github.com/projectsveltos/libsveltos/api/v1beta1"
	"github.com/projectsveltos/sveltoscluster-manager/controllers"
)

var _ = Describe("InitScheme", func() {
	It("registers core Kubernetes types", func() {
		s, err := controllers.InitScheme()
		Expect(err).To(BeNil())

		gvks, _, err := s.ObjectKinds(&corev1.Secret{})
		Expect(err).To(BeNil())
		Expect(gvks).ToNot(BeEmpty())
	})

	It("registers libsveltos types", func() {
		s, err := controllers.InitScheme()
		Expect(err).To(BeNil())

		gvks, _, err := s.ObjectKinds(&libsveltosv1beta1.SveltosCluster{})
		Expect(err).To(BeNil())
		Expect(gvks).ToNot(BeEmpty())
	})
})
