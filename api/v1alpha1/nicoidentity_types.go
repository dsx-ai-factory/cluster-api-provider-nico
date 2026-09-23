// SPDX-FileCopyrightText: Copyright (c) 2026 NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package v1alpha1

import metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

// NicoIdentityCredentialsReference identifies a Secret in the Identity's namespace.
type NicoIdentityCredentialsReference struct {
	// Name is the name of the Secret containing NICo connection and authentication settings.
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=253
	// +kubebuilder:validation:Pattern=`^[a-z0-9]([-a-z0-9]*[a-z0-9])?(\.[a-z0-9]([-a-z0-9]*[a-z0-9])?)*$`
	Name string `json:"name"`
}

// NicoIdentitySpec defines the credentials to observe without changing provisioning.
type NicoIdentitySpec struct {
	// CredentialsRef names the Secret in this Identity's namespace. The reference is mutable.
	CredentialsRef NicoIdentityCredentialsReference `json:"credentialsRef"`
}

// NicoIdentityStatus records the last completed credential validation attempt.
type NicoIdentityStatus struct {
	// Conditions contains Ready for the latest completed validation. Ready's
	// observedGeneration identifies the spec checked. Missing or outdated conditions
	// do not establish current health. Additional condition types may be added.
	// +optional
	// +listType=map
	// +listMapKey=type
	// +kubebuilder:validation:MaxItems=32
	Conditions []metav1.Condition `json:"conditions,omitempty"`

	// LastCheckedTime is when the reported attempt completed, including failures.
	// It is absent before the first completed attempt. Readers must check its age;
	// a condition's lastTransitionTime does not establish observation freshness.
	// +optional
	LastCheckedTime *metav1.Time `json:"lastCheckedTime,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Namespaced,categories=cluster-api
// +kubebuilder:printcolumn:name="Ready",type="string",JSONPath=".status.conditions[?(@.type=='Ready')].status",description="Result of the last completed credential validation"
// +kubebuilder:printcolumn:name="LastChecked",type="date",JSONPath=".status.lastCheckedTime",description="Completion time of the reported validation attempt"
// +kubebuilder:printcolumn:name="Age",type="date",JSONPath=".metadata.creationTimestamp"

// NicoIdentity is an observation of NICo credentials, not a provisioning dependency.
// The API is available before its controller; absent status is not a successful check.
type NicoIdentity struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec NicoIdentitySpec `json:"spec"`
	// +optional
	Status NicoIdentityStatus `json:"status,omitempty,omitzero"`
}

// +kubebuilder:object:root=true

// NicoIdentityList contains a list of NicoIdentity.
type NicoIdentityList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []NicoIdentity `json:"items"`
}

func init() {
	objectTypes = append(objectTypes, &NicoIdentity{}, &NicoIdentityList{})
}

// GetConditions returns the Identity's conditions.
func (i *NicoIdentity) GetConditions() []metav1.Condition {
	return i.Status.Conditions
}

// SetConditions sets the Identity's conditions.
func (i *NicoIdentity) SetConditions(conditions []metav1.Condition) {
	i.Status.Conditions = conditions
}
