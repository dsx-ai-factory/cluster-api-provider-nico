package nicomachine

import (
	infrav1 "github.com/NVIDIA/cluster-api-provider-nico/api/v1alpha1"

	nicosdk "github.com/NVIDIA/ncx-infra-controller-rest/sdk/standard"
)

type InfiniBandInterfaceKey struct {
	PartitionID          string
	Device               string
	DeviceInstanceSet    bool
	DeviceInstance       int32
	IsPhysicalSet        bool
	IsPhysical           bool
	VirtualFunctionIDSet bool
	VirtualFunctionID    int32
}

func InstanceInfiniBandInterfaceKey(instanceInterface nicosdk.InfiniBandInterface) InfiniBandInterfaceKey {
	key := InfiniBandInterfaceKey{
		PartitionID: instanceInterface.GetPartitionId(),
		Device:      instanceInterface.GetDevice(),
	}

	if deviceInstance, ok := instanceInterface.GetDeviceInstanceOk(); ok && deviceInstance != nil {
		key.DeviceInstanceSet = true
		key.DeviceInstance = *deviceInstance
	}
	if isPhysical, ok := instanceInterface.GetIsPhysicalOk(); ok && isPhysical != nil {
		key.IsPhysicalSet = true
		key.IsPhysical = *isPhysical
	}
	if virtualFunctionID, ok := instanceInterface.GetVirtualFunctionIdOk(); ok && virtualFunctionID != nil {
		key.VirtualFunctionIDSet = true
		key.VirtualFunctionID = *virtualFunctionID
	}

	return key
}

func MachineInfiniBandInterfaceKey(machineInterface infrav1.NicoMachineInfiniBandInterface) InfiniBandInterfaceKey {
	key := InfiniBandInterfaceKey{
		PartitionID: machineInterface.PartitionID,
		Device:      machineInterface.Device,
	}

	if machineInterface.DeviceInstance != nil {
		key.DeviceInstanceSet = true
		key.DeviceInstance = *machineInterface.DeviceInstance
	}
	if machineInterface.IsPhysical != nil {
		key.IsPhysicalSet = true
		key.IsPhysical = *machineInterface.IsPhysical
	}
	if machineInterface.VirtualFunctionID != nil {
		key.VirtualFunctionIDSet = true
		key.VirtualFunctionID = *machineInterface.VirtualFunctionID
	}

	return key
}
