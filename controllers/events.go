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

package controllers

import (
	"fmt"

	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/events"

	libsveltosv1beta1 "github.com/projectsveltos/libsveltos/api/v1beta1"
	"github.com/projectsveltos/libsveltos/lib/eventreasons"
	"github.com/projectsveltos/libsveltos/lib/pullmode"
)

// recordEventf records an event on regarding. It does nothing when no recorder is set.
// The note is capped, so an oversized message is not rejected by the API server.
func recordEventf(recorder events.EventRecorder, regarding runtime.Object, eventType, reason, action,
	format string, args ...any) {

	if recorder == nil {
		return
	}

	note := eventreasons.TruncateNote(fmt.Sprintf(format, args...))
	recorder.Eventf(regarding, nil, eventType, reason, action, "%s", note)
}

// recordConnectionTransition records an event when the connection status of the SveltosCluster
// has just changed: to down (the cluster cannot be reached or its agent is not reporting) or from
// down to healthy (recovered). Nothing is recorded while the status stays the same, so a cluster
// that remains down raises a single event. The previous status is the one persisted in
// SveltosCluster.Status, so this also holds across restarts of the controller.
func (r *SveltosClusterReconciler) recordConnectionTransition(sveltosCluster *libsveltosv1beta1.SveltosCluster,
	previousStatus libsveltosv1beta1.ConnectionStatus) {

	currentStatus := sveltosCluster.Status.ConnectionStatus

	switch {
	case previousStatus != libsveltosv1beta1.ConnectionDown && currentStatus == libsveltosv1beta1.ConnectionDown:
		reason := eventreasons.ClusterUnreachable
		if sveltosCluster.Spec.PullMode {
			reason = eventreasons.AgentUnhealthy
		}
		recordEventf(r.EventRecorder, sveltosCluster, "Warning", reason, eventreasons.ActionConnecting,
			"cluster %s/%s: %s", sveltosCluster.Namespace, sveltosCluster.Name, getFailureMessage(sveltosCluster))
	case previousStatus == libsveltosv1beta1.ConnectionDown && currentStatus != libsveltosv1beta1.ConnectionDown:
		message := "connection is healthy again"
		if sveltosCluster.Spec.PullMode {
			message = "agent is reporting again"
		}
		recordEventf(r.EventRecorder, sveltosCluster, "Normal", eventreasons.ClusterRecovered, eventreasons.ActionConnecting,
			"cluster %s/%s: %s", sveltosCluster.Namespace, sveltosCluster.Name, message)
	}
}

// getFailureMessage returns the failure message of the SveltosCluster. In pull mode a heartbeat
// timeout is described as an agent problem.
func getFailureMessage(sveltosCluster *libsveltosv1beta1.SveltosCluster) string {
	if sveltosCluster.Status.FailureMessage == nil {
		return "unknown failure"
	}

	if pullmode.IsAgentTimeoutError(sveltosCluster) {
		return "agent has not reported for longer than the maximum allowed time"
	}

	return *sveltosCluster.Status.FailureMessage
}
