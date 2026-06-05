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
)

const testProviderInstanceID = "instance-1"

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
