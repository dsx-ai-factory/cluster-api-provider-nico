// SPDX-FileCopyrightText: Copyright (c) 2026 NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package controllers

import (
	"context"
	"errors"
	"fmt"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/cluster-api/util"
	"sigs.k8s.io/cluster-api/util/annotations"
	"sigs.k8s.io/cluster-api/util/predicates"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	clusterv1 "sigs.k8s.io/cluster-api/api/core/v1beta2"
	"sigs.k8s.io/cluster-api/util/conditions"
	"sigs.k8s.io/cluster-api/util/patch"

	infrav1 "github.com/NVIDIA/cluster-api-provider-nico/api/v1alpha1"
	"github.com/NVIDIA/cluster-api-provider-nico/internal/nico"
)

const (
	nicoClusterFinalizer     = "infrastructure.cluster.x-k8s.io/nicocluster"
	clusterReadyRequeue      = 5 * time.Minute
	clusterReadyJitterWindow = 1 * time.Minute
	clusterDeleteRequeue     = 15 * time.Second
)

var nicoClusterOwnedConditions = []string{
	clusterv1.AvailableCondition,
	clusterv1.ReadyCondition,
	infrav1.SyncedCondition,
	clusterv1.PausedCondition,
	clusterv1.DeletingCondition,
	infrav1.NicoReadyCondition,
}

// NicoClusterReconciler reconciles a NicoCluster object.
type NicoClusterReconciler struct {
	client.Client
	APIReader        client.Reader
	Scheme           *runtime.Scheme
	ProviderConfig   nico.ProviderConfig
	WatchFilterValue string
}

// +kubebuilder:rbac:groups=infrastructure.cluster.x-k8s.io,resources=nicoclusters,verbs=get;list;watch;update;patch
// +kubebuilder:rbac:groups=infrastructure.cluster.x-k8s.io,resources=nicoclusters/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=infrastructure.cluster.x-k8s.io,resources=nicoclusters/finalizers,verbs=update
// +kubebuilder:rbac:groups=infrastructure.cluster.x-k8s.io,resources=nicomachines,verbs=get;list;watch
// +kubebuilder:rbac:groups=cluster.x-k8s.io,resources=clusters,verbs=get;list;watch
// +kubebuilder:rbac:groups="",resources=secrets,verbs=get;list;watch

func (r *NicoClusterReconciler) Reconcile(ctx context.Context, req ctrl.Request) (result ctrl.Result, retErr error) {
	log := ctrl.LoggerFrom(ctx)

	log.Info("reconciling NicoCluster")

	var nicoCluster infrav1.NicoCluster
	if err := r.reader().Get(ctx, req.NamespacedName, &nicoCluster); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	cluster, err := util.GetOwnerCluster(ctx, r.Client, nicoCluster.ObjectMeta)
	if err != nil {
		return reconcile.Result{}, err
	}
	if cluster == nil {
		log.Info("Waiting for Cluster controller to set OwnerRef on NicoCluster")
		return reconcile.Result{}, nil
	}

	if !controllerutil.ContainsFinalizer(&nicoCluster, nicoClusterFinalizer) {
		controllerutil.AddFinalizer(&nicoCluster, nicoClusterFinalizer)
		if err := r.Update(ctx, &nicoCluster); err != nil {
			return ctrl.Result{}, fmt.Errorf("failed to add finalizer: %w", err)
		}
		return ctrl.Result{}, nil
	}

	patchHelper, err := patch.NewHelper(&nicoCluster, r.Client)
	if err != nil {
		return ctrl.Result{}, err
	}

	defer func() {
		if conditionErr := setNicoClusterConditions(&nicoCluster); conditionErr != nil {
			retErr = errors.Join(retErr, conditionErr)
		}
		if patchErr := patchHelper.Patch(ctx, &nicoCluster, patch.WithOwnedConditions{Conditions: nicoClusterOwnedConditions}); patchErr != nil {
			retErr = errors.Join(retErr, patchErr)
		}
	}()

	if annotations.IsPaused(cluster, &nicoCluster) {
		conditions.Set(&nicoCluster, metav1.Condition{
			Type:   clusterv1.PausedCondition,
			Status: metav1.ConditionTrue,
			Reason: clusterv1.PausedReason,
		})
		log.V(1).Info("Reconciliation is paused")
		return reconcile.Result{}, nil
	}
	conditions.Set(&nicoCluster, metav1.Condition{
		Type:   clusterv1.PausedCondition,
		Status: metav1.ConditionFalse,
		Reason: clusterv1.NotPausedReason,
	})

	if !nicoCluster.DeletionTimestamp.IsZero() {
		return r.reconcileDelete(ctx, cluster, &nicoCluster)
	}

	nicoClient, err := r.nicoClientForCluster(ctx, &nicoCluster)
	if err != nil {
		if apierrors.IsNotFound(err) {
			setNicoReadyFalse(&nicoCluster, infrav1.WaitingForIdentitySecretReason, err.Error())
			return ctrl.Result{RequeueAfter: 15 * time.Second}, nil
		}
		setNicoReadyFalse(&nicoCluster, infrav1.IdentityConfigurationFailedReason, err.Error())
		return ctrl.Result{}, fmt.Errorf("failed to get nico client: %w", err)
	}

	// NicoCluster has no cluster-scoped NICo resources to reconcile, so readiness here is a validation check:
	// can this identity reach NICo and resolve the tenant context needed for machine operations?
	if err := nicoClient.ValidateReadiness(ctx); err != nil {
		setNicoReadyFalse(&nicoCluster, infrav1.TenantResolutionFailedReason, err.Error())
		return ctrl.Result{}, fmt.Errorf("failed to validate nico client readiness: %w", err)
	}

	provisioned := true
	nicoCluster.Status.Initialization.Provisioned = &provisioned
	setNicoReadyTrue(&nicoCluster, infrav1.InfrastructureReadyReason)

	log.V(1).Info("reconciled NicoCluster")
	return ctrl.Result{RequeueAfter: clusterReadyRequeueAfter(nicoCluster)}, nil
}

// reconcileDelete waits for the cluster's NicoMachines to be deleted before
// removing the NicoCluster finalizer.
func (r *NicoClusterReconciler) reconcileDelete(ctx context.Context, cluster *clusterv1.Cluster, nicoCluster *infrav1.NicoCluster) (ctrl.Result, error) {
	conditions.Set(nicoCluster, metav1.Condition{
		Type:    clusterv1.DeletingCondition,
		Status:  metav1.ConditionTrue,
		Reason:  infrav1.DeletingReason,
		Message: "Deleting cluster infrastructure",
	})

	if !controllerutil.ContainsFinalizer(nicoCluster, nicoClusterFinalizer) {
		return ctrl.Result{}, nil
	}

	nicoMachines, err := r.listNicoMachinesForCluster(ctx, cluster)
	if err != nil {
		return ctrl.Result{}, err
	}

	if len(nicoMachines) > 0 {
		ctrl.LoggerFrom(ctx).Info("waiting for NicoMachines to be deleted", "count", len(nicoMachines))
		conditions.Set(nicoCluster, metav1.Condition{
			Type:    clusterv1.DeletingCondition,
			Status:  metav1.ConditionTrue,
			Reason:  infrav1.WaitingForNicoMachinesDeletionReason,
			Message: fmt.Sprintf("Waiting for %d NicoMachines to be deleted", len(nicoMachines)),
		})
		return ctrl.Result{RequeueAfter: clusterDeleteRequeue}, nil
	}

	completeClusterDeletion(ctx, nicoCluster)
	return ctrl.Result{}, nil
}

func completeClusterDeletion(ctx context.Context, nicoCluster *infrav1.NicoCluster) {
	ctrl.LoggerFrom(ctx).Info("removing finalizer")
	conditions.Set(nicoCluster, metav1.Condition{
		Type:    clusterv1.DeletingCondition,
		Status:  metav1.ConditionTrue,
		Reason:  clusterv1.DeletionCompletedReason,
		Message: "Cluster infrastructure deletion completed",
	})
	controllerutil.RemoveFinalizer(nicoCluster, nicoClusterFinalizer)
}

func (r *NicoClusterReconciler) nicoClientForCluster(ctx context.Context, nicoCluster *infrav1.NicoCluster) (nico.API, error) {
	return nicoClientForCluster(ctx, r.Client, nicoCluster, r.ProviderConfig.Credentials)
}

func (r *NicoClusterReconciler) reader() client.Reader {
	if r.APIReader != nil {
		return r.APIReader
	}
	return r.Client
}

func (r *NicoClusterReconciler) listNicoMachinesForCluster(ctx context.Context, cluster *clusterv1.Cluster) ([]infrav1.NicoMachine, error) {
	var nicoMachines infrav1.NicoMachineList
	if err := r.List(ctx, &nicoMachines,
		client.InNamespace(cluster.Namespace),
		client.MatchingLabels{clusterv1.ClusterNameLabel: cluster.Name},
	); err != nil {
		return nil, fmt.Errorf("failed to list NicoMachines for cluster %q: %w", cluster.Name, err)
	}
	return nicoMachines.Items, nil
}

func setNicoReadyFalse(nicoCluster *infrav1.NicoCluster, reason, message string) {
	conditions.Set(nicoCluster, metav1.Condition{
		Type:    infrav1.NicoReadyCondition,
		Status:  metav1.ConditionFalse,
		Reason:  reason,
		Message: message,
	})
}

func setNicoReadyTrue(nicoCluster *infrav1.NicoCluster, reason string) {
	conditions.Set(nicoCluster, metav1.Condition{
		Type:   infrav1.NicoReadyCondition,
		Status: metav1.ConditionTrue,
		Reason: reason,
	})
}

func setNicoClusterConditions(nicoCluster *infrav1.NicoCluster) error {
	if nicoCluster.DeletionTimestamp.IsZero() {
		conditions.Set(nicoCluster, metav1.Condition{
			Type:   clusterv1.DeletingCondition,
			Status: metav1.ConditionFalse,
			Reason: clusterv1.NotDeletingReason,
		})
	}

	if err := conditions.SetSummaryCondition(
		nicoCluster,
		nicoCluster,
		infrav1.SyncedCondition,
		conditions.ForConditionTypes{infrav1.NicoReadyCondition},
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
		return fmt.Errorf("summarize NicoCluster Synced condition: %w", err)
	}

	if err := conditions.SetSummaryCondition(
		nicoCluster,
		nicoCluster,
		clusterv1.AvailableCondition,
		conditions.ForConditionTypes{infrav1.NicoReadyCondition},
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
		return fmt.Errorf("summarize NicoCluster Available condition: %w", err)
	}

	if err := conditions.SetSummaryCondition(
		nicoCluster,
		nicoCluster,
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
		return fmt.Errorf("summarize NicoCluster Ready condition: %w", err)
	}
	nicoCluster.Status.Ready = conditions.IsTrue(nicoCluster, clusterv1.ReadyCondition)
	return nil
}

// clusterReadyRequeueAfter returns a stable jittered interval for steady-state cluster polling.
// The jitter spreads reconciles across the window so many clusters do not requeue at once.
func clusterReadyRequeueAfter(nicoCluster infrav1.NicoCluster) time.Duration {
	return clusterReadyRequeue + deterministicJitter(string(nicoCluster.UID), clusterReadyJitterWindow)
}

func (r *NicoClusterReconciler) SetupWithManager(ctx context.Context, mgr ctrl.Manager) error {
	if r.APIReader == nil {
		r.APIReader = mgr.GetAPIReader()
	}

	predicateLog := ctrl.LoggerFrom(ctx).WithValues("controller", "NicoCluster")
	return ctrl.NewControllerManagedBy(mgr).
		For(&infrav1.NicoCluster{}).
		WithEventFilter(predicates.ResourceHasFilterLabel(mgr.GetScheme(), predicateLog, r.WatchFilterValue)).
		Watches(
			&clusterv1.Cluster{},
			handler.EnqueueRequestsFromMapFunc(util.ClusterToInfrastructureMapFunc(ctx, infrav1.GroupVersion.WithKind("NicoCluster"), mgr.GetClient(), &infrav1.NicoCluster{})),
			builder.WithPredicates(predicates.ClusterPausedTransitions(mgr.GetScheme(), predicateLog)),
		).
		Complete(r)
}
