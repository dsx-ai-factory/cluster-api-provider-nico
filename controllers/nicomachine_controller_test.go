package controllers

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	infrav1 "gitlab-master.nvidia.com/nke/cluster-api-provider-nico/api/v1alpha1"
	"gitlab-master.nvidia.com/nke/cluster-api-provider-nico/internal/nico"

	nicosdk "github.com/NVIDIA/ncx-infra-controller-rest/sdk/standard"
)

const testProviderInstanceID = "instance-1"

func TestSetObservedTopologyPreservesRawForgeValues(t *testing.T) {
	machine := &infrav1.NicoMachine{}
	instance := nicosdk.NewInstance()
	instance.SetMachineId("machine-1")
	instance.SetSiteId("site-1")
	instance.SetVpcId("vpc-1")
	site := nicosdk.NewSite()
	site.SetName("New York / A")
	vpc := nicosdk.NewVPC()
	vpc.SetName("Tenant VPC")

	setObservedTopology(machine, instance, site, vpc)

	assert.Equal(t, "machine-1", machine.Status.MachineID)
	assert.Equal(t, "site-1", machine.Status.SiteID)
	assert.Equal(t, "New York / A", machine.Status.SiteName)
	assert.Equal(t, "vpc-1", machine.Status.VPCID)
	assert.Equal(t, "Tenant VPC", machine.Status.VPCName)
}

func TestSetObservedTopologyLeavesUnavailableNamesAbsent(t *testing.T) {
	machine := &infrav1.NicoMachine{Status: infrav1.NicoMachineStatus{
		SiteName: "stale site name",
		VPCName:  "stale VPC name",
	}}
	instance := nicosdk.NewInstance()
	instance.SetMachineId("machine-1")
	instance.SetSiteId("site-1")
	instance.SetVpcId("vpc-1")

	setObservedTopology(machine, instance, nil, nil)

	assert.Equal(t, "machine-1", machine.Status.MachineID)
	assert.Equal(t, "site-1", machine.Status.SiteID)
	assert.Empty(t, machine.Status.SiteName)
	assert.Equal(t, "vpc-1", machine.Status.VPCID)
	assert.Empty(t, machine.Status.VPCName)
}

func TestObservedTopologyLabels(t *testing.T) {
	// This test protects the post-create VM label update contract, including the
	// late-arriving machine-id and normalized topology name labels.
	machine := &infrav1.NicoMachine{Status: infrav1.NicoMachineStatus{
		MachineID: "machine-1",
		SiteID:    "site-1",
		SiteName:  "Forge VMs Site",
		VPCID:     "vpc-1",
		VPCName:   "Mock VPC",
	}}

	labels := observedTopologyLabels(machine)

	assert.Equal(t, "machine-1", labels[labelKeyMachineID])
	assert.Equal(t, "site-1", labels[labelKeySiteID])
	assert.Equal(t, "forge-vms-site", labels[labelKeySiteName])
	assert.Equal(t, "vpc-1", labels[labelKeyVPCID])
	assert.Equal(t, "mock-vpc", labels[labelKeyVPCName])
}

func TestNicoMachineReconciler_ProviderIDClaimedBy(t *testing.T) {
	type parameters struct {
		existing []infrav1.NicoMachine
		machine  infrav1.NicoMachine
		want     string
	}

	tests := map[string]parameters{
		"returns empty when provider ID is not claimed": {
			existing: []infrav1.NicoMachine{
				nicoMachine("default", "other", "other-instance", "other-uid"),
			},
			machine: nicoMachine("default", "machine", testProviderInstanceID, "machine-uid"),
		},
		"ignores the current NicoMachine": {
			existing: []infrav1.NicoMachine{
				nicoMachine("default", "machine", testProviderInstanceID, "machine-uid"),
			},
			machine: nicoMachine("default", "machine", testProviderInstanceID, "machine-uid"),
		},
		"returns claiming NicoMachine": {
			existing: []infrav1.NicoMachine{
				nicoMachine("default", "claiming-machine", testProviderInstanceID, "claiming-machine-uid"),
			},
			machine: nicoMachine("default", "machine", testProviderInstanceID, "machine-uid"),
			want:    "default/claiming-machine",
		},
	}

	for name, params := range tests {
		t.Run(name, func(t *testing.T) {
			scheme := runtime.NewScheme()
			require.NoError(t, infrav1.AddToScheme(scheme))

			objects := make([]runtime.Object, 0, len(params.existing))
			for i := range params.existing {
				objects = append(objects, &params.existing[i])
			}
			reconciler := NicoMachineReconciler{
				Client: fake.NewClientBuilder().
					WithScheme(scheme).
					WithRuntimeObjects(objects...).
					Build(),
			}

			got, err := reconciler.providerIDClaimedBy(context.Background(), params.machine, testProviderInstanceID)

			require.NoError(t, err)
			assert.Equal(t, params.want, got)
		})
	}
}

func nicoMachine(namespace, name, instanceID, uid string) infrav1.NicoMachine {
	return infrav1.NicoMachine{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: namespace,
			Name:      name,
			UID:       types.UID(uid),
		},
		Spec: infrav1.NicoMachineSpec{
			ProviderID: nico.ProviderID(instanceID),
		},
	}
}
