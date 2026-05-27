package controllers

import (
	"context"
	"fmt"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	clusterv1 "sigs.k8s.io/cluster-api/api/core/v1beta2"
	"sigs.k8s.io/cluster-api/util/conditions"
	"sigs.k8s.io/cluster-api/util/patch"

	infrav1 "gitlab-master.nvidia.com/nke/cluster-api-provider-nico/api/v1alpha1"
	"gitlab-master.nvidia.com/nke/cluster-api-provider-nico/internal/nico"
)

const (
	clusterReadyRequeue      = 5 * time.Minute
	clusterReadyJitterWindow = 1 * time.Minute
)

// NicoClusterReconciler reconciles a NicoCluster object.
type NicoClusterReconciler struct {
	client.Client
	Scheme         *runtime.Scheme
	ProviderConfig nico.ProviderConfig
}

// +kubebuilder:rbac:groups=infrastructure.cluster.x-k8s.io,resources=nicoclusters,verbs=get;list;watch;update;patch
// +kubebuilder:rbac:groups=infrastructure.cluster.x-k8s.io,resources=nicoclusters/status,verbs=get;update;patch
// +kubebuilder:rbac:groups="",resources=secrets,verbs=get;list;watch

func (r *NicoClusterReconciler) Reconcile(ctx context.Context, req ctrl.Request) (result ctrl.Result, retErr error) {
	log := ctrl.LoggerFrom(ctx)

	var nicoCluster infrav1.NicoCluster
	if err := r.Get(ctx, req.NamespacedName, &nicoCluster); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	patchHelper, err := patch.NewHelper(&nicoCluster, r.Client)
	if err != nil {
		return ctrl.Result{}, err
	}

	defer func() {
		if err := patchHelper.Patch(ctx, &nicoCluster, patch.WithOwnedConditions{Conditions: []string{clusterv1.ReadyCondition}}); err != nil && retErr == nil {
			retErr = fmt.Errorf("failed to patch NicoCluster: %w", err)
		}
	}()

	nicoClient, err := nicoClientForCluster(ctx, r.Client, &nicoCluster, r.ProviderConfig.Credentials)
	if err != nil {
		if apierrors.IsNotFound(err) {
			conditions.Set(&nicoCluster, metav1.Condition{
				Type:    clusterv1.ReadyCondition,
				Status:  metav1.ConditionFalse,
				Reason:  "WaitingForIdentitySecret",
				Message: err.Error(),
			})
			nicoCluster.Status.Ready = false
			return ctrl.Result{RequeueAfter: 15 * time.Second}, nil
		}
		conditions.Set(&nicoCluster, metav1.Condition{
			Type:    clusterv1.ReadyCondition,
			Status:  metav1.ConditionFalse,
			Reason:  "IdentityConfigurationFailed",
			Message: err.Error(),
		})
		nicoCluster.Status.Ready = false
		return ctrl.Result{}, fmt.Errorf("failed to get nico client: %w", err)
	}

	// NicoCluster has no cluster-scoped NICo resources to reconcile, so readiness here is a validation check:
	// can this identity reach NICo and resolve the tenant context needed for machine operations?
	if err := nicoClient.ValidateReadiness(ctx); err != nil {
		conditions.Set(&nicoCluster, metav1.Condition{
			Type:    clusterv1.ReadyCondition,
			Status:  metav1.ConditionFalse,
			Reason:  "TenantResolutionFailed",
			Message: err.Error(),
		})
		nicoCluster.Status.Ready = false
		return ctrl.Result{}, fmt.Errorf("failed to validate nico client readiness: %w", err)
	}

	provisioned := true
	nicoCluster.Status.Initialization.Provisioned = &provisioned
	nicoCluster.Status.Ready = true
	conditions.Set(&nicoCluster, metav1.Condition{
		Type:   clusterv1.ReadyCondition,
		Status: metav1.ConditionTrue,
		Reason: "InfrastructureReady",
	})

	log.V(1).Info("reconciled NicoCluster")
	return ctrl.Result{RequeueAfter: clusterReadyRequeueAfter(nicoCluster)}, nil
}

func (r *NicoClusterReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&infrav1.NicoCluster{}).
		Complete(r)
}

// clusterReadyRequeueAfter returns a stable jittered interval for steady-state cluster polling.
// The jitter spreads reconciles across the window so many clusters do not requeue at once.
func clusterReadyRequeueAfter(nicoCluster infrav1.NicoCluster) time.Duration {
	return clusterReadyRequeue + deterministicJitter(string(nicoCluster.UID), clusterReadyJitterWindow)
}
