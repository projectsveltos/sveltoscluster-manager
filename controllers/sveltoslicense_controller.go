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

package controllers

import (
	"context"
	"fmt"
	"time"

	"github.com/go-logr/logr"
	"github.com/pkg/errors"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	libsveltosv1beta1 "github.com/projectsveltos/libsveltos/api/v1beta1"
	license "github.com/projectsveltos/libsveltos/lib/licenses"
	logs "github.com/projectsveltos/libsveltos/lib/logsettings"
)

// SveltosLicenseReconciler reconciles a SveltosLicense object
type SveltosLicenseReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

// +kubebuilder:rbac:groups=lib.projectsveltos.io,resources=sveltoslicenses,verbs=get;list;watch
// +kubebuilder:rbac:groups=lib.projectsveltos.io,resources=sveltoslicenses/status,verbs=get;update;patch

func (r *SveltosLicenseReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	if req.Name != "default" {
		return reconcile.Result{}, nil
	}

	// Fecth the sveltosLicense instance
	sveltosLicense := &libsveltosv1beta1.SveltosLicense{}
	if err := r.Get(ctx, req.NamespacedName, sveltosLicense); err != nil {
		if apierrors.IsNotFound(err) {
			return reconcile.Result{}, nil
		}
		logger.Error(err, "Failed to fetch sveltosLicense")
		return reconcile.Result{}, errors.Wrapf(
			err,
			"Failed to fetch sveltosLicense %s",
			req.NamespacedName,
		)
	}

	if !sveltosLicense.DeletionTimestamp.IsZero() {
		return reconcile.Result{}, nil
	}

	var payload *license.LicensePayload
	logger.V(logs.LogDebug).Info("validate SveltosLicense")
	sveltosLicense, payload = r.validateSveltosLicense(ctx, sveltosLicense, logger)
	err := r.Status().Update(ctx, sveltosLicense)
	if err != nil {
		logger.V(logs.LogDebug).Info(fmt.Sprintf("failed to update SveltosLicense Status: %v", err))
		return reconcile.Result{Requeue: true, RequeueAfter: normalRequeueAfter}, nil
	}

	if payload != nil {
		timeUntilExpiration := time.Until(payload.ExpirationDate)
		if timeUntilExpiration > 0 {
			return reconcile.Result{Requeue: true, RequeueAfter: timeUntilExpiration}, nil
		}
	}

	return reconcile.Result{Requeue: true, RequeueAfter: time.Minute}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *SveltosLicenseReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&libsveltosv1beta1.SveltosLicense{}, builder.WithPredicates(predicate.GenerationChangedPredicate{})).
		Named("sveltoslicense").
		Complete(r)
}

func (r *SveltosLicenseReconciler) validateSveltosLicense(ctx context.Context,
	sveltosLicense *libsveltosv1beta1.SveltosLicense, logger logr.Logger) (*libsveltosv1beta1.SveltosLicense, *license.LicensePayload) {

	publicKey, err := license.GetPublicKey()
	if err != nil {
		logger.V(logs.LogInfo).Info(fmt.Sprintf("failed to get public key: %v", err))
		sveltosLicense.Status.Status = libsveltosv1beta1.LicenseStatusInvalid
	}

	result := license.VerifyLicenseSecret(ctx, r.Client, publicKey, logger)

	if result.Payload != nil {
		sveltosLicense.Status.MaxClusters = &result.Payload.MaxClusters
		sveltosLicense.Status.ExpirationDate = &metav1.Time{Time: result.Payload.ExpirationDate}
	}

	if result.IsValid {
		sveltosLicense.Status.Status = libsveltosv1beta1.LicenseStatusValid
		sveltosLicense.Status.Message = ""
		return sveltosLicense, result.Payload
	}

	// This means license expired but in grace period
	if !result.IsEnforced {
		sveltosLicense.Status.Status = libsveltosv1beta1.LicenseStatusValid
		sveltosLicense.Status.Message = result.Message
		return sveltosLicense, result.Payload
	}

	if result.IsExpired {
		sveltosLicense.Status.Status = libsveltosv1beta1.LicenseStatusExpired
		sveltosLicense.Status.ExpirationDate = &metav1.Time{Time: result.Payload.ExpirationDate}
		sveltosLicense.Status.Message = result.Message
		return sveltosLicense, result.Payload
	}

	sveltosLicense.Status.Status = libsveltosv1beta1.LicenseStatusInvalid
	sveltosLicense.Status.Message = result.Message
	return sveltosLicense, result.Payload
}
