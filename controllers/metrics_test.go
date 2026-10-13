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

package controllers_test

import (
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"k8s.io/klog/v2/textlogger"

	libsveltosv1beta1 "github.com/projectsveltos/libsveltos/api/v1beta1"
	"github.com/projectsveltos/sveltoscluster-manager/controllers"
)

var _ = Describe("metrics", func() {
	var clusterType, namespace, name string
	var logger = textlogger.NewLogger(textlogger.NewConfig(textlogger.Verbosity(1)))

	BeforeEach(func() {
		clusterType = randomString()
		namespace = randomString()
		name = randomString()
	})

	It("updateClusterConnectionStatusMetric sets healthy and down values", func() {
		controllers.UpdateClusterConnectionStatusMetric(clusterType, namespace, name,
			libsveltosv1beta1.ConnectionHealthy, logger)
		gauge := controllers.ClusterConnectivityGauge.With(prometheus.Labels{
			"cluster_type": clusterType, "cluster_namespace": namespace, "cluster_name": name,
		})
		Expect(testutil.ToFloat64(gauge)).To(Equal(controllers.StatusHealthy))

		controllers.UpdateClusterConnectionStatusMetric(clusterType, namespace, name,
			libsveltosv1beta1.ConnectionDown, logger)
		Expect(testutil.ToFloat64(gauge)).To(Equal(controllers.StatusDisconnected))
	})

	It("updateKubernetesVersionMetric replaces the previous version label", func() {
		oldLabels := prometheus.Labels{
			"cluster_type": clusterType, "cluster_namespace": namespace, "cluster_name": name,
			"kubernetes_version": "v1.29.0",
		}
		newLabels := prometheus.Labels{
			"cluster_type": clusterType, "cluster_namespace": namespace, "cluster_name": name,
			"kubernetes_version": "v1.30.0",
		}

		controllers.UpdateKubernetesVersionMetric(clusterType, namespace, name, "v1.29.0", logger)
		Expect(testutil.ToFloat64(controllers.KubernetesVersionGauge.With(oldLabels))).To(Equal(float64(1)))

		controllers.UpdateKubernetesVersionMetric(clusterType, namespace, name, "v1.30.0", logger)
		Expect(testutil.ToFloat64(controllers.KubernetesVersionGauge.With(newLabels))).To(Equal(float64(1)))
		Expect(testutil.ToFloat64(controllers.KubernetesVersionGauge.With(oldLabels))).To(Equal(float64(0)))
	})

	It("updateConnectionFailuresMetric records the failure count", func() {
		controllers.UpdateConnectionFailuresMetric(clusterType, namespace, name, 3, logger)
		gauge := controllers.ConnectionFailuresGauge.With(prometheus.Labels{
			"cluster_type": clusterType, "cluster_namespace": namespace, "cluster_name": name,
		})
		Expect(testutil.ToFloat64(gauge)).To(Equal(float64(3)))
	})

	It("updateAgentLastHeartbeatMetric records the heartbeat unix timestamp", func() {
		reportTime := time.Now().Truncate(time.Second)
		controllers.UpdateAgentLastHeartbeatMetric(clusterType, namespace, name, reportTime, logger)
		gauge := controllers.AgentLastHeartbeatTimestampGauge.With(prometheus.Labels{
			"cluster_type": clusterType, "cluster_namespace": namespace, "cluster_name": name,
		})
		Expect(testutil.ToFloat64(gauge)).To(Equal(float64(reportTime.Unix())))
	})

	It("deleteClusterMetrics removes previously set values", func() {
		controllers.UpdateConnectionFailuresMetric(clusterType, namespace, name, 5, logger)
		gauge := controllers.ConnectionFailuresGauge.With(prometheus.Labels{
			"cluster_type": clusterType, "cluster_namespace": namespace, "cluster_name": name,
		})
		Expect(testutil.ToFloat64(gauge)).To(Equal(float64(5)))

		controllers.DeleteClusterMetrics(clusterType, namespace, name, logger)
		freshGauge := controllers.ConnectionFailuresGauge.With(prometheus.Labels{
			"cluster_type": clusterType, "cluster_namespace": namespace, "cluster_name": name,
		})
		Expect(testutil.ToFloat64(freshGauge)).To(Equal(float64(0)))
	})
})
