package controllers

import (
	"context"
	"errors"
	"fmt"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	clusterv1 "sigs.k8s.io/cluster-api/api/core/v1beta2"
	"sigs.k8s.io/cluster-api/util"
	"sigs.k8s.io/cluster-api/util/conditions"
	"sigs.k8s.io/cluster-api/util/patch"

	infrav1 "gitlab-master.nvidia.com/nke/cluster-api-provider-nico/api/v1alpha1"
	"gitlab-master.nvidia.com/nke/cluster-api-provider-nico/internal/nico"

	nicosdk "github.com/NVIDIA/ncx-infra-controller-rest/sdk/standard"
)

const (
	nicoMachineFinalizer = "infrastructure.cluster.x-k8s.io/nicomachine"
	machineRequeueFast   = 15 * time.Second
	machineRequeueSlow   = 30 * time.Second
)

// NicoMachineReconciler reconciles a NicoMachine object.
type NicoMachineReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

// +kubebuilder:rbac:groups=infrastructure.cluster.x-k8s.io,resources=nicomachines,verbs=get;list;watch;create;update;patch
// +kubebuilder:rbac:groups=infrastructure.cluster.x-k8s.io,resources=nicomachines/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=infrastructure.cluster.x-k8s.io,resources=nicomachines/finalizers,verbs=update
// +kubebuilder:rbac:groups=infrastructure.cluster.x-k8s.io,resources=nicoclusters,verbs=get;list;watch
// +kubebuilder:rbac:groups=cluster.x-k8s.io,resources=machines;clusters,verbs=get;list;watch
// +kubebuilder:rbac:groups="",resources=secrets,verbs=get;list;watch

func (r *NicoMachineReconciler) Reconcile(ctx context.Context, req ctrl.Request) (result ctrl.Result, retErr error) {
	log := ctrl.LoggerFrom(ctx)

	var nicoMachine infrav1.NicoMachine
	if err := r.Get(ctx, req.NamespacedName, &nicoMachine); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	ownerMachine, err := util.GetOwnerMachine(ctx, r.Client, nicoMachine.ObjectMeta)
	if err != nil {
		return ctrl.Result{}, err
	}
	if ownerMachine == nil {
		return ctrl.Result{}, nil
	}

	cluster, err := util.GetClusterFromMetadata(ctx, r.Client, ownerMachine.ObjectMeta)
	if err != nil {
		return ctrl.Result{}, err
	}
	if cluster == nil {
		return ctrl.Result{}, nil
	}
	if cluster.Spec.InfrastructureRef.Name == "" {
		return ctrl.Result{RequeueAfter: machineRequeueFast}, nil
	}

	var nicoCluster infrav1.NicoCluster
	if err := r.Get(ctx, client.ObjectKey{
		Namespace: cluster.Namespace,
		Name:      cluster.Spec.InfrastructureRef.Name,
	}, &nicoCluster); err != nil {
		return ctrl.Result{}, err
	}

	patchHelper, err := patch.NewHelper(&nicoMachine, r.Client)
	if err != nil {
		return ctrl.Result{}, err
	}
	defer func() {
		if err := patchHelper.Patch(ctx, &nicoMachine, patch.WithOwnedConditions{Conditions: []string{clusterv1.ReadyCondition}}); err != nil && retErr == nil {
			retErr = err
		}
	}()

	if !nicoMachine.DeletionTimestamp.IsZero() {
		if controllerutil.ContainsFinalizer(&nicoMachine, nicoMachineFinalizer) && nicoMachine.Status.InstanceID != "" {
			nicoClient, err := nicoClientForCluster(ctx, r.Client, &nicoCluster)
			if err != nil && !apierrors.IsNotFound(err) {
				return ctrl.Result{}, err
			}
			if err == nil {
				if deleteErr := nico.IgnoreNotFound(nicoClient.DeleteInstance(ctx, nicoMachine.Status.InstanceID)); deleteErr != nil {
					return ctrl.Result{}, deleteErr
				}
			}
		}

		controllerutil.RemoveFinalizer(&nicoMachine, nicoMachineFinalizer)
		nicoMachine.Status.Ready = false
		conditions.Set(&nicoMachine, metav1.Condition{
			Type:   clusterv1.ReadyCondition,
			Status: metav1.ConditionFalse,
			Reason: "Deleting",
		})
		return ctrl.Result{}, nil
	}

	if !controllerutil.ContainsFinalizer(&nicoMachine, nicoMachineFinalizer) {
		controllerutil.AddFinalizer(&nicoMachine, nicoMachineFinalizer)
	}

	nicoClient, err := nicoClientForCluster(ctx, r.Client, &nicoCluster)
	if err != nil {
		if apierrors.IsNotFound(err) {
			conditions.Set(&nicoMachine, metav1.Condition{
				Type:    clusterv1.ReadyCondition,
				Status:  metav1.ConditionFalse,
				Reason:  "WaitingForIdentitySecret",
				Message: err.Error(),
			})
			nicoMachine.Status.Ready = false
			return ctrl.Result{RequeueAfter: machineRequeueFast}, nil
		}
		return ctrl.Result{}, err
	}

	tenantID, err := nicoClient.ResolveTenantID(ctx)
	if err != nil {
		conditions.Set(&nicoMachine, metav1.Condition{
			Type:    clusterv1.ReadyCondition,
			Status:  metav1.ConditionFalse,
			Reason:  "TenantResolutionFailed",
			Message: err.Error(),
		})
		nicoMachine.Status.Ready = false
		return ctrl.Result{}, err
	}

	if nicoMachine.Status.InstanceID == "" {
		if ownerMachine.Spec.Bootstrap.DataSecretName == nil || *ownerMachine.Spec.Bootstrap.DataSecretName == "" {
			conditions.Set(&nicoMachine, metav1.Condition{
				Type:   clusterv1.ReadyCondition,
				Status: metav1.ConditionFalse,
				Reason: "WaitingForBootstrapData",
			})
			nicoMachine.Status.Ready = false
			return ctrl.Result{RequeueAfter: machineRequeueFast}, nil
		}

		var bootstrapSecret corev1.Secret
		if err := r.Get(ctx, client.ObjectKey{
			Namespace: ownerMachine.Namespace,
			Name:      *ownerMachine.Spec.Bootstrap.DataSecretName,
		}, &bootstrapSecret); err != nil {
			if apierrors.IsNotFound(err) {
				conditions.Set(&nicoMachine, metav1.Condition{
					Type:   clusterv1.ReadyCondition,
					Status: metav1.ConditionFalse,
					Reason: "WaitingForBootstrapData",
				})
				nicoMachine.Status.Ready = false
				return ctrl.Result{RequeueAfter: machineRequeueFast}, nil
			}
			return ctrl.Result{}, err
		}

		bootstrapCloudConfig, err := bootstrapCloudConfigFromSecret(&bootstrapSecret)
		if err != nil {
			return ctrl.Result{}, err
		}
		if nicoMachine.Spec.CloudInitInjectHostname {
			bootstrapCloudConfig, err = nico.InjectHostnameCloudConfig(bootstrapCloudConfig, ownerMachine.Name)
			if err != nil {
				return ctrl.Result{}, err
			}
		}

		createReq, err := buildInstanceCreateRequest(ownerMachine.Name, tenantID, &nicoCluster, &nicoMachine, cluster.Name, bootstrapCloudConfig)
		if err != nil {
			return ctrl.Result{}, err
		}

		log.Info("creating NICo instance", "machine", ownerMachine.Name)
		instance, err := nicoClient.CreateInstance(ctx, *createReq)
		if err != nil {
			if errors.Is(err, nico.ErrAlreadyExists) {
				instance, err = nicoClient.FindInstanceByName(ctx, nico.InstanceLookup{
					Name:   ownerMachine.Name,
					VPCID:  nicoCluster.Spec.VPCID,
					SiteID: nicoCluster.Spec.SiteID,
				})
			}
			if err != nil {
				return ctrl.Result{}, err
			}
		}

		nicoMachine.Status.InstanceID = instance.GetId()
		nicoMachine.Spec.ProviderID = nico.ProviderID(instance.GetId())
	}

	instance, err := nicoClient.GetInstance(ctx, nicoMachine.Status.InstanceID)
	if err != nil {
		if errors.Is(err, nico.ErrNotFound) {
			nicoMachine.Status.InstanceID = ""
			nicoMachine.Spec.ProviderID = ""
			nicoMachine.Status.Ready = false
			nicoMachine.Status.Addresses = nil
			conditions.Set(&nicoMachine, metav1.Condition{
				Type:    clusterv1.ReadyCondition,
				Status:  metav1.ConditionFalse,
				Reason:  "InstanceMissing",
				Message: "Backing NICo instance was not found and will be recreated",
			})
			return ctrl.Result{RequeueAfter: machineRequeueFast}, nil
		}
		return ctrl.Result{}, err
	}

	if ip := firstIPv4FromInstance(instance); ip != "" {
		nicoMachine.Status.Addresses = []clusterv1.MachineAddress{{
			Type:    clusterv1.MachineInternalIP,
			Address: ip,
		}}
	}

	provisioned := true
	nicoMachine.Status.Initialization.Provisioned = &provisioned
	if nico.IsReady(instance) {
		nicoMachine.Status.Ready = true
		conditions.Set(&nicoMachine, metav1.Condition{
			Type:   clusterv1.ReadyCondition,
			Status: metav1.ConditionTrue,
			Reason: "InstanceReady",
		})
		return ctrl.Result{RequeueAfter: machineRequeueSlow}, nil
	}

	nicoMachine.Status.Ready = false
	conditions.Set(&nicoMachine, metav1.Condition{
		Type:    clusterv1.ReadyCondition,
		Status:  metav1.ConditionFalse,
		Reason:  "InstanceProvisioning",
		Message: fmt.Sprintf("Instance status is %s", instanceStatusString(instance)),
	})
	return ctrl.Result{RequeueAfter: machineRequeueSlow}, nil
}

func (r *NicoMachineReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&infrav1.NicoMachine{}).
		Complete(r)
}

func buildInstanceCreateRequest(
	name string,
	tenantID string,
	nicoCluster *infrav1.NicoCluster,
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
			return nil, fmt.Errorf("spec.interfaces[%d] must set exactly one of subnetId or vpcPrefixId", i)
		}

		req := nicosdk.NewInterfaceCreateRequest()
		if iface.SubnetID != "" {
			req.SetSubnetId(iface.SubnetID)
		}
		if iface.VPCPrefixID != "" {
			req.SetVpcPrefixId(iface.VPCPrefixID)
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

	createReq := nicosdk.NewInstanceCreateRequest(name, tenantID, nicoCluster.Spec.VPCID, interfaces)
	createReq.SetDescription("Managed by Cluster API")
	createReq.SetInstanceTypeId(nicoMachine.Spec.InstanceTypeID)
	createReq.SetUserData(bootstrapCloudConfig)
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

func instanceStatusString(instance *nicosdk.Instance) string {
	if instance == nil || instance.Status == nil {
		return "Unknown"
	}
	return string(*instance.Status)
}
