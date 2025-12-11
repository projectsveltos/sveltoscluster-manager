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
  licenseData: eyJpZCI6IjU0ZDFjNzkxLTY2OTAtNGM2NS1hNzZmLTFhOWQ1NjhlYmQ0NCIsImN1c3RvbWVyTmFtZSI6IkFjbWUgSW5jIiwiZmVhdHVyZXMiOlsiUHVsbE1vZGUiXSwiZXhwaXJhdGlvbkRhdGUiOiIyMDI2LTA3LTI2VDE0OjMxOjUzLjI4NzE3MloiLCJncmFjZVBlcmlvZERheXMiOjcsIm1heENsdXN0ZXJzIjoxLCJpc3N1ZWRBdCI6IjIwMjUtMDctMjZUMTQ6MzE6NTMuMjg3MTcyWiJ9
  licenseSignature: 181gp8Vy4dr3FfwBfLfgp6fijyONv5e+klBctviAyEJyCM6xQ+ny7/n/cVU5b0gywBYPQv4Bx1HBHOKsxTX9xJH11YSixKLwuXxs8DAQk+CF6Eg2CypV9TgzXL8UphKY7yEfL+y3t4KWlXagTw9kibtkHN0dBUolb2rECSp44XJhIn6d41o5drnkwkYiLCjwsxbJrpopsmxMd3ohZRiNgQOKgTezhdbUVnHyV70PziKDGEWIF+r9IXTq/bPArFU1H2qDTsSlJdbkTzMHQ8+RqpYmeYzATDTykwgt4MGmdj4mZ48y1kc2zHIQvfwwgzqWzw7VeW62+aVF+oJggt5awdiMeVlnrFYhtoL/lpvDyqnPGNaE9/O4BujSy4LdOAzasf2fWnSoi0IDVbIwFN359fGaEZvgYJkVwrc4yBW0V6b8d7Ct9gaxD4JaFZ+pxt7k+UIgs5hs4W9FmPF0444Ngk6363W7IevD1pvr7vxDfwhseQN8Pk/bkIWJRAxfgI+3Z3MjkmQ0nm+uPBxYWGQSSMNWeqV5diMn6LY4AnVINVW7v//sxQKvdGLSLic8VAh5G75fVZMWmHc44sClzJkvJ39O1Hd+/r0lKh12c7mWi8j/4RpVKz+8ijjZwTBUqtKJIoXMrPxYvmySk30LMFbKOtFc1ICa7Lta9pWX6AsPC34=`
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
		Expect(actualUTCStr).To(Equal("2026-07-26 14:31:53 +0000 UTC"))

		Expect(k8sClient.Get(context.TODO(),
			types.NamespacedName{Name: sveltosLicense.Name},
			currentSveltosLicense)).To(Succeed())
		Expect(k8sClient.Delete(context.TODO(), sveltosLicense)).To(Succeed())
	})
})
