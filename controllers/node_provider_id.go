// SPDX-FileCopyrightText: Copyright (c) 2026 NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package controllers

import (
	"context"
	"fmt"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/client-go/tools/clientcmd"
	clusterv1 "sigs.k8s.io/cluster-api/api/core/v1beta2"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	infrav1 "github.com/dsx-ai-factory/cluster-api-provider-nico/api/v1alpha1"
	"github.com/dsx-ai-factory/cluster-api-provider-nico/internal/nico"
)

const (
	skipNodeProviderIDReconciliationAnnotation = "nico.nvidia.com/skip-node-provider-id-reconciliation"
	workloadKubeconfigDataKey                  = "value"
)

func newWorkloadClusterClient(ctx context.Context, managementClient client.Client, cluster client.ObjectKey) (client.Client, error) {
	kubeconfigSecret := &corev1.Secret{}
	kubeconfigSecretKey := client.ObjectKey{Namespace: cluster.Namespace, Name: cluster.Name + "-kubeconfig"}
	if err := managementClient.Get(ctx, kubeconfigSecretKey, kubeconfigSecret); err != nil {
		return nil, fmt.Errorf("get workload cluster kubeconfig: %w", err)
	}
	kubeconfig, ok := kubeconfigSecret.Data[workloadKubeconfigDataKey]
	if !ok {
		return nil, fmt.Errorf("workload cluster kubeconfig Secret %s is missing data key %q", kubeconfigSecretKey, workloadKubeconfigDataKey)
	}

	restConfig, err := clientcmd.RESTConfigFromKubeConfig(kubeconfig)
	if err != nil {
		return nil, fmt.Errorf("parse workload cluster kubeconfig: %w", err)
	}
	restConfig.Timeout = 10 * time.Second

	workloadClient, err := client.New(restConfig, client.Options{Scheme: managementClient.Scheme()})
	if err != nil {
		return nil, fmt.Errorf("create workload cluster client: %w", err)
	}
	return workloadClient, nil
}

func (r *NicoMachineReconciler) reconcileNodeProviderID(
	ctx context.Context,
	machine *clusterv1.Machine,
	cluster *clusterv1.Cluster,
	nicoMachine *infrav1.NicoMachine,
) (ctrl.Result, error) {
	if cluster.Annotations[skipNodeProviderIDReconciliationAnnotation] == "true" ||
		nicoMachine.Spec.ProviderID == "" ||
		nicoMachine.Status.InstanceID == "" {
		return ctrl.Result{}, nil
	}
	instanceID, err := nico.InstanceID(nicoMachine.Spec.ProviderID)
	if err != nil || instanceID != nicoMachine.Status.InstanceID {
		return ctrl.Result{}, nil
	}

	workloadClient, err := newWorkloadClusterClient(ctx, r.Client, client.ObjectKeyFromObject(cluster))
	if err != nil {
		if apierrors.IsNotFound(err) {
			ctrl.LoggerFrom(ctx).V(1).Info("Waiting for workload cluster kubeconfig")
			return ctrl.Result{RequeueAfter: machineRequeueFast}, nil
		}
		return ctrl.Result{}, fmt.Errorf("failed to get workload cluster client: %w", err)
	}

	nodeName := machine.Name
	if machine.Status.NodeRef.IsDefined() {
		nodeName = machine.Status.NodeRef.Name
	}

	node := &corev1.Node{}
	if err := workloadClient.Get(ctx, client.ObjectKey{Name: nodeName}, node); err != nil {
		if apierrors.IsNotFound(err) {
			ctrl.LoggerFrom(ctx).V(1).Info("Waiting for workload cluster Node", "node", nodeName)
			return ctrl.Result{RequeueAfter: machineRequeueFast}, nil
		}
		return ctrl.Result{}, fmt.Errorf("failed to get workload cluster Node %q: %w", nodeName, err)
	}

	if node.Spec.ProviderID == nicoMachine.Spec.ProviderID {
		return ctrl.Result{}, nil
	}
	if node.Spec.ProviderID != "" {
		return ctrl.Result{}, fmt.Errorf(
			"workload cluster Node %q has providerID %q, expected %q; set %s=true on Cluster %s/%s when an external cloud provider owns providerID",
			nodeName,
			node.Spec.ProviderID,
			nicoMachine.Spec.ProviderID,
			skipNodeProviderIDReconciliationAnnotation,
			cluster.Namespace,
			cluster.Name,
		)
	}

	patch := client.StrategicMergeFrom(node.DeepCopy(), client.MergeFromWithOptimisticLock{})
	node.Spec.ProviderID = nicoMachine.Spec.ProviderID
	if err := workloadClient.Patch(ctx, node, patch); err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to set providerID on workload cluster Node %q: %w", nodeName, err)
	}

	ctrl.LoggerFrom(ctx).Info("set workload cluster Node providerID", "node", nodeName, "providerID", node.Spec.ProviderID)
	return ctrl.Result{}, nil
}
