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
	testSubnetID    = "subnet-1"
	testVPCPrefixID = "prefix-1"
	testIPAddress   = "10.0.0.11"
	testDevice      = "eth0"
	testPhysical    = false
	testDeviceIndex = int32(0)
)

func TestInterfaceKey_InstanceInterfaceKey(t *testing.T) {
	type parameters struct {
		in   nicosdk.Interface
		want nicomachine.InterfaceKey
	}

	tests := map[string]parameters{
		"unset optional fields": {
			in: func() nicosdk.Interface {
				iface := nicosdk.NewInterface()
				iface.SetSubnetId(testSubnetID)
				iface.SetRequestedIpAddress(testIPAddress)
				iface.SetDevice(testDevice)
				return *iface
			}(),
			want: nicomachine.InterfaceKey{
				SubnetID:  testSubnetID,
				IPAddress: testIPAddress,
				Device:    testDevice,
			},
		},
		"set optional zero values": {
			in: func() nicosdk.Interface {
				iface := nicosdk.NewInterface()
				iface.SetVpcPrefixId(testVPCPrefixID)
				iface.SetIsPhysical(testPhysical)
				iface.SetDeviceInstance(testDeviceIndex)
				return *iface
			}(),
			want: nicomachine.InterfaceKey{
				VPCPrefixID:       testVPCPrefixID,
				PhysicalSet:       true,
				Physical:          testPhysical,
				DeviceInstanceSet: true,
				DeviceInstance:    testDeviceIndex,
			},
		},
	}

	for name, params := range tests {
		t.Run(name, func(t *testing.T) {
			assert.Equal(t, params.want, nicomachine.InstanceInterfaceKey(params.in))
		})
	}
}

func TestInterfaceKey_MachineInterfaceKey(t *testing.T) {
	type parameters struct {
		in   infrav1.NicoMachineInterface
		want nicomachine.InterfaceKey
	}

	tests := map[string]parameters{
		"unset optional fields": {
			in: infrav1.NicoMachineInterface{
				SubnetID:  testSubnetID,
				IPAddress: testIPAddress,
				Device:    testDevice,
			},
			want: nicomachine.InterfaceKey{
				SubnetID:  testSubnetID,
				IPAddress: testIPAddress,
				Device:    testDevice,
			},
		},
		"set optional zero values": {
			in: infrav1.NicoMachineInterface{
				VPCPrefixID:    testVPCPrefixID,
				Physical:       new(testPhysical),
				DeviceInstance: new(testDeviceIndex),
			},
			want: nicomachine.InterfaceKey{
				VPCPrefixID:       testVPCPrefixID,
				PhysicalSet:       true,
				Physical:          testPhysical,
				DeviceInstanceSet: true,
				DeviceInstance:    testDeviceIndex,
			},
		},
	}

	for name, params := range tests {
		t.Run(name, func(t *testing.T) {
			assert.Equal(t, params.want, nicomachine.MachineInterfaceKey(params.in))
		})
	}
}

func TestInterfaceKey_InterfaceMatches(t *testing.T) {
	type parameters struct {
		instance nicosdk.Interface
		machine  infrav1.NicoMachineInterface
		want     bool
	}

	physicalTrue := true
	physicalFalse := false
	otherDeviceInstance := int32(1)

	tests := map[string]parameters{
		"prefix-only spec matches instance with populated optional fields": {
			instance: func() nicosdk.Interface {
				iface := nicosdk.NewInterface()
				iface.SetVpcPrefixId(testVPCPrefixID)
				iface.SetIsPhysical(true)
				iface.SetRequestedIpAddress(testIPAddress)
				iface.SetDevice(testDevice)
				iface.SetDeviceInstance(testDeviceIndex)
				return *iface
			}(),
			machine: infrav1.NicoMachineInterface{VPCPrefixID: testVPCPrefixID},
			want:    true,
		},
		"subnet-only spec matches instance with populated optional fields": {
			instance: func() nicosdk.Interface {
				iface := nicosdk.NewInterface()
				iface.SetSubnetId(testSubnetID)
				iface.SetIsPhysical(true)
				return *iface
			}(),
			machine: infrav1.NicoMachineInterface{SubnetID: testSubnetID},
			want:    true,
		},
		"rejects prefix mismatch": {
			instance: func() nicosdk.Interface {
				iface := nicosdk.NewInterface()
				iface.SetVpcPrefixId("other-prefix")
				return *iface
			}(),
			machine: infrav1.NicoMachineInterface{VPCPrefixID: testVPCPrefixID},
			want:    false,
		},
		"rejects subnet mismatch": {
			instance: func() nicosdk.Interface {
				iface := nicosdk.NewInterface()
				iface.SetSubnetId("other-subnet")
				return *iface
			}(),
			machine: infrav1.NicoMachineInterface{SubnetID: testSubnetID},
			want:    false,
		},
		"rejects when spec physical disagrees": {
			instance: func() nicosdk.Interface {
				iface := nicosdk.NewInterface()
				iface.SetVpcPrefixId(testVPCPrefixID)
				iface.SetIsPhysical(true)
				return *iface
			}(),
			machine: infrav1.NicoMachineInterface{
				VPCPrefixID: testVPCPrefixID,
				Physical:    &physicalFalse,
			},
			want: false,
		},
		"accepts when spec physical agrees": {
			instance: func() nicosdk.Interface {
				iface := nicosdk.NewInterface()
				iface.SetVpcPrefixId(testVPCPrefixID)
				iface.SetIsPhysical(true)
				return *iface
			}(),
			machine: infrav1.NicoMachineInterface{
				VPCPrefixID: testVPCPrefixID,
				Physical:    &physicalTrue,
			},
			want: true,
		},
		"rejects when spec ipAddress disagrees": {
			instance: func() nicosdk.Interface {
				iface := nicosdk.NewInterface()
				iface.SetVpcPrefixId(testVPCPrefixID)
				iface.SetRequestedIpAddress(testIPAddress)
				return *iface
			}(),
			machine: infrav1.NicoMachineInterface{
				VPCPrefixID: testVPCPrefixID,
				IPAddress:   "10.0.0.99",
			},
			want: false,
		},
		"rejects when spec device disagrees": {
			instance: func() nicosdk.Interface {
				iface := nicosdk.NewInterface()
				iface.SetVpcPrefixId(testVPCPrefixID)
				iface.SetDevice(testDevice)
				return *iface
			}(),
			machine: infrav1.NicoMachineInterface{
				VPCPrefixID: testVPCPrefixID,
				Device:      "eth1",
			},
			want: false,
		},
		"rejects when spec deviceInstance disagrees": {
			instance: func() nicosdk.Interface {
				iface := nicosdk.NewInterface()
				iface.SetVpcPrefixId(testVPCPrefixID)
				iface.SetDeviceInstance(testDeviceIndex)
				return *iface
			}(),
			machine: infrav1.NicoMachineInterface{
				VPCPrefixID:    testVPCPrefixID,
				DeviceInstance: &otherDeviceInstance,
			},
			want: false,
		},
	}

	for name, params := range tests {
		t.Run(name, func(t *testing.T) {
			assert.Equal(t, params.want, nicomachine.InterfaceMatches(params.instance, params.machine))
		})
	}
}
