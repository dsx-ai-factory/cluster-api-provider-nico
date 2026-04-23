package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	clusterv1 "sigs.k8s.io/cluster-api/api/core/v1beta2"
)

// NicoMachineInterface defines one NICo interface attachment request.
type NicoMachineInterface struct {
	// SubnetID is the subnet ID for this attachment.
	// Use SubnetID for Ethernet network virtualization.
	// Exactly one of SubnetID or VPCPrefixID must be set.
	// +optional
	SubnetID string `json:"subnetId,omitempty"`

	// VPCPrefixID is the VPC prefix ID for this attachment.
	// Use VPCPrefixID for next-generation networking.
	// Exactly one of SubnetID or VPCPrefixID must be set.
	// +optional
	VPCPrefixID string `json:"vpcPrefixId,omitempty"`

	// Physical requests this attachment over a physical interface.
	// +optional
	Physical *bool `json:"physical,omitempty"`

	// Device is the device name to use for this attachment.
	// +optional
	Device string `json:"device,omitempty"`

	// DeviceInstance is the device instance index to use for this attachment.
	// +optional
	DeviceInstance *int32 `json:"deviceInstance,omitempty"`
}

// NicoMachineSpec defines the desired state of NicoMachine.
type NicoMachineSpec struct {
	// InstanceTypeID is the instance type ID to provision.
	InstanceTypeID string `json:"instanceTypeId"`

	// Interfaces are the interface attachment requests for the instance.
	// Use subnet-based attachments for Ethernet network virtualization and VPC prefix-based attachments for next-generation networking.
	// +kubebuilder:validation:MinItems=1
	Interfaces []NicoMachineInterface `json:"interfaces"`

	// SSHKeyGroupIDs are the allowed SSH key group IDs for Serial over LAN access.
	// +optional
	SSHKeyGroupIDs []string `json:"sshKeyGroupIds,omitempty"`

	// IpxeScript is the iPXE script used to boot this instance.
	// +optional
	IpxeScript string `json:"ipxeScript,omitempty"`

	// CloudInitInjectHostname prepends hostname directives to the bootstrap cloud-config.
	// +optional
	CloudInitInjectHostname bool `json:"cloudInitInjectHostname,omitempty"`

	// Labels are applied to the instance.
	// +optional
	Labels map[string]string `json:"labels,omitempty"`

	// MachineID is the machine ID for targeted instance creation.
	// +optional
	MachineID string `json:"machineId,omitempty"`

	// AllowUnhealthyMachine allows targeted instance creation on a machine in Error status.
	// +optional
	AllowUnhealthyMachine bool `json:"allowUnhealthyMachine,omitempty"`

	// ProviderID is set to `nico://<instance-id>` after the NICo instance is created.
	// +optional
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=512
	ProviderID string `json:"providerID,omitempty"`
}

// NicoMachineInitializationStatus provides observations of the NicoMachine initialization process.
// +kubebuilder:validation:MinProperties=1
type NicoMachineInitializationStatus struct {
	// Provisioned is true once the backing instance has been created.
	// +optional
	Provisioned *bool `json:"provisioned,omitempty"`
}

// NicoMachineStatus defines the observed state of NicoMachine.
type NicoMachineStatus struct {
	// Initialization provides provisioning observations for the backing instance.
	// +optional
	Initialization NicoMachineInitializationStatus `json:"initialization,omitempty,omitzero"`

	// Conditions describes the current machine state.
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`

	// Ready is true once the backing instance reaches Ready.
	// +optional
	Ready bool `json:"ready,omitempty"`

	// InstanceID is the instance identifier backing this machine.
	// +optional
	InstanceID string `json:"instanceId,omitempty"`

	// MachineID is the machine ID for this machine.
	// +optional
	MachineID string `json:"machineId,omitempty"`

	// TpmEkPubHash is the TPM EK public hash for this machine.
	// +optional
	TpmEkPubHash string `json:"tpmEkPubHash,omitempty"`

	// Addresses contains addresses observed on the backing instance.
	// +optional
	Addresses []clusterv1.MachineAddress `json:"addresses,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Namespaced,categories=cluster-api,shortName=nicom

// NicoMachine is the Schema for the nicomachines API.
type NicoMachine struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   NicoMachineSpec   `json:"spec,omitempty"`
	Status NicoMachineStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// NicoMachineList contains a list of NicoMachine.
type NicoMachineList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []NicoMachine `json:"items"`
}

func init() {
	objectTypes = append(objectTypes, &NicoMachine{}, &NicoMachineList{})
}

// GetConditions returns the set of conditions for this object.
func (m *NicoMachine) GetConditions() []metav1.Condition {
	return m.Status.Conditions
}

// SetConditions sets conditions for this object.
func (m *NicoMachine) SetConditions(conditions []metav1.Condition) {
	m.Status.Conditions = conditions
}
