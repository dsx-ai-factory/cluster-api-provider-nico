// SPDX-FileCopyrightText: Copyright (c) 2026 NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package nicomachine

import (
	"fmt"

	"k8s.io/apimachinery/pkg/labels"

	infrav1 "github.com/NVIDIA/cluster-api-provider-nico/api/v1alpha1"

	nicosdk "github.com/NVIDIA/ncx-infra-controller-rest/sdk/standard"
)

func ValidateInstance(instance *nicosdk.Instance, machine infrav1.NicoMachine) error {
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
	if err := validateInfiniBandInterfaces(instance.GetInfinibandInterfaces(), machine.Spec.InfinibandInterfaces); err != nil {
		return err
	}
	if err := validateNVLinkInterfaces(instance.GetNvLinkInterfaces(), machine.Spec.NVLinkInterfaces); err != nil {
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
	if !KeyedSlicesMatch(instanceInterfaces, machineInterfaces, InstanceInterfaceKey, MachineInterfaceKey) {
		return fmt.Errorf("instance and machine interfaces do not match")
	}
	return nil
}

func validateInfiniBandInterfaces(instanceInterfaces []nicosdk.InfiniBandInterface, machineInterfaces []infrav1.NicoMachineInfiniBandInterface) error {
	if !KeyedSlicesMatch(instanceInterfaces, machineInterfaces, InstanceInfiniBandInterfaceKey, MachineInfiniBandInterfaceKey) {
		return fmt.Errorf("instance and machine InfiniBand interfaces do not match")
	}
	return nil
}

func validateNVLinkInterfaces(instanceInterfaces []nicosdk.NVLinkInterface, machineInterfaces []infrav1.NicoMachineNVLinkInterface) error {
	if !KeyedSlicesMatch(instanceInterfaces, machineInterfaces, InstanceNVLinkInterfaceKey, MachineNVLinkInterfaceKey) {
		return fmt.Errorf("instance and machine NVLink interfaces do not match")
	}
	return nil
}
