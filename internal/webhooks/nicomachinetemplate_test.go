// SPDX-FileCopyrightText: Copyright (c) 2026 NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package webhooks

import (
	"testing"

	"github.com/stretchr/testify/require"
	admissionv1 "k8s.io/api/admission/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	clusterv1 "sigs.k8s.io/cluster-api/api/core/v1beta2"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	infrav1 "github.com/dsx-ai-factory/cluster-api-provider-nico/api/v1alpha1"
)

func TestNicoMachineTemplateValidateUpdate(t *testing.T) {
	tests := []struct {
		name            string
		changeSpec      bool
		dryRun          bool
		topology        bool
		invalidMetadata bool
		providerID      string
		wantInvalid     bool
	}{
		{
			name: "accepts unchanged spec",
		},
		{
			name:        "rejects changed spec",
			changeSpec:  true,
			wantInvalid: true,
		},
		{
			name:        "rejects changed spec during ordinary dry run",
			changeSpec:  true,
			dryRun:      true,
			wantInvalid: true,
		},
		{
			name:        "rejects changed spec with annotation alone",
			changeSpec:  true,
			topology:    true,
			wantInvalid: true,
		},
		{
			name:       "accepts changed spec during topology dry run",
			changeSpec: true,
			dryRun:     true,
			topology:   true,
		},
		{
			name:        "rejects provider ID during topology dry run",
			dryRun:      true,
			topology:    true,
			providerID:  "nico://instance-1",
			wantInvalid: true,
		},
		{
			name:            "rejects invalid metadata during topology dry run",
			changeSpec:      true,
			dryRun:          true,
			topology:        true,
			invalidMetadata: true,
			wantInvalid:     true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			oldObj := &infrav1.NicoMachineTemplate{}
			oldObj.Spec.Template.Spec.VPCID = "vpc-1"
			newObj := oldObj.DeepCopy()
			newObj.Spec.Template.Spec.ProviderID = tc.providerID

			if tc.changeSpec {
				newObj.Spec.Template.Spec.VPCID = "vpc-2"
			}
			if tc.topology {
				newObj.Annotations = map[string]string{
					clusterv1.TopologyDryRunAnnotation: "",
				}
			}
			if tc.invalidMetadata {
				newObj.Spec.Template.ObjectMeta.Labels = map[string]string{
					"invalid/key/": "value",
				}
			}

			ctx := admission.NewContextWithRequest(t.Context(), admission.Request{
				AdmissionRequest: admissionv1.AdmissionRequest{
					DryRun: new(tc.dryRun),
				},
			})

			_, err := (&NicoMachineTemplate{}).ValidateUpdate(ctx, oldObj, newObj)
			if tc.wantInvalid {
				require.True(t, apierrors.IsInvalid(err), "expected Invalid, got %v", err)
			} else {
				require.NoError(t, err)
			}
		})
	}
}

func TestNicoMachineTemplateValidateUpdateRequiresRequest(t *testing.T) {
	obj := &infrav1.NicoMachineTemplate{}

	_, err := (&NicoMachineTemplate{}).ValidateUpdate(t.Context(), obj, obj.DeepCopy())

	require.True(t, apierrors.IsBadRequest(err), "expected BadRequest, got %v", err)
}
