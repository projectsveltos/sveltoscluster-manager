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
	"encoding/base64"
	"fmt"
	"time"

	"github.com/go-logr/logr"
	"github.com/pkg/errors"
	authenticationv1 "k8s.io/api/authentication/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	apiv1 "k8s.io/client-go/tools/clientcmd/api/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	libsveltosv1beta1 "github.com/projectsveltos/libsveltos/api/v1beta1"
	"github.com/projectsveltos/libsveltos/lib/clusterproxy"
	logs "github.com/projectsveltos/libsveltos/lib/logsettings"
	"github.com/projectsveltos/libsveltos/lib/sharding"
	"github.com/projectsveltos/libsveltos/lib/utils"
	"github.com/projectsveltos/sveltoscluster-manager/pkg/scope"
)

const (
	// normalRequeueAfter is how long to wait before checking again to see if the cluster can be moved
	// to ready after or workload features (for instance ingress or reporter) have failed
	normalRequeueAfter = 10 * time.Second

	versionLabel = "projectsveltos.io/version"
)

// SveltosClusterReconciler reconciles a SveltosCluster object
type SveltosClusterReconciler struct {
	client.Client
	rest.Config
	Scheme               *runtime.Scheme
	ConcurrentReconciles int
	ShardKey             string // when set, only clusters matching the ShardKey will be reconciled
}

//+kubebuilder:rbac:groups=lib.projectsveltos.io,resources=sveltosclusters,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=lib.projectsveltos.io,resources=sveltosclusters/status,verbs=get;update;patch
//+kubebuilder:rbac:groups="",resources=secrets,verbs=get;list;watch;update
//+kubebuilder:rbac:groups=lib.projectsveltos.io,resources=debuggingconfigurations,verbs=get;list;watch

func (r *SveltosClusterReconciler) Reconcile(ctx context.Context, req ctrl.Request) (_ ctrl.Result, reterr error) {
	logger := ctrl.LoggerFrom(ctx)
	logger.V(logs.LogInfo).Info("Reconciling")

	// Fecth the sveltosCluster instance
	sveltosCluster := &libsveltosv1beta1.SveltosCluster{}
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

	var isMatch bool
	isMatch, err = r.isClusterAShardMatch(ctx, sveltosCluster, logger)
	if err != nil {
		return reconcile.Result{Requeue: true, RequeueAfter: normalRequeueAfter}, nil
	} else if !isMatch {
		// This sveltoscluster pod is not a shard match.
		return reconcile.Result{}, nil
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
	// Reconcile back in normalRequeueAfter time
	return reconcile.Result{Requeue: true, RequeueAfter: normalRequeueAfter}, nil
}

func (r *SveltosClusterReconciler) reconcileNormal(
	ctx context.Context,
	sveltosClusterScope *scope.SveltosClusterScope,
) {

	logger := sveltosClusterScope.Logger
	logger.V(logs.LogInfo).Info("Reconciling SveltosCluster")

	s := runtime.NewScheme()
	if err := clientgoscheme.AddToScheme(s); err != nil {
		errorMessage := err.Error()
		logger.V(logs.LogInfo).Info(fmt.Sprintf("failed to get scheme: %v", err))
		sveltosClusterScope.SveltosCluster.Status.Ready = false
		sveltosClusterScope.SveltosCluster.Status.FailureMessage = &errorMessage
		return
	}

	// Get managed cluster rest.Config
	config, err := clusterproxy.GetSveltosKubernetesRestConfig(ctx, logger, r.Client,
		sveltosClusterScope.SveltosCluster.Namespace, sveltosClusterScope.SveltosCluster.Name)
	if err != nil {
		errorMessage := err.Error()
		logger.V(logs.LogInfo).Info(fmt.Sprintf("failed to get client: %v", err))
		sveltosClusterScope.SveltosCluster.Status.Ready = false
		sveltosClusterScope.SveltosCluster.Status.FailureMessage = &errorMessage
		return
	}

	// Get managed cluster client
	var c client.Client
	c, err = client.New(config, client.Options{Scheme: s})
	if err != nil {
		errorMessage := err.Error()
		logger.V(logs.LogInfo).Info(fmt.Sprintf("failed to get client: %v", err))
		sveltosClusterScope.SveltosCluster.Status.Ready = false
		sveltosClusterScope.SveltosCluster.Status.FailureMessage = &errorMessage
		return
	}

	logger.V(logs.LogInfo).Info("got client")
	sveltosClusterScope.SveltosCluster.Status.Ready = true
	sveltosClusterScope.SveltosCluster.Status.FailureMessage = nil

	ns := &corev1.Namespace{}
	err = c.Get(context.TODO(), types.NamespacedName{Name: "projectsveltos"}, ns)
	if err != nil && !apierrors.IsNotFound(err) {
		errorMessage := err.Error()
		logger.V(logs.LogInfo).Info(fmt.Sprintf("failed to get projectsveltos namespace: %v", err))
		sveltosClusterScope.SveltosCluster.Status.Ready = false
		sveltosClusterScope.SveltosCluster.Status.FailureMessage = &errorMessage
	} else {
		currentVersion, err := utils.GetKubernetesVersion(ctx, config, logger)
		if err != nil {
			logger.V(logs.LogInfo).Info(fmt.Sprintf("failed to get cluster kubernetes version %v", err))
			errorMessage := err.Error()
			sveltosClusterScope.SveltosCluster.Status.FailureMessage = &errorMessage
		} else {
			sveltosClusterScope.SetLabel(versionLabel, currentVersion)
			sveltosClusterScope.SveltosCluster.Status.Version = currentVersion
			logger.V(logs.LogDebug).Info(fmt.Sprintf("cluster version %s", currentVersion))
			if r.shouldRenewTokenRequest(sveltosClusterScope, logger) {
				err = r.handleTokenRequestRenewal(ctx, sveltosClusterScope, config)
				if err != nil {
					errorMessage := err.Error()
					sveltosClusterScope.SveltosCluster.Status.FailureMessage = &errorMessage
				}
			}
		}
	}
}

// SetupWithManager sets up the controller with the Manager.
func (r *SveltosClusterReconciler) SetupWithManager(mgr ctrl.Manager) error {
	_, err := ctrl.NewControllerManagedBy(mgr).
		For(&libsveltosv1beta1.SveltosCluster{}).
		WithOptions(controller.Options{
			MaxConcurrentReconciles: r.ConcurrentReconciles,
		}).
		Build(r)
	return err
}

// isClusterAShardMatch checks if cluster is matching this addon-controller deployment shard.
func (r *SveltosClusterReconciler) isClusterAShardMatch(ctx context.Context,
	sveltosCluster *libsveltosv1beta1.SveltosCluster, logger logr.Logger) (bool, error) {

	cluster, err := clusterproxy.GetCluster(ctx, r.Client, sveltosCluster.Namespace,
		sveltosCluster.Name, libsveltosv1beta1.ClusterTypeSveltos)
	if err != nil {
		// If Cluster does not exist anymore, make it match any shard
		if apierrors.IsNotFound(err) {
			return true, nil
		}

		logger.V(logs.LogDebug).Info(fmt.Sprintf("failed to get cluster: %v", err))
		return false, err
	}

	if !sharding.IsShardAMatch(r.ShardKey, cluster) {
		logger.V(logs.LogDebug).Info("not a shard match")
		return false, nil
	}

	return true, nil
}

func (r *SveltosClusterReconciler) shouldRenewTokenRequest(sveltosClusterScope *scope.SveltosClusterScope,
	logger logr.Logger) bool {

	sveltosCluster := sveltosClusterScope.SveltosCluster
	if sveltosCluster.Spec.TokenRequestRenewalOption == nil {
		return false
	}

	currentTime := time.Now()
	lastRenewal := sveltosCluster.CreationTimestamp
	if sveltosCluster.Status.LastReconciledTokenRequestAt != "" {
		parsedTime, err := time.Parse(time.RFC3339, sveltosCluster.Status.LastReconciledTokenRequestAt)
		if err != nil {
			logger.V(logs.LogInfo).Info(fmt.Sprintf("failed to parse LastReconciledTokenRequestAt: %v. Using CreationTimestamep", err))
		} else {
			lastRenewal = metav1.Time{Time: parsedTime}
		}
	}

	// Calculate how much time has passed since lastRenewal
	elapsed := currentTime.Sub(lastRenewal.Time)
	return elapsed.Seconds() > sveltosCluster.Spec.TokenRequestRenewalOption.RenewTokenRequestInterval.Seconds()
}

func (r *SveltosClusterReconciler) handleTokenRequestRenewal(ctx context.Context,
	sveltosClusterScope *scope.SveltosClusterScope, remoteConfig *rest.Config) error {

	sveltosCluster := sveltosClusterScope.SveltosCluster

	if sveltosCluster.Spec.TokenRequestRenewalOption != nil {
		logger := sveltosClusterScope.Logger

		saExpirationInSecond := sveltosCluster.Spec.TokenRequestRenewalOption.RenewTokenRequestInterval.Duration.Seconds()
		// Minimum duration for a TokenRequest is 10 minutes. SveltosCluster reconciler always set the expiration to be
		// sveltosCluster.Spec.TokenRequestRenewalOption.RenewTokenRequestInterval plus 30 minutes. That will also allow
		// reconciler to renew it again before it current tokenRequest expires
		const secondsToAddToTokenRequest = 30 * 60 // 30 minutes
		saExpirationInSecond += float64(secondsToAddToTokenRequest)

		data, err := clusterproxy.GetSveltosSecretData(ctx, logger, r.Client,
			sveltosCluster.Namespace, sveltosCluster.Name)
		if err != nil {
			logger.V(logs.LogInfo).Info(fmt.Sprintf("failed to get Secret with Kubeconfig: %v", err))
			return err
		}

		var u *unstructured.Unstructured
		u, err = utils.GetUnstructured(data)
		if err != nil {
			logger.V(logs.LogInfo).Info(fmt.Sprintf("failed to get unstructured %v", err))
			return err
		}

		config := &apiv1.Config{}
		err = runtime.DefaultUnstructuredConverter.
			FromUnstructured(u.UnstructuredContent(), config)
		if err != nil {
			logger.V(logs.LogInfo).Info(fmt.Sprintf("failed to get convert unstructured to v1.Config %v", err))
			return err
		}

		for i := range config.Contexts {
			cc := &config.Contexts[i]
			namespace := cc.Context.Namespace
			user := cc.Context.AuthInfo

			tokenRequest, err := r.getServiceAccountTokenRequest(ctx, remoteConfig, namespace, user, saExpirationInSecond, logger)
			if err != nil {
				logger.V(logs.LogInfo).Info(fmt.Sprintf("failed to get tokenRequest %v", err))
				continue
			}

			logger.V(logs.LogDebug).Info("Get Kubeconfig from TokenRequest")
			data := r.getKubeconfigFromToken(namespace, user, tokenRequest.Token, remoteConfig)
			err = clusterproxy.UpdateSveltosSecretData(ctx, logger, r.Client, sveltosCluster.Namespace, sveltosCluster.Name, data)
			if err != nil {
				logger.V(logs.LogInfo).Info(fmt.Sprintf("failed to update SveltosCluster's Secret %v", err))
				continue
			}

			sveltosCluster.Status.LastReconciledTokenRequestAt = time.Now().Format(time.RFC3339)
		}
	}

	return nil
}

// getServiceAccountTokenRequest returns token for a serviceaccount
func (r *SveltosClusterReconciler) getServiceAccountTokenRequest(ctx context.Context, remoteConfig *rest.Config,
	serviceAccountNamespace, serviceAccountName string, saExpirationInSecond float64, logger logr.Logger,
) (*authenticationv1.TokenRequestStatus, error) {

	expiration := int64(saExpirationInSecond)

	treq := &authenticationv1.TokenRequest{
		Spec: authenticationv1.TokenRequestSpec{
			ExpirationSeconds: &expiration,
		},
	}

	clientset, err := kubernetes.NewForConfig(remoteConfig)
	if err != nil {
		return nil, err
	}

	logger.V(logs.LogDebug).Info(
		fmt.Sprintf("Create Token for ServiceAccount %s/%s", serviceAccountNamespace, serviceAccountName))
	var tokenRequest *authenticationv1.TokenRequest
	tokenRequest, err = clientset.CoreV1().ServiceAccounts(serviceAccountNamespace).
		CreateToken(ctx, serviceAccountName, treq, metav1.CreateOptions{})
	if err != nil {
		logger.V(logs.LogDebug).Info(
			fmt.Sprintf("Failed to create token for ServiceAccount %s/%s: %v",
				serviceAccountNamespace, serviceAccountName, err))
		return nil, err
	}

	return &tokenRequest.Status, nil
}

// getKubeconfigFromToken returns Kubeconfig to access management cluster from token.
func (r *SveltosClusterReconciler) getKubeconfigFromToken(namespace, serviceAccountName, token string,
	remoteConfig *rest.Config) string {

	template := `apiVersion: v1
kind: Config
clusters:
- name: local
  cluster:
    server: %s
    certificate-authority-data: "%s"
users:
- name: %s
  user:
    token: %s
contexts:
- name: sveltos-context
  context:
    cluster: local
    namespace: %s
    user: %s
current-context: sveltos-context`

	data := fmt.Sprintf(template, remoteConfig.Host,
		base64.StdEncoding.EncodeToString(remoteConfig.CAData), serviceAccountName, token, namespace, serviceAccountName)

	return data
}
