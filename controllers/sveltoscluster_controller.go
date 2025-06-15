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
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/Masterminds/semver"
	"github.com/go-logr/logr"
	"github.com/pkg/errors"
	"github.com/robfig/cron/v3"
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
	"github.com/projectsveltos/libsveltos/lib/k8s_utils"
	logs "github.com/projectsveltos/libsveltos/lib/logsettings"
	"github.com/projectsveltos/libsveltos/lib/sharding"
	"github.com/projectsveltos/sveltoscluster-manager/pkg/scope"
)

const (
	// normalRequeueAfter is how long to wait before checking again to see if the cluster can be moved
	// to ready after or workload features (for instance ingress or reporter) have failed
	normalRequeueAfter = 10 * time.Second

	versionLabel = "projectsveltos.io/k8s-version"
)

// SveltosClusterReconciler reconciles a SveltosCluster object
type SveltosClusterReconciler struct {
	client.Client
	rest.Config
	Scheme               *runtime.Scheme
	ConcurrentReconciles int
	ShardKey             string // when set, only clusters matching the ShardKey will be reconciled
}

type matchStatus struct {
	Matching bool   `json:"matching"`
	Message  string `json:"message"`
}

type checkStatus struct {
	Pass    bool   `json:"pass"`
	Message string `json:"message"`
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
	// Periodically reconcile. We need to keep evaluating connectivity
	return reconcile.Result{Requeue: true, RequeueAfter: time.Minute}, nil
}

func (r *SveltosClusterReconciler) reconcileNormal(
	ctx context.Context,
	sveltosClusterScope *scope.SveltosClusterScope,
) {

	logger := sveltosClusterScope.Logger
	logger.V(logs.LogInfo).Info("Reconciling SveltosCluster")

	defer handleAutomaticPauseUnPause(sveltosClusterScope.SveltosCluster, time.Now(), logger)

	if sveltosClusterScope.SveltosCluster.Spec.PullMode {
		r.reconcilePullModeCluster(sveltosClusterScope, logger)
		return
	}

	s := runtime.NewScheme()
	if err := clientgoscheme.AddToScheme(s); err != nil {
		errorMessage := err.Error()
		logger.V(logs.LogInfo).Info(fmt.Sprintf("failed to get scheme: %v", err))
		sveltosClusterScope.SveltosCluster.Status.FailureMessage = &errorMessage
		updateConnectionStatus(sveltosClusterScope, logger)
		return
	}

	// Get managed cluster rest.Config
	config, err := clusterproxy.GetSveltosKubernetesRestConfig(ctx, logger, r.Client,
		sveltosClusterScope.SveltosCluster.Namespace, sveltosClusterScope.SveltosCluster.Name)
	if err != nil {
		errorMessage := err.Error()
		logger.V(logs.LogInfo).Info(fmt.Sprintf("failed to get client: %v", err))
		sveltosClusterScope.SveltosCluster.Status.FailureMessage = &errorMessage
		updateConnectionStatus(sveltosClusterScope, logger)
		return
	}

	// Get managed cluster client
	var c client.Client
	c, err = client.New(config, client.Options{Scheme: s})
	if err != nil {
		errorMessage := err.Error()
		logger.V(logs.LogInfo).Info(fmt.Sprintf("failed to get client: %v", err))
		sveltosClusterScope.SveltosCluster.Status.FailureMessage = &errorMessage
		updateConnectionStatus(sveltosClusterScope, logger)
		return
	}

	logger.V(logs.LogInfo).Info("got client")
	sveltosClusterScope.SveltosCluster.Status.FailureMessage = nil

	ns := &corev1.Namespace{}
	err = c.Get(context.TODO(), types.NamespacedName{Name: "kube-system"}, ns)
	if err != nil && !apierrors.IsNotFound(err) {
		errorMessage := err.Error()
		logger.V(logs.LogInfo).Info(fmt.Sprintf("failed to get kube-system namespace: %v", err))
		sveltosClusterScope.SveltosCluster.Status.FailureMessage = &errorMessage
	} else {
		err = r.runChecks(ctx, config, sveltosClusterScope.SveltosCluster, logger)
		if err != nil {
			errorMessage := err.Error()
			sveltosClusterScope.SveltosCluster.Status.FailureMessage = &errorMessage
		} else {
			sveltosClusterScope.SveltosCluster.Status.Ready = true
			currentVersion, err := k8s_utils.GetKubernetesVersion(ctx, config, logger)
			if err != nil {
				logger.V(logs.LogInfo).Info(fmt.Sprintf("failed to get cluster kubernetes version %v", err))
				errorMessage := err.Error()
				sveltosClusterScope.SveltosCluster.Status.FailureMessage = &errorMessage
			} else {
				currentSemVersion, err := semver.NewVersion(currentVersion)
				if err != nil {
					logger.Error(err, "failed to get semver for current version %s", currentVersion)
				} else {
					kubernetesVersion := fmt.Sprintf("v%d.%d.%d", currentSemVersion.Major(), currentSemVersion.Minor(), currentSemVersion.Patch())
					sveltosClusterScope.SetLabel(versionLabel,
						kubernetesVersion)
					updateKubernetesVersionMetric(string(libsveltosv1beta1.ClusterTypeSveltos), sveltosClusterScope.SveltosCluster.Namespace,
						sveltosClusterScope.SveltosCluster.Name, kubernetesVersion, logger)
				}
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

	updateConnectionStatus(sveltosClusterScope, logger)
	updateClusterConnectionStatusMetric(string(libsveltosv1beta1.ClusterTypeSveltos), sveltosClusterScope.SveltosCluster.Namespace,
		sveltosClusterScope.SveltosCluster.Name, sveltosClusterScope.SveltosCluster.Status.ConnectionStatus, logger)
}

func updateConnectionStatus(sveltosClusterScope *scope.SveltosClusterScope, logger logr.Logger) {
	if sveltosClusterScope.SveltosCluster.Status.FailureMessage != nil {
		logger.V(logs.LogDebug).Info("increasing connectionFailures")
		sveltosClusterScope.SveltosCluster.Status.ConnectionFailures++
		if sveltosClusterScope.SveltosCluster.Status.ConnectionFailures >= sveltosClusterScope.SveltosCluster.Spec.ConsecutiveFailureThreshold {
			logger.V(logs.LogDebug).Info("connectionFailures is higher than consecutiveFailureThreshold. Set connectionStatus to down")
			sveltosClusterScope.SveltosCluster.Status.ConnectionStatus = libsveltosv1beta1.ConnectionDown
		}
	} else {
		sveltosClusterScope.SveltosCluster.Status.ConnectionStatus = libsveltosv1beta1.ConnectionHealthy
		sveltosClusterScope.SveltosCluster.Status.ConnectionFailures = 0
	}
}

// SetupWithManager sets up the controller with the Manager.
func (r *SveltosClusterReconciler) SetupWithManager(mgr ctrl.Manager) error {
	_, err := ctrl.NewControllerManagedBy(mgr).
		For(&libsveltosv1beta1.SveltosCluster{}).
		WithEventFilter(SveltosClusterPredicates(ctrl.Log)).
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

	renewalInterval := sveltosCluster.Spec.TokenRequestRenewalOption.RenewTokenRequestInterval.Seconds()
	// Calculate the time 10 minutes before the renewal interval
	const tenMinutes = 10 * 60
	var renewalThreshold time.Duration
	if renewalInterval > tenMinutes {
		renewalThreshold = time.Duration(renewalInterval-tenMinutes) * time.Second
	} else {
		renewalThreshold = time.Duration(renewalInterval) * time.Second
	}

	// Calculate how much time has passed since lastRenewal
	elapsed := currentTime.Sub(lastRenewal.Time)
	return elapsed.Seconds() > renewalThreshold.Seconds()
}

// getEffectiveTokenDuration returns the token duration to use when requesting a ServiceAccount token.
// If TokenDuration is explicitly set, it is used. Otherwise, the fallback is RenewTokenRequestInterval.
// This ensures a default behavior even when TokenDuration is omitted.
func (r *SveltosClusterReconciler) getEffectiveTokenDurationInSecond(
	opt *libsveltosv1beta1.TokenRequestRenewalOption) float64 {

	if opt.TokenDuration.Duration == 0 {
		// Minimum duration for a TokenRequest is 10 minutes. SveltosCluster reconciler always set the expiration to be
		// sveltosCluster.Spec.TokenRequestRenewalOption.RenewTokenRequestInterval plus 30 minutes. That will also allow
		// reconciler to renew it again before it current tokenRequest expires
		const secondsToAddToTokenRequest = 30 * 60 // 30 minutes
		saExpirationInSecond := opt.RenewTokenRequestInterval.Duration.Seconds()
		saExpirationInSecond += float64(secondsToAddToTokenRequest)
		return saExpirationInSecond
	}
	return opt.TokenDuration.Duration.Seconds()
}

func (r *SveltosClusterReconciler) handleTokenRequestRenewal(ctx context.Context,
	sveltosClusterScope *scope.SveltosClusterScope, remoteConfig *rest.Config) error {

	sveltosCluster := sveltosClusterScope.SveltosCluster

	if sveltosCluster.Spec.TokenRequestRenewalOption != nil {
		logger := sveltosClusterScope.Logger

		saExpirationInSecond := r.getEffectiveTokenDurationInSecond(sveltosCluster.Spec.TokenRequestRenewalOption)

		data, err := clusterproxy.GetSveltosSecretData(ctx, logger, r.Client,
			sveltosCluster.Namespace, sveltosCluster.Name)
		if err != nil {
			logger.V(logs.LogInfo).Info(fmt.Sprintf("failed to get Secret with Kubeconfig: %v", err))
			return err
		}

		var u *unstructured.Unstructured
		u, err = k8s_utils.GetUnstructured(data)
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
			saNamespace := cc.Context.Namespace
			saName := cc.Context.AuthInfo

			if sveltosCluster.Spec.TokenRequestRenewalOption.SANamespace != "" && sveltosCluster.Spec.TokenRequestRenewalOption.SAName != "" {
				saNamespace = sveltosCluster.Spec.TokenRequestRenewalOption.SANamespace
				saName = sveltosCluster.Spec.TokenRequestRenewalOption.SAName
			}

			tokenRequest, err := r.getServiceAccountTokenRequest(ctx, remoteConfig, saNamespace, saName, saExpirationInSecond, logger)
			if err != nil {
				logger.V(logs.LogInfo).Info(fmt.Sprintf("failed to get tokenRequest %v", err))
				continue
			}

			sveltosCluster.Spec.TokenRequestRenewalOption.RenewTokenRequestInterval =
				r.adjustTokenRequestRenewalOption(sveltosCluster.Spec.TokenRequestRenewalOption, tokenRequest, logger)

			logger.V(logs.LogDebug).Info("Get Kubeconfig from TokenRequest")
			key := "re-kubeconfig"
			data := r.getKubeconfigFromToken(saNamespace, saName, tokenRequest.Token, remoteConfig)
			err = clusterproxy.UpdateSveltosSecretData(ctx, logger, r.Client, sveltosCluster.Namespace, sveltosCluster.Name, data, key)
			if err != nil {
				logger.V(logs.LogInfo).Info(fmt.Sprintf("failed to update SveltosCluster's Secret %v", err))
				continue
			}

			sveltosCluster.Spec.KubeconfigKeyName = key
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

func (r *SveltosClusterReconciler) getTokenExpiration(jwt string) (time.Time, error) {
	parts := strings.Split(jwt, ".")
	//nolint: mnd // expected format
	if len(parts) != 3 {
		return time.Time{}, fmt.Errorf("invalid JWT format")
	}

	payloadBase64 := parts[1]
	payloadBytes, err := base64.RawURLEncoding.DecodeString(payloadBase64)
	if err != nil {
		return time.Time{}, fmt.Errorf("failed to decode payload: %w", err)
	}

	var payload map[string]interface{}
	if err := json.Unmarshal(payloadBytes, &payload); err != nil {
		return time.Time{}, fmt.Errorf("failed to unmarshal payload: %w", err)
	}

	exp, ok := payload["exp"].(float64)
	if !ok {
		return time.Time{}, fmt.Errorf("expiration time not found in payload")
	}

	expirationTime := time.Unix(int64(exp), 0)
	return expirationTime, nil
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

func handleAutomaticPauseUnPause(sveltosCluster *libsveltosv1beta1.SveltosCluster,
	currentTime time.Time, logger logr.Logger) {

	if sveltosCluster.Spec.ActiveWindow == nil {
		sveltosCluster.Status.NextUnpause = nil
		sveltosCluster.Status.NextPause = nil
		return
	}

	if sveltosCluster.Status.NextPause != nil && sveltosCluster.Status.NextUnpause != nil {
		if currentTime.After(sveltosCluster.Status.NextUnpause.Time) &&
			currentTime.Before(sveltosCluster.Status.NextPause.Time) {

			sveltosCluster.Spec.Paused = false
			return
		} else if currentTime.Before(sveltosCluster.Status.NextUnpause.Time) {
			sveltosCluster.Spec.Paused = true
			return
		} else if currentTime.After(sveltosCluster.Status.NextPause.Time) {
			sveltosCluster.Spec.Paused = true
		} else if currentTime.Before(sveltosCluster.Status.NextPause.Time) {
			// Updates NextFrom and NextTo only once current time is past NextTo
			return
		}
	} else {
		sveltosCluster.Spec.Paused = true
	}

	if sveltosCluster.Status.NextUnpause == nil || currentTime.After(sveltosCluster.Status.NextUnpause.Time) {
		lastRunTime := sveltosCluster.CreationTimestamp
		if sveltosCluster.Status.NextUnpause != nil {
			lastRunTime = *sveltosCluster.Status.NextUnpause
		}

		nextFromTime, err := getNextScheduleTime(sveltosCluster.Spec.ActiveWindow.From, &lastRunTime, currentTime)
		if err != nil {
			logger.V(logs.LogInfo).Error(err, "failed to get next from time")
			return
		}
		sveltosCluster.Status.NextUnpause = &metav1.Time{Time: *nextFromTime}
	}

	if sveltosCluster.Status.NextPause == nil || currentTime.After(sveltosCluster.Status.NextPause.Time) {
		lastRunTime := sveltosCluster.CreationTimestamp
		if sveltosCluster.Status.NextPause != nil {
			lastRunTime = *sveltosCluster.Status.NextPause
		}

		nextToTime, err := getNextScheduleTime(sveltosCluster.Spec.ActiveWindow.To, &lastRunTime, currentTime)
		if err != nil {
			logger.V(logs.LogInfo).Error(err, "failed to get next to time")
		}

		sveltosCluster.Status.NextPause = &metav1.Time{Time: *nextToTime}
	}
}

// getNextScheduleTime gets the time of next schedule after last scheduled and before now
func getNextScheduleTime(schedule string, lastRunTime *metav1.Time, now time.Time) (*time.Time, error) {
	sched, err := cron.ParseStandard(schedule)
	if err != nil {
		return nil, fmt.Errorf("unparseable schedule %q: %w", schedule, err)
	}

	if lastRunTime == nil {
		return nil, fmt.Errorf("last run time must be specified")
	}

	starts := 0
	for t := sched.Next(lastRunTime.Time); t.Before(now); t = sched.Next(t) {
		const maxNumberOfFailures = 100
		starts++
		if starts > maxNumberOfFailures {
			return nil,
				fmt.Errorf("too many missed start times (> %d). Set or check clock skew",
					maxNumberOfFailures)
		}
	}

	next := sched.Next(now)
	return &next, nil
}

func (r *SveltosClusterReconciler) adjustTokenRequestRenewalOption(
	tokenRequestRenewalOption *libsveltosv1beta1.TokenRequestRenewalOption,
	tokenRequest *authenticationv1.TokenRequestStatus, logger logr.Logger) metav1.Duration {

	jwt := tokenRequest.Token

	expirationTime, err := r.getTokenExpiration(jwt)
	if err != nil {
		logger.V(logs.LogInfo).Info(fmt.Sprintf("failed to get token expiration: %v", err))
		return tokenRequestRenewalOption.RenewTokenRequestInterval
	}

	logger.V(logs.LogDebug).Info(fmt.Sprintf("token expires at: %v\n", expirationTime))

	saExpirationInSecond := tokenRequestRenewalOption.RenewTokenRequestInterval.Duration.Seconds()
	expectedExpiration := time.Now().Add(time.Duration(saExpirationInSecond) * time.Second)

	if expirationTime.Before(expectedExpiration) {
		diff := expectedExpiration.Sub(expirationTime)
		logger.V(logs.LogInfo).Info(
			fmt.Sprintf("Token expiration is shorter than expected by %v. Requested: %v, Actual: %v",
				diff, expectedExpiration, expirationTime))

		actualLifetime := time.Until(expirationTime)
		return metav1.Duration{Duration: actualLifetime}
	}

	return tokenRequestRenewalOption.RenewTokenRequestInterval
}

func (r *SveltosClusterReconciler) runChecks(ctx context.Context, remotConfig *rest.Config,
	sveltosCluster *libsveltosv1beta1.SveltosCluster, logger logr.Logger) error {

	if !sveltosCluster.Status.Ready {
		// Run ReadinessChecks
		return runChecks(ctx, remotConfig, sveltosCluster.Spec.ReadinessChecks, logger)
	}

	// Run LivenessChecks
	return runChecks(ctx, remotConfig, sveltosCluster.Spec.LivenessChecks, logger)
}

func (r *SveltosClusterReconciler) reconcilePullModeCluster(
	sveltosClusterScope *scope.SveltosClusterScope, logger logr.Logger,
) {

	cluster := sveltosClusterScope.SveltosCluster
	if !cluster.Status.Ready {
		return
	}

	if cluster.Status.AgentLastReportTime == nil {
		return
	}

	lastReportAge := time.Since(cluster.Status.AgentLastReportTime.Time)

	maxAllowedAge := 10 * time.Minute

	if lastReportAge > maxAllowedAge {
		msg := "AgentLastReportTime is older than max allowed age."
		sveltosClusterScope.SveltosCluster.Status.FailureMessage = &msg
	}

	updateConnectionStatus(sveltosClusterScope, logger)
	updateClusterConnectionStatusMetric(string(libsveltosv1beta1.ClusterTypeSveltos),
		sveltosClusterScope.SveltosCluster.Namespace, sveltosClusterScope.SveltosCluster.Name,
		sveltosClusterScope.SveltosCluster.Status.ConnectionStatus, logger)
}
