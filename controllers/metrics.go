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

	"github.com/go-logr/logr"
	"github.com/prometheus/client_golang/prometheus"
	"sigs.k8s.io/controller-runtime/pkg/metrics"

	libsveltosv1beta1 "github.com/projectsveltos/libsveltos/api/v1beta1"
	logs "github.com/projectsveltos/libsveltos/lib/logsettings"
)

const (
	StatusHealthy      = 0.0
	StatusDisconnected = 1.0
)

var (
	clusterConnectivityGauge = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Namespace: "projectsveltos",
			Name:      "cluster_connectivity_status",
			Help:      "Connectivity status of each cluster (0 for healthy, 1 for disconnected)",
		},
		[]string{"cluster_type", "cluster_namespace", "cluster_name"},
	)

	kubernetesVersionGauge = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Namespace: "projectsveltos",
			Name:      "kubernetes_version_info",
			Help:      "Kubernetes version (major.minor.patch) of each cluster",
		},
		[]string{"cluster_type", "cluster_namespace", "cluster_name", "kubernetes_version"},
	)
)

//nolint:gochecknoinits // forced pattern, can't workaround
func init() {
	// Register custom metrics with the global prometheus registry
	metrics.Registry.MustRegister(clusterConnectivityGauge, kubernetesVersionGauge)
}

func updateClusterConnectionStatusMetric(clusterType, namespace, name string, status libsveltosv1beta1.ConnectionStatus, logger logr.Logger) {
	val := StatusDisconnected
	if status == libsveltosv1beta1.ConnectionHealthy {
		val = StatusHealthy
	}

	clusterConnectivityGauge.With(prometheus.Labels{
		"cluster_type":      clusterType,
		"cluster_namespace": namespace,
		"cluster_name":      name,
	}).Set(val)

	logger.V(logs.LogVerbose).Info(fmt.Sprintf("Updated connection status metric for cluster %s/%s to %f",
		namespace, name, val))
}

func updateKubernetesVersionMetric(clusterType, namespace, name, version string, logger logr.Logger) {
	kubernetesVersionGauge.With(prometheus.Labels{
		"cluster_type":       clusterType,
		"cluster_namespace":  namespace,
		"cluster_name":       name,
		"kubernetes_version": version,
	}).Set(1)

	logger.V(logs.LogVerbose).Info(fmt.Sprintf("Updated Kubernetes version metric for cluster %s/%s  %s",
		namespace, name, version))
}
