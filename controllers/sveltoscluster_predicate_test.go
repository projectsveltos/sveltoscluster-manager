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
	"github.com/go-logr/logr"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/klog/v2/textlogger"
	"sigs.k8s.io/controller-runtime/pkg/event"

	"github.com/projectsveltos/sveltoscluster-manager/controllers"
)

var _ = Describe("SveltosCluster: Predicates", func() {
	var logger logr.Logger

	BeforeEach(func() {
		logger = textlogger.NewLogger(textlogger.NewConfig(textlogger.Verbosity(1)))
	})

	It("UpdateFunc returns true when SveltosCluster starts being deleted", func() {
		oldCluster := getSveltosClusterInstance(randomString(), randomString())
		newCluster := oldCluster.DeepCopy()
		now := metav1.Now()
		newCluster.DeletionTimestamp = &now

		predicates := controllers.SveltosClusterPredicates(logger)
		Expect(predicates.Update(event.UpdateEvent{ObjectOld: oldCluster, ObjectNew: newCluster})).To(BeTrue())
	})

	It("UpdateFunc returns false when neither Spec nor DeletionTimestamp changed", func() {
		oldCluster := getSveltosClusterInstance(randomString(), randomString())
		newCluster := oldCluster.DeepCopy()

		predicates := controllers.SveltosClusterPredicates(logger)
		Expect(predicates.Update(event.UpdateEvent{ObjectOld: oldCluster, ObjectNew: newCluster})).To(BeFalse())
	})
})
