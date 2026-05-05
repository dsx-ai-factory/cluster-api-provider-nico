package v1alpha1

import (
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// NicoClusterSpec defines the desired state of NicoCluster.
type NicoClusterSpec struct {
	// IdentityRef points to the Secret with NICo endpoint and authentication settings.
	// When unset, the controller falls back to the provider-level credentials Secret
	// in the manager namespace.
	// +optional
	IdentityRef corev1.LocalObjectReference `json:"identityRef,omitempty,omitzero"`

	// SiteID is the site where this provider should create and look up instances.
	SiteID string `json:"siteId"`

	// VPCID is the VPC where this provider should create instances.
	VPCID string `json:"vpcId"`
}

// NicoClusterInitializationStatus provides observations of the NicoCluster initialization process.
// +kubebuilder:validation:MinProperties=1
type NicoClusterInitializationStatus struct {
	// Provisioned is true once the provider has validated NICo access for this cluster.
	// +optional
	Provisioned *bool `json:"provisioned,omitempty"`
}

// NicoClusterStatus defines the observed state of NicoCluster.
type NicoClusterStatus struct {
	// Initialization provides provisioning observations for this NICo-backed cluster.
	// +optional
	Initialization NicoClusterInitializationStatus `json:"initialization,omitempty,omitzero"`

	// Conditions describes the current cluster state.
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`

	// Ready is true once the provider can reach NICo and resolve tenant context for this cluster.
	// +optional
	Ready bool `json:"ready,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Namespaced,categories=cluster-api,shortName=nicoc

// NicoCluster is the Schema for the nicoclusters API.
type NicoCluster struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   NicoClusterSpec   `json:"spec,omitempty"`
	Status NicoClusterStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// NicoClusterList contains a list of NicoCluster.
type NicoClusterList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []NicoCluster `json:"items"`
}

func init() {
	objectTypes = append(objectTypes, &NicoCluster{}, &NicoClusterList{})
}

// GetConditions returns the set of conditions for this object.
func (c *NicoCluster) GetConditions() []metav1.Condition {
	return c.Status.Conditions
}

// SetConditions sets conditions for this object.
func (c *NicoCluster) SetConditions(conditions []metav1.Condition) {
	c.Status.Conditions = conditions
}
