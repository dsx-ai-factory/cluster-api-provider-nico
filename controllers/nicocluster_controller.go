// SPDX-FileCopyrightText: Copyright (c) 2026 NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package controllers

import (
	"context"
	"fmt"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/cluster-api/util"
	"sigs.k8s.io/cluster-api/util/paused"
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

// NicoClusterReconciler reconciles a NicoCluster object.
type NicoClusterReconciler struct {
	client.Client
	Scheme         *runtime.Scheme
	ProviderConfig nico.ProviderConfig
	// nicoClientFactory optionally overrides client construction after Secret load (tests).
	nicoClientFactory nicoClientFactory
}

// +kubebuilder:rbac:groups=infrastructure.cluster.x-k8s.io,resources=nicoclusters,verbs=get;list;watch;update;patch
// +kubebuilder:rbac:groups=infrastructure.cluster.x-k8s.io,resources=nicoclusters/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=infrastructure.cluster.x-k8s.io,resources=nicoclusters/finalizers,verbs=update
// +kubebuilder:rbac:groups=infrastructure.cluster.x-k8s.io,resources=nicomachines,verbs=get;list;watch
// +kubebuilder:rbac:groups=cluster.x-k8s.io,resources=clusters,verbs=get;list;watch
// +kubebuilder:rbac:groups="",resources=secrets,verbs=get;list;watch

func (r *NicoClusterReconciler) Reconcile(ctx context.Context, req ctrl.Request) (result ctrl.Result, retErr error) {
	log := ctrl.LoggerFrom(ctx)

	var nicoCluster infrav1.NicoCluster
	if err := r.Get(ctx, req.NamespacedName, &nicoCluster); err != nil {
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

	patchHelper, err := patch.NewHelper(&nicoCluster, r.Client)
	if err != nil {
		return ctrl.Result{}, err
	}

	defer func() {
		if err := patchHelper.Patch(ctx, &nicoCluster, patch.WithOwnedConditions{Conditions: []string{clusterv1.ReadyCondition}}); err != nil && retErr == nil {
			retErr = fmt.Errorf("failed to patch NicoCluster: %w", err)
		}
	}()

	if isPaused, requeue, err := paused.EnsurePausedCondition(ctx, r.Client, cluster, &nicoCluster); err != nil || isPaused || requeue {
		return reconcile.Result{}, err
	}

	if !nicoCluster.DeletionTimestamp.IsZero() {
		if !controllerutil.ContainsFinalizer(&nicoCluster, nicoClusterFinalizer) {
			return ctrl.Result{}, nil
		}

		nicoMachines, err := r.listNicoMachinesForCluster(ctx, cluster)
		if err != nil {
			return ctrl.Result{}, err
		}

		if len(nicoMachines) > 0 {
			log.Info("waiting for NicoMachines to be deleted", "count", len(nicoMachines))
			setNicoClusterReadyFalse(&nicoCluster, infrav1.WaitingForNicoMachinesDeletionReason, fmt.Sprintf("Waiting for %d NicoMachines to be deleted", len(nicoMachines)))
			return ctrl.Result{RequeueAfter: clusterDeleteRequeue}, nil
		}

		log.Info("removing finalizer")
		controllerutil.RemoveFinalizer(&nicoCluster, nicoClusterFinalizer)
		setNicoClusterReadyFalse(&nicoCluster, infrav1.DeletingReason, "")
		return ctrl.Result{}, nil
	}

	if !controllerutil.ContainsFinalizer(&nicoCluster, nicoClusterFinalizer) {
		controllerutil.AddFinalizer(&nicoCluster, nicoClusterFinalizer)
	}

	nicoClient, err := r.nicoClientForCluster(ctx, &nicoCluster)
	if err != nil {
		if apierrors.IsNotFound(err) {
			setNicoClusterReadyFalse(&nicoCluster, infrav1.WaitingForIdentitySecretReason, err.Error())
			return ctrl.Result{RequeueAfter: 15 * time.Second}, nil
		}
		setNicoClusterReadyFalse(&nicoCluster, infrav1.IdentityConfigurationFailedReason, err.Error())
		return ctrl.Result{}, fmt.Errorf("failed to get nico client: %w", err)
	}

	// NicoCluster has no cluster-scoped NICo resources to reconcile, so readiness here is a validation check:
	// can this identity reach NICo and resolve the tenant context needed for machine operations?
	if err := nicoClient.ValidateReadiness(ctx); err != nil {
		setNicoClusterReadyFalse(&nicoCluster, infrav1.TenantResolutionFailedReason, err.Error())
		return ctrl.Result{}, fmt.Errorf("failed to validate nico client readiness: %w", err)
	}

	provisioned := true
	nicoCluster.Status.Initialization.Provisioned = &provisioned
	setNicoClusterReadyTrue(&nicoCluster, infrav1.InfrastructureReadyReason)

	log.V(1).Info("reconciled NicoCluster")
	return ctrl.Result{RequeueAfter: clusterReadyRequeueAfter(nicoCluster)}, nil
}

func (r *NicoClusterReconciler) nicoClientForCluster(ctx context.Context, nicoCluster *infrav1.NicoCluster) (nico.API, error) {
	return nicoClientForCluster(ctx, r.Client, nicoCluster, r.ProviderConfig.Credentials, r.nicoClientFactory)
}

func (r *NicoClusterReconciler) SetupWithManager(ctx context.Context, mgr ctrl.Manager) error {
	predicateLog := ctrl.LoggerFrom(ctx).WithValues("controller", "NicoCluster")
	return ctrl.NewControllerManagedBy(mgr).
		For(&infrav1.NicoCluster{}).
		Watches(
			&clusterv1.Cluster{},
			handler.EnqueueRequestsFromMapFunc(util.ClusterToInfrastructureMapFunc(ctx, infrav1.GroupVersion.WithKind("NicoCluster"), mgr.GetClient(), &infrav1.NicoCluster{})),
			builder.WithPredicates(predicates.ClusterPausedTransitions(mgr.GetScheme(), predicateLog)),
		).
		Complete(r)
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

func setNicoClusterReadyFalse(nicoCluster *infrav1.NicoCluster, reason, message string) {
	nicoCluster.Status.Ready = false
	conditions.Set(nicoCluster, metav1.Condition{
		Type:    clusterv1.ReadyCondition,
		Status:  metav1.ConditionFalse,
		Reason:  reason,
		Message: message,
	})
}

func setNicoClusterReadyTrue(nicoCluster *infrav1.NicoCluster, reason string) {
	nicoCluster.Status.Ready = true
	conditions.Set(nicoCluster, metav1.Condition{
		Type:   clusterv1.ReadyCondition,
		Status: metav1.ConditionTrue,
		Reason: reason,
	})
}

// clusterReadyRequeueAfter returns a stable jittered interval for steady-state cluster polling.
// The jitter spreads reconciles across the window so many clusters do not requeue at once.
func clusterReadyRequeueAfter(nicoCluster infrav1.NicoCluster) time.Duration {
	return clusterReadyRequeue + deterministicJitter(string(nicoCluster.UID), clusterReadyJitterWindow)
}
