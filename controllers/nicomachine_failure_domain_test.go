// SPDX-FileCopyrightText: Copyright (c) 2026 NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package controllers

import (
	"context"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	nicosdk "github.com/NVIDIA/infra-controller/rest-api/sdk/standard"

	infrav1 "github.com/dsx-ai-factory/cluster-api-provider-nico/api/v1alpha1"
	"github.com/dsx-ai-factory/cluster-api-provider-nico/internal/fake"
	"github.com/dsx-ai-factory/cluster-api-provider-nico/internal/nico"
)

func TestObserveFailureDomainBackfillsExistingMachine(t *testing.T) {
	t.Parallel()

	api := fake.New()
	api.SeedToken("test-token")
	machine := nicosdk.NewMachine()
	machine.SetId("machine-1")
	machine.SetLabels(map[string]string{"failure_domain": "fd-a"})
	api.SeedMachine("org-1", *machine)
	server := httptest.NewServer(api.Handler())
	t.Cleanup(server.Close)

	client, err := nico.NewClient(context.Background(), nico.SecretConfig{
		Endpoint: server.URL,
		OrgID:    "org-1",
		Token:    "test-token",
	})
	require.NoError(t, err)
	instance := nicosdk.NewInstance()
	instance.SetMachineId("machine-1")
	nicoMachine := &infrav1.NicoMachine{
		Status: infrav1.NicoMachineStatus{MachineID: "machine-1"},
	}

	observed, err := observeFailureDomain(
		context.Background(),
		client,
		nicoMachine,
		"failure_domain",
		instance,
		"",
	)

	require.NoError(t, err)
	assert.Equal(t, "fd-a", observed)
}
