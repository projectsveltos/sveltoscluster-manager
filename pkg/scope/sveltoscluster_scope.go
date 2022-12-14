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

package scope

import (
	"context"

	"github.com/go-logr/logr"
	"github.com/pkg/errors"
	"sigs.k8s.io/cluster-api/util/patch"
	"sigs.k8s.io/controller-runtime/pkg/client"

	libsveltosv1alpha1 "github.com/projectsveltos/libsveltos/api/v1alpha1"
)

// SveltosClusterScopeParams defines the input parameters used to create a new SveltosCluster Scope.
type SveltosClusterScopeParams struct {
	Client         client.Client
	Logger         logr.Logger
	SveltosCluster *libsveltosv1alpha1.SveltosCluster
	ControllerName string
}

// NewSveltosClusterScope creates a new SveltosCluster Scope from the supplied parameters.
// This is meant to be called for each reconcile iteration.
func NewSveltosClusterScope(params SveltosClusterScopeParams) (*SveltosClusterScope, error) {
	if params.Client == nil {
		return nil, errors.New("client is required when creating a SveltosClusterScope")
	}
	if params.SveltosCluster == nil {
		return nil, errors.New("failed to generate new scope from nil SveltosCluster")
	}

	helper, err := patch.NewHelper(params.SveltosCluster, params.Client)
	if err != nil {
		return nil, errors.Wrap(err, "failed to init patch helper")
	}
	return &SveltosClusterScope{
		Logger:         params.Logger,
		client:         params.Client,
		SveltosCluster: params.SveltosCluster,
		patchHelper:    helper,
		controllerName: params.ControllerName,
	}, nil
}

// SveltosClusterScope defines the basic context for an actuator to operate upon.
type SveltosClusterScope struct {
	logr.Logger
	client         client.Client
	patchHelper    *patch.Helper
	SveltosCluster *libsveltosv1alpha1.SveltosCluster
	controllerName string
}

// PatchObject persists the feature configuration and status.
func (s *SveltosClusterScope) PatchObject(ctx context.Context) error {
	return s.patchHelper.Patch(
		ctx,
		s.SveltosCluster,
	)
}

// Close closes the current scope persisting the sveltoscluster configuration and status.
func (s *SveltosClusterScope) Close(ctx context.Context) error {
	return s.PatchObject(ctx)
}

// Name returns the SveltosCluster name.
func (s *SveltosClusterScope) Name() string {
	return s.SveltosCluster.Name
}

// ControllerName returns the name of the controller that
// created the SveltosClusterScope.
func (s *SveltosClusterScope) ControllerName() string {
	return s.controllerName
}
