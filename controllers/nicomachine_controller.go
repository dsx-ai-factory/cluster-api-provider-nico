package controllers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/cluster-api/util/paused"
	"sigs.k8s.io/cluster-api/util/predicates"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/handler"

	clusterv1 "sigs.k8s.io/cluster-api/api/core/v1beta2"
	"sigs.k8s.io/cluster-api/util"
	"sigs.k8s.io/cluster-api/util/conditions"
	"sigs.k8s.io/cluster-api/util/patch"

	infrav1 "github.com/NVIDIA/cluster-api-provider-nico/api/v1alpha1"
	"github.com/NVIDIA/cluster-api-provider-nico/internal/nico"
	nicomachine "github.com/NVIDIA/cluster-api-provider-nico/internal/nicomachine"

	nicosdk "github.com/NVIDIA/ncx-infra-controller-rest/sdk/standard"
)

const (
	nicoMachineFinalizer          = "infrastructure.cluster.x-k8s.io/nicomachine"
	lastRebootTriggeredAnnotation = "nico.nvidia.com/last-reboot-triggered-timestamp"
	machineRequeueFast            = 15 * time.Second
	machineRequeueSlow            = 30 * time.Second
	machineReadyRequeue           = 5 * time.Minute
	machineReadyJitterWindow      = 1 * time.Minute
	instanceTypeUnavailableWait   = 2 * time.Minute

	// These NKE label keys are applied to the backing NICo instance so the VM
	// records carry the same topology identifiers that Kubernetes nodes expose.
	labelKeyMachineID = "nke.nvidia.com/machine-id"
	labelKeySiteID    = "nke.nvidia.com/site-id"
	labelKeySiteName  = "nke.nvidia.com/site-name"
	labelKeyVPCID     = "nke.nvidia.com/vpc-id"
	labelKeyVPCName   = "nke.nvidia.com/vpc-name"
)

// NicoMachineReconciler reconciles a NicoMachine object.
type NicoMachineReconciler struct {
	client.Client
	Scheme         *runtime.Scheme
	ProviderConfig nico.ProviderConfig
	// nicoClientFactory optionally overrides client construction after Secret load (tests).
	nicoClientFactory nicoClientFactory
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

	var nicoMachine infrav1.NicoMachine
	if err := r.Get(ctx, req.NamespacedName, &nicoMachine); err != nil {
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
	if cluster.Spec.InfrastructureRef.Name == "" {
		log.Info("Waiting for Cluster to have InfrastructureRef set")
		return ctrl.Result{RequeueAfter: machineRequeueFast}, nil
	}

	var nicoCluster infrav1.NicoCluster
	if err := r.Get(ctx, client.ObjectKey{
		Namespace: cluster.Namespace,
		Name:      cluster.Spec.InfrastructureRef.Name,
	}, &nicoCluster); err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to get nico cluster: %w", err)
	}

	patchHelper, err := patch.NewHelper(&nicoMachine, r.Client)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to create patch helper: %w", err)
	}
	defer func() {
		if err := patchHelper.Patch(ctx, &nicoMachine, patch.WithOwnedConditions{Conditions: []string{clusterv1.ReadyCondition}}); err != nil && retErr == nil {
			retErr = fmt.Errorf("failed to patch NicoMachine: %w", err)
		}
	}()

	if isPaused, requeue, err := paused.EnsurePausedCondition(ctx, r.Client, cluster, &nicoMachine); err != nil || isPaused || requeue {
		return ctrl.Result{}, err
	}

	if !nicoMachine.DeletionTimestamp.IsZero() {
		if controllerutil.ContainsFinalizer(&nicoMachine, nicoMachineFinalizer) && nicoMachine.Status.InstanceID != "" {
			nicoClient, err := r.nicoClientForCluster(ctx, &nicoCluster)
			if err != nil {
				return ctrl.Result{}, fmt.Errorf("delete NicoMachine: failed to get nico client: %w", err)
			}

			var healthIssue *nicosdk.MachineHealthIssue
			if r.ProviderConfig.RepairAnnotation != "" {
				if annotationValue, ok := ownerMachine.Annotations[r.ProviderConfig.RepairAnnotation]; ok && annotationValue != "" {
					log.Info("repair annotation present, flagging instance for repair", "instanceID", nicoMachine.Status.InstanceID, "annotation", r.ProviderConfig.RepairAnnotation)
					healthIssue = nicosdk.NewMachineHealthIssue()
					var parsed struct {
						Category string  `json:"category"`
						Summary  string  `json:"summary"`
						Details  *string `json:"details,omitempty"`
					}
					if err := json.Unmarshal([]byte(annotationValue), &parsed); err != nil {
						// Annotation is a plain string (legacy format); treat as summary with a generic category.
						healthIssue.SetCategory("Other")
						healthIssue.SetSummary(annotationValue)
					} else {
						healthIssue.SetCategory(parsed.Category)
						healthIssue.SetSummary(parsed.Summary)
						if parsed.Details != nil {
							healthIssue.SetDetails(*parsed.Details)
						}
					}
				}
			}

			if healthIssue != nil {
				log.Info("deleting NICo instance with health issue", "instanceID", nicoMachine.Status.InstanceID, "category", healthIssue.GetCategory(), "summary", healthIssue.GetSummary())
			} else {
				log.Info("deleting NICo instance", "instanceID", nicoMachine.Status.InstanceID)
			}
			if err := nico.IgnoreNotFound(nicoClient.DeleteInstance(ctx, nicoMachine.Status.InstanceID, healthIssue)); err != nil {
				return ctrl.Result{}, fmt.Errorf("delete NicoMachine: failed to delete NICo instance: %w", err)
			}
		}

		log.Info("removing finalizer")
		controllerutil.RemoveFinalizer(&nicoMachine, nicoMachineFinalizer)
		setReadyFalse(&nicoMachine, infrav1.DeletingReason, "")
		return ctrl.Result{}, nil
	}

	if !controllerutil.ContainsFinalizer(&nicoMachine, nicoMachineFinalizer) {
		controllerutil.AddFinalizer(&nicoMachine, nicoMachineFinalizer)
	}

	nicoClient, err := r.nicoClientForCluster(ctx, &nicoCluster)
	if err != nil {
		if apierrors.IsNotFound(err) {
			setReadyFalse(&nicoMachine, infrav1.WaitingForIdentitySecretReason, err.Error())
			return ctrl.Result{RequeueAfter: machineRequeueFast}, nil
		}
		return ctrl.Result{}, fmt.Errorf("failed to get nico client: %w", err)
	}

	// if we have not yet created a NICo instance for this nicoMachine CR and this is not an NICo instance import
	if nicoMachine.Status.InstanceID == "" && nicoMachine.Spec.ProviderID == "" {
		if ownerMachine.Spec.Bootstrap.DataSecretName == nil || *ownerMachine.Spec.Bootstrap.DataSecretName == "" {
			setReadyFalse(&nicoMachine, infrav1.WaitingForBootstrapDataReason, "")
			return ctrl.Result{RequeueAfter: machineRequeueFast}, nil
		}

		var bootstrapSecret corev1.Secret
		if err := r.Get(ctx, client.ObjectKey{
			Namespace: ownerMachine.Namespace,
			Name:      *ownerMachine.Spec.Bootstrap.DataSecretName,
		}, &bootstrapSecret); err != nil {
			if apierrors.IsNotFound(err) {
				setReadyFalse(&nicoMachine, infrav1.WaitingForBootstrapDataReason, err.Error())
				return ctrl.Result{RequeueAfter: machineRequeueFast}, nil
			}
			return ctrl.Result{}, fmt.Errorf("failed to get bootstrap secret: %w", err)
		}

		bootstrapCloudConfig, err := bootstrapCloudConfigFromSecret(&bootstrapSecret)
		if err != nil {
			setReadyFalse(&nicoMachine, infrav1.BootstrapDataInvalidReason, err.Error())
			return ctrl.Result{}, fmt.Errorf("failed to get bootstrap cloud config: %w", err)
		}
		if nicoMachine.Spec.CloudInitInjectHostname {
			bootstrapCloudConfig, err = nico.InjectHostnameCloudConfig(bootstrapCloudConfig, ownerMachine.Name)
			if err != nil {
				setReadyFalse(&nicoMachine, infrav1.BootstrapDataInvalidReason, err.Error())
				return ctrl.Result{}, fmt.Errorf("failed to inject hostname cloud config: %w", err)
			}
		}

		tenantID, err := nicoClient.ResolveTenantID(ctx)
		if err != nil {
			setReadyFalse(&nicoMachine, infrav1.TenantResolutionFailedReason, err.Error())
			return ctrl.Result{}, fmt.Errorf("failed to resolve tenant ID: %w", err)
		}

		createReq, err := buildInstanceCreateRequest(ownerMachine.Name, tenantID, &nicoMachine, cluster.Name, bootstrapCloudConfig)
		if err != nil {
			setReadyFalse(&nicoMachine, infrav1.InstanceCreateRequestInvalidReason, err.Error())
			return ctrl.Result{}, fmt.Errorf("failed to build instance create request: %w", err)
		}

		if nicoMachine.Spec.InstanceTypeID != "" {
			// Query instance type availability before creating the instance when creating instances by instance type.
			// When instances are completely consumed, the instance creation API call will always fail. This preflight
			// check avoids spamming the NICo API logs with errors, and changes the re-queue interval.
			available, reason, message, err := instanceTypeAvailable(ctx, nicoClient, nicoMachine.Spec.InstanceTypeID)
			if err != nil {
				setReadyFalse(&nicoMachine, infrav1.AvailabilityCheckFailedReason, err.Error())
				return ctrl.Result{}, fmt.Errorf("failed to check instance type availability: %w", err)
			}
			if !available {
				setReadyFalse(&nicoMachine, reason, message)
				return ctrl.Result{RequeueAfter: instanceTypeUnavailableWait}, nil
			}
		}

		log.Info("creating NICo instance")
		instance, err := nicoClient.CreateInstance(ctx, *createReq)
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
				setReadyFalse(&nicoMachine, infrav1.InstanceCreateFailedReason, err.Error())
				return ctrl.Result{}, fmt.Errorf("failed to create or find instance: %w", err)
			}
		}

		nicoMachine.Status.InstanceID = instance.GetId()
		nicoMachine.Spec.ProviderID = nico.ProviderID(instance.GetId())
	}

	instanceID, err := nico.InstanceID(nicoMachine.Spec.ProviderID)
	if err != nil {
		setReadyFalse(&nicoMachine, infrav1.InstanceNotFoundReason, err.Error())
		return ctrl.Result{}, nil
	}

	instance, err := nicoClient.GetInstance(ctx, instanceID)
	if err != nil {
		if errors.Is(err, nico.ErrNotFound) {
			setReadyFalse(&nicoMachine, infrav1.InstanceMissingReason, err.Error())
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
			setReadyFalse(&nicoMachine, infrav1.InstanceAlreadyClaimedReason, fmt.Sprintf("failed to import instance: NICo instance %q is already referenced by NicoMachine %s", instanceID, claimedBy))
			return ctrl.Result{}, nil
		}
		if err := nicomachine.ValidateInstance(instance, nicoMachine); err != nil {
			setReadyFalse(&nicoMachine, clusterv1.InspectionFailedReason, fmt.Sprintf("failed to import instance: %s", err.Error()))
			return ctrl.Result{}, nil
		}
		nicoMachine.Status.InstanceID = instance.GetId()
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
	setObservedTopology(&nicoMachine, instance, site, vpc)
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
		log.V(1).Info("NICo instance not ready", "instanceID", nicoMachine.Status.InstanceID, "machineID", nicoMachine.Status.MachineID, "instanceStatus", instanceStatusString(instance))
		setReadyFalse(&nicoMachine, infrav1.InstanceNotReadyReason, fmt.Sprintf("Instance status is %s", instanceStatusString(instance)))
		return ctrl.Result{RequeueAfter: machineRequeueSlow}, nil
	}

	log.V(1).Info("reconciled NicoMachine", "instanceID", nicoMachine.Status.InstanceID, "machineID", nicoMachine.Status.MachineID)
	setReadyTrue(&nicoMachine, infrav1.InstanceReadyReason)
	return ctrl.Result{RequeueAfter: machineReadyRequeueAfter(nicoMachine)}, nil
}

func setObservedTopology(nicoMachine *infrav1.NicoMachine, instance *nicosdk.Instance, site *nicosdk.Site, vpc *nicosdk.VPC) {
	nicoMachine.Status.MachineID = instance.GetMachineId()
	nicoMachine.Status.SiteID = instance.GetSiteId()
	nicoMachine.Status.VPCID = instance.GetVpcId()
	nicoMachine.Status.SiteName = ""
	nicoMachine.Status.VPCName = ""

	if site != nil {
		nicoMachine.Status.SiteName = site.GetName()
	}
	if vpc != nil {
		nicoMachine.Status.VPCName = vpc.GetName()
	}
}

// applyObservedTopologyLabels merges the observed machine/topology labels into
// the existing instance labels and sends them back to NICo as a label update.
func applyObservedTopologyLabels(ctx context.Context, nicoClient nico.API, instanceID string, instance *nicosdk.Instance, nicoMachine *infrav1.NicoMachine) error {
	labels := mergeLabels(instance.GetLabels(), observedTopologyLabels(nicoMachine))
	if len(labels) == 0 {
		return nil
	}

	_, err := nicoClient.ApplyInstanceLabels(ctx, instanceID, labels)
	return err
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
	return nicoClientForCluster(ctx, r.Client, nicoCluster, r.ProviderConfig.Credentials, r.nicoClientFactory)
}

func (r *NicoMachineReconciler) SetupWithManager(ctx context.Context, mgr ctrl.Manager) error {

	predicateLog := ctrl.LoggerFrom(ctx).WithValues("controller", "NicoMachine")
	clusterToNicoMachines, err := util.ClusterToTypedObjectsMapper(mgr.GetClient(), &infrav1.NicoMachineList{}, mgr.GetScheme())
	if err != nil {
		return err
	}

	return ctrl.NewControllerManagedBy(mgr).
		For(&infrav1.NicoMachine{}).
		Watches(
			&clusterv1.Machine{},
			handler.EnqueueRequestsFromMapFunc(util.MachineToInfrastructureMapFunc(infrav1.GroupVersion.WithKind("NicoMachine"))),
		).
		Watches(
			&clusterv1.Cluster{},
			handler.EnqueueRequestsFromMapFunc(clusterToNicoMachines),
			builder.WithPredicates(predicates.ClusterPausedTransitionsOrInfrastructureProvisioned(mgr.GetScheme(), predicateLog)),
		).
		Complete(r)
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

func setReadyFalse(nicoMachine *infrav1.NicoMachine, reason, message string) {
	nicoMachine.Status.Ready = false
	conditions.Set(nicoMachine, metav1.Condition{
		Type:    clusterv1.ReadyCondition,
		Status:  metav1.ConditionFalse,
		Reason:  reason,
		Message: message,
	})
}

func setReadyTrue(nicoMachine *infrav1.NicoMachine, reason string) {
	nicoMachine.Status.Ready = true
	conditions.Set(nicoMachine, metav1.Condition{
		Type:   clusterv1.ReadyCondition,
		Status: metav1.ConditionTrue,
		Reason: reason,
	})
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
) (*nicosdk.InstanceCreateRequest, error) {
	if len(nicoMachine.Spec.Interfaces) == 0 {
		return nil, fmt.Errorf("spec.interfaces must contain at least one entry")
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

	createReq := nicosdk.NewInstanceCreateRequest(name, tenantID, nicoMachine.Spec.VPCID, interfaces)
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

	if len(nicoMachine.Spec.InfinibandInterfaces) > 0 {
		ibInterfaces := make([]nicosdk.InfiniBandInterfaceCreateRequest, 0, len(nicoMachine.Spec.InfinibandInterfaces))
		for _, ib := range nicoMachine.Spec.InfinibandInterfaces {
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
			ibInterfaces = append(ibInterfaces, *req)
		}
		createReq.InfinibandInterfaces = ibInterfaces
	}

	if len(nicoMachine.Spec.NVLinkInterfaces) > 0 {
		nvLinkInterfaces := make([]nicosdk.NVLinkInterfaceCreateRequest, 0, len(nicoMachine.Spec.NVLinkInterfaces))
		for _, nv := range nicoMachine.Spec.NVLinkInterfaces {
			req := nicosdk.NewNVLinkInterfaceCreateRequest()
			req.SetNvLinklogicalPartitionId(nv.NVLinkLogicalPartitionID)
			if nv.DeviceInstance != nil {
				req.SetDeviceInstance(*nv.DeviceInstance)
			}
			nvLinkInterfaces = append(nvLinkInterfaces, *req)
		}
		createReq.NvLinkInterfaces = nvLinkInterfaces
	}

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

func instanceTypeAvailable(ctx context.Context, nicoClient nico.API, instanceTypeID string) (bool, string, string, error) {
	log := ctrl.LoggerFrom(ctx)
	instanceType, err := nicoClient.GetInstanceTypeWithAllocationStats(ctx, instanceTypeID)
	log.V(2).Info("instance type availability check result", "instanceType", instanceType, "err", err)
	if err != nil {
		if errors.Is(err, nico.ErrNotFound) {
			return false, infrav1.InstanceTypeNotFoundReason, fmt.Sprintf("Instance type %q was not found", instanceTypeID), nil
		}
		return false, "", "", err
	}
	if instanceType == nil || instanceType.AllocationStats == nil || instanceType.AllocationStats.UnusedUsable == nil {
		return false, "", "", fmt.Errorf("instance type %q response did not include allocationStats.unusedUsable", instanceTypeID)
	}
	if *instanceType.AllocationStats.UnusedUsable > 0 {
		return true, "", "", nil
	}

	message := fmt.Sprintf("Instance type %q has no unused usable allocations (total=%d used=%d unused=%d unusedUsable=%d)",
		instanceTypeID,
		instanceType.AllocationStats.GetTotal(),
		instanceType.AllocationStats.GetUsed(),
		instanceType.AllocationStats.GetUnused(),
		instanceType.AllocationStats.GetUnusedUsable(),
	)

	return false, infrav1.InstanceTypeUnavailableReason, message, nil
}

func instanceStatusString(instance *nicosdk.Instance) string {
	if instance == nil || instance.Status == nil {
		return "Unknown"
	}
	return string(*instance.Status)
}
