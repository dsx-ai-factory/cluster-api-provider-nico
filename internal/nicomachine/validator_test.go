// SPDX-FileCopyrightText: Copyright (c) 2026 NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package nicomachine_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	infrav1 "github.com/NVIDIA/cluster-api-provider-nico/api/v1alpha1"
	"github.com/NVIDIA/cluster-api-provider-nico/internal/nicomachine"

	nicosdk "github.com/NVIDIA/ncx-infra-controller-rest/sdk/standard"
)

const (
	testInstanceTypeID = "instance-type-1"
	testIpxeScript     = "#!ipxe"
	testLabelKey       = "cluster.x-k8s.io/cluster-name"
	testLabelValue     = "cluster-1"
	testMetadataLabel  = "metadata-cluster"
	testSSHKeyGroupID  = "ssh-key-group-1"
	testVPCID          = "vpc-1"
)

func TestValidator_ValidateInstance(t *testing.T) {
	type parameters struct {
		instance     *nicosdk.Instance
		machine      infrav1.NicoMachine
		capabilities nicomachine.InstanceTypeCapabilities
		wantErr      string
	}

	tests := map[string]parameters{
		"valid instance": {
			instance: validInstance(),
			machine:  validMachine(),
		},
		"rejects instance type mismatch": {
			instance: validInstanceWith(func(instance *nicosdk.Instance) {
				instance.SetInstanceTypeId("other-instance-type")
			}),
			machine: validMachine(),
			wantErr: "instanceTypeId does not match",
		},
		"rejects machine ID mismatch": {
			instance: validInstanceWith(func(instance *nicosdk.Instance) {
				instance.SetMachineId("other-machine")
			}),
			machine: validMachineWith(func(machine *infrav1.NicoMachine) {
				machine.Spec.InstanceTypeID = ""
				machine.Spec.MachineID = "machine-1"
			}),
			wantErr: "machineId does not match",
		},
		"rejects vpc ID mismatch": {
			instance: validInstanceWith(func(instance *nicosdk.Instance) {
				instance.SetVpcId("other-vpc")
			}),
			machine: validMachine(),
			wantErr: "vpcId does not match",
		},
		"rejects ipxe script mismatch": {
			instance: validInstanceWith(func(instance *nicosdk.Instance) {
				instance.SetIpxeScript("#!ipxe\necho other")
			}),
			machine: validMachine(),
			wantErr: "ipxeScript does not match",
		},
		"rejects labels mismatch": {
			instance: validInstanceWith(func(instance *nicosdk.Instance) {
				instance.SetLabels(map[string]string{testLabelKey: "other-cluster"})
			}),
			machine: validMachine(),
			wantErr: "labels do not match",
		},
		"allows extra instance labels": {
			instance: validInstanceWith(func(instance *nicosdk.Instance) {
				instance.SetLabels(map[string]string{
					testLabelKey:                    testLabelValue,
					"cluster.x-k8s.io/machine-name": "machine-1",
				})
			}),
			machine: validMachine(),
		},
		"rejects missing machine labels": {
			instance: validInstanceWith(func(instance *nicosdk.Instance) {
				instance.SetLabels(nil)
			}),
			machine: validMachine(),
			wantErr: "labels do not match",
		},
		"rejects ssh key group mismatch": {
			instance: validInstanceWith(func(instance *nicosdk.Instance) {
				instance.SetSshKeyGroupIds([]string{"other-ssh-key-group"})
			}),
			machine: validMachine(),
			wantErr: "sshKeyGroupIds do not match",
		},
		"rejects interface mismatch": {
			instance: validInstanceWith(func(instance *nicosdk.Instance) {
				instance.SetInterfaces([]nicosdk.Interface{instanceInterface(testVPCPrefixID)})
			}),
			machine: validMachine(),
			wantErr: "instance and machine interfaces do not match",
		},
		"rejects InfiniBand interface mismatch": {
			instance: validInstanceWith(func(instance *nicosdk.Instance) {
				instance.SetInfinibandInterfaces([]nicosdk.InfiniBandInterface{instanceInfiniBandInterface("other-partition")})
			}),
			machine: validMachine(),
			wantErr: "instance and machine InfiniBand interfaces do not match",
		},
		"rejects NVLink interface mismatch": {
			instance: validInstanceWith(func(instance *nicosdk.Instance) {
				instance.SetNvLinkInterfaces([]nicosdk.NVLinkInterface{instanceNVLinkInterface("other-nvlink-partition")})
			}),
			machine: validMachine(),
			wantErr: "instance and machine NVLink interfaces do not match",
		},
		"accepts InfiniBand partition ID expansion": {
			instance: validInstanceWith(func(instance *nicosdk.Instance) {
				instance.SetInfinibandInterfaces([]nicosdk.InfiniBandInterface{
					instanceInfiniBandInterface(testPartitionID),
					instanceInfiniBandInterface(testPartitionID),
				})
			}),
			machine: validMachineWith(func(machine *infrav1.NicoMachine) {
				machine.Spec.InfinibandInterfaces = nil
				machine.Spec.InfinibandPartitionID = testPartitionID
			}),
			capabilities: nicomachine.InstanceTypeCapabilities{
				InfiniBandActiveDeviceIDs: []int32{0, 2},
				InfiniBandSupported:       true,
			},
		},
		"rejects InfiniBand partition ID count mismatch": {
			instance: validInstance(),
			machine: validMachineWith(func(machine *infrav1.NicoMachine) {
				machine.Spec.InfinibandInterfaces = nil
				machine.Spec.InfinibandPartitionID = testPartitionID
			}),
			capabilities: nicomachine.InstanceTypeCapabilities{
				InfiniBandActiveDeviceIDs: []int32{0, 2},
				InfiniBandSupported:       true,
			},
			wantErr: "instance has 1 InfiniBand interfaces, expected 2",
		},
		"rejects InfiniBand partition ID without instance type support": {
			instance: validInstance(),
			machine: validMachineWith(func(machine *infrav1.NicoMachine) {
				machine.Spec.InfinibandInterfaces = nil
				machine.Spec.InfinibandPartitionID = testPartitionID
			}),
			wantErr: `instance type "instance-type-1" does not support InfiniBand`,
		},
		"rejects InfiniBand partition ID mismatch": {
			instance: validInstanceWith(func(instance *nicosdk.Instance) {
				instance.SetInfinibandInterfaces([]nicosdk.InfiniBandInterface{instanceInfiniBandInterface("other-partition")})
			}),
			machine: validMachineWith(func(machine *infrav1.NicoMachine) {
				machine.Spec.InfinibandInterfaces = nil
				machine.Spec.InfinibandPartitionID = testPartitionID
			}),
			capabilities: nicomachine.InstanceTypeCapabilities{
				InfiniBandActiveDeviceIDs: []int32{0},
				InfiniBandSupported:       true,
			},
			wantErr: "instance InfiniBand interface partition does not match",
		},
		"accepts NVLink partition ID expansion": {
			instance: validInstanceWith(func(instance *nicosdk.Instance) {
				instance.SetNvLinkInterfaces([]nicosdk.NVLinkInterface{
					instanceNVLinkInterface(testNVLinkLogicalPartitionID),
					instanceNVLinkInterface(testNVLinkLogicalPartitionID),
				})
			}),
			machine: validMachineWith(func(machine *infrav1.NicoMachine) {
				machine.Spec.NVLinkInterfaces = nil
				machine.Spec.NVLinkLogicalPartitionID = testNVLinkLogicalPartitionID
			}),
			capabilities: nicomachine.InstanceTypeCapabilities{
				NVLinkActiveDeviceIDs: []int32{0, 2},
				NVLinkSupported:       true,
			},
		},
		"rejects NVLink partition ID count mismatch": {
			instance: validInstance(),
			machine: validMachineWith(func(machine *infrav1.NicoMachine) {
				machine.Spec.NVLinkInterfaces = nil
				machine.Spec.NVLinkLogicalPartitionID = testNVLinkLogicalPartitionID
			}),
			capabilities: nicomachine.InstanceTypeCapabilities{
				NVLinkActiveDeviceIDs: []int32{0, 2},
				NVLinkSupported:       true,
			},
			wantErr: "instance has 1 NVLink interfaces, expected 2",
		},
		"rejects NVLink partition ID mismatch": {
			instance: validInstanceWith(func(instance *nicosdk.Instance) {
				instance.SetNvLinkInterfaces([]nicosdk.NVLinkInterface{instanceNVLinkInterface("other-partition")})
			}),
			machine: validMachineWith(func(machine *infrav1.NicoMachine) {
				machine.Spec.NVLinkInterfaces = nil
				machine.Spec.NVLinkLogicalPartitionID = testNVLinkLogicalPartitionID
			}),
			capabilities: nicomachine.InstanceTypeCapabilities{
				NVLinkActiveDeviceIDs: []int32{0},
				NVLinkSupported:       true,
			},
			wantErr: "instance NVLink interface partition does not match",
		},
		"rejects NVLink partition ID without instance type support": {
			instance: validInstance(),
			machine: validMachineWith(func(machine *infrav1.NicoMachine) {
				machine.Spec.NVLinkInterfaces = nil
				machine.Spec.NVLinkLogicalPartitionID = testNVLinkLogicalPartitionID
			}),
			wantErr: `instance type "instance-type-1" does not support NVLink`,
		},
	}

	for name, params := range tests {
		t.Run(name, func(t *testing.T) {
			err := nicomachine.ValidateInstance(params.instance, params.machine, instanceTypeForCapabilities(params.capabilities))
			if params.wantErr == "" {
				assert.NoError(t, err)
				return
			}
			assert.ErrorContains(t, err, params.wantErr)
		})
	}
}

func instanceTypeForCapabilities(capabilities nicomachine.InstanceTypeCapabilities) *nicosdk.InstanceType {
	if !capabilities.InfiniBandSupported && !capabilities.NVLinkSupported {
		return nil
	}

	machineCapabilities := make([]nicosdk.MachineCapability, 0, 2)
	if capabilities.InfiniBandSupported {
		capability := nicosdk.NewMachineCapability()
		capability.SetType("InfiniBand")
		capability.SetName("mlx5")
		capability.SetCount(int32(len(capabilities.InfiniBandActiveDeviceIDs)))
		machineCapabilities = append(machineCapabilities, *capability)
	}
	if capabilities.NVLinkSupported {
		capability := nicosdk.NewMachineCapability()
		capability.SetType("NVLink")
		capability.SetCount(int32(len(capabilities.NVLinkActiveDeviceIDs)))
		machineCapabilities = append(machineCapabilities, *capability)
	}

	instanceType := nicosdk.NewInstanceType()
	instanceType.SetMachineCapabilities(machineCapabilities)
	return instanceType
}

func validInstanceWith(mutate func(*nicosdk.Instance)) *nicosdk.Instance {
	instance := validInstance()
	mutate(instance)
	return instance
}

func validInstance() *nicosdk.Instance {
	instance := nicosdk.NewInstance()
	instance.SetInstanceTypeId(testInstanceTypeID)
	instance.SetVpcId(testVPCID)
	instance.SetIpxeScript(testIpxeScript)
	instance.SetLabels(map[string]string{testLabelKey: testLabelValue})
	instance.SetSshKeyGroupIds([]string{testSSHKeyGroupID})
	instance.SetInterfaces([]nicosdk.Interface{instanceInterface(testSubnetID)})
	instance.SetInfinibandInterfaces([]nicosdk.InfiniBandInterface{instanceInfiniBandInterface(testPartitionID)})
	instance.SetNvLinkInterfaces([]nicosdk.NVLinkInterface{instanceNVLinkInterface(testNVLinkLogicalPartitionID)})
	return instance
}

func validMachineWith(mutate func(*infrav1.NicoMachine)) infrav1.NicoMachine {
	machine := validMachine()
	mutate(&machine)
	return machine
}

func validMachine() infrav1.NicoMachine {
	return infrav1.NicoMachine{
		ObjectMeta: metav1.ObjectMeta{
			Labels: map[string]string{testLabelKey: testMetadataLabel},
		},
		Spec: infrav1.NicoMachineSpec{
			VPCID:          testVPCID,
			InstanceTypeID: testInstanceTypeID,
			IpxeScript:     testIpxeScript,
			Labels:         map[string]string{testLabelKey: testLabelValue},
			SSHKeyGroupIDs: []string{testSSHKeyGroupID},
			Interfaces:     []infrav1.NicoMachineInterface{machineInterface(testSubnetID)},
			InfinibandInterfaces: []infrav1.NicoMachineInfiniBandInterface{
				machineInfiniBandInterface(testPartitionID),
			},
			NVLinkInterfaces: []infrav1.NicoMachineNVLinkInterface{
				machineNVLinkInterface(testNVLinkLogicalPartitionID),
			},
		},
	}
}

func instanceInterface(subnetID string) nicosdk.Interface {
	iface := nicosdk.NewInterface()
	iface.SetSubnetId(subnetID)
	iface.SetRequestedIpAddress(testIPAddress)
	iface.SetDevice(testDevice)
	return *iface
}

func machineInterface(subnetID string) infrav1.NicoMachineInterface {
	return infrav1.NicoMachineInterface{
		SubnetID:  subnetID,
		IPAddress: testIPAddress,
		Device:    testDevice,
	}
}

func instanceInfiniBandInterface(partitionID string) nicosdk.InfiniBandInterface {
	iface := nicosdk.NewInfiniBandInterface()
	iface.SetPartitionId(partitionID)
	iface.SetDevice(testInfiniBandDevice)
	return *iface
}

func machineInfiniBandInterface(partitionID string) infrav1.NicoMachineInfiniBandInterface {
	return infrav1.NicoMachineInfiniBandInterface{
		PartitionID: partitionID,
		Device:      testInfiniBandDevice,
	}
}

func instanceNVLinkInterface(partitionID string) nicosdk.NVLinkInterface {
	iface := nicosdk.NewNVLinkInterface()
	iface.SetNvLinkLogicalPartitionId(partitionID)
	return *iface
}

func machineNVLinkInterface(partitionID string) infrav1.NicoMachineNVLinkInterface {
	return infrav1.NicoMachineNVLinkInterface{
		NVLinkLogicalPartitionID: partitionID,
	}
}
