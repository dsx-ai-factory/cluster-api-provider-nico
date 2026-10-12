// SPDX-FileCopyrightText: Copyright (c) 2026 NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package webhooks

import (
	"context"
	"fmt"
	"reflect"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/util/validation/field"
	"sigs.k8s.io/cluster-api/util/topology"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	infrav1 "github.com/dsx-ai-factory/cluster-api-provider-nico/api/v1alpha1"
)

// NicoMachineTemplate validates NicoMachineTemplate updates.
// +kubebuilder:object:generate=false
type NicoMachineTemplate struct{}

var _ admission.Validator[*infrav1.NicoMachineTemplate] = &NicoMachineTemplate{}

// SetupWebhookWithManager registers the validating webhook with the manager.
func (w *NicoMachineTemplate) SetupWebhookWithManager(mgr ctrl.Manager) error {
	return ctrl.NewWebhookManagedBy(mgr, &infrav1.NicoMachineTemplate{}).
		WithValidator(w).
		Complete()
}

// +kubebuilder:webhook:verbs=create;update,path=/validate-infrastructure-cluster-x-k8s-io-v1alpha1-nicomachinetemplate,mutating=false,failurePolicy=fail,matchPolicy=Equivalent,groups=infrastructure.cluster.x-k8s.io,resources=nicomachinetemplates,versions=v1alpha1,name=validation.nicomachinetemplate.infrastructure.cluster.x-k8s.io,sideEffects=None,admissionReviewVersions=v1

// ValidateCreate validates template metadata and forbids a fixed provider ID.
func (*NicoMachineTemplate) ValidateCreate(_ context.Context, obj *infrav1.NicoMachineTemplate) (admission.Warnings, error) {
	allErrs := obj.Spec.Template.ObjectMeta.Validate(field.NewPath("spec", "template", "metadata"))
	if obj.Spec.Template.Spec.ProviderID != "" {
		allErrs = append(allErrs, field.Forbidden(field.NewPath("spec", "template", "spec", "providerID"), "providerID must be empty in a NicoMachineTemplate"))
	}
	if len(allErrs) > 0 {
		return nil, apierrors.NewInvalid(infrav1.GroupVersion.WithKind("NicoMachineTemplate").GroupKind(), obj.Name, allErrs)
	}
	return nil, nil
}

// ValidateUpdate enforces spec immutability except during topology dry-runs.
func (*NicoMachineTemplate) ValidateUpdate(ctx context.Context, oldObj, newObj *infrav1.NicoMachineTemplate) (admission.Warnings, error) {
	req, err := admission.RequestFromContext(ctx)
	if err != nil {
		return nil, apierrors.NewBadRequest(fmt.Sprintf("expected an admission.Request inside context: %v", err))
	}

	var allErrs field.ErrorList
	if !topology.IsDryRunRequest(req, newObj) &&
		!reflect.DeepEqual(oldObj.Spec.Template.Spec, newObj.Spec.Template.Spec) {
		allErrs = append(allErrs,
			field.Invalid(
				field.NewPath("spec", "template", "spec"),
				newObj.Spec.Template.Spec,
				"NicoMachineTemplate.spec.template.spec cannot be changed. Create a new NicoMachineTemplate to update the machine configuration.",
			),
		)
	}

	allErrs = append(allErrs, newObj.Spec.Template.ObjectMeta.Validate(field.NewPath("spec", "template", "metadata"))...)
	if len(allErrs) > 0 {
		return nil, apierrors.NewInvalid(infrav1.GroupVersion.WithKind("NicoMachineTemplate").GroupKind(), newObj.Name, allErrs)
	}

	return nil, nil
}

// ValidateDelete accepts template deletion.
func (*NicoMachineTemplate) ValidateDelete(_ context.Context, _ *infrav1.NicoMachineTemplate) (admission.Warnings, error) {
	return nil, nil
}
