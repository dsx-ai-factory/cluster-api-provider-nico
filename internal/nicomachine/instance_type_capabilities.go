// SPDX-FileCopyrightText: Copyright (c) 2026 NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package nicomachine

import (
	"fmt"
	"math"
	"slices"
	"strings"

	infrav1 "github.com/NVIDIA/cluster-api-provider-nico/api/v1alpha1"

	nicosdk "github.com/NVIDIA/infra-controller/rest-api/sdk/standard"
)

const (
	machineCapabilityTypeInfiniBand = "InfiniBand"

	// NICo spells NVLink capabilities several ways: a capability type of its
	// own ("NVLink", "NVLink Interface") or a GPU capability whose device type
	// names NVLink. Match on the substring rather than enumerating them.
	nvLinkCapabilityMarker = "nvlink"
)

// InstanceTypeCapabilities contains the device information needed to expand
// partition-level NicoMachine requests.
type InstanceTypeCapabilities struct {
	InfiniBandDeviceName      string
	InfiniBandActiveDeviceIDs []int32
	InfiniBandSupported       bool
	NVLinkActiveDeviceIDs     []int32
	NVLinkSupported           bool
}

// ValidatePartitionSupport verifies that the instance type can satisfy the
// requested partition expansions.
func (c InstanceTypeCapabilities) ValidatePartitionSupport(spec infrav1.NicoMachineSpec) error {
	if spec.InfinibandPartitionID != "" && !c.InfiniBandSupported {
		return fmt.Errorf("instance type %q does not support InfiniBand", spec.InstanceTypeID)
	}
	if spec.NVLinkLogicalPartitionID != "" && !c.NVLinkSupported {
		return fmt.Errorf("instance type %q does not support NVLink", spec.InstanceTypeID)
	}
	return nil
}

// ParseInstanceTypeCapabilities extracts active InfiniBand and NVLink devices
// from a NICo instance type.
func ParseInstanceTypeCapabilities(instanceType *nicosdk.InstanceType) InstanceTypeCapabilities {
	if instanceType == nil {
		return InstanceTypeCapabilities{}
	}

	result := InstanceTypeCapabilities{}

	for _, capability := range instanceType.GetMachineCapabilities() {
		activeDeviceIDs := getActiveDeviceIDs(capability)
		deviceName := capability.GetName()

		if isInfiniBandCapability(capability) {
			result.InfiniBandDeviceName = deviceName
			result.InfiniBandActiveDeviceIDs = activeDeviceIDs
			result.InfiniBandSupported = true
		}
		if isNVLinkCapability(capability) {
			result.NVLinkActiveDeviceIDs = activeDeviceIDs
			result.NVLinkSupported = true
		}
	}

	return result
}

func isInfiniBandCapability(capability nicosdk.MachineCapability) bool {
	return capability.GetType() == machineCapabilityTypeInfiniBand && capability.GetName() != ""
}

func isNVLinkCapability(capability nicosdk.MachineCapability) bool {
	return namesNVLink(capability.GetType()) || namesNVLink(capability.GetDeviceType())
}

func namesNVLink(value string) bool {
	return strings.Contains(strings.ToLower(value), nvLinkCapabilityMarker)
}

func getActiveDeviceIDs(capability nicosdk.MachineCapability) []int32 {
	inactiveDevices := capability.GetInactiveDevices()
	active := make([]int32, 0, capability.GetCount())
	for deviceID := range capability.GetCount() {
		if deviceID > math.MaxInt32 {
			break
		}
		if !slices.Contains(inactiveDevices, deviceID) {
			active = append(active, int32(deviceID))
		}
	}
	return active
}
