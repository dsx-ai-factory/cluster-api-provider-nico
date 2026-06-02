package v1alpha1

const (
	// DeletingReason indicates the resource is being deleted.
	DeletingReason = "Deleting"

	// WaitingForIdentitySecretReason indicates the configured identity Secret is not available yet.
	WaitingForIdentitySecretReason = "WaitingForIdentitySecret"

	// IdentityConfigurationFailedReason indicates the identity configuration is invalid.
	IdentityConfigurationFailedReason = "IdentityConfigurationFailed"

	// TenantResolutionFailedReason indicates NICo tenant resolution failed.
	TenantResolutionFailedReason = "TenantResolutionFailed"

	// InfrastructureReadyReason indicates the infrastructure resource is ready.
	InfrastructureReadyReason = "InfrastructureReady"

	// WaitingForBootstrapDataReason indicates bootstrap data is not available yet.
	WaitingForBootstrapDataReason = "WaitingForBootstrapData"

	// BootstrapDataInvalidReason indicates bootstrap data could not be used.
	BootstrapDataInvalidReason = "BootstrapDataInvalid"

	// InstanceCreateRequestInvalidReason indicates the NICo instance create request could not be built.
	InstanceCreateRequestInvalidReason = "InstanceCreateRequestInvalid"

	// AvailabilityCheckFailedReason indicates the instance type availability check failed.
	AvailabilityCheckFailedReason = "AvailabilityCheckFailed"

	// InstanceTypeNotFoundReason indicates the configured instance type does not exist.
	InstanceTypeNotFoundReason = "InstanceTypeNotFound"

	// InstanceTypeUnavailableReason indicates the configured instance type has no available capacity.
	InstanceTypeUnavailableReason = "InstanceTypeUnavailable"

	// InstanceCreateFailedReason indicates NICo instance creation failed.
	InstanceCreateFailedReason = "InstanceCreateFailed"

	// InstanceMissingReason indicates the backing NICo instance could not be found.
	InstanceMissingReason = "InstanceMissing"

	// InstanceNotReadyReason indicates the backing NICo instance is not ready yet.
	InstanceNotReadyReason = "InstanceNotReady"

	// InstanceReadyReason indicates the backing NICo instance is ready.
	InstanceReadyReason = "InstanceReady"

	// WaitingForNicoMachinesDeletionReason indicates the cluster is waiting for all NicoMachines to be deleted.
	WaitingForNicoMachinesDeletionReason = "WaitingForNicoMachinesDeletion"
)
