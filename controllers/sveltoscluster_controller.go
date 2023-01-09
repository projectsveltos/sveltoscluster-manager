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

	"github.com/pkg/errors"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	libsveltosv1alpha1 "github.com/projectsveltos/libsveltos/api/v1alpha1"
	"github.com/projectsveltos/libsveltos/lib/clusterproxy"
	logs "github.com/projectsveltos/libsveltos/lib/logsettings"
	"github.com/projectsveltos/sveltoscluster-manager/pkg/scope"
)

// SveltosClusterReconciler reconciles a SveltosCluster object
type SveltosClusterReconciler struct {
	client.Client
	Scheme               *runtime.Scheme
	ConcurrentReconciles int
}

//+kubebuilder:rbac:groups=lib.projectsveltos.io,resources=sveltosclusters,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=lib.projectsveltos.io,resources=sveltosclusters/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=lib.projectsveltos.io,resources=sveltosclusters/finalizers,verbs=update
//+kubebuilder:rbac:groups="",resources=secrets,verbs=get;list;watch
//+kubebuilder:rbac:groups=lib.projectsveltos.io,resources=debuggingconfigurations,verbs=get;list;watch

func (r *SveltosClusterReconciler) Reconcile(ctx context.Context, req ctrl.Request) (_ ctrl.Result, reterr error) {
	logger := ctrl.LoggerFrom(ctx)
	logger.V(logs.LogInfo).Info("Reconciling")

	// Fecth the sveltosCluster instance
	sveltosCluster := &libsveltosv1alpha1.SveltosCluster{}
	if err := r.Get(ctx, req.NamespacedName, sveltosCluster); err != nil {
		if apierrors.IsNotFound(err) {
			return reconcile.Result{}, nil
		}
		logger.Error(err, "Failed to fetch sveltosCluster")
		return reconcile.Result{}, errors.Wrapf(
			err,
			"Failed to fetch sveltosCluster %s",
			req.NamespacedName,
		)
	}

	sveltosClusterScope, err := scope.NewSveltosClusterScope(scope.SveltosClusterScopeParams{
		Client:         r.Client,
		Logger:         logger,
		SveltosCluster: sveltosCluster,
		ControllerName: "sveltoscluster",
	})
	if err != nil {
		logger.Error(err, "Failed to create clusterProfileScope")
		return reconcile.Result{}, errors.Wrapf(
			err,
			"unable to create clusterprofile scope for %s",
			req.NamespacedName,
		)
	}

	// Always close the scope when exiting this function so we can persist any ClusterProfile
	// changes.
	defer func() {
		if err := sveltosClusterScope.Close(ctx); err != nil {
			reterr = err
		}
	}()

	// Handle deleted clusterProfile
	if !sveltosCluster.DeletionTimestamp.IsZero() {
		return reconcile.Result{}, nil
	}

	// Handle non-deleted clusterProfile
	r.reconcileNormal(ctx, sveltosClusterScope)
	return reconcile.Result{}, nil
}

func (r *SveltosClusterReconciler) reconcileNormal(
	ctx context.Context,
	sveltosClusterScope *scope.SveltosClusterScope,
) {

	logger := sveltosClusterScope.Logger
	logger.V(logs.LogInfo).Info("Reconciling SveltosCluster")

	_, err := clusterproxy.GetSveltosKubernetesClient(ctx, logger, r.Client, r.Scheme,
		sveltosClusterScope.SveltosCluster.Namespace, sveltosClusterScope.SveltosCluster.Name)
	if err != nil {
		errorMessage := err.Error()
		logger.V(logs.LogInfo).Info(fmt.Sprintf("failed to get client: %v", err))
		sveltosClusterScope.SveltosCluster.Status.Ready = false
		sveltosClusterScope.SveltosCluster.Status.FailureMessage = &errorMessage
	} else {
		logger.V(logs.LogInfo).Info("got client")
		sveltosClusterScope.SveltosCluster.Status.Ready = true
		sveltosClusterScope.SveltosCluster.Status.FailureMessage = nil
	}

	logger.V(logs.LogInfo).Info("Reconcile success")
}

// SetupWithManager sets up the controller with the Manager.
func (r *SveltosClusterReconciler) SetupWithManager(mgr ctrl.Manager) error {
	_, err := ctrl.NewControllerManagedBy(mgr).
		For(&libsveltosv1alpha1.SveltosCluster{}).
		WithOptions(controller.Options{
			MaxConcurrentReconciles: r.ConcurrentReconciles,
		}).
		Build(r)
	return err
}
