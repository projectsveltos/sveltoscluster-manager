/*
Copyright 2024. projectsveltos.io. All rights reserved.

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
	"encoding/json"
	"fmt"

	"github.com/go-logr/logr"
	lua "github.com/yuin/gopher-lua"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/discovery"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/restmapper"

	libsveltosv1beta1 "github.com/projectsveltos/libsveltos/api/v1beta1"
	sveltoscel "github.com/projectsveltos/libsveltos/lib/cel"
	logs "github.com/projectsveltos/libsveltos/lib/logsettings"
	sveltoslua "github.com/projectsveltos/libsveltos/lib/lua"
)

func runChecks(ctx context.Context, remotConfig *rest.Config, checks []libsveltosv1beta1.ClusterCheck,
	logger logr.Logger) error {

	for i := range checks {
		pass, message, err := runCheck(ctx, remotConfig, &checks[i], logger)
		if err != nil {
			return err
		}
		if !pass {
			logger.V(logs.LogInfo).Info(fmt.Sprintf("cluster check %s failed", checks[i].Name))
			return fmt.Errorf("cluster check %s failed (message: %s)", checks[i].Name, message)
		}
	}

	return nil
}

func runCheck(ctx context.Context, remotConfig *rest.Config, check *libsveltosv1beta1.ClusterCheck,
	logger logr.Logger) (passed bool, message string, err error) {

	var resources []*unstructured.Unstructured
	resources, err = getResources(ctx, remotConfig, check.ResourceSelectors, logger)
	if err != nil {
		return false, "", err
	}

	return validateCheck(check.Condition, resources, logger)
}

// getResources returns resources matching ResourceSelectors.
func getResources(ctx context.Context, remotConfig *rest.Config, resourceSelectors []libsveltosv1beta1.ResourceSelector,
	logger logr.Logger) ([]*unstructured.Unstructured, error) {

	resources := []*unstructured.Unstructured{}
	for i := range resourceSelectors {
		matching, err := getResourcesMatchinResourceSelector(ctx, remotConfig, &resourceSelectors[i], logger)
		if err != nil {
			return nil, err
		}

		resources = append(resources, matching...)
	}

	return resources, nil
}

// getResourcesMatchinResourceSelector returns resources matching ResourceSelector.
func getResourcesMatchinResourceSelector(ctx context.Context, remotConfig *rest.Config,
	resourceSelector *libsveltosv1beta1.ResourceSelector, logger logr.Logger) ([]*unstructured.Unstructured, error) {

	gvk := schema.GroupVersionKind{
		Group:   resourceSelector.Group,
		Version: resourceSelector.Version,
		Kind:    resourceSelector.Kind,
	}

	dc := discovery.NewDiscoveryClientForConfigOrDie(remotConfig)
	groupResources, err := restmapper.GetAPIGroupResources(dc)
	if err != nil {
		return nil, err
	}
	mapper := restmapper.NewDiscoveryRESTMapper(groupResources)

	mapping, err := mapper.RESTMapping(gvk.GroupKind(), gvk.Version)
	if err != nil {
		if meta.IsNoMatchError(err) {
			return nil, nil
		}
		return nil, err
	}

	resourceId := schema.GroupVersionResource{
		Group:    gvk.Group,
		Version:  gvk.Version,
		Resource: mapping.Resource.Resource,
	}

	options := metav1.ListOptions{}

	if len(resourceSelector.LabelFilters) > 0 {
		options.LabelSelector = addLabelFilters(resourceSelector.LabelFilters)
	}

	if resourceSelector.Namespace != "" {
		options.FieldSelector += fmt.Sprintf("metadata.namespace=%s", resourceSelector.Namespace)
	}

	if resourceSelector.Name != "" {
		if options.FieldSelector != "" {
			options.FieldSelector += ","
		}
		options.FieldSelector += fmt.Sprintf("metadata.name=%s", resourceSelector.Name)
	}

	d := dynamic.NewForConfigOrDie(remotConfig)
	var list *unstructured.UnstructuredList
	list, err = d.Resource(resourceId).List(ctx, options)
	if err != nil {
		return nil, err
	}

	logger.V(logs.LogDebug).Info(fmt.Sprintf("found %d resources", len(list.Items)))

	resources := []*unstructured.Unstructured{}
	for i := range list.Items {
		resource := &list.Items[i]
		if !resource.GetDeletionTimestamp().IsZero() {
			continue
		}
		isMatch, err := isMatch(resource, resourceSelector, logger)
		if err != nil {
			return nil, err
		}

		if isMatch {
			resources = append(resources, resource)
		}
	}

	return resources, nil
}

func isMatch(u *unstructured.Unstructured, resourceSelector *libsveltosv1beta1.ResourceSelector,
	logger logr.Logger) (bool, error) {

	var isMatch bool

	isMatch, err := sveltoscel.EvaluateRules(u, resourceSelector.EvaluateCEL, logger)
	if err != nil {
		return false, err
	}

	if isMatch {
		return true, nil
	}

	if resourceSelector.EvaluateCEL != nil && resourceSelector.Evaluate == "" {
		// Before CEL, Sveltos behavior was for a resource to be a match if Evaluate was empty
		// If CEL rules are present and Lua is not defined, treat as non-match (new behavior)
		return false, nil
	}

	return isMatchForLua(u, resourceSelector.Evaluate, logger)
}

func addLabelFilters(labelFilters []libsveltosv1beta1.LabelFilter) string {
	labelFilter := ""
	if len(labelFilters) > 0 {
		for i := range labelFilters {
			if labelFilter != "" {
				labelFilter += ","
			}
			f := labelFilters[i]
			switch f.Operation {
			case libsveltosv1beta1.OperationEqual:
				labelFilter += fmt.Sprintf("%s=%s", f.Key, f.Value)
			case libsveltosv1beta1.OperationDifferent:
				labelFilter += fmt.Sprintf("%s!=%s", f.Key, f.Value)
			case libsveltosv1beta1.OperationHas:
				// Key exists, value is not checked
				labelFilter += f.Key
			case libsveltosv1beta1.OperationDoesNotHave:
				// Key does not exist
				labelFilter += fmt.Sprintf("!%s", f.Key)
			}
		}
	}

	return labelFilter
}

func isMatchForLua(resource *unstructured.Unstructured, script string, logger logr.Logger) (bool, error) {
	if script == "" {
		return true, nil
	}

	l := lua.NewState()
	defer l.Close()

	obj := sveltoslua.MapToTable(resource.UnstructuredContent())

	if err := l.DoString(script); err != nil {
		logger.V(logs.LogInfo).Info(fmt.Sprintf("doString failed: %v", err))
		return false, err
	}

	l.SetGlobal("obj", obj)

	if err := l.CallByParam(lua.P{
		Fn:      l.GetGlobal("evaluate"), // name of Lua function
		NRet:    1,                       // number of returned values
		Protect: true,                    // return err or panic
	}, obj); err != nil {
		logger.V(logs.LogInfo).Info(fmt.Sprintf("failed to evaluate health for resource: %v", err))
		return false, err
	}

	lv := l.Get(-1)
	tbl, ok := lv.(*lua.LTable)
	if !ok {
		logger.V(logs.LogInfo).Info(sveltoslua.LuaTableError)
		return false, fmt.Errorf("%s", sveltoslua.LuaTableError)
	}

	goResult := sveltoslua.ToGoValue(tbl)
	resultJson, err := json.Marshal(goResult)
	if err != nil {
		logger.V(logs.LogInfo).Info(fmt.Sprintf("failed to marshal result: %v", err))
		return false, err
	}

	var result matchStatus
	err = json.Unmarshal(resultJson, &result)
	if err != nil {
		logger.V(logs.LogInfo).Info(fmt.Sprintf("failed to marshal result: %v", err))
		return false, err
	}

	if result.Message != "" {
		logger.V(logs.LogInfo).Info(fmt.Sprintf("message: %s", result.Message))
	}

	logger.V(logs.LogDebug).Info(fmt.Sprintf("is a match: %t", result.Matching))

	return result.Matching, nil
}

func validateCheck(luaScript string, resources []*unstructured.Unstructured,
	logger logr.Logger) (passed bool, message string, err error) {

	if luaScript == "" {
		return true, "", nil
	}

	// Create a new Lua state
	l := lua.NewState()
	defer l.Close()

	// Load the Lua script
	if err := l.DoString(luaScript); err != nil {
		logger.V(logs.LogInfo).Info(fmt.Sprintf("doString failed: %v", err))
		return false, "", err
	}

	// Create an argument table
	argTable := l.NewTable()
	for _, resource := range resources {
		obj := sveltoslua.MapToTable(resource.UnstructuredContent())
		argTable.Append(obj)
	}

	l.SetGlobal("resources", argTable)

	if err := l.CallByParam(lua.P{
		Fn:      l.GetGlobal("evaluate"), // name of Lua function
		NRet:    1,                       // number of returned values
		Protect: true,                    // return err or panic
	}, argTable); err != nil {
		logger.V(logs.LogInfo).Info(fmt.Sprintf("failed to call evaluate function: %s", err.Error()))
		return false, "", err
	}

	lv := l.Get(-1)
	tbl, ok := lv.(*lua.LTable)
	if !ok {
		logger.V(logs.LogInfo).Info(sveltoslua.LuaTableError)
		return false, "", fmt.Errorf("%s", sveltoslua.LuaTableError)
	}

	goResult := sveltoslua.ToGoValue(tbl)
	resultJson, err := json.Marshal(goResult)
	if err != nil {
		logger.V(logs.LogInfo).Info(fmt.Sprintf("failed to marshal result: %v", err))
		return false, "", err
	}

	var result checkStatus
	err = json.Unmarshal(resultJson, &result)
	if err != nil {
		logger.V(logs.LogInfo).Info(fmt.Sprintf("failed to marshal result: %v", err))
		return false, "", err
	}

	if result.Message != "" {
		logger.V(logs.LogInfo).Info(fmt.Sprintf("message: %s", result.Message))
	}

	return result.Pass, result.Message, nil
}
