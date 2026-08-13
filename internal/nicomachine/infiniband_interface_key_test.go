// SPDX-FileCopyrightText: Copyright (c) 2026 NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package nicomachine_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	infrav1 "github.com/NVIDIA/cluster-api-provider-nico/api/v1alpha1"
	"github.com/NVIDIA/cluster-api-provider-nico/internal/nicomachine"

	nicosdk "github.com/NVIDIA/ncx-infra-controller-rest/sdk/standard"
)

const (
	testPartitionID      = "partition-1"
	testInfiniBandDevice = "ib0"
	testVirtualFunction  = int32(0)
)

func TestInfiniBandInterfaceKey_InstanceInfiniBandInterfaceKey(t *testing.T) {
	type parameters struct {
		in   nicosdk.InfiniBandInterface
		want nicomachine.InfiniBandInterfaceKey
	}

	tests := map[string]parameters{
		"unset optional fields": {
			in: func() nicosdk.InfiniBandInterface {
				iface := nicosdk.NewInfiniBandInterface()
				iface.SetPartitionId(testPartitionID)
				iface.SetDevice(testInfiniBandDevice)
				return *iface
			}(),
			want: nicomachine.InfiniBandInterfaceKey{
				PartitionID: testPartitionID,
				Device:      testInfiniBandDevice,
			},
		},
		"set optional zero values": {
			in: func() nicosdk.InfiniBandInterface {
				iface := nicosdk.NewInfiniBandInterface()
				iface.SetPartitionId(testPartitionID)
				iface.SetDeviceInstance(testDeviceIndex)
				iface.SetIsPhysical(testPhysical)
				iface.SetVirtualFunctionId(testVirtualFunction)
				return *iface
			}(),
			want: nicomachine.InfiniBandInterfaceKey{
				PartitionID:          testPartitionID,
				DeviceInstanceSet:    true,
				DeviceInstance:       testDeviceIndex,
				IsPhysicalSet:        true,
				IsPhysical:           testPhysical,
				VirtualFunctionIDSet: true,
				VirtualFunctionID:    testVirtualFunction,
			},
		},
	}

	for name, params := range tests {
		t.Run(name, func(t *testing.T) {
			assert.Equal(t, params.want, nicomachine.InstanceInfiniBandInterfaceKey(params.in))
		})
	}
}

func TestInfiniBandInterfaceKey_MachineInfiniBandInterfaceKey(t *testing.T) {
	type parameters struct {
		in   infrav1.NicoMachineInfiniBandInterface
		want nicomachine.InfiniBandInterfaceKey
	}

	tests := map[string]parameters{
		"unset optional fields": {
			in: infrav1.NicoMachineInfiniBandInterface{
				PartitionID: testPartitionID,
				Device:      testInfiniBandDevice,
			},
			want: nicomachine.InfiniBandInterfaceKey{
				PartitionID: testPartitionID,
				Device:      testInfiniBandDevice,
			},
		},
		"set optional zero values": {
			in: infrav1.NicoMachineInfiniBandInterface{
				PartitionID:       testPartitionID,
				DeviceInstance:    new(testDeviceIndex),
				IsPhysical:        new(testPhysical),
				VirtualFunctionID: new(testVirtualFunction),
			},
			want: nicomachine.InfiniBandInterfaceKey{
				PartitionID:          testPartitionID,
				DeviceInstanceSet:    true,
				DeviceInstance:       testDeviceIndex,
				IsPhysicalSet:        true,
				IsPhysical:           testPhysical,
				VirtualFunctionIDSet: true,
				VirtualFunctionID:    testVirtualFunction,
			},
		},
	}

	for name, params := range tests {
		t.Run(name, func(t *testing.T) {
			assert.Equal(t, params.want, nicomachine.MachineInfiniBandInterfaceKey(params.in))
		})
	}
}
