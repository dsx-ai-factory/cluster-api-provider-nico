// SPDX-FileCopyrightText: Copyright (c) 2026 NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package controllers

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	infrav1 "github.com/NVIDIA/cluster-api-provider-nico/api/v1alpha1"
	"github.com/NVIDIA/cluster-api-provider-nico/internal/nicomachine"
)

func TestBuildInstanceCreateRequestExpandsPartitionIDs(t *testing.T) {
	machine := &infrav1.NicoMachine{
		Spec: infrav1.NicoMachineSpec{
			VPCID:                    "vpc-1",
			InstanceTypeID:           "type-1",
			Interfaces:               []infrav1.NicoMachineInterface{{SubnetID: "subnet-1"}},
			InfinibandPartitionID:    "ib-partition-1",
			NVLinkLogicalPartitionID: "nvlink-partition-1",
		},
	}
	capabilities := nicomachine.InstanceTypeCapabilities{
		InfiniBandDeviceName:      "mlx5",
		InfiniBandActiveDeviceIDs: []int32{0, 2},
		InfiniBandSupported:       true,
		NVLinkActiveDeviceIDs:     []int32{1, 3},
		NVLinkSupported:           true,
	}

	request, err := buildInstanceCreateRequest("machine-1", "tenant-1", machine, "cluster-1", "#cloud-config", capabilities)
	require.NoError(t, err)

	ibInterfaces := request.GetInfinibandInterfaces()
	require.Len(t, ibInterfaces, 2)
	for i, deviceID := range []int32{0, 2} {
		assert.Equal(t, "ib-partition-1", ibInterfaces[i].GetPartitionId())
		assert.Equal(t, "mlx5", ibInterfaces[i].GetDevice())
		assert.Equal(t, deviceID, ibInterfaces[i].GetDeviceInstance())
		assert.True(t, ibInterfaces[i].GetIsPhysical())
	}

	nvLinkInterfaces := request.GetNvLinkInterfaces()
	require.Len(t, nvLinkInterfaces, 2)
	for i, deviceID := range []int32{1, 3} {
		assert.Equal(t, "nvlink-partition-1", nvLinkInterfaces[i].GetNvLinklogicalPartitionId())
		assert.Equal(t, deviceID, nvLinkInterfaces[i].GetDeviceInstance())
	}
}

func TestBuildInstanceCreateRequestRejectsUnsupportedPartitions(t *testing.T) {
	machine := &infrav1.NicoMachine{
		Spec: infrav1.NicoMachineSpec{
			VPCID:                    "vpc-1",
			InstanceTypeID:           "type-1",
			Interfaces:               []infrav1.NicoMachineInterface{{SubnetID: "subnet-1"}},
			InfinibandPartitionID:    "ib-partition-1",
			NVLinkLogicalPartitionID: "nvlink-partition-1",
		},
	}

	_, err := buildInstanceCreateRequest("machine-1", "tenant-1", machine, "cluster-1", "#cloud-config", nicomachine.InstanceTypeCapabilities{})
	require.ErrorContains(t, err, `instance type "type-1" does not support InfiniBand`)
}
