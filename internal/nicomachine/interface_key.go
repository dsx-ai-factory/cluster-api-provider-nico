package nicomachine

import (
	infrav1 "gitlab-master.nvidia.com/nke/cluster-api-provider-nico/api/v1alpha1"

	nicosdk "github.com/NVIDIA/ncx-infra-controller-rest/sdk/standard"
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
