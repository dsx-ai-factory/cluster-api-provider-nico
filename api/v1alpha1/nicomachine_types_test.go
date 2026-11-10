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
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
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
	namespace := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "machine-network"}}
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
