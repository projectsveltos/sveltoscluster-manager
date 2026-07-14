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
	"fmt"
	"time"

	"github.com/go-logr/logr"
	"github.com/prometheus/client_golang/prometheus"
	"sigs.k8s.io/controller-runtime/pkg/metrics"

	libsveltosv1beta1 "github.com/projectsveltos/libsveltos/api/v1beta1"
	logs "github.com/projectsveltos/libsveltos/lib/logsettings"
)

const (
	// metricNamespace prefixes every custom metric in this file, purely to avoid name collisions with
	// unrelated tools scraped by the same Prometheus instance. Deliberately a fixed literal rather than
	// the Kubernetes namespace this component happens to be deployed into: that's user-configurable per
	// install, so a fixed prefix keeps dashboards/alerts consistent across every install regardless.
	metricNamespace = "projectsveltos"

	StatusHealthy      = 0.0
	StatusDisconnected = 1.0

	labelClusterType       = "cluster_type"
	labelClusterNamespace  = "cluster_namespace"
	labelClusterName       = "cluster_name"
	labelKubernetesVersion = "kubernetes_version"
)

var (
	clusterConnectivityGauge = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Namespace: metricNamespace,
			Name:      "cluster_connectivity_status",
			Help:      "Connectivity status of each cluster (0 for healthy, 1 for disconnected)",
		},
		[]string{labelClusterType, labelClusterNamespace, labelClusterName},
	)

	// kubernetesVersionGauge uses the "info metric" pattern: value is always 1, the actual data is
	// carried in the kubernetes_version label. updateKubernetesVersionMetric always clears any prior
	// entry for the cluster before setting the current one, so a version upgrade doesn't leave the old
	// version's row behind alongside the new one.
	kubernetesVersionGauge = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Namespace: metricNamespace,
			Name:      "kubernetes_version_info",
			Help:      "Kubernetes version (major.minor.patch) of each cluster",
		},
		[]string{labelClusterType, labelClusterNamespace, labelClusterName, labelKubernetesVersion},
	)

	// connectionFailuresGauge mirrors SveltosCluster.status.connectionFailures. A gauge, not a counter:
	// updateConnectionStatus resets it to 0 on the next healthy check, so it answers "is this cluster
	// stuck failing connectivity checks right now," unlike a plain counter which would only accumulate.
	connectionFailuresGauge = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Namespace: metricNamespace,
			Name:      "connection_failures",
			Help:      "Number of consecutive connectivity check failures for a cluster; reset to 0 on the next successful check",
		},
		[]string{labelClusterType, labelClusterNamespace, labelClusterName},
	)

	// agentLastHeartbeatTimestampGauge records the Unix timestamp of the last heartbeat received from a
	// pull-mode cluster's sveltos-agent (SveltosCluster.status.agentLastReportTime). Distinct from
	// cluster_connectivity_status: heartbeat staleness is specific to the pull-mode agent-push mechanism,
	// not general management-cluster-to-workload-cluster API connectivity. Query
	// time() - projectsveltos_agent_last_heartbeat_timestamp_seconds for "seconds since last heartbeat."
	agentLastHeartbeatTimestampGauge = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Namespace: metricNamespace,
			Name:      "agent_last_heartbeat_timestamp_seconds",
			Help:      "Unix timestamp of the last heartbeat received from a pull-mode cluster's sveltos-agent",
		},
		[]string{labelClusterType, labelClusterNamespace, labelClusterName},
	)
)

//nolint:gochecknoinits // forced pattern, can't workaround
func init() {
	// Register custom metrics with the global prometheus registry
	metrics.Registry.MustRegister(clusterConnectivityGauge, kubernetesVersionGauge, connectionFailuresGauge,
		agentLastHeartbeatTimestampGauge)
}

func updateClusterConnectionStatusMetric(clusterType, namespace, name string, status libsveltosv1beta1.ConnectionStatus, logger logr.Logger) {
	val := StatusDisconnected
	if status == libsveltosv1beta1.ConnectionHealthy {
		val = StatusHealthy
	}

	clusterConnectivityGauge.With(prometheus.Labels{
		labelClusterType:      clusterType,
		labelClusterNamespace: namespace,
		labelClusterName:      name,
	}).Set(val)

	logger.V(logs.LogVerbose).Info(fmt.Sprintf("Updated connection status metric for cluster %s/%s to %f",
		namespace, name, val))
}

func updateKubernetesVersionMetric(clusterType, namespace, name, version string, logger logr.Logger) {
	// Clear any prior entry for this cluster first. Without this, a version upgrade would leave the
	// old version's row in place alongside the new one, since the version is part of the label set.
	kubernetesVersionGauge.DeletePartialMatch(prometheus.Labels{
		labelClusterType:      clusterType,
		labelClusterNamespace: namespace,
		labelClusterName:      name,
	})

	kubernetesVersionGauge.With(prometheus.Labels{
		labelClusterType:       clusterType,
		labelClusterNamespace:  namespace,
		labelClusterName:       name,
		labelKubernetesVersion: version,
	}).Set(1)

	logger.V(logs.LogVerbose).Info(fmt.Sprintf("Updated Kubernetes version metric for cluster %s/%s  %s",
		namespace, name, version))
}

func updateConnectionFailuresMetric(clusterType, namespace, name string, failures int, logger logr.Logger) {
	connectionFailuresGauge.With(prometheus.Labels{
		labelClusterType:      clusterType,
		labelClusterNamespace: namespace,
		labelClusterName:      name,
	}).Set(float64(failures))

	logger.V(logs.LogVerbose).Info(fmt.Sprintf("Updated connection failures metric for cluster %s/%s to %d",
		namespace, name, failures))
}

func updateAgentLastHeartbeatMetric(clusterType, namespace, name string, lastReportTime time.Time, logger logr.Logger) {
	agentLastHeartbeatTimestampGauge.With(prometheus.Labels{
		labelClusterType:      clusterType,
		labelClusterNamespace: namespace,
		labelClusterName:      name,
	}).Set(float64(lastReportTime.Unix()))

	logger.V(logs.LogVerbose).Info(fmt.Sprintf("Updated agent last heartbeat metric for cluster %s/%s: %s",
		namespace, name, lastReportTime))
}

// deleteClusterMetrics removes all metrics for a cluster. Called when a SveltosCluster is deleted, so a
// deregistered cluster's last-known status doesn't persist in Prometheus indefinitely, indistinguishable
// from a cluster that's still registered and simply hasn't reconciled recently.
func deleteClusterMetrics(clusterType, namespace, name string, logger logr.Logger) {
	labels := prometheus.Labels{
		labelClusterType:      clusterType,
		labelClusterNamespace: namespace,
		labelClusterName:      name,
	}

	clusterConnectivityGauge.Delete(labels)
	kubernetesVersionGauge.DeletePartialMatch(labels)
	connectionFailuresGauge.Delete(labels)
	agentLastHeartbeatTimestampGauge.Delete(labels)

	logger.V(logs.LogVerbose).Info(fmt.Sprintf("Deleted metrics for cluster %s/%s", namespace, name))
}
