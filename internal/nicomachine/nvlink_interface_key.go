// SPDX-FileCopyrightText: Copyright (c) 2026 NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package nicomachine

import (
	infrav1 "github.com/NVIDIA/cluster-api-provider-nico/api/v1alpha1"

	nicosdk "github.com/NVIDIA/infra-controller/rest-api/sdk/standard"
)

type NVLinkInterfaceKey struct {
	NVLinkLogicalPartitionID string
	DeviceInstanceSet        bool
	DeviceInstance           int32
}

func InstanceNVLinkInterfaceKey(instanceInterface nicosdk.NVLinkInterface) NVLinkInterfaceKey {
	key := NVLinkInterfaceKey{
		NVLinkLogicalPartitionID: instanceInterface.GetNvLinkLogicalPartitionId(),
	}

	if deviceInstance, ok := instanceInterface.GetDeviceInstanceOk(); ok && deviceInstance != nil {
		key.DeviceInstanceSet = true
		key.DeviceInstance = *deviceInstance
	}

	return key
}

func MachineNVLinkInterfaceKey(machineInterface infrav1.NicoMachineNVLinkInterface) NVLinkInterfaceKey {
	key := NVLinkInterfaceKey{
		NVLinkLogicalPartitionID: machineInterface.NVLinkLogicalPartitionID,
	}

	if machineInterface.DeviceInstance != nil {
		key.DeviceInstanceSet = true
		key.DeviceInstance = *machineInterface.DeviceInstance
	}

	return key
}
