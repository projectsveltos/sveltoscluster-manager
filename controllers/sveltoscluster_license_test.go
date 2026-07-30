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

//nolint:lll // this file has long lines due to signed licenses
package controllers_test

import (
	"context"
	"encoding/base64"
	"reflect"

	"github.com/go-logr/logr"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/klog/v2/textlogger"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	libsveltosv1beta1 "github.com/projectsveltos/libsveltos/api/v1beta1"
	license "github.com/projectsveltos/libsveltos/lib/licenses"
	"github.com/projectsveltos/sveltoscluster-manager/controllers"
)

// validLicenseDataB64 / validLicenseSignatureB64 are the licenseData/licenseSignature values
// of a valid, non-expired license (Enterprise plan, PullMode feature, MaxClusters 1).
const (
	validLicenseDataB64      = "eyJpZCI6IjdmNDU0Mjk4LWUyYjItNDFhOC05MWRjLTc4ZjU5MjE4Nzk5MiIsImN1c3RvbWVyTmFtZSI6IkFjbWUgSW5jIiwiZmVhdHVyZXMiOlsiUHVsbE1vZGUiXSwicGxhbiI6IkVudGVycHJpc2UiLCJleHBpcmF0aW9uRGF0ZSI6IjIwMjctMDctMjZUMTM6NDc6MzEuODUyMjE2WiIsImdyYWNlUGVyaW9kRGF5cyI6NywibWF4Q2x1c3RlcnMiOjEsImlzc3VlZEF0IjoiMjAyNi0wNy0yNlQxMzo0NzozMS44NTIyMTZaIn0="
	validLicenseSignatureB64 = "BjYc6SDAcfrj4rBc4Fi0LfcdkU5qtAEojunwFV/0Llpsgx+IYi+XDyiq6VhZFDGl24tMcKOSHyPoTA3/5s5IIeZVpDRSd2+BXXFd9ccBScfyKRzDlO8Cg5rs0ejNwzrJKTNPxjorB7WxB3WK7ad6hbrmU6/PI6vdER7XjPsb3BuaDpzA5Z3wiuoyzB+BFJ9fbeVSDL2XxaZ+M/ifaM8/bGKsj7dUXwrP+ArNouObikxBsJsqZ9n1mgQx6WZm8fJOxct79Qmva1ys4O8QcP4MYY6rsbfag0xshpKKaZKMO10XugqtDYWz14pDV9vMKWEM0jMa4oHZmebMd4Wq+F/tB+GVIyYU8aWroYVKU5kkWFdFOcQozdNmyyLOn1umdJd2JouEWkcDHIRKfA1TpIfwaeKHB6JjW5WpKzbFfnrZUW9LWVOSegmv/HpgvXMiAXUyvVlhPHArPjBJyKhb5FNcb2kaYAk8ouFv/ydhFFcerTJMWp7bqkeLWlgMfUtw/oC0GKBLM090ovCkYAeaxjgWqm9sHVJCTFGeeqh2hff98UfAoQ5g9R9mP8e1rXNtWtdWK2UIaxcwymTKS7RNe2K3TpVORE4ESuOpSJMjUut7w2oAo1O7C2ZAlfoTi4JQmqBU9S+XG4AoEBkFPPbIlA8ak0xZU9Hh3Ony7WGr59tmkOU="

	staleAnnotationValue = "stale"
)

func newLicenseSecret(namespace string, payloadData, signatureData []byte) *corev1.Secret {
	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: namespace,
			Name:      "sveltos-license",
		},
		Data: map[string][]byte{
			"licenseData":      payloadData,
			"licenseSignature": signatureData,
		},
	}
}

var _ = Describe("SveltosCluster: updateLicenseAnnotations", func() {
	var licenseNamespace string
	var sveltosCluster *libsveltosv1beta1.SveltosCluster
	var logger logr.Logger
	var payloadData, signatureData []byte

	BeforeEach(func() {
		var err error
		payloadData, err = base64.StdEncoding.DecodeString(validLicenseDataB64)
		Expect(err).To(BeNil())
		signatureData, err = base64.StdEncoding.DecodeString(validLicenseSignatureB64)
		Expect(err).To(BeNil())

		licenseNamespace = randomString()
		sveltosCluster = getSveltosClusterInstance(randomString(), randomString())
		logger = textlogger.NewLogger(textlogger.NewConfig(textlogger.Verbosity(1)))
	})

	It("sets payload/signature annotations when a valid license is found", func() {
		secret := newLicenseSecret(licenseNamespace, payloadData, signatureData)
		c := fake.NewClientBuilder().WithObjects(secret).Build()
		reconciler := &controllers.SveltosClusterReconciler{
			Client:           c,
			Scheme:           scheme,
			SveltosNamespace: licenseNamespace,
		}

		controllers.UpdateLicenseAnnotations(reconciler, context.TODO(), sveltosCluster, logger)

		Expect(sveltosCluster.Annotations[license.LicensePayloadAnnotation]).To(Equal(validLicenseDataB64))
		Expect(sveltosCluster.Annotations[license.LicenseSignatureAnnotation]).To(Equal(validLicenseSignatureB64))
	})

	It("relays an expired-but-validly-signed license too, so sveltos-applier can tell present-but-expired apart from absent", func() {
		expiredPayload, err := base64.StdEncoding.DecodeString(
			"eyJpZCI6IjY0Mzk5ZTY3LTdhMjctNDY5MS05YzU1LWY4NmY0YjQ4MGFkOSIsImN1c3RvbWVyTmFtZSI6IkFjbWUgSW5jIiwiZmVhdHVyZXMiOlsiUHVsbE1vZGUiXSwiZXhwaXJhdGlvbkRhdGUiOiIyMDI0LTA3LTI1VDExOjU3OjIxLjk2MTYwOFoiLCJpc3N1ZWRBdCI6IjIwMjUtMDctMjVUMTE6NTc6MjEuOTYxNjA4WiJ9")
		Expect(err).To(BeNil())
		expiredSignature, err := base64.StdEncoding.DecodeString(
			"Nk+Q3x/ZBg2DydTMcAhGzi8+xCBma4bsLfKXlN5f217/OqJVcfFDqlG3Q46nVRI92i/hOvXVAeEOnBpv8/0iDbUvSZB1fBilkyzglcH00hC7Y3CFF9CnxcmLlqWBl5ucL+MTmzCsgxMHhzklOF4oCMAAbigfty9xVCXE81rQN0jKPktZcVui15uubs7PVgXkvc7+NZrmmchXnECXz912S8ayllRWcgKL482xi8bf9XsKubg+mzQm/S4KvPBR1R8Yugnp1byyZmpzQmNMF1KYC5YT/vVqk7ojVZTPVG9y1SxnpFXGVO+4HRBnbEWoVnifg5U74FcU3kiIgOxpUoylsX88PCfZXdaJT5Mh65cZJVRx1RTYLgnBX260gzaLzuPF33uu5IZ1J182Si5RatkvNdPQd7mtLC2T/lyQK4gMqS2g0iidlxA2iwEeqC/UV42aeXrel3KRJ38TL0SNiCpMLly3ueC5sftdvRWARNel7aV/DAE+nfANIBO9YuLpiJY9EMndr1mpGclMZF6KbXkzOnEqbsiNmXANl7Y2lAKORWElC58IznD0WKFoFuc1ZltUDecGEFoExkdstrIPJ8HYi0dJ0OBaHfQNlo7MjEuHWkmZ1XoeUqMPxjFBrULlX74Lbowqif1lDnZhmZTTJs+qqGYLz424HtcVmir8UD5IboQ=")
		Expect(err).To(BeNil())

		secret := newLicenseSecret(licenseNamespace, expiredPayload, expiredSignature)
		c := fake.NewClientBuilder().WithObjects(secret).Build()
		reconciler := &controllers.SveltosClusterReconciler{
			Client:           c,
			Scheme:           scheme,
			SveltosNamespace: licenseNamespace,
		}

		controllers.UpdateLicenseAnnotations(reconciler, context.TODO(), sveltosCluster, logger)

		Expect(sveltosCluster.Annotations[license.LicensePayloadAnnotation]).ToNot(BeEmpty())
		Expect(sveltosCluster.Annotations[license.LicenseSignatureAnnotation]).ToNot(BeEmpty())
	})

	It("clears stale annotations when no license secret is found", func() {
		sveltosCluster.Annotations = map[string]string{
			license.LicensePayloadAnnotation:   staleAnnotationValue,
			license.LicenseSignatureAnnotation: staleAnnotationValue,
		}

		c := fake.NewClientBuilder().Build()
		reconciler := &controllers.SveltosClusterReconciler{
			Client:           c,
			Scheme:           scheme,
			SveltosNamespace: licenseNamespace,
		}

		controllers.UpdateLicenseAnnotations(reconciler, context.TODO(), sveltosCluster, logger)

		Expect(sveltosCluster.Annotations).ToNot(HaveKey(license.LicensePayloadAnnotation))
		Expect(sveltosCluster.Annotations).ToNot(HaveKey(license.LicenseSignatureAnnotation))
	})

	It("clears stale annotations when the signature no longer verifies", func() {
		tamperedPayload := append(append([]byte{}, payloadData...), []byte("tampered")...)
		secret := newLicenseSecret(licenseNamespace, tamperedPayload, signatureData)
		c := fake.NewClientBuilder().WithObjects(secret).Build()
		reconciler := &controllers.SveltosClusterReconciler{
			Client:           c,
			Scheme:           scheme,
			SveltosNamespace: licenseNamespace,
		}

		sveltosCluster.Annotations = map[string]string{
			license.LicensePayloadAnnotation:   staleAnnotationValue,
			license.LicenseSignatureAnnotation: staleAnnotationValue,
		}

		controllers.UpdateLicenseAnnotations(reconciler, context.TODO(), sveltosCluster, logger)

		Expect(sveltosCluster.Annotations).ToNot(HaveKey(license.LicensePayloadAnnotation))
		Expect(sveltosCluster.Annotations).ToNot(HaveKey(license.LicenseSignatureAnnotation))
	})

	It("is a no-op when annotations already match the current license (avoids an unnecessary write)", func() {
		secret := newLicenseSecret(licenseNamespace, payloadData, signatureData)
		c := fake.NewClientBuilder().WithObjects(secret).Build()
		reconciler := &controllers.SveltosClusterReconciler{
			Client:           c,
			Scheme:           scheme,
			SveltosNamespace: licenseNamespace,
		}

		sveltosCluster.Annotations = map[string]string{
			license.LicensePayloadAnnotation:   validLicenseDataB64,
			license.LicenseSignatureAnnotation: validLicenseSignatureB64,
		}
		beforePtr := reflect.ValueOf(sveltosCluster.Annotations).Pointer()

		controllers.UpdateLicenseAnnotations(reconciler, context.TODO(), sveltosCluster, logger)

		Expect(sveltosCluster.Annotations[license.LicensePayloadAnnotation]).To(Equal(validLicenseDataB64))
		Expect(sveltosCluster.Annotations[license.LicenseSignatureAnnotation]).To(Equal(validLicenseSignatureB64))
		// Same underlying map: the function returned before ever writing to cluster.Annotations.
		Expect(reflect.ValueOf(sveltosCluster.Annotations).Pointer()).To(Equal(beforePtr))
	})
})
