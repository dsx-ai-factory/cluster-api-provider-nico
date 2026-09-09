// SPDX-FileCopyrightText: Copyright (c) 2026 NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package v1alpha1

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestNicoMachineStatusTopologyFieldsRoundTrip(t *testing.T) {
	status := NicoMachineStatus{
		MachineID: "machine-1",
		SiteID:    "site-1",
		SiteName:  "New York / A",
		VPCID:     "vpc-1",
		VPCName:   "Tenant VPC",
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
