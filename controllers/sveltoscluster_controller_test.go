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
	"encoding/base64"
	"time"

	"github.com/go-logr/logr"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/klog/v2/textlogger"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/yaml"

	libsveltosv1beta1 "github.com/projectsveltos/libsveltos/api/v1beta1"
	"github.com/projectsveltos/libsveltos/lib/pullmode"
	"github.com/projectsveltos/sveltoscluster-manager/controllers"
	"github.com/projectsveltos/sveltoscluster-manager/pkg/scope"
)

var _ = Describe("SveltosCluster: Reconciler", func() {
	var sveltosCluster *libsveltosv1beta1.SveltosCluster
	var logger logr.Logger

	const (
		defaultNamespace = "default"
	)

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
				secretDataKey: testEnv.Kubeconfig,
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

		currentSveltosCluster := &libsveltosv1beta1.SveltosCluster{}
		err = testEnv.Get(context.TODO(), sveltosClusterName, currentSveltosCluster)
		Expect(err).To(BeNil())
		Expect(currentSveltosCluster.Status.ConnectionFailures).To(Equal(0))
		Expect(currentSveltosCluster.Status.ConnectionStatus).To(Equal(libsveltosv1beta1.ConnectionHealthy))
	})

	It("reconcile set connection down after enough consecutive failed connection", func() {
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
				secretDataKey: []byte("not a valid kubeconfig"),
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
				!currentSveltosCluster.Status.Ready &&
				currentSveltosCluster.Status.ConnectionFailures == 1
		}, timeout, pollingInterval).Should(BeTrue())

		_, err = reconciler.Reconcile(context.TODO(), ctrl.Request{
			NamespacedName: sveltosClusterName,
		})
		Expect(err).ToNot(HaveOccurred())

		Eventually(func() bool {
			currentSveltosCluster := &libsveltosv1beta1.SveltosCluster{}
			err := testEnv.Get(context.TODO(), sveltosClusterName, currentSveltosCluster)
			return err == nil &&
				!currentSveltosCluster.Status.Ready &&
				currentSveltosCluster.Status.ConnectionFailures == 2
		}, timeout, pollingInterval).Should(BeTrue())

		_, err = reconciler.Reconcile(context.TODO(), ctrl.Request{
			NamespacedName: sveltosClusterName,
		})
		Expect(err).ToNot(HaveOccurred())

		Eventually(func() bool {
			currentSveltosCluster := &libsveltosv1beta1.SveltosCluster{}
			err := testEnv.Get(context.TODO(), sveltosClusterName, currentSveltosCluster)
			return err == nil &&
				!currentSveltosCluster.Status.Ready &&
				currentSveltosCluster.Status.ConnectionFailures == 3 &&
				currentSveltosCluster.Status.ConnectionStatus == libsveltosv1beta1.ConnectionDown
		}, timeout, pollingInterval).Should(BeTrue())
	})

	It("shouldRenewTokenRequest returns true when enough time has passed since last TokenRequest renewal", func() {
		sveltosCluster.Spec.TokenRequestRenewalOption = &libsveltosv1beta1.TokenRequestRenewalOption{
			RenewTokenRequestInterval: metav1.Duration{Duration: time.Minute},
			TokenDuration:             metav1.Duration{Duration: time.Hour},
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
		Expect(currentSveltosCluster.Spec.TokenRequestRenewalOption.TokenDuration).To(
			Equal(metav1.Duration{Duration: time.Hour}))

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
		expectedUnpause := time.Date(2024, time.August, 2, 20, 0, 0, 0, time.UTC)
		Expect(sveltosCluster.Status.NextUnpause.Time).To(Equal(expectedUnpause))

		// Next pause coming monday at 7AM
		Expect(sveltosCluster.Status.NextPause).ToNot(BeNil())
		expectedPause := time.Date(2024, time.August, 5, 7, 0, 0, 0, time.UTC)
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
		// 9:00 AM CEST converts to 7:00 AM UTC. We need it to be PAST 7:00 AM UTC.
		mondayLocalTime := time.Date(2024, time.August, 5, 9, 1, 0, 0, loc)
		controllers.HandleAutomaticPauseUnPause(sveltosCluster, mondayLocalTime, logger)
		Expect(sveltosCluster.Spec.Paused).To(BeTrue())
		expectedUnpause = time.Date(2024, time.August, 9, 20, 0, 0, 0, time.UTC)
		expectedPause = time.Date(2024, time.August, 12, 7, 0, 0, 0, time.UTC)
		Expect(sveltosCluster.Status.NextPause.Time).To(Equal(expectedPause))
		Expect(sveltosCluster.Status.NextUnpause.Time).To(Equal(expectedUnpause))
	})

	It("Hash mismatch (ActiveWindow change) forces recalculation of NextPause/NextUnpause", func() {
		// 1. Initial setup: Cluster paused, with a long future window
		initialFrom := cronMonday10AM // Monday 10 AM
		initialTo := cronMonday11AM   // Monday 11 AM
		sveltosCluster.Spec.ActiveWindow = &libsveltosv1beta1.ActiveWindow{
			From: initialFrom,
			To:   initialTo,
		}

		loc, err := time.LoadLocation("Europe/Rome")
		Expect(err).To(BeNil())

		// Start time: Tuesday, well after the initial window, so it's paused.
		startTuesday := time.Date(2024, time.July, 30, 12, 0, 0, 0, loc)
		sveltosCluster.CreationTimestamp = metav1.Time{Time: startTuesday}

		// --- First Run: Calculates original window ---
		controllers.HandleAutomaticPauseUnPause(sveltosCluster, startTuesday, logger)

		// Expected initial status (Next Monday)
		initialUnpause := time.Date(2024, time.August, 5, 10, 0, 0, 0, time.UTC)
		initialPause := time.Date(2024, time.August, 5, 11, 0, 0, 0, time.UTC)
		Expect(sveltosCluster.Status.NextUnpause.Time).To(Equal(initialUnpause))
		Expect(sveltosCluster.Status.NextPause.Time).To(Equal(initialPause))
		Expect(sveltosCluster.Status.ActiveWindowHash).ToNot(BeNil())

		// Save the initial hash for later assertion
		initialHash := sveltosCluster.Status.ActiveWindowHash

		// 3. CHANGE the ActiveWindow spec (Bug verification point)
		newFrom := "0 15 * * 5" // New: Friday 3 PM
		newTo := "0 16 * * 5"   // New: Friday 4 PM
		sveltosCluster.Spec.ActiveWindow = &libsveltosv1beta1.ActiveWindow{
			From: newFrom,
			To:   newTo,
		}

		// Set Generation to mimic a spec update (not strictly needed, but good practice)
		sveltosCluster.Generation = 2

		// 4. Run the handler again *before* the initialNextUnpause time (Initial time: Aug 5th)
		nextWednesday := time.Date(2024, time.July, 31, 12, 0, 0, 0, loc)
		controllers.HandleAutomaticPauseUnPause(sveltosCluster, nextWednesday, logger)

		// 5. ASSERT that the NextPause/NextUnpause times reflect the new schedule
		// The bug was that these times would *not* update here.

		// Expected NEW status (Next Friday)
		newExpectedUnpause := time.Date(2024, time.August, 2, 15, 0, 0, 0, time.UTC)
		newExpectedPause := time.Date(2024, time.August, 2, 16, 0, 0, 0, time.UTC)

		Expect(sveltosCluster.Status.NextUnpause.Time).To(Equal(newExpectedUnpause),
			"NextUnpause should be recalculated based on the new 'From' schedule")
		Expect(sveltosCluster.Status.NextPause.Time).To(Equal(newExpectedPause),
			"NextPause should be recalculated based on the new 'To' schedule")

		// Assert the hash was updated
		Expect(sveltosCluster.Status.ActiveWindowHash).ToNot(Equal(initialHash),
			"ActiveWindowHash must be updated to the new hash")
		Expect(sveltosCluster.Spec.Paused).To(BeTrue(),
			"Cluster should still be paused as current time is not in the new window")
	})

	It("Unrelated spec change (same ActiveWindowHash) does NOT force time recalculation", func() {
		// 1. Initial setup: Set a schedule and run once
		sveltosCluster.Spec.ActiveWindow = &libsveltosv1beta1.ActiveWindow{
			From: cronMonday10AM, // Monday 10 AM
			To:   cronMonday11AM, // Monday 11 AM
		}
		loc, err := time.LoadLocation("Europe/Rome")
		Expect(err).To(BeNil())
		startTuesday := time.Date(2024, time.July, 30, 12, 0, 0, 0, loc)
		sveltosCluster.CreationTimestamp = metav1.Time{Time: startTuesday}

		// --- First Run: Calculates original window ---
		controllers.HandleAutomaticPauseUnPause(sveltosCluster, startTuesday, logger)

		// Save the stable status times and hash
		initialUnpause := sveltosCluster.Status.NextUnpause.Time
		initialPause := sveltosCluster.Status.NextPause.Time
		initialHash := sveltosCluster.Status.ActiveWindowHash
		Expect(sveltosCluster.Spec.Paused).To(BeTrue())

		// 2. CHANGE an unrelated field
		// We change Spec.Paused manually and force a new reconcile loop (via Generation)
		sveltosCluster.Spec.Paused = false                  // User manually unpauses cluster
		sveltosCluster.Spec.ConsecutiveFailureThreshold = 5 // Another unrelated spec change
		sveltosCluster.Generation = 2                       // Generation changes, but hash should not

		// 3. Run the handler again at a time before any scheduled event
		nextWednesday := time.Date(2024, time.July, 31, 12, 0, 0, 0, loc)
		controllers.HandleAutomaticPauseUnPause(sveltosCluster, nextWednesday, logger)

		// 4. ASSERT that the times DID NOT change
		// The hash check should pass, causing the routine to hit an early 'return'
		// in the first pause/unpause conditional block, skipping recalculation.

		Expect(sveltosCluster.Status.NextUnpause.Time).To(Equal(initialUnpause),
			"NextUnpause time must remain unchanged")
		Expect(sveltosCluster.Status.NextPause.Time).To(Equal(initialPause),
			"NextPause time must remain unchanged")
		Expect(sveltosCluster.Status.ActiveWindowHash).To(Equal(initialHash),
			"ActiveWindowHash must be the same")

		// Assert Spec.Paused is reset back to true because the time is before NextUnpause
		// (This tests the existing core logic after the hash check passes)
		Expect(sveltosCluster.Spec.Paused).To(BeTrue())
	})

	It("getTokenExpiration returns token expiration time", func() {
		initObjects := []client.Object{
			sveltosCluster,
		}

		c := fake.NewClientBuilder().WithScheme(scheme).WithStatusSubresource(initObjects...).WithObjects(initObjects...).Build()

		reconciler := getClusterProfileReconciler(c)

		//nolint: gosec,lll // this is an already expired token
		const token = "eyJhbGciOiJSUzI1NiIsImtpZCI6InpuMW5mc25rdFZRLWM2UlF2Sjk3OHFXZS10LWIwSVpHVHphYjhfZHNPaE0ifQ.eyJhdWQiOlsiaHR0cHM6Ly9rdWJlcm5ldGVzLmRlZmF1bHQuc3ZjLmNsdXN0ZXIubG9jYWwiLCJrM3MiXSwiZXhwIjoxNzM5MTcyNDI0LCJpYXQiOjE3Mzg4MjY4MjQsImlzcyI6Imh0dHBzOi8va3ViZXJuZXRlcy5kZWZhdWx0LnN2Yy5jbHVzdGVyLmxvY2FsIiwianRpIjoiYzdhMGUwZGItNDZiYS00OTdlLTg5NmYtZDU1NWY0NmM2YmJmIiwia3ViZXJuZXRlcy5pbyI6eyJuYW1lc3BhY2UiOiJkZWZhdWx0Iiwic2VydmljZWFjY291bnQiOnsibmFtZSI6InBsYXRmb3JtIiwidWlkIjoiMzVjOTc5NjgtMmNiNi00YWE1LWFlMzMtZDE2YjEzNWRhMjg1In19LCJuYmYiOjE3Mzg4MjY4MjQsInN1YiI6InN5c3RlbTpzZXJ2aWNlYWNjb3VudDpkZWZhdWx0OnBsYXRmb3JtIn0.eYTP3rPp2gG8p91sxh7E6mS6GlizBJOO4fPDR5G95VzOrdZFZflxv_pika-Z34Ut3qtggtZejxytZdZkSMv_fq9sYFJG57vqC48u1oUa_7nD6ej_37ojW-y-VZ5IFgkws-_37VNzsdSFaQPU4ybHaDVkyXrSnSYs90zRT40LY-Ee1Ro__-UDEPxuQS-qPKOwGb-V590PP4td9LUq8Wkh1SwKv7XZUlSACqTxN3OYYVoqcBYeqob6QtkTgxNZMlY4DQZBTlCSQCpo2GKnASxDfLWFX6yivchD2HpJpIe1JnDs6R17u6iXidrnobyYWWEf_DAyAkoEq9HoivbYCs5IBQ"

		expirationTime, err := controllers.GetTokenExpiration(reconciler, token)
		Expect(err).To(BeNil())

		cetLocation, err := time.LoadLocation("Europe/Berlin") // Berlin is in CET
		Expect(err).To(BeNil())
		cetTime := expirationTime.In(cetLocation)

		Expect(cetTime.String()).To(Equal("2025-02-10 08:27:04 +0100 CET"))
	})

	It("adjustTokenRequestRenewalOption returns correct time when renew is due", func() {
		reconciler := getClusterProfileReconciler(testEnv.Client)

		sa := &corev1.ServiceAccount{
			ObjectMeta: metav1.ObjectMeta{
				Name:      randomString(),
				Namespace: defaultNamespace,
			},
		}

		Expect(testEnv.Create(context.TODO(), sa)).To(Succeed())
		Expect(waitForObject(context.TODO(), testEnv.Client, sa)).To(Succeed())

		// Create a token with an expiration time of one day
		const oneDayInSeconds = 24 * 60 * 60
		tokenRequestStatus, err := controllers.GetServiceAccountTokenRequest(reconciler, context.TODO(), testEnv.Config,
			sa.Namespace, sa.Name, oneDayInSeconds, logger)
		Expect(err).To(BeNil())

		const twoDaysInSecond = 2 * oneDayInSeconds
		tokenRequestRenewalOption := &libsveltosv1beta1.TokenRequestRenewalOption{
			RenewTokenRequestInterval: metav1.Duration{Duration: twoDaysInSecond * time.Second},
		}

		// Token had an expiration of 24 hours (one day)
		// SveltosCluster TokenRequestRenewalOption was set to request a renawal every 48 hours (two days)
		// AdjustTokenRequestRenewalOption will return a token that is set to less than one day
		newDuration := controllers.AdjustTokenRequestRenewalOption(reconciler, tokenRequestRenewalOption, tokenRequestStatus, logger)
		Expect(newDuration.Duration < oneDayInSeconds*time.Second).To(BeTrue())
	})

	It("adjustTokenRequestRenewalOption does not shrink RenewTokenRequestInterval when TokenDuration is the intended buffer",
		func() {
			reconciler := getClusterProfileReconciler(testEnv.Client)

			sa := &corev1.ServiceAccount{
				ObjectMeta: metav1.ObjectMeta{
					Name:      randomString(),
					Namespace: defaultNamespace,
				},
			}

			Expect(testEnv.Create(context.TODO(), sa)).To(Succeed())
			Expect(waitForObject(context.TODO(), testEnv.Client, sa)).To(Succeed())

			// Pull-mode-realistic shape: renew every 15 minutes, but the token itself is
			// requested with a much longer (24h) lifetime as a buffer against connectivity
			// loss, matching how sveltosctl's --token registration sets these two fields.
			const renewInterval = 15 * time.Minute
			const tokenDuration = 24 * time.Hour
			tokenRequestRenewalOption := &libsveltosv1beta1.TokenRequestRenewalOption{
				RenewTokenRequestInterval: metav1.Duration{Duration: renewInterval},
				TokenDuration:             metav1.Duration{Duration: tokenDuration},
			}

			tokenRequestStatus, err := controllers.GetServiceAccountTokenRequest(reconciler, context.TODO(), testEnv.Config,
				sa.Namespace, sa.Name, tokenDuration.Seconds(), logger)
			Expect(err).To(BeNil())

			// The token's actual lifetime (24h) comfortably exceeds RenewTokenRequestInterval
			// (15m): that gap is the buffer TokenDuration is meant to provide, not a shortfall,
			// so RenewTokenRequestInterval must come back unchanged.
			newDuration := controllers.AdjustTokenRequestRenewalOption(reconciler, tokenRequestRenewalOption, tokenRequestStatus, logger)
			Expect(newDuration.Duration).To(Equal(renewInterval))
		})

	It("reconcilePullModeCluster verifies last update from sveltos-applier", func() {
		ns := &corev1.Namespace{
			ObjectMeta: metav1.ObjectMeta{
				Name: sveltosCluster.Namespace,
			},
		}

		Expect(testEnv.Create(context.TODO(), ns)).To(Succeed())
		Expect(waitForObject(context.TODO(), testEnv.Client, ns)).To(Succeed())

		sveltosCluster.Spec.PullMode = true
		Expect(testEnv.Create(context.TODO(), sveltosCluster)).To(Succeed())
		Expect(waitForObject(context.TODO(), testEnv.Client, sveltosCluster)).To(Succeed())

		reconciler := getClusterProfileReconciler(testEnv.Client)

		sveltosClusterName := client.ObjectKey{
			Name:      sveltosCluster.Name,
			Namespace: sveltosCluster.Namespace,
		}

		currentSveltosCluster := &libsveltosv1beta1.SveltosCluster{}
		Expect(testEnv.Get(context.TODO(), sveltosClusterName, currentSveltosCluster)).To(Succeed())
		currentTime := metav1.Now()
		currentSveltosCluster.Status.AgentLastReportTime = &currentTime
		currentSveltosCluster.Status.ConnectionStatus = libsveltosv1beta1.ConnectionHealthy
		currentSveltosCluster.Status.Ready = true

		Expect(testEnv.Status().Update(context.TODO(), currentSveltosCluster)).To(Succeed())

		// wait for cache to sync status
		Eventually(func() bool {
			currentSveltosCluster := &libsveltosv1beta1.SveltosCluster{}
			err := testEnv.Get(context.TODO(), sveltosClusterName, currentSveltosCluster)
			return err == nil &&
				currentSveltosCluster.Status.AgentLastReportTime != nil
		}, timeout, pollingInterval).Should(BeTrue())

		sveltosClusterScope, err := scope.NewSveltosClusterScope(scope.SveltosClusterScopeParams{
			Client:         testEnv.Client,
			SveltosCluster: currentSveltosCluster,
			ControllerName: randomString(),
			Logger:         logger,
		})
		Expect(err).To(BeNil())

		controllers.ReconcilePullModeCluster(reconciler, context.TODO(), sveltosClusterScope, logger)

		Expect(currentSveltosCluster.Status.FailureMessage).To(BeNil())
		Expect(currentSveltosCluster.Status.ConnectionStatus).To(Equal(libsveltosv1beta1.ConnectionHealthy))

		Expect(testEnv.Get(context.TODO(), sveltosClusterName, currentSveltosCluster)).To(Succeed())
		currentTime = metav1.Time{Time: time.Now().UTC().Add(-1 * time.Hour)}
		currentSveltosCluster.Status.AgentLastReportTime = &currentTime
		currentSveltosCluster.Status.ConnectionStatus = libsveltosv1beta1.ConnectionHealthy
		currentSveltosCluster.Status.Ready = true

		Expect(testEnv.Status().Update(context.TODO(), currentSveltosCluster)).To(Succeed())

		// wait for cache to sync status
		Eventually(func() bool {
			currentSveltosCluster := &libsveltosv1beta1.SveltosCluster{}
			err := testEnv.Get(context.TODO(), sveltosClusterName, currentSveltosCluster)
			if err != nil || currentSveltosCluster.Status.AgentLastReportTime == nil {
				return false
			}
			age := time.Since(currentSveltosCluster.Status.AgentLastReportTime.Time)
			return age > 5*time.Minute
		}, timeout, pollingInterval).Should(BeTrue())

		controllers.ReconcilePullModeCluster(reconciler, context.TODO(), sveltosClusterScope, logger)

		Expect(currentSveltosCluster.Status.FailureMessage).ToNot(BeNil())
	})

	It("handlePullModeTokenRequestRenewal stages the renewed kubeconfig using the ConfigMap's server URL, not r.Config.Host",
		func() {
			ns := &corev1.Namespace{
				ObjectMeta: metav1.ObjectMeta{
					Name: sveltosCluster.Namespace,
				},
			}
			Expect(testEnv.Create(context.TODO(), ns)).To(Succeed())
			Expect(waitForObject(context.TODO(), testEnv.Client, ns)).To(Succeed())

			sa := &corev1.ServiceAccount{
				ObjectMeta: metav1.ObjectMeta{
					Name:      sveltosCluster.Name,
					Namespace: sveltosCluster.Namespace,
				},
			}
			Expect(testEnv.Create(context.TODO(), sa)).To(Succeed())
			Expect(waitForObject(context.TODO(), testEnv.Client, sa)).To(Succeed())

			// testManagementClusterURL is what sveltosctl's --management-cluster-url would
			// persist at registration time.
			serverConfigMap := &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Name:      sveltosCluster.Name,
					Namespace: sveltosCluster.Namespace,
				},
				Data: map[string]string{
					controllers.PullModeManagementClusterURLConfigMapKey: testManagementClusterURL,
					controllers.PullModeManagementClusterCAConfigMapKey:  testManagementClusterCA,
				},
			}
			Expect(testEnv.Create(context.TODO(), serverConfigMap)).To(Succeed())
			Expect(waitForObject(context.TODO(), testEnv.Client, serverConfigMap)).To(Succeed())

			sveltosCluster.Spec.PullMode = true
			sveltosCluster.Spec.TokenRequestRenewalOption = &libsveltosv1beta1.TokenRequestRenewalOption{
				RenewTokenRequestInterval: metav1.Duration{Duration: time.Hour},
				TokenDuration:             metav1.Duration{Duration: 24 * time.Hour},
				SAName:                    sa.Name,
				SANamespace:               sa.Namespace,
			}
			Expect(testEnv.Create(context.TODO(), sveltosCluster)).To(Succeed())
			Expect(waitForObject(context.TODO(), testEnv.Client, sveltosCluster)).To(Succeed())

			reconciler := &controllers.SveltosClusterReconciler{
				Client:           testEnv.Client,
				Config:           *testEnv.Config,
				Scheme:           scheme,
				SveltosNamespace: testSveltosOwnNamespace,
			}

			sveltosClusterScope, err := scope.NewSveltosClusterScope(scope.SveltosClusterScopeParams{
				Client:         testEnv.Client,
				SveltosCluster: sveltosCluster,
				ControllerName: randomString(),
				Logger:         logger,
			})
			Expect(err).To(BeNil())

			Expect(controllers.HandlePullModeTokenRequestRenewal(reconciler, context.TODO(), sveltosClusterScope, logger)).
				To(Succeed())
			Expect(sveltosCluster.Status.LastReconciledTokenRequestAt).ToNot(BeEmpty())

			var bundles libsveltosv1beta1.ConfigurationBundleList
			Eventually(func() int {
				Expect(testEnv.List(context.TODO(), &bundles, client.InNamespace(sveltosCluster.Namespace),
					client.MatchingLabels(pullmode.GetClusterLabels(sveltosCluster.Namespace, sveltosCluster.Name)))).To(Succeed())
				return len(bundles.Items)
			}, timeout, pollingInterval).Should(Equal(1))

			Expect(bundles.Items[0].Spec.Resources).To(HaveLen(1))

			// The bundle resource is the YAML of a corev1.Secret; its kubeconfig content is
			// base64-encoded in Data, so decode it before checking which server address it has.
			deliveredSecret := &corev1.Secret{}
			Expect(yaml.Unmarshal([]byte(bundles.Items[0].Spec.Resources[0]), deliveredSecret)).To(Succeed())

			// The Secret's own namespace must be sveltoscluster-manager's namespace
			// (r.SveltosNamespace, read from NAMESPACE), not a hardcoded "projectsveltos": every
			// Sveltos component, on both the management and the managed cluster, is deployed into
			// the same namespace name by convention, and that name need not be "projectsveltos".
			Expect(deliveredSecret.Namespace).To(Equal(testSveltosOwnNamespace))

			deliveredKubeconfig := string(deliveredSecret.Data[controllers.PullModeKubeconfigSecretKey])
			Expect(deliveredKubeconfig).To(ContainSubstring(testManagementClusterURL))
			Expect(deliveredKubeconfig).ToNot(ContainSubstring(testEnv.Config.Host))

			// The kubeconfig's certificate-authority-data must be the CA persisted in the
			// ConfigMap (proven to validate the external endpoint), not sveltoscluster-manager's
			// own in-cluster CA, which is not guaranteed to validate it too (observed on Civo).
			Expect(deliveredKubeconfig).To(ContainSubstring(base64.StdEncoding.EncodeToString([]byte(testManagementClusterCA))))
			Expect(deliveredKubeconfig).ToNot(ContainSubstring(base64.StdEncoding.EncodeToString(testEnv.Config.CAData)))

			// sveltos-applier's deployResourcesInConfigurationBundle dereferences SourceRef
			// whenever the bundle doesn't set ReferencedObjectKind (ours never does), so a nil
			// SourceRef here causes a nil pointer panic on the managed cluster.
			var groups libsveltosv1beta1.ConfigurationGroupList
			Expect(testEnv.List(context.TODO(), &groups, client.InNamespace(sveltosCluster.Namespace),
				client.MatchingLabels(pullmode.GetClusterLabels(sveltosCluster.Namespace, sveltosCluster.Name)))).To(Succeed())
			Expect(groups.Items).To(HaveLen(1))
			Expect(groups.Items[0].Spec.SourceRef).ToNot(BeNil())
			Expect(groups.Items[0].Spec.SourceRef.Kind).To(Equal(libsveltosv1beta1.SveltosClusterKind))
			Expect(groups.Items[0].Spec.SourceRef.Name).To(Equal(sveltosCluster.Name))
			Expect(groups.Items[0].Spec.SourceRef.Namespace).To(Equal(sveltosCluster.Namespace))
		})

	It("terminatePullModeTokenRenewalTrackingIfProvisioned removes the ConfigurationGroup/Bundle once provisioned",
		func() {
			ns := &corev1.Namespace{
				ObjectMeta: metav1.ObjectMeta{
					Name: sveltosCluster.Namespace,
				},
			}
			Expect(testEnv.Create(context.TODO(), ns)).To(Succeed())
			Expect(waitForObject(context.TODO(), testEnv.Client, ns)).To(Succeed())

			sa := &corev1.ServiceAccount{
				ObjectMeta: metav1.ObjectMeta{
					Name:      sveltosCluster.Name,
					Namespace: sveltosCluster.Namespace,
				},
			}
			Expect(testEnv.Create(context.TODO(), sa)).To(Succeed())
			Expect(waitForObject(context.TODO(), testEnv.Client, sa)).To(Succeed())

			serverConfigMap := &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Name:      sveltosCluster.Name,
					Namespace: sveltosCluster.Namespace,
				},
				Data: map[string]string{
					controllers.PullModeManagementClusterURLConfigMapKey: testManagementClusterURL,
					controllers.PullModeManagementClusterCAConfigMapKey:  testManagementClusterCA,
				},
			}
			Expect(testEnv.Create(context.TODO(), serverConfigMap)).To(Succeed())
			Expect(waitForObject(context.TODO(), testEnv.Client, serverConfigMap)).To(Succeed())

			sveltosCluster.Spec.PullMode = true
			sveltosCluster.Spec.TokenRequestRenewalOption = &libsveltosv1beta1.TokenRequestRenewalOption{
				RenewTokenRequestInterval: metav1.Duration{Duration: time.Hour},
				TokenDuration:             metav1.Duration{Duration: 24 * time.Hour},
				SAName:                    sa.Name,
				SANamespace:               sa.Namespace,
			}
			Expect(testEnv.Create(context.TODO(), sveltosCluster)).To(Succeed())
			Expect(waitForObject(context.TODO(), testEnv.Client, sveltosCluster)).To(Succeed())

			reconciler := &controllers.SveltosClusterReconciler{
				Client:           testEnv.Client,
				Config:           *testEnv.Config,
				Scheme:           scheme,
				SveltosNamespace: testSveltosOwnNamespace,
			}

			sveltosClusterScope, err := scope.NewSveltosClusterScope(scope.SveltosClusterScopeParams{
				Client:         testEnv.Client,
				SveltosCluster: sveltosCluster,
				ControllerName: randomString(),
				Logger:         logger,
			})
			Expect(err).To(BeNil())

			Expect(controllers.HandlePullModeTokenRequestRenewal(reconciler, context.TODO(), sveltosClusterScope, logger)).
				To(Succeed())

			var groups libsveltosv1beta1.ConfigurationGroupList
			Eventually(func() int {
				Expect(testEnv.List(context.TODO(), &groups, client.InNamespace(sveltosCluster.Namespace),
					client.MatchingLabels(pullmode.GetClusterLabels(sveltosCluster.Namespace, sveltosCluster.Name)))).To(Succeed())
				return len(groups.Items)
			}, timeout, pollingInterval).Should(Equal(1))

			// Simulate sveltos-applier confirming it applied the renewed kubeconfig.
			provisioned := libsveltosv1beta1.FeatureStatusProvisioned
			currentGroup := &groups.Items[0]
			currentGroup.Status.DeploymentStatus = &provisioned
			Expect(testEnv.Status().Update(context.TODO(), currentGroup)).To(Succeed())

			Eventually(func() bool {
				controllers.TerminatePullModeTokenRenewalTrackingIfProvisioned(reconciler, context.TODO(), sveltosCluster, logger)

				var remainingGroups libsveltosv1beta1.ConfigurationGroupList
				Expect(testEnv.List(context.TODO(), &remainingGroups, client.InNamespace(sveltosCluster.Namespace),
					client.MatchingLabels(pullmode.GetClusterLabels(sveltosCluster.Namespace, sveltosCluster.Name)))).To(Succeed())
				var remainingBundles libsveltosv1beta1.ConfigurationBundleList
				Expect(testEnv.List(context.TODO(), &remainingBundles, client.InNamespace(sveltosCluster.Namespace),
					client.MatchingLabels(pullmode.GetClusterLabels(sveltosCluster.Namespace, sveltosCluster.Name)))).To(Succeed())

				return len(remainingGroups.Items) == 0 && len(remainingBundles.Items) == 0
			}, timeout, pollingInterval).Should(BeTrue())
		})

	It("reconcile adds the finalizer to a new SveltosCluster", func() {
		ns := &corev1.Namespace{
			ObjectMeta: metav1.ObjectMeta{
				Name: sveltosCluster.Namespace,
			},
		}
		Expect(testEnv.Create(context.TODO(), ns)).To(Succeed())
		Expect(waitForObject(context.TODO(), testEnv.Client, ns)).To(Succeed())

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

		currentSveltosCluster := &libsveltosv1beta1.SveltosCluster{}
		Expect(testEnv.Get(context.TODO(), sveltosClusterName, currentSveltosCluster)).To(Succeed())
		Expect(controllerutil.ContainsFinalizer(currentSveltosCluster, libsveltosv1beta1.SveltosClusterFinalizer)).To(BeTrue())
	})

	It("reconcile removes the finalizer immediately when CleanupGracePeriod is not set", func() {
		ns := &corev1.Namespace{
			ObjectMeta: metav1.ObjectMeta{
				Name: sveltosCluster.Namespace,
			},
		}
		Expect(testEnv.Create(context.TODO(), ns)).To(Succeed())
		Expect(waitForObject(context.TODO(), testEnv.Client, ns)).To(Succeed())

		Expect(testEnv.Create(context.TODO(), sveltosCluster)).To(Succeed())
		Expect(waitForObject(context.TODO(), testEnv.Client, sveltosCluster)).To(Succeed())

		reconciler := getClusterProfileReconciler(testEnv.Client)

		sveltosClusterName := client.ObjectKey{
			Name:      sveltosCluster.Name,
			Namespace: sveltosCluster.Namespace,
		}
		// First reconcile adds the finalizer.
		_, err := reconciler.Reconcile(context.TODO(), ctrl.Request{NamespacedName: sveltosClusterName})
		Expect(err).ToNot(HaveOccurred())

		Expect(testEnv.Delete(context.TODO(), sveltosCluster)).To(Succeed())

		Eventually(func() error {
			_, err := reconciler.Reconcile(context.TODO(), ctrl.Request{NamespacedName: sveltosClusterName})
			if err != nil {
				return err
			}
			return testEnv.Get(context.TODO(), sveltosClusterName, &libsveltosv1beta1.SveltosCluster{})
		}, timeout, pollingInterval).Should(MatchError(apierrors.IsNotFound, "IsNotFound"))
	})

	It("reconcile delays finalizer removal until CleanupGracePeriod elapses", func() {
		ns := &corev1.Namespace{
			ObjectMeta: metav1.ObjectMeta{
				Name: sveltosCluster.Namespace,
			},
		}
		Expect(testEnv.Create(context.TODO(), ns)).To(Succeed())
		Expect(waitForObject(context.TODO(), testEnv.Client, ns)).To(Succeed())

		sveltosCluster.Spec.CleanupGracePeriod = &metav1.Duration{Duration: time.Hour}
		Expect(testEnv.Create(context.TODO(), sveltosCluster)).To(Succeed())
		Expect(waitForObject(context.TODO(), testEnv.Client, sveltosCluster)).To(Succeed())

		reconciler := getClusterProfileReconciler(testEnv.Client)

		sveltosClusterName := client.ObjectKey{
			Name:      sveltosCluster.Name,
			Namespace: sveltosCluster.Namespace,
		}
		// First reconcile adds the finalizer.
		_, err := reconciler.Reconcile(context.TODO(), ctrl.Request{NamespacedName: sveltosClusterName})
		Expect(err).ToNot(HaveOccurred())

		Expect(testEnv.Delete(context.TODO(), sveltosCluster)).To(Succeed())

		result, err := reconciler.Reconcile(context.TODO(), ctrl.Request{NamespacedName: sveltosClusterName})
		Expect(err).ToNot(HaveOccurred())
		Expect(result.RequeueAfter).To(Equal(controllers.DeleteRequeueAfter))

		currentSveltosCluster := &libsveltosv1beta1.SveltosCluster{}
		Expect(testEnv.Get(context.TODO(), sveltosClusterName, currentSveltosCluster)).To(Succeed())
		Expect(controllerutil.ContainsFinalizer(currentSveltosCluster, libsveltosv1beta1.SveltosClusterFinalizer)).To(BeTrue())
	})

	It("reconcile removes the finalizer once CleanupGracePeriod has elapsed", func() {
		ns := &corev1.Namespace{
			ObjectMeta: metav1.ObjectMeta{
				Name: sveltosCluster.Namespace,
			},
		}
		Expect(testEnv.Create(context.TODO(), ns)).To(Succeed())
		Expect(waitForObject(context.TODO(), testEnv.Client, ns)).To(Succeed())

		const cleanupGracePeriod = 100 * time.Millisecond
		sveltosCluster.Spec.CleanupGracePeriod = &metav1.Duration{Duration: cleanupGracePeriod}
		Expect(testEnv.Create(context.TODO(), sveltosCluster)).To(Succeed())
		Expect(waitForObject(context.TODO(), testEnv.Client, sveltosCluster)).To(Succeed())

		reconciler := getClusterProfileReconciler(testEnv.Client)

		sveltosClusterName := client.ObjectKey{
			Name:      sveltosCluster.Name,
			Namespace: sveltosCluster.Namespace,
		}
		// First reconcile adds the finalizer.
		_, err := reconciler.Reconcile(context.TODO(), ctrl.Request{NamespacedName: sveltosClusterName})
		Expect(err).ToNot(HaveOccurred())

		Expect(testEnv.Delete(context.TODO(), sveltosCluster)).To(Succeed())

		time.Sleep(2 * cleanupGracePeriod)

		Eventually(func() error {
			_, err := reconciler.Reconcile(context.TODO(), ctrl.Request{NamespacedName: sveltosClusterName})
			if err != nil {
				return err
			}
			return testEnv.Get(context.TODO(), sveltosClusterName, &libsveltosv1beta1.SveltosCluster{})
		}, timeout, pollingInterval).Should(MatchError(apierrors.IsNotFound, "IsNotFound"))
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
