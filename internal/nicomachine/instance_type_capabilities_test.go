// SPDX-FileCopyrightText: Copyright (c) 2026 NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package nicomachine_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/NVIDIA/cluster-api-provider-nico/internal/nicomachine"

	nicosdk "github.com/NVIDIA/ncx-infra-controller-rest/sdk/standard"
)

func TestParseInstanceTypeCapabilities(t *testing.T) {
	tests := map[string]struct {
		instanceType *nicosdk.InstanceType
		want         nicomachine.InstanceTypeCapabilities
	}{
		"extracts active InfiniBand and NVLink devices": {
			instanceType: instanceTypeWithCapabilities(
				infiniBandCapability("mlx5", 4, []int32{1}),
				nvLinkCapability("GPU", "NVLink", 4, []int32{0, 3}),
			),
			want: nicomachine.InstanceTypeCapabilities{
				InfiniBandDeviceName:      "mlx5",
				InfiniBandActiveDeviceIDs: []int32{0, 2, 3},
				InfiniBandSupported:       true,
				NVLinkActiveDeviceIDs:     []int32{1, 2},
				NVLinkSupported:           true,
			},
		},
		"ignores InfiniBand capability without a device name": {
			instanceType: instanceTypeWithCapabilities(
				infiniBandCapability("", 2, nil),
			),
			want: nicomachine.InstanceTypeCapabilities{},
		},
		"last InfiniBand capability wins": {
			instanceType: instanceTypeWithCapabilities(
				infiniBandCapability("mlx5", 2, nil),
				infiniBandCapability("mlx5_1", 1, nil),
			),
			want: nicomachine.InstanceTypeCapabilities{
				InfiniBandDeviceName:      "mlx5_1",
				InfiniBandActiveDeviceIDs: []int32{0},
				InfiniBandSupported:       true,
			},
		},
		"matches NVLink by capability type name": {
			instanceType: instanceTypeWithCapabilities(
				nvLinkCapability("NVLink Interface", "", 2, nil),
			),
			want: nicomachine.InstanceTypeCapabilities{
				NVLinkActiveDeviceIDs: []int32{0, 1},
				NVLinkSupported:       true,
			},
		},
		"nil instance type is unsupported": {
			want: nicomachine.InstanceTypeCapabilities{},
		},
	}

	for name, params := range tests {
		t.Run(name, func(t *testing.T) {
			assert.Equal(t, params.want, nicomachine.ParseInstanceTypeCapabilities(params.instanceType))
		})
	}
}

func instanceTypeWithCapabilities(capabilities ...nicosdk.MachineCapability) *nicosdk.InstanceType {
	instanceType := nicosdk.NewInstanceType()
	instanceType.SetMachineCapabilities(capabilities)
	return instanceType
}

func infiniBandCapability(name string, count int32, inactive []int32) nicosdk.MachineCapability {
	capability := nicosdk.NewMachineCapability()
	capability.SetType("InfiniBand")
	if name != "" {
		capability.SetName(name)
	}
	capability.SetCount(count)
	if inactive != nil {
		capability.SetInactiveDevices(inactive)
	}
	return *capability
}

func nvLinkCapability(capabilityType, deviceType string, count int32, inactive []int32) nicosdk.MachineCapability {
	capability := nicosdk.NewMachineCapability()
	capability.SetType(capabilityType)
	if deviceType != "" {
		capability.SetDeviceType(deviceType)
	}
	capability.SetCount(count)
	if inactive != nil {
		capability.SetInactiveDevices(inactive)
	}
	return *capability
}
