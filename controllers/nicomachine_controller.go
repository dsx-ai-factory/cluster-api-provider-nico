// SPDX-FileCopyrightText: Copyright (c) 2026 NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package controllers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/cluster-api/util/annotations"
	"sigs.k8s.io/cluster-api/util/predicates"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	crpredicate "sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	clusterv1 "sigs.k8s.io/cluster-api/api/core/v1beta2"
	"sigs.k8s.io/cluster-api/util"
	"sigs.k8s.io/cluster-api/util/conditions"
	"sigs.k8s.io/cluster-api/util/patch"

	infrav1 "github.com/dsx-ai-factory/cluster-api-provider-nico/api/v1alpha1"
	"github.com/dsx-ai-factory/cluster-api-provider-nico/internal/nico"
	nicomachine "github.com/dsx-ai-factory/cluster-api-provider-nico/internal/nicomachine"

	nicosdk "github.com/NVIDIA/infra-controller/rest-api/sdk/standard"
)

const (
	nicoMachineFinalizer                 = "infrastructure.cluster.x-k8s.io/nicomachine"
	nicoMachineCredentialsSecretRefIndex = "nicoMachineCredentialsSecretRef"
	lastRebootTriggeredAnnotation        = "nico.nvidia.com/last-reboot-triggered-timestamp"
	machineRequeueFast                   = 15 * time.Second
	machineRequeueSlow                   = 30 * time.Second
	machineReadyRequeue                  = 5 * time.Minute
	machineReadyJitterWindow             = 1 * time.Minute
	instanceTypeUnavailableWait          = 2 * time.Minute

	// These NKE label keys are applied to the backing NICo instance so the VM
	// records carry the same topology identifiers that Kubernetes nodes expose.
	labelKeyMachineID = "nke.nvidia.com/machine-id"
	labelKeySiteID    = "nke.nvidia.com/site-id"
	labelKeySiteName  = "nke.nvidia.com/site-name"
	labelKeyVPCID     = "nke.nvidia.com/vpc-id"
	labelKeyVPCName   = "nke.nvidia.com/vpc-name"
)

var nicoMachineOwnedConditions = []string{
	clusterv1.AvailableCondition,
	clusterv1.ReadyCondition,
	infrav1.SyncedCondition,
	clusterv1.PausedCondition,
	clusterv1.DeletingCondition,
	infrav1.MachineProvisionedCondition,
	infrav1.FailureDomainDriftedCondition,
}

// NicoMachineReconciler reconciles a NicoMachine object.
type NicoMachineReconciler struct {
	client.Client

	// APIReader reads straight from the API server, bypassing the manager's
	// cache. Reconciliation branches on identifiers this controller wrote a
	// moment earlier, and its own write wakes it again through the watch.
	// Reading the cache there can return the version from before that write,
	// which reads as "nothing created yet" and creates a second instance. The
	// cost is one uncached read per pass, against one object.
	APIReader        client.Reader
	Scheme           *runtime.Scheme
	ProviderConfig   nico.ProviderConfig
	WatchFilterValue string
}

// +kubebuilder:rbac:groups=infrastructure.cluster.x-k8s.io,resources=nicomachines,verbs=get;list;watch;create;update;patch
// +kubebuilder:rbac:groups=infrastructure.cluster.x-k8s.io,resources=nicomachines/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=infrastructure.cluster.x-k8s.io,resources=nicomachines/finalizers,verbs=update
// +kubebuilder:rbac:groups=infrastructure.cluster.x-k8s.io,resources=nicoclusters,verbs=get;list;watch
// +kubebuilder:rbac:groups=cluster.x-k8s.io,resources=machines,verbs=get;list;watch;patch;update
// +kubebuilder:rbac:groups=cluster.x-k8s.io,resources=clusters,verbs=get;list;watch
// +kubebuilder:rbac:groups="",resources=secrets,verbs=get;list;watch

func (r *NicoMachineReconciler) Reconcile(ctx context.Context, req ctrl.Request) (result ctrl.Result, retErr error) { //nolint:gocyclo
	log := ctrl.LoggerFrom(ctx)

	log.Info("reconciling NicoMachine")

	var nicoMachine infrav1.NicoMachine
	if err := r.reader().Get(ctx, req.NamespacedName, &nicoMachine); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	ownerMachine, err := util.GetOwnerMachine(ctx, r.Client, nicoMachine.ObjectMeta)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to get owner machine: %w", err)
	}
	if ownerMachine == nil {
		log.Info("Waiting for Machine controller to set OwnerRef on NicoMachine")
		return ctrl.Result{}, nil
	}

	cluster, err := util.GetClusterFromMetadata(ctx, r.Client, ownerMachine.ObjectMeta)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to get cluster from metadata: %w", err)
	}
	if cluster == nil {
		log.Info("Waiting for Machine to have Cluster set")
		return ctrl.Result{}, nil
	}

	patchHelper, err := patch.NewHelper(&nicoMachine, r.Client)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to create patch helper: %w", err)
	}
	defer func() {
		if conditionErr := setNicoMachineConditions(&nicoMachine); conditionErr != nil {
			retErr = errors.Join(retErr, conditionErr)
		}
		if patchErr := patchHelper.Patch(ctx, &nicoMachine, patch.WithOwnedConditions{Conditions: nicoMachineOwnedConditions}); patchErr != nil {
			retErr = errors.Join(retErr, patchErr)
		}
	}()

	if annotations.IsPaused(cluster, &nicoMachine) {
		conditions.Set(&nicoMachine, metav1.Condition{
			Type:   clusterv1.PausedCondition,
			Status: metav1.ConditionTrue,
			Reason: clusterv1.PausedReason,
		})
		log.V(1).Info("Reconciliation is paused")
		return ctrl.Result{}, nil
	}
	conditions.Set(&nicoMachine, metav1.Condition{
		Type:   clusterv1.PausedCondition,
		Status: metav1.ConditionFalse,
		Reason: clusterv1.NotPausedReason,
	})

	if !nicoMachine.DeletionTimestamp.IsZero() {
		return r.reconcileDelete(ctx, ownerMachine, cluster, &nicoMachine)
	}

	if controllerutil.AddFinalizer(&nicoMachine, nicoMachineFinalizer) {
		// Persist the finalizer before creating anything external, so a crash
		// between create and patch can never orphan an instance.
		return ctrl.Result{Requeue: true}, nil
	}

	nicoCluster, err := r.resolveNicoCluster(ctx, cluster)
	if err != nil {
		setMachineProvisionedFalse(&nicoMachine, infrav1.WaitingForClusterInfrastructureReason, err.Error())
		return ctrl.Result{}, err
	}

	nicoClient, err := r.nicoClientForCluster(ctx, nicoCluster)
	if err != nil {
		if apierrors.IsNotFound(err) {
			setMachineProvisionedFalse(&nicoMachine, infrav1.WaitingForIdentitySecretReason, err.Error())
			return ctrl.Result{RequeueAfter: machineRequeueFast}, nil
		}
		setMachineProvisionedFalse(&nicoMachine, infrav1.IdentityConfigurationFailedReason, err.Error())
		return ctrl.Result{}, fmt.Errorf("failed to get nico client: %w", err)
	}

	// Adopt before the capacity check: a create can outlive a lost response or
	// status patch, and the orphan's machine would fail that check forever.
	if nicoMachine.Status.InstanceID == "" && nicoMachine.Spec.ProviderID == "" {
		instance, err := nicoClient.FindInstanceByName(ctx, nico.InstanceLookup{
			Name:              ownerMachine.Name,
			VPCID:             nicoMachine.Spec.VPCID,
			SiteID:            nicoCluster.Spec.SiteID,
			ExcludeTerminated: true,
		})
		if err == nil {
			log.Info("adopting existing NICo instance", "instanceID", instance.GetId())
			nicoMachine.Status.InstanceID = instance.GetId()
			nicoMachine.Spec.ProviderID = nico.ProviderID(instance.GetId())
		} else if !errors.Is(err, nico.ErrNotFound) {
			setMachineProvisionedFalse(&nicoMachine, infrav1.InstanceCreateFailedReason, err.Error())
			return ctrl.Result{}, fmt.Errorf("failed to find existing instance before create: %w", err)
		}
	}

	// if we have not yet created a NICo instance for this nicoMachine CR and this is not an NICo instance import
	if nicoMachine.Status.InstanceID == "" && nicoMachine.Spec.ProviderID == "" {
		if ownerMachine.Spec.Bootstrap.DataSecretName == nil || *ownerMachine.Spec.Bootstrap.DataSecretName == "" {
			setMachineProvisionedFalse(&nicoMachine, infrav1.WaitingForBootstrapDataReason, "")
			return ctrl.Result{RequeueAfter: machineRequeueFast}, nil
		}

		var bootstrapSecret corev1.Secret
		if err := r.Get(ctx, client.ObjectKey{
			Namespace: ownerMachine.Namespace,
			Name:      *ownerMachine.Spec.Bootstrap.DataSecretName,
		}, &bootstrapSecret); err != nil {
			if apierrors.IsNotFound(err) {
				setMachineProvisionedFalse(&nicoMachine, infrav1.WaitingForBootstrapDataReason, err.Error())
				return ctrl.Result{RequeueAfter: machineRequeueFast}, nil
			}
			return ctrl.Result{}, fmt.Errorf("failed to get bootstrap secret: %w", err)
		}

		bootstrapCloudConfig, err := bootstrapCloudConfigFromSecret(&bootstrapSecret)
		if err != nil {
			setMachineProvisionedFalse(&nicoMachine, infrav1.BootstrapDataInvalidReason, err.Error())
			return ctrl.Result{}, fmt.Errorf("failed to get bootstrap cloud config: %w", err)
		}
		if nicoMachine.Spec.CloudInitInjectHostname {
			bootstrapCloudConfig, err = nico.InjectHostnameCloudConfig(bootstrapCloudConfig, ownerMachine.Name)
			if err != nil {
				setMachineProvisionedFalse(&nicoMachine, infrav1.BootstrapDataInvalidReason, err.Error())
				return ctrl.Result{}, fmt.Errorf("failed to inject hostname cloud config: %w", err)
			}
		}

		tenantID, err := nicoClient.ResolveTenantID(ctx)
		if err != nil {
			setMachineProvisionedFalse(&nicoMachine, infrav1.TenantResolutionFailedReason, err.Error())
			return ctrl.Result{}, fmt.Errorf("failed to resolve tenant ID: %w", err)
		}

		var instanceTypeCapabilities nicomachine.InstanceTypeCapabilities
		if nicoMachine.Spec.InstanceTypeID != "" {
			// Query instance type availability before creating the instance when creating instances by instance type.
			// When instances are completely consumed, the instance creation API call will always fail. This preflight
			// check avoids spamming the NICo API logs with errors, and changes the re-queue interval.
			availability, err := instanceTypeAvailable(ctx, nicoClient, nicoMachine.Spec.InstanceTypeID)
			if err != nil {
				setMachineProvisionedFalse(&nicoMachine, infrav1.AvailabilityCheckFailedReason, err.Error())
				return ctrl.Result{}, fmt.Errorf("failed to check instance type availability: %w", err)
			}
			instanceTypeCapabilities = nicomachine.ParseInstanceTypeCapabilities(availability.instanceType)

			// Control-plane priority: defer workers before handling exhausted
			// capacity so a fresh control-plane machine gets the next allocation.
			if shouldDefer, err := r.deferForControlPlanePriority(ctx, &nicoMachine, availability.unusedUsable); err != nil {
				return ctrl.Result{}, err
			} else if shouldDefer {
				return ctrl.Result{RequeueAfter: instanceTypeUnavailableWait}, nil
			}

			if !availability.available {
				setMachineProvisionedFalse(&nicoMachine, availability.reason, availability.message)
				return ctrl.Result{RequeueAfter: instanceTypeUnavailableWait}, nil
			}
		}

		createReq, err := buildInstanceCreateRequest(ownerMachine.Name, tenantID, &nicoMachine, cluster.Name, bootstrapCloudConfig, instanceTypeCapabilities)
		if err != nil {
			setMachineProvisionedFalse(&nicoMachine, infrav1.InstanceCreateRequestInvalidReason, err.Error())
			return ctrl.Result{}, fmt.Errorf("failed to build instance create request: %w", err)
		}

		failureDomain := ownerMachine.Spec.FailureDomain
		log.Info("creating NICo instance", "failureDomain", failureDomain)
		instance, err := nicoClient.CreateInstance(ctx, *createReq, nico.InstancePlacement{
			FailureDomain: failureDomain,
			LabelKey:      nicoCluster.Spec.FailureDomainLabelKey,
		})
		if err != nil {
			if errors.Is(err, nico.ErrAlreadyExists) {
				log.Info("backing NICo instance already exists, find by name")
				instance, err = nicoClient.FindInstanceByName(ctx, nico.InstanceLookup{
					Name:   ownerMachine.Name,
					VPCID:  nicoMachine.Spec.VPCID,
					SiteID: nicoCluster.Spec.SiteID,
				})
			}
			if err != nil {
				if reason, requeueAfter, ok := failureDomainPlacementFailure(err, failureDomain); ok {
					setMachineProvisionedFalse(&nicoMachine, reason, fmt.Sprintf("failure domain %q: %s", failureDomain, err.Error()))
					if requeueAfter > 0 {
						return ctrl.Result{RequeueAfter: requeueAfter}, nil
					}
					return ctrl.Result{}, fmt.Errorf("failed to place instance in failure domain %q: %w", failureDomain, err)
				}
				reason := infrav1.InstanceCreateFailedReason
				if errors.Is(err, nico.ErrBadRequest) {
					// HTTP 400 is NICo's validation failure: unknown VPC, bad interfaces,
					// and also "No Machines are available for specified Instance Type"
					// (infra-controller folds that check into the same preflight).
					// Stamp InstanceCreateRequestInvalid so a spec error is visible.
					//
					// unusedUsable is already checked above, but it does not drop until
					// a create lands. Two machines can both see unusedUsable > 0; the
					// second create then 400s because the last machine was taken. That is
					// acceptable here: the next pass runs the capacity check again,
					// and stamps InstanceTypeUnavailableReason if the type is still empty.
					// NOTE: This reason is not in allowlisted in capacityWaitReasons that prioritize control-plane machines
					reason = infrav1.InstanceCreateRequestInvalidReason
				}
				setMachineProvisionedFalse(&nicoMachine, reason, err.Error())
				return ctrl.Result{}, fmt.Errorf("failed to create or find instance: %w", err)
			}
		}

		nicoMachine.Status.InstanceID = instance.GetId()
		nicoMachine.Spec.ProviderID = nico.ProviderID(instance.GetId())

		// Written before the first poll, so a restart resumes this instance rather
		// than creating a second one for the same Machine.
		return ctrl.Result{RequeueAfter: machineRequeueFast}, nil
	}

	instanceID, err := nico.InstanceID(nicoMachine.Spec.ProviderID)
	if err != nil {
		setMachineProvisionedFalse(&nicoMachine, infrav1.InstanceNotFoundReason, err.Error())
		return ctrl.Result{}, nil
	}

	instance, err := nicoClient.GetInstance(ctx, instanceID)
	if err != nil {
		if errors.Is(err, nico.ErrNotFound) {
			setMachineProvisionedFalse(&nicoMachine, infrav1.InstanceMissingReason, err.Error())
			return ctrl.Result{}, fmt.Errorf("backing NICo instance %q was not found: %w", instanceID, err)
		}
		return ctrl.Result{}, fmt.Errorf("failed to get instance: %w", err)
	}

	// if this is a NICo Machine import
	if nicoMachine.Spec.ProviderID != "" && nicoMachine.Status.InstanceID == "" {
		claimedBy, err := r.providerIDClaimedBy(ctx, nicoMachine, instanceID)
		if err != nil {
			return ctrl.Result{}, fmt.Errorf("failed to check if NICo instance %q is already claimed: %w", instanceID, err)
		}
		if claimedBy != "" {
			setMachineProvisionedFalse(&nicoMachine, infrav1.InstanceAlreadyClaimedReason, fmt.Sprintf("failed to import instance: NICo instance %q is already referenced by NicoMachine %s", instanceID, claimedBy))
			return ctrl.Result{}, nil
		}
		var instanceType *nicosdk.InstanceType
		if instanceTypeID := instance.GetInstanceTypeId(); instanceTypeID != "" {
			instanceType, err = nicoClient.GetInstanceTypeWithAllocationStats(ctx, instanceTypeID)
			if err != nil {
				setMachineProvisionedFalse(&nicoMachine, clusterv1.InspectionFailedReason, fmt.Sprintf("failed to inspect instance type: %s", err.Error()))
				return ctrl.Result{}, nil
			}
		}
		if err := nicomachine.ValidateInstance(instance, nicoMachine, instanceType); err != nil {
			setMachineProvisionedFalse(&nicoMachine, clusterv1.InspectionFailedReason, fmt.Sprintf("failed to import instance: %s", err.Error()))
			return ctrl.Result{}, nil
		}
		nicoMachine.Status.InstanceID = instance.GetId()
	}

	requestedDomain := ownerMachine.Spec.FailureDomain
	observedDomain, err := observeFailureDomain(ctx, nicoClient, &nicoMachine, nicoCluster.Spec.FailureDomainLabelKey, instance, requestedDomain)
	if err != nil {
		setMachineProvisionedFalse(&nicoMachine, infrav1.FailureDomainVerificationFailedReason, err.Error())
		return ctrl.Result{}, fmt.Errorf("failed to verify machine failure domain: %w", err)
	}

	var site *nicosdk.Site
	if siteID := instance.GetSiteId(); siteID != "" {
		var err error
		site, err = nicoClient.GetSite(ctx, siteID)
		if err != nil {
			// Topology names are supplementary metadata. Do not delay provisioning if they cannot be read.
			log.Error(err, "failed to get NICo site for machine topology", "siteID", siteID)
		}
	}

	var vpc *nicosdk.VPC
	if vpcID := instance.GetVpcId(); vpcID != "" {
		var err error
		vpc, err = nicoClient.GetVPC(ctx, vpcID)
		if err != nil {
			// Topology names are supplementary metadata. Do not delay provisioning if they cannot be read.
			log.Error(err, "failed to get NICo VPC for machine topology", "vpcID", vpcID)
		}
	}
	setObservedTopology(&nicoMachine, instance, observedDomain, site, vpc)
	setFailureDomainDrift(ctx, &nicoMachine, instance, nicoCluster.Spec.FailureDomainLabelKey, requestedDomain, observedDomain)
	// Machine ID and normalized topology names are only known after NICo returns
	// the instance, so apply them after the status observation step.
	if err := applyObservedTopologyLabels(ctx, nicoClient, instanceID, instance, &nicoMachine); err != nil {
		log.Error(err, "failed to apply observed topology labels to NICo instance", "instanceID", instanceID)
	}

	if handled, err := r.reconcileReboot(ctx, ownerMachine, &nicoMachine, nicoClient, instanceID); handled || err != nil {
		return ctrl.Result{}, err
	}

	if ip := firstIPv4FromInstance(instance); ip != "" {
		nicoMachine.Status.Addresses = []clusterv1.MachineAddress{{
			Type:    clusterv1.MachineInternalIP,
			Address: ip,
		}}
	}

	tpmEkCert := instance.GetTpmEkCertificate()
	if tpmEkCert != "" {
		tpmEkPubHash, err := nico.ComputePublicKeyHash(tpmEkCert)
		if err != nil {
			// Log error, but don't return an error, which would prevent provisioning progress, even if the TPM is not used.
			log.Error(err, "failed to compute TPM EK public hash")
		} else {
			nicoMachine.Status.TpmEkPubHash = tpmEkPubHash
		}
	}

	provisioned := true
	nicoMachine.Status.Initialization.Provisioned = &provisioned

	if !nico.IsReady(instance) {
		log.V(1).Info("NICo instance not ready", "instanceID", nicoMachine.Status.InstanceID, "machineID", nicoMachine.Status.MachineID, "instanceStatus", nico.InstanceStatus(instance))
		setMachineProvisionedFalse(&nicoMachine, infrav1.InstanceNotReadyReason, fmt.Sprintf("Instance status is %s", nico.InstanceStatus(instance)))
		return ctrl.Result{RequeueAfter: machineRequeueSlow}, nil
	}

	log.V(1).Info("reconciled NicoMachine", "instanceID", nicoMachine.Status.InstanceID, "machineID", nicoMachine.Status.MachineID)
	setMachineProvisionedTrue(&nicoMachine, infrav1.InstanceReadyReason)
	return ctrl.Result{RequeueAfter: machineReadyRequeueAfter(nicoMachine)}, nil
}

// reconcileDelete releases the backing NICo instance and holds the finalizer
// until the API reports that the instance is released.
func (r *NicoMachineReconciler) reconcileDelete(ctx context.Context, ownerMachine *clusterv1.Machine, cluster *clusterv1.Cluster, nicoMachine *infrav1.NicoMachine) (ctrl.Result, error) {
	conditions.Set(nicoMachine, metav1.Condition{
		Type:   clusterv1.DeletingCondition,
		Status: metav1.ConditionTrue,
		Reason: clusterv1.DeletingReason,
	})

	if !controllerutil.ContainsFinalizer(nicoMachine, nicoMachineFinalizer) {
		return ctrl.Result{}, nil
	}
	if nicoMachine.Status.InstanceID == "" {
		completeMachineDeletion(ctx, nicoMachine)
		return ctrl.Result{}, nil
	}

	nicoCluster, err := r.resolveNicoCluster(ctx, cluster)
	if err != nil {
		conditions.Set(nicoMachine, metav1.Condition{
			Type:    clusterv1.DeletingCondition,
			Status:  metav1.ConditionTrue,
			Reason:  infrav1.WaitingForClusterInfrastructureReason,
			Message: err.Error(),
		})
		return ctrl.Result{}, fmt.Errorf("delete NicoMachine: %w", err)
	}

	nicoClient, err := r.nicoClientForCluster(ctx, nicoCluster)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("delete NicoMachine: failed to get nico client: %w", err)
	}

	done, err := r.releaseInstance(ctx, nicoClient, ownerMachine, nicoMachine)
	if err != nil || !done {
		return ctrl.Result{RequeueAfter: machineRequeueSlow}, err
	}

	completeMachineDeletion(ctx, nicoMachine)
	return ctrl.Result{}, nil
}

func completeMachineDeletion(ctx context.Context, nicoMachine *infrav1.NicoMachine) {
	ctrl.LoggerFrom(ctx).Info("removing finalizer")
	conditions.Set(nicoMachine, metav1.Condition{
		Type:    clusterv1.DeletingCondition,
		Status:  metav1.ConditionTrue,
		Reason:  clusterv1.DeletionCompletedReason,
		Message: "Machine infrastructure deletion completed",
	})
	controllerutil.RemoveFinalizer(nicoMachine, nicoMachineFinalizer)
}

// releaseInstance reports whether the instance is released. It reads before
// deleting so an instance already released or mid-teardown is not deleted
// again. NICo retains terminated instance records, so both Terminated and
// NotFound are complete.
func (r *NicoMachineReconciler) releaseInstance(ctx context.Context, nicoClient nico.API, ownerMachine *clusterv1.Machine, nicoMachine *infrav1.NicoMachine) (bool, error) {
	log := ctrl.LoggerFrom(ctx)
	instanceID := nicoMachine.Status.InstanceID

	instance, err := nicoClient.GetInstance(ctx, instanceID)
	if errors.Is(err, nico.ErrNotFound) {
		log.Info("NICo instance is released", "instanceID", instanceID)
		nicoMachine.Status.InstanceID = ""
		return true, nil
	}
	if err != nil {
		return false, fmt.Errorf("delete NicoMachine: failed to get NICo instance: %w", err)
	}

	switch {
	case nico.IsTerminated(instance):
		log.Info("NICo instance is released", "instanceID", instanceID, "status", nico.InstanceStatus(instance))
		nicoMachine.Status.InstanceID = ""
		return true, nil
	case nico.IsTerminating(instance):
		log.V(1).Info("waiting for NICo instance teardown", "instanceID", instanceID, "status", nico.InstanceStatus(instance))
		return false, nil
	}

	healthIssue := r.machineHealthIssue(ctx, ownerMachine, instanceID)
	if healthIssue != nil {
		log.Info("deleting NICo instance with health issue", "instanceID", instanceID, "category", healthIssue.GetCategory(), "summary", healthIssue.GetSummary())
	} else {
		log.Info("deleting NICo instance", "instanceID", instanceID)
	}
	if err := nicoClient.DeleteInstance(ctx, instanceID, healthIssue); err != nil && !errors.Is(err, nico.ErrNotFound) {
		return false, fmt.Errorf("delete NicoMachine: failed to delete NICo instance: %w", err)
	}
	log.Info("requested NICo instance deletion", "instanceID", instanceID)

	return false, nil
}

func (r *NicoMachineReconciler) machineHealthIssue(ctx context.Context, ownerMachine *clusterv1.Machine, instanceID string) *nicosdk.MachineHealthIssue {
	if r.ProviderConfig.RepairAnnotation == "" {
		return nil
	}
	annotationValue, ok := ownerMachine.Annotations[r.ProviderConfig.RepairAnnotation]
	if !ok || annotationValue == "" {
		return nil
	}

	ctrl.LoggerFrom(ctx).Info("repair annotation present, flagging instance for repair", "instanceID", instanceID, "annotation", r.ProviderConfig.RepairAnnotation)
	var parsed struct {
		Category string  `json:"category"`
		Summary  string  `json:"summary"`
		Details  *string `json:"details,omitempty"`
	}
	if err := json.Unmarshal([]byte(annotationValue), &parsed); err != nil {
		// Annotation is a plain string (legacy format); treat as summary with a generic category.
		return nicosdk.NewMachineHealthIssue(
			"Other",
			*nicosdk.NewNullableString(&annotationValue),
			*nicosdk.NewNullableString(nil),
		)
	}

	return nicosdk.NewMachineHealthIssue(
		parsed.Category,
		*nicosdk.NewNullableString(&parsed.Summary),
		*nicosdk.NewNullableString(parsed.Details),
	)
}

// observeFailureDomain returns the failure domain of the machine NICo assigned to
// the instance, or the empty string when none is assigned or labelKey is unset.
// NICo is queried only when the assignment changed or a requested domain is still
// unconfirmed, so a steady-state reconcile does not read the machine again. A
// non-nil error means a requested domain could not be verified.
func observeFailureDomain(
	ctx context.Context,
	nicoClient nico.API,
	nicoMachine *infrav1.NicoMachine,
	labelKey string,
	instance *nicosdk.Instance,
	requestedDomain string,
) (string, error) {
	machineID := instance.GetMachineId()
	if machineID == "" || labelKey == "" {
		return "", nil
	}

	observed := nicoMachine.Status.FailureDomain
	if machineID == nicoMachine.Status.MachineID && (requestedDomain == "" || observed == requestedDomain) {
		return observed, nil
	}

	machine, err := nicoClient.GetMachine(ctx, machineID)
	if err != nil {
		if requestedDomain != "" {
			return "", err
		}
		ctrl.LoggerFrom(ctx).Error(err, "failed to get NICo machine for topology", "machineID", machineID)
		return observed, nil
	}
	return nico.MachineFailureDomain(machine, labelKey), nil
}

func setObservedTopology(
	nicoMachine *infrav1.NicoMachine,
	instance *nicosdk.Instance,
	failureDomain string,
	site *nicosdk.Site,
	vpc *nicosdk.VPC,
) {
	nicoMachine.Status.MachineID = instance.GetMachineId()
	nicoMachine.Status.SiteID = instance.GetSiteId()
	nicoMachine.Status.VPCID = instance.GetVpcId()
	nicoMachine.Status.FailureDomain = failureDomain
	nicoMachine.Status.SiteName = ""
	nicoMachine.Status.VPCName = ""

	if site != nil {
		nicoMachine.Status.SiteName = site.GetName()
	}
	if vpc != nil {
		nicoMachine.Status.VPCName = vpc.GetName()
	}
}

func applyObservedTopologyLabels(ctx context.Context, nicoClient nico.API, instanceID string, instance *nicosdk.Instance, nicoMachine *infrav1.NicoMachine) error {
	existing := instance.GetLabels()
	desired := mergeLabels(existing, observedTopologyLabels(nicoMachine))
	if !needsLabelUpdate(existing, desired) {
		return nil
	}

	_, err := nicoClient.ApplyInstanceLabels(ctx, instanceID, desired)
	return err
}

func needsLabelUpdate(existing, desired map[string]string) bool {
	return !maps.Equal(desired, existing)
}

// observedTopologyLabels converts the latest observed NICo instance topology
// into the NKE labels required on the backing VM.
func observedTopologyLabels(nicoMachine *infrav1.NicoMachine) map[string]string {
	labels := map[string]string{}
	if nicoMachine.Status.MachineID != "" {
		labels[labelKeyMachineID] = nicoMachine.Status.MachineID
	}
	if nicoMachine.Status.SiteID != "" {
		labels[labelKeySiteID] = nicoMachine.Status.SiteID
	}
	if name := normalizeLabelValue(nicoMachine.Status.SiteName); name != "" {
		labels[labelKeySiteName] = name
	}
	if nicoMachine.Status.VPCID != "" {
		labels[labelKeyVPCID] = nicoMachine.Status.VPCID
	}
	if name := normalizeLabelValue(nicoMachine.Status.VPCName); name != "" {
		labels[labelKeyVPCName] = name
	}
	return labels
}

func (r *NicoMachineReconciler) providerIDClaimedBy(ctx context.Context, nicoMachine infrav1.NicoMachine, instanceID string) (string, error) {
	var nicoMachines infrav1.NicoMachineList
	if err := r.List(ctx, &nicoMachines); err != nil {
		return "", fmt.Errorf("error listing NicoMachines: %w", err)
	}

	providerID := nico.ProviderID(instanceID)
	for _, existing := range nicoMachines.Items {
		if existing.UID == nicoMachine.UID {
			continue
		}
		if existing.Spec.ProviderID == providerID {
			return fmt.Sprintf("%s/%s", existing.Namespace, existing.Name), nil
		}
	}

	return "", nil
}

func (r *NicoMachineReconciler) nicoClientForCluster(ctx context.Context, nicoCluster *infrav1.NicoCluster) (nico.API, error) {
	return nicoClientForCluster(ctx, r.Client, nicoCluster, r.ProviderConfig.Credentials)
}

func (r *NicoMachineReconciler) resolveNicoCluster(ctx context.Context, cluster *clusterv1.Cluster) (*infrav1.NicoCluster, error) {
	if cluster.Spec.InfrastructureRef.Name == "" {
		return nil, fmt.Errorf("cluster %s/%s has no infrastructure reference", cluster.Namespace, cluster.Name)
	}

	nicoCluster := &infrav1.NicoCluster{}
	key := client.ObjectKey{Namespace: cluster.Namespace, Name: cluster.Spec.InfrastructureRef.Name}
	if err := r.reader().Get(ctx, key, nicoCluster); err != nil {
		return nil, fmt.Errorf("read NicoCluster %s: %w", key, err)
	}
	return nicoCluster, nil
}

// reader returns the uncached reader, falling back to the cached client when
// none was wired so the reconciler stays usable outside a manager.
func (r *NicoMachineReconciler) reader() client.Reader {
	if r.APIReader != nil {
		return r.APIReader
	}
	return r.Client
}

// reconcileReboot actuates Machine annotation reboot requests and removes the
// request annotation once NICo has accepted the reboot trigger.
func (r *NicoMachineReconciler) reconcileReboot(
	ctx context.Context,
	machine *clusterv1.Machine,
	nicoMachine *infrav1.NicoMachine,
	nicoClient nico.API,
	instanceID string,
) (bool, error) {
	rebootAnnotation := r.ProviderConfig.RebootAnnotation
	if machine.GetAnnotations()[rebootAnnotation] == "" {
		return false, nil
	}

	if _, err := nicoClient.TriggerInstanceReboot(ctx, instanceID); err != nil {
		return true, fmt.Errorf("failed to trigger instance reboot: %w", err)
	}

	nicoMachineAnnotations := nicoMachine.GetAnnotations()
	if nicoMachineAnnotations == nil {
		nicoMachineAnnotations = map[string]string{}
	}
	nicoMachineAnnotations[lastRebootTriggeredAnnotation] = time.Now().UTC().Format(time.RFC3339)
	nicoMachine.SetAnnotations(nicoMachineAnnotations)

	machinePatch := client.MergeFrom(machine.DeepCopy())
	machineAnnotations := machine.GetAnnotations()
	delete(machineAnnotations, rebootAnnotation)
	machine.SetAnnotations(machineAnnotations)
	if err := r.Patch(ctx, machine, machinePatch); err != nil {
		return true, fmt.Errorf("failed to patch machine reboot request annotation: %w", err)
	}
	return true, nil
}

// deferForControlPlanePriority checks if this worker instance create should be
// deferred to allow control-plane machines to claim capacity first.
func (r *NicoMachineReconciler) deferForControlPlanePriority(
	ctx context.Context,
	nicoMachine *infrav1.NicoMachine,
	unusedUsable int32,
) (bool, error) {
	log := ctrl.LoggerFrom(ctx)

	// Control-plane machines always proceed without deferral.
	if isControlPlaneNicoMachine(nicoMachine) {
		return false, nil
	}

	cpWaiting, sample, err := countControlPlaneWaitingForInstanceType(
		ctx, r.Client, nicoMachine.Spec.InstanceTypeID, nicoMachine)
	if err != nil {
		return false, fmt.Errorf("failed to count waiting control plane machines: %w", err)
	}

	if cpWaiting == 0 {
		return false, nil
	}

	message := fmt.Sprintf(
		"Deferring instance create: %d control-plane NicoMachine(s) (e.g. %s) are waiting on "+
			"instance type %q with only %d unused usable allocation(s)",
		cpWaiting, sample, nicoMachine.Spec.InstanceTypeID, unusedUsable)
	log.Info("deferring worker instance create for control-plane priority",
		"instanceTypeID", nicoMachine.Spec.InstanceTypeID,
		"controlPlaneWaiting", cpWaiting,
		"unusedUsable", unusedUsable)
	setMachineProvisionedFalse(nicoMachine, infrav1.ControlPlanePriorityDeferredReason, message)
	return true, nil
}

// setMachineProvisionedFalse marks the machine unprovisioned with a reason.
func setMachineProvisionedFalse(nicoMachine *infrav1.NicoMachine, reason, message string) {
	conditions.Set(nicoMachine, metav1.Condition{
		Type:    infrav1.MachineProvisionedCondition,
		Status:  metav1.ConditionFalse,
		Reason:  reason,
		Message: message,
	})
}

func setMachineProvisionedTrue(nicoMachine *infrav1.NicoMachine, reason string) {
	conditions.Set(nicoMachine, metav1.Condition{
		Type:   infrav1.MachineProvisionedCondition,
		Status: metav1.ConditionTrue,
		Reason: reason,
	})
}

func setNicoMachineConditions(nicoMachine *infrav1.NicoMachine) error {
	if nicoMachine.DeletionTimestamp.IsZero() {
		conditions.Set(nicoMachine, metav1.Condition{
			Type:   clusterv1.DeletingCondition,
			Status: metav1.ConditionFalse,
			Reason: clusterv1.NotDeletingReason,
		})
	}

	if err := conditions.SetSummaryCondition(
		nicoMachine,
		nicoMachine,
		infrav1.SyncedCondition,
		conditions.ForConditionTypes{infrav1.MachineProvisionedCondition},
		conditions.CustomMergeStrategy{
			MergeStrategy: conditions.DefaultMergeStrategy(
				conditions.ComputeReasonFunc(conditions.GetDefaultComputeMergeReasonFunc(
					infrav1.NotSyncedReason,
					infrav1.SyncUnknownReason,
					infrav1.SyncedReason,
				)),
			),
		},
	); err != nil {
		return fmt.Errorf("summarize NicoMachine Synced condition: %w", err)
	}

	if err := conditions.SetSummaryCondition(
		nicoMachine,
		nicoMachine,
		clusterv1.AvailableCondition,
		conditions.ForConditionTypes{infrav1.MachineProvisionedCondition},
		conditions.CustomMergeStrategy{
			MergeStrategy: conditions.DefaultMergeStrategy(
				conditions.ComputeReasonFunc(conditions.GetDefaultComputeMergeReasonFunc(
					clusterv1.NotAvailableReason,
					clusterv1.AvailableUnknownReason,
					clusterv1.AvailableReason,
				)),
			),
		},
	); err != nil {
		return fmt.Errorf("summarize NicoMachine Available condition: %w", err)
	}

	if err := conditions.SetSummaryCondition(
		nicoMachine,
		nicoMachine,
		clusterv1.ReadyCondition,
		conditions.ForConditionTypes{
			clusterv1.AvailableCondition,
			clusterv1.DeletingCondition,
		},
		conditions.NegativePolarityConditionTypes{clusterv1.DeletingCondition},
		conditions.CustomMergeStrategy{
			MergeStrategy: conditions.DefaultMergeStrategy(
				conditions.GetPriorityFunc(conditions.GetDefaultMergePriorityFunc(clusterv1.DeletingCondition)),
				conditions.ComputeReasonFunc(conditions.GetDefaultComputeMergeReasonFunc(
					clusterv1.NotReadyReason,
					clusterv1.ReadyUnknownReason,
					clusterv1.ReadyReason,
				)),
			),
		},
	); err != nil {
		return fmt.Errorf("summarize NicoMachine Ready condition: %w", err)
	}
	nicoMachine.Status.Ready = conditions.IsTrue(nicoMachine, clusterv1.ReadyCondition)
	return nil
}

// machineReadyRequeueAfter returns a stable jittered interval for steady-state machine polling.
// The jitter spreads reconciles across the window so large clusters do not requeue all machines at once.
func machineReadyRequeueAfter(nicoMachine infrav1.NicoMachine) time.Duration {
	return machineReadyRequeue + deterministicJitter(string(nicoMachine.UID), machineReadyJitterWindow)
}

func buildInstanceCreateRequest(
	name string,
	tenantID string,
	nicoMachine *infrav1.NicoMachine,
	clusterName string,
	bootstrapCloudConfig string,
	instanceTypeCapabilities nicomachine.InstanceTypeCapabilities,
) (*nicosdk.InstanceCreateRequest, error) {
	if len(nicoMachine.Spec.Interfaces) == 0 {
		return nil, fmt.Errorf("spec.interfaces must contain at least one entry")
	}
	if err := instanceTypeCapabilities.ValidatePartitionSupport(nicoMachine.Spec); err != nil {
		return nil, err
	}

	interfaces := make([]nicosdk.InterfaceCreateRequest, 0, len(nicoMachine.Spec.Interfaces))
	for i, iface := range nicoMachine.Spec.Interfaces {
		if (iface.SubnetID == "") == (iface.VPCPrefixID == "") {
			return nil, fmt.Errorf("spec.interfaces[%d] must set exactly one of subnetID or vpcPrefixID", i)
		}

		req := nicosdk.NewInterfaceCreateRequest()
		if iface.SubnetID != "" {
			req.SetSubnetId(iface.SubnetID)
		}
		if iface.VPCPrefixID != "" {
			req.SetVpcPrefixId(iface.VPCPrefixID)
		}
		if iface.IPAddress != "" {
			req.SetIpAddress(iface.IPAddress)
		}
		if iface.Physical != nil {
			req.SetIsPhysical(*iface.Physical)
		}
		if iface.Device != "" {
			req.SetDevice(iface.Device)
		}
		if iface.DeviceInstance != nil {
			req.SetDeviceInstance(*iface.DeviceInstance)
		}
		interfaces = append(interfaces, *req)
	}

	createReq := nicosdk.NewInstanceCreateRequest(name, tenantID, nicoMachine.Spec.VPCID)
	createReq.SetInterfaces(interfaces)
	createReq.SetDescription("Managed by Cluster API")
	createReq.SetUserData(bootstrapCloudConfig)
	if nicoMachine.Spec.InstanceTypeID != "" {
		createReq.SetInstanceTypeId(nicoMachine.Spec.InstanceTypeID)
	}
	if nicoMachine.Spec.IpxeScript != "" {
		createReq.SetIpxeScript(nicoMachine.Spec.IpxeScript)
	}
	if len(nicoMachine.Spec.SSHKeyGroupIDs) > 0 {
		createReq.SetSshKeyGroupIds(nicoMachine.Spec.SSHKeyGroupIDs)
	}
	if nicoMachine.Spec.MachineID != "" {
		createReq.SetMachineId(nicoMachine.Spec.MachineID)
	}
	if nicoMachine.Spec.AllowUnhealthyMachine {
		createReq.SetAllowUnhealthyMachine(true)
	}

	createReq.InfinibandInterfaces = buildInfiniBandInterfaces(nicoMachine.Spec, instanceTypeCapabilities)
	createReq.NvLinkInterfaces = buildNVLinkInterfaces(nicoMachine.Spec, instanceTypeCapabilities)

	labels := mergeLabels(
		map[string]string{
			"cluster.x-k8s.io/cluster-name": clusterName,
			"cluster.x-k8s.io/machine-name": name,
		},
		nicoMachine.Spec.Labels,
	)
	if labels != nil {
		createReq.SetLabels(labels)
	}

	return createReq, nil
}

func buildInfiniBandInterfaces(spec infrav1.NicoMachineSpec, capabilities nicomachine.InstanceTypeCapabilities) []nicosdk.InfiniBandInterfaceCreateRequest {
	if spec.InfinibandPartitionID != "" {
		interfaces := make([]nicosdk.InfiniBandInterfaceCreateRequest, 0, len(capabilities.InfiniBandActiveDeviceIDs))
		for _, deviceID := range capabilities.InfiniBandActiveDeviceIDs {
			req := nicosdk.NewInfiniBandInterfaceCreateRequest()
			req.SetPartitionId(spec.InfinibandPartitionID)
			req.SetDevice(capabilities.InfiniBandDeviceName)
			req.SetDeviceInstance(deviceID)
			req.SetIsPhysical(true)
			interfaces = append(interfaces, *req)
		}
		return interfaces
	}

	if len(spec.InfinibandInterfaces) == 0 {
		return nil
	}

	interfaces := make([]nicosdk.InfiniBandInterfaceCreateRequest, 0, len(spec.InfinibandInterfaces))
	for _, ib := range spec.InfinibandInterfaces {
		req := nicosdk.NewInfiniBandInterfaceCreateRequest()
		req.SetPartitionId(ib.PartitionID)
		if ib.Device != "" {
			req.SetDevice(ib.Device)
		}
		if ib.DeviceInstance != nil {
			req.SetDeviceInstance(*ib.DeviceInstance)
		}
		if ib.Vendor != "" {
			req.SetVendor(ib.Vendor)
		}
		if ib.IsPhysical != nil {
			req.SetIsPhysical(*ib.IsPhysical)
		}
		if ib.VirtualFunctionID != nil {
			req.SetVirtualFunctionId(*ib.VirtualFunctionID)
		}
		interfaces = append(interfaces, *req)
	}
	return interfaces
}

func buildNVLinkInterfaces(spec infrav1.NicoMachineSpec, capabilities nicomachine.InstanceTypeCapabilities) []nicosdk.NVLinkInterfaceCreateOrUpdateRequest {
	if spec.NVLinkLogicalPartitionID != "" {
		interfaces := make([]nicosdk.NVLinkInterfaceCreateOrUpdateRequest, 0, len(capabilities.NVLinkActiveDeviceIDs))
		for _, deviceID := range capabilities.NVLinkActiveDeviceIDs {
			req := nicosdk.NewNVLinkInterfaceCreateOrUpdateRequest()
			req.SetNvLinkLogicalPartitionId(spec.NVLinkLogicalPartitionID)
			req.SetDeviceInstance(deviceID)
			interfaces = append(interfaces, *req)
		}
		return interfaces
	}

	if len(spec.NVLinkInterfaces) == 0 {
		return nil
	}

	interfaces := make([]nicosdk.NVLinkInterfaceCreateOrUpdateRequest, 0, len(spec.NVLinkInterfaces))
	for _, nv := range spec.NVLinkInterfaces {
		req := nicosdk.NewNVLinkInterfaceCreateOrUpdateRequest()
		req.SetNvLinkLogicalPartitionId(nv.NVLinkLogicalPartitionID)
		if nv.DeviceInstance != nil {
			req.SetDeviceInstance(*nv.DeviceInstance)
		}
		interfaces = append(interfaces, *req)
	}
	return interfaces
}

type instanceTypeAvailability struct {
	available    bool
	unusedUsable int32
	reason       string
	message      string
	instanceType *nicosdk.InstanceType
}

func instanceTypeAvailable(ctx context.Context, nicoClient nico.API, instanceTypeID string) (instanceTypeAvailability, error) {
	log := ctrl.LoggerFrom(ctx)
	instanceType, err := nicoClient.GetInstanceTypeWithAllocationStats(ctx, instanceTypeID)
	log.V(2).Info("instance type availability check result", "instanceType", instanceType, "err", err)
	if err != nil {
		if errors.Is(err, nico.ErrNotFound) {
			return instanceTypeAvailability{
				reason:  infrav1.InstanceTypeNotFoundReason,
				message: fmt.Sprintf("Instance type %q was not found", instanceTypeID),
			}, nil
		}
		return instanceTypeAvailability{}, err
	}
	if instanceType == nil || instanceType.AllocationStats == nil || instanceType.AllocationStats.UnusedUsable == nil {
		return instanceTypeAvailability{}, fmt.Errorf("instance type %q response did not include allocationStats.unusedUsable", instanceTypeID)
	}

	unusedUsable := instanceType.AllocationStats.GetUnusedUsable()
	if unusedUsable > 0 {
		return instanceTypeAvailability{available: true, unusedUsable: unusedUsable, instanceType: instanceType}, nil
	}

	message := fmt.Sprintf("Instance type %q has no unused usable allocations (total=%d used=%d unused=%d unusedUsable=%d)",
		instanceTypeID,
		instanceType.AllocationStats.GetTotal(),
		instanceType.AllocationStats.GetUsed(),
		instanceType.AllocationStats.GetUnused(),
		unusedUsable,
	)

	return instanceTypeAvailability{
		unusedUsable: unusedUsable,
		reason:       infrav1.InstanceTypeUnavailableReason,
		message:      message,
		instanceType: instanceType,
	}, nil
}

func (r *NicoMachineReconciler) SetupWithManager(ctx context.Context, mgr ctrl.Manager) error {
	if r.APIReader == nil {
		r.APIReader = mgr.GetAPIReader()
	}

	if err := mgr.GetFieldIndexer().IndexField(ctx, &infrav1.NicoCluster{}, nicoMachineCredentialsSecretRefIndex, func(object client.Object) []string {
		nicoCluster := object.(*infrav1.NicoCluster)
		secretKey, ok := credentialsSecretKey(nicoCluster, r.ProviderConfig.Credentials)
		if !ok {
			return nil
		}
		return []string{secretKey.String()}
	}); err != nil {
		return fmt.Errorf("index NicoClusters by credentials Secret for NicoMachines: %w", err)
	}

	log := ctrl.LoggerFrom(ctx).WithValues("controller", "NicoMachine")
	clusterToNicoMachines, err := util.ClusterToTypedObjectsMapper(mgr.GetClient(), &infrav1.NicoMachineList{}, mgr.GetScheme())
	if err != nil {
		return err
	}

	return ctrl.NewControllerManagedBy(mgr).
		For(&infrav1.NicoMachine{}).
		WithEventFilter(predicates.ResourceHasFilterLabel(mgr.GetScheme(), log, r.WatchFilterValue)).
		// When a control-plane machine claims shared instance-type capacity, wake
		// unscheduled machines using the same instance type so they can retry promptly.
		Watches(
			&infrav1.NicoMachine{},
			handler.EnqueueRequestsFromMapFunc(func(ctx context.Context, object client.Object) []reconcile.Request {
				scheduled := object.(*infrav1.NicoMachine)
				instanceTypeID := scheduled.Spec.InstanceTypeID
				if instanceTypeID == "" {
					return nil
				}

				var machines infrav1.NicoMachineList
				if err := r.List(ctx, &machines); err != nil {
					ctrl.LoggerFrom(ctx).Error(err, "failed to list NicoMachines for scheduled machine", "nicoMachine", client.ObjectKeyFromObject(scheduled))
					return nil
				}

				requests := make([]reconcile.Request, 0, len(machines.Items))
				for i := range machines.Items {
					candidate := &machines.Items[i]
					if candidate.Spec.ProviderID != "" || candidate.Spec.InstanceTypeID != instanceTypeID {
						continue
					}
					if r.WatchFilterValue != "" && candidate.Labels[clusterv1.WatchLabel] != r.WatchFilterValue {
						continue
					}
					requests = append(requests, reconcile.Request{NamespacedName: client.ObjectKeyFromObject(candidate)})
				}
				return requests
			}),
			builder.WithPredicates(crpredicate.Funcs{
				CreateFunc:  func(event.CreateEvent) bool { return false },
				DeleteFunc:  func(event.DeleteEvent) bool { return false },
				GenericFunc: func(event.GenericEvent) bool { return false },
				UpdateFunc: func(e event.UpdateEvent) bool {
					oldMachine := e.ObjectOld.(*infrav1.NicoMachine)
					newMachine := e.ObjectNew.(*infrav1.NicoMachine)
					return isControlPlaneNicoMachine(newMachine) && oldMachine.Spec.ProviderID == "" && newMachine.Spec.ProviderID != ""
				},
			}),
		).
		Watches(
			&clusterv1.Machine{},
			handler.EnqueueRequestsFromMapFunc(util.MachineToInfrastructureMapFunc(infrav1.GroupVersion.WithKind("NicoMachine"))),
		).
		Watches(
			&clusterv1.Cluster{},
			handler.EnqueueRequestsFromMapFunc(clusterToNicoMachines),
			builder.WithPredicates(predicates.ClusterPausedTransitionsOrInfrastructureProvisioned(mgr.GetScheme(), log)),
		).
		Watches(
			&corev1.Secret{},
			handler.EnqueueRequestsFromMapFunc(func(ctx context.Context, object client.Object) []reconcile.Request {
				secretKey := client.ObjectKeyFromObject(object)
				listOptions := []client.ListOption{
					client.MatchingFields{nicoMachineCredentialsSecretRefIndex: secretKey.String()},
				}
				if secretKey != r.ProviderConfig.Credentials {
					listOptions = append(listOptions, client.InNamespace(secretKey.Namespace))
				}

				var nicoClusters infrav1.NicoClusterList
				if err := r.List(ctx, &nicoClusters, listOptions...); err != nil {
					ctrl.LoggerFrom(ctx).Error(err, "failed to list NicoClusters for credentials Secret", "secret", secretKey)
					return nil
				}

				requests := []reconcile.Request{}
				for i := range nicoClusters.Items {
					cluster, err := util.GetOwnerCluster(ctx, r.Client, nicoClusters.Items[i].ObjectMeta)
					if err != nil {
						ctrl.LoggerFrom(ctx).Error(err, "failed to get Cluster for credentials Secret", "secret", secretKey, "nicoCluster", client.ObjectKeyFromObject(&nicoClusters.Items[i]))
						continue
					}
					if cluster != nil {
						requests = append(requests, clusterToNicoMachines(ctx, cluster)...)
					}
				}
				return requests
			}),
		).
		Complete(r)
}
