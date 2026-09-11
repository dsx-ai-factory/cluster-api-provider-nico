// SPDX-FileCopyrightText: Copyright (c) 2026 NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package nicomachine

import (
	infrav1 "github.com/dsx-ai-factory/cluster-api-provider-nico/api/v1alpha1"

	nicosdk "github.com/NVIDIA/infra-controller/rest-api/sdk/standard"
)

type InterfaceKey struct {
	SubnetID          string
	VPCPrefixID       string
	IPAddress         string
	PhysicalSet       bool
	Physical          bool
	Device            string
	DeviceInstanceSet bool
	DeviceInstance    int32
}

func InstanceInterfaceKey(instanceInterface nicosdk.Interface) InterfaceKey {
	key := InterfaceKey{
		SubnetID:    instanceInterface.GetSubnetId(),
		VPCPrefixID: instanceInterface.GetVpcPrefixId(),
		IPAddress:   instanceInterface.GetRequestedIpAddress(),
		Device:      instanceInterface.GetDevice(),
	}

	if physical, ok := instanceInterface.GetIsPhysicalOk(); ok && physical != nil {
		key.PhysicalSet = true
		key.Physical = *physical
	}
	if deviceInstance, ok := instanceInterface.GetDeviceInstanceOk(); ok && deviceInstance != nil {
		key.DeviceInstanceSet = true
		key.DeviceInstance = *deviceInstance
	}

	return key
}

func MachineInterfaceKey(machineInterface infrav1.NicoMachineInterface) InterfaceKey {
	key := InterfaceKey{
		SubnetID:    machineInterface.SubnetID,
		VPCPrefixID: machineInterface.VPCPrefixID,
		IPAddress:   machineInterface.IPAddress,
		Device:      machineInterface.Device,
	}

	if machineInterface.Physical != nil {
		key.PhysicalSet = true
		key.Physical = *machineInterface.Physical
	}
	if machineInterface.DeviceInstance != nil {
		key.DeviceInstanceSet = true
		key.DeviceInstance = *machineInterface.DeviceInstance
	}

	return key
}

// InterfaceMatches reports whether an instance interface satisfies the interface
// requested by the machine spec. Only subnetID and vpcPrefixID identify the
// attachment; the other fields are optional in the spec and NICo populates them
// on the instance, so they are compared only when the spec sets them.
func InterfaceMatches(instanceInterface nicosdk.Interface, machineInterface infrav1.NicoMachineInterface) bool {
	actual := InstanceInterfaceKey(instanceInterface)
	expected := MachineInterfaceKey(machineInterface)

	if actual.SubnetID != expected.SubnetID || actual.VPCPrefixID != expected.VPCPrefixID {
		return false
	}
	if expected.IPAddress != "" && actual.IPAddress != expected.IPAddress {
		return false
	}
	if expected.Device != "" && actual.Device != expected.Device {
		return false
	}
	if expected.PhysicalSet && (!actual.PhysicalSet || actual.Physical != expected.Physical) {
		return false
	}
	if expected.DeviceInstanceSet && (!actual.DeviceInstanceSet || actual.DeviceInstance != expected.DeviceInstance) {
		return false
	}
	return true
}
