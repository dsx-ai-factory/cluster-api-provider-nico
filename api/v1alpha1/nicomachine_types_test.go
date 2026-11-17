// SPDX-FileCopyrightText: Copyright (c) 2026 NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package v1alpha1

import (
	"encoding/json"
	"maps"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
)

func TestNicoMachineNetworkAdmission(t *testing.T) {
	ctx := t.Context()
	environment := &envtest.Environment{
		CRDDirectoryPaths:     []string{filepath.Join("..", "..", "config", "crd", "bases")},
		ErrorIfCRDPathMissing: true,
		UseExistingCluster:    new(false),
	}
	config, err := environment.Start()
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, environment.Stop()) })
	scheme := runtime.NewScheme()
	require.NoError(t, corev1.AddToScheme(scheme))
	require.NoError(t, AddToScheme(scheme))
	c, err := client.New(config, client.Options{Scheme: scheme})
	require.NoError(t, err)
	namespace := &corev1.Namespace{Name: "machine-network"}
	require.NoError(t, c.Create(ctx, namespace))

	subnet := []any{map[string]any{"subnetID": "subnet-1"}}
	cases := []struct {
		name    string
		network map[string]any
		valid   bool
	}{
		{name: "interfaces only", network: map[string]any{"interfaces": subnet}, valid: true},
		{name: "autoNetwork only", network: map[string]any{"autoNetwork": true}, valid: true},
		{name: "both", network: map[string]any{"autoNetwork": true, "interfaces": subnet}},
		{name: "neither", network: map[string]any{}},
		{name: "autoNetwork false without interfaces", network: map[string]any{"autoNetwork": false}},
		{name: "empty interfaces", network: map[string]any{"interfaces": []any{}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			spec := map[string]any{"vpcID": "vpc-1", "machineID": "machine-1"}
			maps.Copy(spec, tc.network)
			object := &unstructured.Unstructured{Object: map[string]any{
				"apiVersion": GroupVersion.String(),
				"kind":       "NicoMachine",
				"metadata":   map[string]any{"generateName": "machine-", "namespace": namespace.Name},
				"spec":       spec,
			}}
			err := c.Create(ctx, object)
			if !tc.valid {
				require.True(t, apierrors.IsInvalid(err), "expected admission rejection, got %v", err)
				return
			}
			require.NoError(t, err)
		})
	}

	// Once providerID is set, autoNetwork is compared by value, not presence: a
	// typed client drops an explicit false because the field is omitempty.
	updates := []struct {
		name   string
		before map[string]any
		after  map[string]any
		valid  bool
	}{
		{name: "explicit false dropped", before: map[string]any{"interfaces": subnet, "autoNetwork": false}, after: map[string]any{"interfaces": subnet}, valid: true},
		{name: "explicit false added", before: map[string]any{"interfaces": subnet}, after: map[string]any{"interfaces": subnet, "autoNetwork": false}, valid: true},
		{name: "autoNetwork turned off", before: map[string]any{"autoNetwork": true}, after: map[string]any{"interfaces": subnet}},
	}
	for _, tc := range updates {
		t.Run("update "+tc.name, func(t *testing.T) {
			provisioned := func(network map[string]any) map[string]any {
				spec := map[string]any{"vpcID": "vpc-1", "machineID": "machine-1", "providerID": "nico://instance-1"}
				maps.Copy(spec, network)
				return spec
			}
			object := &unstructured.Unstructured{Object: map[string]any{
				"apiVersion": GroupVersion.String(),
				"kind":       "NicoMachine",
				"metadata":   map[string]any{"generateName": "machine-", "namespace": namespace.Name},
				"spec":       provisioned(tc.before),
			}}
			require.NoError(t, c.Create(ctx, object))
			object.Object["spec"] = provisioned(tc.after)
			err := c.Update(ctx, object)
			if !tc.valid {
				require.True(t, apierrors.IsInvalid(err), "expected admission rejection, got %v", err)
				require.ErrorContains(t, err, "spec.autoNetwork is immutable after providerID is set")
				return
			}
			require.NoError(t, err)
		})
	}
}

func TestNicoMachineDPUExtensionServiceAdmission(t *testing.T) {
	ctx := t.Context()
	environment := &envtest.Environment{
		CRDDirectoryPaths:     []string{filepath.Join("..", "..", "config", "crd", "bases")},
		ErrorIfCRDPathMissing: true,
		UseExistingCluster:    new(false),
	}
	config, err := environment.Start()
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, environment.Stop()) })
	scheme := runtime.NewScheme()
	require.NoError(t, corev1.AddToScheme(scheme))
	require.NoError(t, AddToScheme(scheme))
	c, err := client.New(config, client.Options{Scheme: scheme})
	require.NoError(t, err)
	namespace := &corev1.Namespace{Name: "machine-dpu-services"}
	require.NoError(t, c.Create(ctx, namespace))

	deployment := func(id, version string) []any {
		return []any{map[string]any{"dpuExtensionServiceID": id, "version": version}}
	}
	object := func(kind string, deployments []any) *unstructured.Unstructured {
		spec := map[string]any{
			"vpcID": "vpc-1", "machineID": "machine-1", "autoNetwork": true,
		}
		if deployments != nil {
			spec["dpuExtensionServiceDeployments"] = deployments
		}
		if kind == "NicoMachineTemplate" {
			spec = map[string]any{"template": map[string]any{"spec": spec}}
		}
		return &unstructured.Unstructured{Object: map[string]any{
			"apiVersion": GroupVersion.String(),
			"kind":       kind,
			"metadata":   map[string]any{"generateName": "dpu-services-", "namespace": namespace.Name},
			"spec":       spec,
		}}
	}

	for _, kind := range []string{"NicoMachine", "NicoMachineTemplate"} {
		for _, tc := range []struct {
			name        string
			deployments []any
			valid       bool
		}{
			{name: "omitted", valid: true},
			{name: "valid", deployments: deployment("service-1", "v1"), valid: true},
			{name: "empty list", deployments: []any{}},
			{name: "empty service ID", deployments: deployment("", "v1")},
			{name: "empty version", deployments: deployment("service-1", "")},
			{name: "missing service ID", deployments: []any{map[string]any{"version": "v1"}}},
			{name: "missing version", deployments: []any{map[string]any{"dpuExtensionServiceID": "service-1"}}},
		} {
			t.Run(kind+"/"+tc.name, func(t *testing.T) {
				err := c.Create(ctx, object(kind, tc.deployments))
				if !tc.valid {
					require.True(t, apierrors.IsInvalid(err), "expected admission rejection, got %v", err)
					return
				}
				require.NoError(t, err)
			})
		}
	}

	for _, tc := range []struct {
		name        string
		before      []any
		after       []any
		provisioned bool
		valid       bool
	}{
		{name: "editable before provisioning", before: deployment("service-1", "v1"), after: deployment("service-1", "v2"), valid: true},
		{name: "unchanged after provisioning", before: deployment("service-1", "v1"), after: deployment("service-1", "v1"), provisioned: true, valid: true},
		{name: "service ID immutable", before: deployment("service-1", "v1"), after: deployment("service-2", "v1"), provisioned: true},
		{name: "version immutable", before: deployment("service-1", "v1"), after: deployment("service-1", "v2"), provisioned: true},
		{name: "cannot add after provisioning", after: deployment("service-1", "v1"), provisioned: true},
		{name: "cannot remove after provisioning", before: deployment("service-1", "v1"), provisioned: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			machine := object("NicoMachine", tc.before)
			require.NoError(t, c.Create(ctx, machine))
			if tc.provisioned {
				require.NoError(t, unstructured.SetNestedField(machine.Object, "nico://instance-1", "spec", "providerID"))
				require.NoError(t, c.Update(ctx, machine))
			}
			if tc.after == nil {
				unstructured.RemoveNestedField(machine.Object, "spec", "dpuExtensionServiceDeployments")
			} else {
				require.NoError(t, unstructured.SetNestedSlice(machine.Object, tc.after, "spec", "dpuExtensionServiceDeployments"))
			}
			err := c.Update(ctx, machine)
			if !tc.valid {
				require.True(t, apierrors.IsInvalid(err), "expected admission rejection, got %v", err)
				require.ErrorContains(t, err, "spec.dpuExtensionServiceDeployments is immutable after providerID is set")
				return
			}
			require.NoError(t, err)
		})
	}
}

func TestNicoMachineStatusTopologyFieldsRoundTrip(t *testing.T) {
	status := NicoMachineStatus{
		MachineID:           "machine-1",
		PrimaryDPUMachineID: "dpu-1",
		SiteID:              "site-1",
		SiteName:            "New York / A",
		VPCID:               "vpc-1",
		VPCName:             "Tenant VPC",
	}

	b, err := json.Marshal(status)
	require.NoError(t, err)

	var got NicoMachineStatus
	require.NoError(t, json.Unmarshal(b, &got))
	require.Equal(t, status, got)
}

func TestNicoMachineStatusFailureDomainRoundTrip(t *testing.T) {
	status := NicoMachineStatus{FailureDomain: "fd-a"}

	b, err := json.Marshal(status)
	require.NoError(t, err)
	require.Contains(t, string(b), `"failureDomain":"fd-a"`)

	var got NicoMachineStatus
	require.NoError(t, json.Unmarshal(b, &got))
	require.Equal(t, status, got)
}

func TestNicoMachineStatusFailureDomainOmittedWhenEmpty(t *testing.T) {
	// Keeps the field out of the golden fixtures while NICo reports nothing.
	b, err := json.Marshal(NicoMachineStatus{})
	require.NoError(t, err)
	require.NotContains(t, string(b), "failureDomain")
}
