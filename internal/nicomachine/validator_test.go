package nicomachine_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	infrav1 "gitlab-master.nvidia.com/nke/cluster-api-provider-nico/api/v1alpha1"
	"gitlab-master.nvidia.com/nke/cluster-api-provider-nico/internal/nicomachine"

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
		instance *nicosdk.Instance
		machine  infrav1.NicoMachine
		wantErr  string
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
	}

	for name, params := range tests {
		t.Run(name, func(t *testing.T) {
			err := nicomachine.ValidateInstance(params.instance, params.machine)
			if params.wantErr == "" {
				assert.NoError(t, err)
				return
			}
			assert.ErrorContains(t, err, params.wantErr)
		})
	}
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
