// SPDX-FileCopyrightText: Copyright (c) 2026 NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package nicomachine_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"k8s.io/utils/ptr"

	infrav1 "github.com/NVIDIA/cluster-api-provider-nico/api/v1alpha1"
	"github.com/NVIDIA/cluster-api-provider-nico/internal/nicomachine"

	nicosdk "github.com/NVIDIA/ncx-infra-controller-rest/sdk/standard"
)

const testNVLinkLogicalPartitionID = "nvlink-logical-partition-1"

func TestNVLinkInterfaceKey_InstanceNVLinkInterfaceKey(t *testing.T) {
	type parameters struct {
		in   nicosdk.NVLinkInterface
		want nicomachine.NVLinkInterfaceKey
	}

	tests := map[string]parameters{
		"unset optional fields": {
			in: func() nicosdk.NVLinkInterface {
				iface := nicosdk.NewNVLinkInterface()
				iface.SetNvLinkLogicalPartitionId(testNVLinkLogicalPartitionID)
				return *iface
			}(),
			want: nicomachine.NVLinkInterfaceKey{
				NVLinkLogicalPartitionID: testNVLinkLogicalPartitionID,
			},
		},
		"set optional zero values": {
			in: func() nicosdk.NVLinkInterface {
				iface := nicosdk.NewNVLinkInterface()
				iface.SetNvLinkLogicalPartitionId(testNVLinkLogicalPartitionID)
				iface.SetDeviceInstance(testDeviceIndex)
				return *iface
			}(),
			want: nicomachine.NVLinkInterfaceKey{
				NVLinkLogicalPartitionID: testNVLinkLogicalPartitionID,
				DeviceInstanceSet:        true,
				DeviceInstance:           testDeviceIndex,
			},
		},
	}

	for name, params := range tests {
		t.Run(name, func(t *testing.T) {
			assert.Equal(t, params.want, nicomachine.InstanceNVLinkInterfaceKey(params.in))
		})
	}
}

func TestNVLinkInterfaceKey_MachineNVLinkInterfaceKey(t *testing.T) {
	type parameters struct {
		in   infrav1.NicoMachineNVLinkInterface
		want nicomachine.NVLinkInterfaceKey
	}

	tests := map[string]parameters{
		"unset optional fields": {
			in: infrav1.NicoMachineNVLinkInterface{
				NVLinkLogicalPartitionID: testNVLinkLogicalPartitionID,
			},
			want: nicomachine.NVLinkInterfaceKey{
				NVLinkLogicalPartitionID: testNVLinkLogicalPartitionID,
			},
		},
		"set optional zero values": {
			in: infrav1.NicoMachineNVLinkInterface{
				NVLinkLogicalPartitionID: testNVLinkLogicalPartitionID,
				DeviceInstance:           ptr.To(testDeviceIndex),
			},
			want: nicomachine.NVLinkInterfaceKey{
				NVLinkLogicalPartitionID: testNVLinkLogicalPartitionID,
				DeviceInstanceSet:        true,
				DeviceInstance:           testDeviceIndex,
			},
		},
	}

	for name, params := range tests {
		t.Run(name, func(t *testing.T) {
			assert.Equal(t, params.want, nicomachine.MachineNVLinkInterfaceKey(params.in))
		})
	}
}
