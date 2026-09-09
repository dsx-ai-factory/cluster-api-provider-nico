// SPDX-FileCopyrightText: Copyright (c) 2026 NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package nicomachine

import (
	"fmt"

	"k8s.io/apimachinery/pkg/labels"

	infrav1 "github.com/NVIDIA/cluster-api-provider-nico/api/v1alpha1"

	nicosdk "github.com/NVIDIA/infra-controller/rest-api/sdk/standard"
)

func ValidateInstance(instance *nicosdk.Instance, machine infrav1.NicoMachine, instanceType *nicosdk.InstanceType) error {
	capabilities := ParseInstanceTypeCapabilities(instanceType)
	if err := capabilities.ValidatePartitionSupport(machine.Spec); err != nil {
		return err
	}
	if machine.Spec.InstanceTypeID != "" && instance.GetInstanceTypeId() != machine.Spec.InstanceTypeID {
		return fmt.Errorf("instanceTypeId does not match")
	}
	if machine.Spec.MachineID != "" && instance.GetMachineId() != machine.Spec.MachineID {
		return fmt.Errorf("machineId does not match")
	}
	if instance.GetVpcId() != machine.Spec.VPCID {
		return fmt.Errorf("vpcId does not match")
	}
	if instance.GetIpxeScript() != machine.Spec.IpxeScript {
		return fmt.Errorf("ipxeScript does not match")
	}
	if !labelsMatch(instance.GetLabels(), machine.Spec.Labels) {
		return fmt.Errorf("labels do not match")
	}
	if err := validateSshKeyGroups(instance.GetSshKeyGroupIds(), machine.Spec.SSHKeyGroupIDs); err != nil {
		return err
	}
	if err := validateInterfaces(instance.GetInterfaces(), machine.Spec.Interfaces); err != nil {
		return err
	}
	if err := validateInfiniBandInterfaces(instance.GetInfinibandInterfaces(), machine.Spec.InfinibandInterfaces, machine.Spec.InfinibandPartitionID, capabilities); err != nil {
		return err
	}
	if err := validateNVLinkInterfaces(instance.GetNvLinkInterfaces(), machine.Spec.NVLinkInterfaces, machine.Spec.NVLinkLogicalPartitionID, capabilities); err != nil {
		return err
	}

	return nil
}

func labelsMatch(instanceLabels, machineLabels map[string]string) bool {
	return labels.SelectorFromSet(machineLabels).Matches(labels.Set(instanceLabels))
}

func validateSshKeyGroups(instanceValues, machineValues []string) error {
	getKey := func(value string) string { return value }
	if !KeyedSlicesMatch(instanceValues, machineValues, getKey, getKey) {
		return fmt.Errorf("sshKeyGroupIds do not match")
	}
	return nil
}

func validateInterfaces(instanceInterfaces []nicosdk.Interface, machineInterfaces []infrav1.NicoMachineInterface) error {
	if !SlicesMatchFunc(instanceInterfaces, machineInterfaces, InterfaceMatches) {
		return fmt.Errorf("instance and machine interfaces do not match")
	}
	return nil
}

func validateInfiniBandInterfaces(instanceInterfaces []nicosdk.InfiniBandInterface, machineInterfaces []infrav1.NicoMachineInfiniBandInterface, partitionID string, capabilities InstanceTypeCapabilities) error {
	if partitionID != "" {
		expectedCount := len(capabilities.InfiniBandActiveDeviceIDs)
		if len(instanceInterfaces) != expectedCount {
			return fmt.Errorf("instance has %d InfiniBand interfaces, expected %d", len(instanceInterfaces), expectedCount)
		}
		for _, instanceInterface := range instanceInterfaces {
			if instanceInterface.GetPartitionId() != partitionID {
				return fmt.Errorf("instance InfiniBand interface partition does not match")
			}
		}
		return nil
	}
	if !KeyedSlicesMatch(instanceInterfaces, machineInterfaces, InstanceInfiniBandInterfaceKey, MachineInfiniBandInterfaceKey) {
		return fmt.Errorf("instance and machine InfiniBand interfaces do not match")
	}
	return nil
}

func validateNVLinkInterfaces(instanceInterfaces []nicosdk.NVLinkInterface, machineInterfaces []infrav1.NicoMachineNVLinkInterface, partitionID string, capabilities InstanceTypeCapabilities) error {
	if partitionID != "" {
		expectedCount := len(capabilities.NVLinkActiveDeviceIDs)
		if len(instanceInterfaces) != expectedCount {
			return fmt.Errorf("instance has %d NVLink interfaces, expected %d", len(instanceInterfaces), expectedCount)
		}
		for _, instanceInterface := range instanceInterfaces {
			if instanceInterface.GetNvLinkLogicalPartitionId() != partitionID {
				return fmt.Errorf("instance NVLink interface partition does not match")
			}
		}
		return nil
	}
	if !KeyedSlicesMatch(instanceInterfaces, machineInterfaces, InstanceNVLinkInterfaceKey, MachineNVLinkInterfaceKey) {
		return fmt.Errorf("instance and machine NVLink interfaces do not match")
	}
	return nil
}
