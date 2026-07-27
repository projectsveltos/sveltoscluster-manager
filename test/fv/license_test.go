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

//nolint:lll // This file has long lines due to signed licenses
package fv_test

import (
	"context"
	"fmt"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	libsveltosv1beta1 "github.com/projectsveltos/libsveltos/api/v1beta1"
	"github.com/projectsveltos/libsveltos/lib/k8s_utils"
)

var (
	// This is a valid license (maxClusters = 1)
	license = `apiVersion: v1
kind: Secret
metadata:
  name: sveltos-license
  namespace: projectsveltos
type: Opaque
data:
  licenseData: eyJpZCI6IjdmNDU0Mjk4LWUyYjItNDFhOC05MWRjLTc4ZjU5MjE4Nzk5MiIsImN1c3RvbWVyTmFtZSI6IkFjbWUgSW5jIiwiZmVhdHVyZXMiOlsiUHVsbE1vZGUiXSwicGxhbiI6IkVudGVycHJpc2UiLCJleHBpcmF0aW9uRGF0ZSI6IjIwMjctMDctMjZUMTM6NDc6MzEuODUyMjE2WiIsImdyYWNlUGVyaW9kRGF5cyI6NywibWF4Q2x1c3RlcnMiOjEsImlzc3VlZEF0IjoiMjAyNi0wNy0yNlQxMzo0NzozMS44NTIyMTZaIn0=
  licenseSignature: BjYc6SDAcfrj4rBc4Fi0LfcdkU5qtAEojunwFV/0Llpsgx+IYi+XDyiq6VhZFDGl24tMcKOSHyPoTA3/5s5IIeZVpDRSd2+BXXFd9ccBScfyKRzDlO8Cg5rs0ejNwzrJKTNPxjorB7WxB3WK7ad6hbrmU6/PI6vdER7XjPsb3BuaDpzA5Z3wiuoyzB+BFJ9fbeVSDL2XxaZ+M/ifaM8/bGKsj7dUXwrP+ArNouObikxBsJsqZ9n1mgQx6WZm8fJOxct79Qmva1ys4O8QcP4MYY6rsbfag0xshpKKaZKMO10XugqtDYWz14pDV9vMKWEM0jMa4oHZmebMd4Wq+F/tB+GVIyYU8aWroYVKU5kkWFdFOcQozdNmyyLOn1umdJd2JouEWkcDHIRKfA1TpIfwaeKHB6JjW5WpKzbFfnrZUW9LWVOSegmv/HpgvXMiAXUyvVlhPHArPjBJyKhb5FNcb2kaYAk8ouFv/ydhFFcerTJMWp7bqkeLWlgMfUtw/oC0GKBLM090ovCkYAeaxjgWqm9sHVJCTFGeeqh2hff98UfAoQ5g9R9mP8e1rXNtWtdWK2UIaxcwymTKS7RNe2K3TpVORE4ESuOpSJMjUut7w2oAo1O7C2ZAlfoTi4JQmqBU9S+XG4AoEBkFPPbIlA8ak0xZU9Hh3Ony7WGr59tmkOU=`
)

var _ = Describe("Sveltos License", func() {
	It("Verifies SveltosLicense and Update Status", Label("FV"), func() {
		secret, err := k8s_utils.GetUnstructured([]byte(license))
		Expect(err).To(BeNil())

		By(fmt.Sprintf("Create Secret %s/%s with SveltosLicense", secret.GetNamespace(), secret.GetName()))
		Expect(k8sClient.Create(context.TODO(), secret)).To(Succeed())

		sveltosLicense := &libsveltosv1beta1.SveltosLicense{
			ObjectMeta: metav1.ObjectMeta{
				Name: "default",
			},
		}
		By("Create SveltosLicense")
		Expect(k8sClient.Create(context.TODO(), sveltosLicense)).To(Succeed())

		By("Verify SveltosLicense Status is set as valid")
		// Verify SveltosLicense status moves to valid
		Eventually(func() bool {
			currentSveltosLicense := &libsveltosv1beta1.SveltosLicense{}
			err := k8sClient.Get(context.TODO(),
				types.NamespacedName{Name: sveltosLicense.Name},
				currentSveltosLicense)
			if err != nil {
				return false
			}
			return currentSveltosLicense.Status.Status == libsveltosv1beta1.LicenseStatusValid
		}, timeout, pollingInterval).Should(BeTrue())

		currentSveltosLicense := &libsveltosv1beta1.SveltosLicense{}
		Expect(k8sClient.Get(context.TODO(),
			types.NamespacedName{Name: sveltosLicense.Name},
			currentSveltosLicense)).To(Succeed())

		Expect(currentSveltosLicense.Status.Message).To(Equal(""))
		Expect(currentSveltosLicense.Status.MaxClusters).ToNot(BeNil())
		Expect(*currentSveltosLicense.Status.MaxClusters).To(Equal(1))
		actualUTCStr := currentSveltosLicense.Status.ExpirationDate.Time.In(time.UTC).String()
		Expect(actualUTCStr).To(Equal("2027-07-26 13:47:31 +0000 UTC"))

		Expect(k8sClient.Get(context.TODO(),
			types.NamespacedName{Name: sveltosLicense.Name},
			currentSveltosLicense)).To(Succeed())
		Expect(k8sClient.Delete(context.TODO(), sveltosLicense)).To(Succeed())
	})
})
