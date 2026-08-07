package nicomachine_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"k8s.io/utils/ptr"

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
				Physical:       ptr.To(testPhysical),
				DeviceInstance: ptr.To(testDeviceIndex),
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
