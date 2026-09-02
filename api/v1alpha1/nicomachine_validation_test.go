// SPDX-FileCopyrightText: Copyright (c) 2026 NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package v1alpha1

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

var _ = Describe("NicoMachine validation", func() {
	It("allows spec updates before providerID and the initial providerID assignment", func() {
		machine := instanceTypeNicoMachine("pre-provision", "")
		Expect(k8sClient.Create(ctx, machine)).To(Succeed())

		machine.Spec.Labels = map[string]string{"environment": "test"}
		machine.Spec.CloudInitInjectHostname = true
		Expect(k8sClient.Update(ctx, machine)).To(Succeed())

		machine.Spec.ProviderID = "nico://instance-pre-provision"
		Expect(k8sClient.Update(ctx, machine)).To(Succeed())
	})

	tests := []struct {
		name    string
		field   string
		machine *NicoMachine
		mutate  func(*NicoMachine)
	}{
		{name: "vpc-id", field: "vpcID", machine: instanceTypeNicoMachine("vpc-id", "nico://instance-vpc-id"), mutate: func(machine *NicoMachine) {
			machine.Spec.VPCID = "vpc-2"
		}},
		{name: "instance-type-id", field: "instanceTypeID", machine: instanceTypeNicoMachine("instance-type-id", "nico://instance-instance-type-id"), mutate: func(machine *NicoMachine) {
			machine.Spec.InstanceTypeID = "instance-type-2"
		}},
		{name: "interfaces", field: "interfaces", machine: instanceTypeNicoMachine("interfaces", "nico://instance-interfaces"), mutate: func(machine *NicoMachine) {
			machine.Spec.Interfaces[0].SubnetID = "subnet-2"
		}},
		{name: "infiniband-interfaces", field: "infinibandInterfaces", machine: instanceTypeNicoMachine("infiniband-interfaces", "nico://instance-infiniband-interfaces"), mutate: func(machine *NicoMachine) {
			machine.Spec.InfinibandInterfaces = []NicoMachineInfiniBandInterface{{PartitionID: "partition-1"}}
		}},
		{name: "infiniband-partition-id", field: "infinibandPartitionID", machine: instanceTypeNicoMachine("infiniband-partition-id", "nico://instance-infiniband-partition-id"), mutate: func(machine *NicoMachine) {
			machine.Spec.InfinibandPartitionID = "partition-1"
		}},
		{name: "nvlink-interfaces", field: "nvLinkInterfaces", machine: instanceTypeNicoMachine("nvlink-interfaces", "nico://instance-nvlink-interfaces"), mutate: func(machine *NicoMachine) {
			machine.Spec.NVLinkInterfaces = []NicoMachineNVLinkInterface{{NVLinkLogicalPartitionID: "partition-1"}}
		}},
		{name: "nvlink-logical-partition-id", field: "nvLinkLogicalPartitionID", machine: instanceTypeNicoMachine("nvlink-logical-partition-id", "nico://instance-nvlink-logical-partition-id"), mutate: func(machine *NicoMachine) {
			machine.Spec.NVLinkLogicalPartitionID = "partition-1"
		}},
		{name: "ssh-key-group-ids", field: "sshKeyGroupIDs", machine: instanceTypeNicoMachine("ssh-key-group-ids", "nico://instance-ssh-key-group-ids"), mutate: func(machine *NicoMachine) {
			machine.Spec.SSHKeyGroupIDs = []string{"key-group-1"}
		}},
		{name: "ipxe-script", field: "ipxeScript", machine: instanceTypeNicoMachine("ipxe-script", "nico://instance-ipxe-script"), mutate: func(machine *NicoMachine) {
			machine.Spec.IpxeScript = "#!ipxe"
		}},
		{name: "cloud-init-inject-hostname", field: "cloudInitInjectHostname", machine: instanceTypeNicoMachine("cloud-init-inject-hostname", "nico://instance-cloud-init-inject-hostname"), mutate: func(machine *NicoMachine) {
			machine.Spec.CloudInitInjectHostname = true
		}},
		{name: "labels", field: "labels", machine: instanceTypeNicoMachine("labels", "nico://instance-labels"), mutate: func(machine *NicoMachine) {
			machine.Spec.Labels = map[string]string{"environment": "test"}
		}},
		{name: "machine-id", field: "machineID", machine: targetedNicoMachine("machine-id", "nico://instance-machine-id"), mutate: func(machine *NicoMachine) {
			machine.Spec.MachineID = "machine-2"
		}},
		{name: "allow-unhealthy-machine", field: "allowUnhealthyMachine", machine: targetedNicoMachine("allow-unhealthy-machine", "nico://instance-allow-unhealthy-machine"), mutate: func(machine *NicoMachine) {
			machine.Spec.AllowUnhealthyMachine = true
		}},
		{name: "provider-id", field: "providerID", machine: instanceTypeNicoMachine("provider-id", "nico://instance-provider-id"), mutate: func(machine *NicoMachine) {
			machine.Spec.ProviderID = "nico://instance-provider-id-updated"
		}},
	}

	for _, test := range tests {
		test := test
		It("rejects an update to spec."+test.field+" after providerID is set", func() {
			Expect(k8sClient.Create(ctx, test.machine)).To(Succeed())
			test.mutate(test.machine)

			err := k8sClient.Update(ctx, test.machine)
			Expect(err).Should(HaveOccurred())
			Expect(apierrors.IsInvalid(err)).Should(BeTrue())
			Expect(err.Error()).Should(ContainSubstring("spec." + test.field + " is immutable after"))
		})
	}
})

func instanceTypeNicoMachine(name, providerID string) *NicoMachine {
	return &NicoMachine{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "validation"},
		Spec: NicoMachineSpec{
			VPCID:          "vpc-1",
			InstanceTypeID: "instance-type-1",
			Interfaces:     []NicoMachineInterface{{SubnetID: "subnet-1"}},
			ProviderID:     providerID,
		},
	}
}

func targetedNicoMachine(name, providerID string) *NicoMachine {
	return &NicoMachine{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "validation"},
		Spec: NicoMachineSpec{
			VPCID:      "vpc-1",
			MachineID:  "machine-1",
			Interfaces: []NicoMachineInterface{{SubnetID: "subnet-1"}},
			ProviderID: providerID,
		},
	}
}
