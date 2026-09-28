// SPDX-FileCopyrightText: Copyright (c) 2026 NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package controllers

import (
	"context"
	"errors"
	"fmt"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	clusterv1 "sigs.k8s.io/cluster-api/api/core/v1beta2"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	infrav1 "github.com/dsx-ai-factory/cluster-api-provider-nico/api/v1alpha1"
	"github.com/dsx-ai-factory/cluster-api-provider-nico/internal/nico"
)

const (
	softRebootAnnotation          = nico.DefaultSoftRebootAnnotation
	hardRebootAnnotation          = nico.DefaultHardRebootAnnotation
	lastRebootTriggeredAnnotation = "nico.nvidia.com/last-reboot-triggered-timestamp"

	rebootModeFallback     = "graceful-fallback"
	rebootModeGracefulOnly = "graceful-only"
	rebootModeHard         = "hard"
	rebootPathGraceful     = "graceful"

	rebootPhasePrepared           = "prepared"
	rebootPhaseGracefulDispatched = "graceful-dispatched"
	rebootPhaseHardDispatched     = "hard-dispatched"
	rebootPhaseCompleted          = "completed"

	rebootRecoveryTimeout = 30 * time.Minute
	rebootPollInterval    = 15 * time.Second
)

// reconcileReboot handles one request at a time. The state write preceding each
// NICo call deliberately favors at-most-once dispatch over retrying an action
// whose response may have been lost.
// instanceClient can be nil during dispatched recovery; hard fallback loads it
// only after the recovery deadline.
func (r *NicoMachineReconciler) reconcileReboot(
	ctx context.Context,
	machine *clusterv1.Machine,
	cluster *clusterv1.Cluster,
	nicoCluster *infrav1.NicoCluster,
	nicoMachine *infrav1.NicoMachine,
	instanceClient nico.API,
	instanceID string,
) (ctrl.Result, bool, error) {
	state := nicoMachine.Status.Reboot
	requestAnnotation, mode, requestCount := requestedReboot(machine, r.ProviderConfig.RebootAnnotation)
	if state == nil || state.Phase == rebootPhaseCompleted {
		if requestCount == 0 {
			return ctrl.Result{}, false, nil
		}
		// An uncached read avoids treating the annotation we just removed as a
		// fresh request when the manager cache has not caught up yet.
		if state != nil {
			fresh := &clusterv1.Machine{}
			if err := r.reader().Get(ctx, client.ObjectKeyFromObject(machine), fresh); err != nil {
				return ctrl.Result{}, true, fmt.Errorf("read reboot request from Machine: %w", err)
			}
			machine = fresh
			requestAnnotation, mode, requestCount = requestedReboot(machine, r.ProviderConfig.RebootAnnotation)
			if requestCount == 0 {
				return ctrl.Result{}, false, nil
			}
		}
		if requestCount > 1 {
			r.rebootEvent(machine, corev1.EventTypeWarning, "RebootRequestConflict", "Only one reboot annotation can be set at a time")
			return ctrl.Result{RequeueAfter: rebootPollInterval}, true, nil
		}
		freshState := &infrav1.NicoMachineRebootStatus{
			Annotation:      requestAnnotation,
			AnnotationValue: machine.Annotations[requestAnnotation],
			Mode:            mode,
			Phase:           rebootPhasePrepared,
			StartedAt:       metav1.NewTime(time.Now().UTC()),
		}
		if err := r.persistRebootState(ctx, nicoMachine, freshState); err != nil {
			return ctrl.Result{}, true, fmt.Errorf("record reboot request: %w", err)
		}
		return r.reconcilePreparedReboot(ctx, machine, cluster, nicoCluster, nicoMachine, instanceClient, instanceID)
	}

	switch state.Phase {
	case rebootPhasePrepared:
		return r.reconcilePreparedReboot(ctx, machine, cluster, nicoCluster, nicoMachine, instanceClient, instanceID)
	case rebootPhaseGracefulDispatched:
		bootID, ready, err := r.observeRebootNode(ctx, machine, cluster)
		if err != nil {
			ctrl.LoggerFrom(ctx).V(1).Info("cannot observe workload Node reboot", "error", err)
		}
		if state.BootID != "" && bootID != "" && bootID != state.BootID {
			if ready {
				r.rebootEvent(machine, corev1.EventTypeNormal, "GracefulRebootRecovered", "Workload Node boot ID changed and the Node is Ready")
				return r.completeReboot(ctx, machine, nicoMachine, state, rebootPathGraceful, "Workload Node rebooted and returned Ready")
			}
			// A new boot ID proves the host rebooted. A hard reboot cannot repair
			// delayed DPU or network recovery and could interrupt it again.
			if state.Deadline != nil && !time.Now().Before(state.Deadline.Time) {
				r.rebootEvent(machine, corev1.EventTypeWarning, "NodeRecoveryIncomplete", "Workload Node boot ID changed, but the Node is not Ready after 30 minutes")
				return r.completeReboot(ctx, machine, nicoMachine, state, rebootPathGraceful, "Host rebooted, but the workload Node did not return Ready within 30 minutes")
			}
			return ctrl.Result{RequeueAfter: rebootPollInterval}, true, nil
		}
		if state.Deadline != nil && time.Now().Before(state.Deadline.Time) {
			return ctrl.Result{RequeueAfter: rebootPollInterval}, true, nil
		}
		if state.Mode == rebootModeGracefulOnly {
			r.rebootEvent(machine, corev1.EventTypeWarning, "GracefulRebootUnconfirmed", "No changed workload Node boot ID was observed before the recovery deadline")
			return r.completeReboot(ctx, machine, nicoMachine, state, rebootPathGraceful, "Graceful reboot was not confirmed before the recovery deadline")
		}
		if instanceClient == nil {
			instanceID, err = nico.InstanceID(nicoMachine.Spec.ProviderID)
			if err != nil {
				return ctrl.Result{}, true, fmt.Errorf("resolve instance ID for hard reboot fallback: %w", err)
			}
			instanceClient, err = r.nicoClientForCluster(ctx, nicoCluster)
			if err != nil {
				return ctrl.Result{}, true, fmt.Errorf("get NICo client for hard reboot fallback: %w", err)
			}
		}
		r.rebootEvent(machine, corev1.EventTypeWarning, "HardRebootFallback", "No changed workload Node boot ID was observed within 30 minutes; using hard reboot")
		return r.dispatchHardReboot(ctx, machine, nicoMachine, instanceClient, instanceID, state, true)
	case rebootPhaseHardDispatched:
		return r.completeReboot(ctx, machine, nicoMachine, state, rebootModeHard, state.Message)
	default:
		return ctrl.Result{}, true, fmt.Errorf("unknown reboot phase %q", state.Phase)
	}
}

func (r *NicoMachineReconciler) reconcilePreparedReboot(
	ctx context.Context,
	machine *clusterv1.Machine,
	cluster *clusterv1.Cluster,
	nicoCluster *infrav1.NicoCluster,
	nicoMachine *infrav1.NicoMachine,
	instanceClient nico.API,
	instanceID string,
) (ctrl.Result, bool, error) {
	state := nicoMachine.Status.Reboot
	fresh := &clusterv1.Machine{}
	if err := r.reader().Get(ctx, client.ObjectKeyFromObject(machine), fresh); err != nil {
		return ctrl.Result{}, true, fmt.Errorf("read Machine before reboot dispatch: %w", err)
	}
	machine = fresh
	currentValue := machine.Annotations[state.Annotation]
	if currentValue == "" {
		return r.completeReboot(ctx, machine, nicoMachine, state, "none", "Reboot request removed before dispatch")
	}
	if state.AnnotationValue != "" && currentValue != state.AnnotationValue {
		return r.completeReboot(ctx, machine, nicoMachine, state, "none", "Reboot request replaced before dispatch")
	}
	if _, _, count := requestedReboot(machine, r.ProviderConfig.RebootAnnotation); count > 1 {
		r.rebootEvent(machine, corev1.EventTypeWarning, "RebootRequestConflict", "Only one reboot annotation can be set at a time")
		return ctrl.Result{RequeueAfter: rebootPollInterval}, true, nil
	}
	if state.Mode == rebootModeHard {
		return r.dispatchHardReboot(ctx, machine, nicoMachine, instanceClient, instanceID, state, false)
	}
	if nicoMachine.Status.MachineID == "" {
		if state.Mode == rebootModeFallback {
			r.rebootEvent(machine, corev1.EventTypeWarning, "GracefulRebootUnavailable", "NICo Machine ID is unavailable; using hard reboot")
			return r.dispatchHardReboot(ctx, machine, nicoMachine, instanceClient, instanceID, state, true)
		}
		return ctrl.Result{RequeueAfter: rebootPollInterval}, true, nil
	}
	powerClient, err := powerControlClientForCluster(ctx, r.Client, nicoCluster, instanceClient)
	if err != nil {
		if state.Mode == rebootModeFallback {
			r.rebootEvent(machine, corev1.EventTypeWarning, "GracefulRebootUnavailable", "Provider power-control credentials are unavailable; using hard reboot")
			return r.dispatchHardReboot(ctx, machine, nicoMachine, instanceClient, instanceID, state, true)
		}
		return ctrl.Result{RequeueAfter: machineRequeueSlow}, true, fmt.Errorf("get provider power-control client: %w", err)
	}
	return r.dispatchGracefulReboot(ctx, machine, cluster, nicoMachine, powerClient, instanceClient, instanceID, state)
}

func requestedReboot(machine *clusterv1.Machine, configured string) (string, string, int) {
	if configured == "" {
		configured = nico.DefaultRebootAnnotation
	}
	var key, mode string
	count := 0
	for _, candidate := range []struct{ key, mode string }{
		{configured, rebootModeFallback},
		{softRebootAnnotation, rebootModeGracefulOnly},
		{hardRebootAnnotation, rebootModeHard},
	} {
		if machine.Annotations[candidate.key] != "" {
			key, mode = candidate.key, candidate.mode
			count++
		}
	}
	return key, mode, count
}

func (r *NicoMachineReconciler) dispatchGracefulReboot(ctx context.Context, machine *clusterv1.Machine, cluster *clusterv1.Cluster, nicoMachine *infrav1.NicoMachine, powerClient, instanceClient nico.API, instanceID string, state *infrav1.NicoMachineRebootStatus) (ctrl.Result, bool, error) {
	copy := *state
	copy.Phase = rebootPhaseGracefulDispatched
	bootID, _, err := r.observeRebootNode(ctx, machine, cluster)
	if err != nil {
		ctrl.LoggerFrom(ctx).V(1).Info("cannot capture workload Node boot ID before reboot", "error", err)
	}
	copy.BootID = bootID
	deadline := metav1.NewTime(time.Now().UTC().Add(rebootRecoveryTimeout))
	copy.Deadline = &deadline
	if err := r.persistRebootState(ctx, nicoMachine, &copy); err != nil {
		return ctrl.Result{}, true, fmt.Errorf("record graceful reboot dispatch: %w", err)
	}
	if err := powerClient.GracefulRestartMachine(ctx, nicoMachine.Status.MachineID); err != nil {
		// A concrete HTTP rejection means NICo did not accept the action. An
		// interrupted request is uncertain and must not be retried or immediately
		// followed by a hard reboot.
		if definiteRebootRejection(err) {
			r.rebootEvent(machine, corev1.EventTypeWarning, "GracefulRebootRejected", "NICo rejected GracefulRestart")
			if state.Mode == rebootModeFallback {
				r.rebootEvent(machine, corev1.EventTypeWarning, "HardRebootFallback", "NICo rejected GracefulRestart; using hard reboot")
				return r.dispatchHardReboot(ctx, machine, nicoMachine, instanceClient, instanceID, nicoMachine.Status.Reboot, true)
			}
			return r.completeReboot(ctx, machine, nicoMachine, nicoMachine.Status.Reboot, "none", "NICo rejected graceful reboot")
		}
		r.rebootEvent(machine, corev1.EventTypeWarning, "GracefulRebootUnconfirmed", "NICo did not confirm GracefulRestart acceptance")
		return ctrl.Result{RequeueAfter: rebootPollInterval}, true, nil
	}
	recordRebootTriggered(nicoMachine)
	r.rebootEvent(machine, corev1.EventTypeNormal, "GracefulRebootAccepted", "NICo accepted GracefulRestart; waiting for workload Node recovery")
	return ctrl.Result{RequeueAfter: rebootPollInterval}, true, nil
}

func (r *NicoMachineReconciler) dispatchHardReboot(ctx context.Context, machine *clusterv1.Machine, nicoMachine *infrav1.NicoMachine, instanceClient nico.API, instanceID string, state *infrav1.NicoMachineRebootStatus, fallback bool) (ctrl.Result, bool, error) {
	copy := *state
	copy.Phase = rebootPhaseHardDispatched
	copy.Path = rebootModeHard
	copy.Message = "Hard reboot result is uncertain; the action will not be repeated automatically"
	if err := r.persistRebootState(ctx, nicoMachine, &copy); err != nil {
		return ctrl.Result{}, true, fmt.Errorf("record hard reboot dispatch: %w", err)
	}
	if _, err := instanceClient.TriggerInstanceReboot(ctx, instanceID); err != nil {
		if definiteRebootRejection(err) {
			r.rebootEvent(machine, corev1.EventTypeWarning, "HardRebootRejected", "NICo rejected hard reboot")
			return r.completeReboot(ctx, machine, nicoMachine, nicoMachine.Status.Reboot, "none", "NICo rejected hard reboot")
		}
		r.rebootEvent(machine, corev1.EventTypeWarning, "HardRebootUnconfirmed", "NICo did not confirm hard reboot acceptance; the action will not be repeated automatically")
		return ctrl.Result{Requeue: true}, true, nil
	}
	copy.Message = "NICo accepted hard reboot"
	if fallback {
		copy.Message = "NICo accepted hard reboot fallback"
	}
	if err := r.persistRebootState(ctx, nicoMachine, &copy); err != nil {
		return ctrl.Result{}, true, fmt.Errorf("record hard reboot acceptance: %w", err)
	}
	recordRebootTriggered(nicoMachine)
	r.rebootEvent(machine, corev1.EventTypeNormal, "HardRebootAccepted", copy.Message)
	return ctrl.Result{Requeue: true}, true, nil
}

func definiteRebootRejection(err error) bool {
	return errors.Is(err, nico.ErrForbidden) || errors.Is(err, nico.ErrUnauthorized) ||
		errors.Is(err, nico.ErrBadRequest) || errors.Is(err, nico.ErrNotFound)
}

func (r *NicoMachineReconciler) completeReboot(ctx context.Context, machine *clusterv1.Machine, nicoMachine *infrav1.NicoMachine, state *infrav1.NicoMachineRebootStatus, path, message string) (ctrl.Result, bool, error) {
	currentValue := machine.Annotations[state.Annotation]
	replaced := state.AnnotationValue != "" && currentValue != "" && currentValue != state.AnnotationValue
	if currentValue != "" && !replaced {
		before := machine.DeepCopy()
		delete(machine.Annotations, state.Annotation)
		if err := r.Patch(ctx, machine, client.MergeFromWithOptions(before, client.MergeFromWithOptimisticLock{})); err != nil {
			return ctrl.Result{}, true, fmt.Errorf("remove Machine reboot annotation: %w", err)
		}
	}
	copy := *state
	copy.Phase = rebootPhaseCompleted
	copy.Path = path
	copy.Message = message
	completed := metav1.NewTime(time.Now().UTC())
	copy.CompletedAt = &completed
	if err := r.persistRebootState(ctx, nicoMachine, &copy); err != nil {
		return ctrl.Result{}, true, fmt.Errorf("record reboot completion: %w", err)
	}
	return ctrl.Result{Requeue: replaced}, true, nil
}

func (r *NicoMachineReconciler) persistRebootState(ctx context.Context, nicoMachine *infrav1.NicoMachine, state *infrav1.NicoMachineRebootStatus) error {
	// Patch a copy: the API response can contain an older spec while this
	// reconcile still has a pending providerID or metadata update in memory.
	updated := nicoMachine.DeepCopy()
	before := updated.DeepCopy()
	updated.Status.Reboot = state.DeepCopy()
	if err := r.Status().Patch(ctx, updated, client.MergeFromWithOptions(before, client.MergeFromWithOptimisticLock{})); err != nil {
		return err
	}
	nicoMachine.ResourceVersion = updated.ResourceVersion
	nicoMachine.Status.Reboot = state.DeepCopy()
	return nil
}

func (r *NicoMachineReconciler) rebootEvent(machine *clusterv1.Machine, eventType, reason, message string) {
	if r.Recorder != nil {
		r.Recorder.Eventf(machine, nil, eventType, reason, "Reboot", message)
	}
}

func recordRebootTriggered(nicoMachine *infrav1.NicoMachine) {
	if nicoMachine.Annotations == nil {
		nicoMachine.Annotations = map[string]string{}
	}
	nicoMachine.Annotations[lastRebootTriggeredAnnotation] = time.Now().UTC().Format(time.RFC3339)
}

func (r *NicoMachineReconciler) observeRebootNode(ctx context.Context, machine *clusterv1.Machine, cluster *clusterv1.Cluster) (string, bool, error) {
	if !machine.Status.NodeRef.IsDefined() {
		return "", false, nil
	}
	secret := &corev1.Secret{}
	key := client.ObjectKey{Namespace: cluster.Namespace, Name: cluster.Name + "-kubeconfig"}
	if err := r.Get(ctx, key, secret); err != nil {
		if apierrors.IsNotFound(err) {
			return "", false, nil
		}
		return "", false, fmt.Errorf("get workload kubeconfig Secret: %w", err)
	}
	kubeconfig := secret.Data[workloadKubeconfigDataKey]
	if len(kubeconfig) == 0 {
		return "", false, nil
	}
	workloadClient, err := r.workloadClientFactory()(kubeconfig, r.Scheme)
	if err != nil {
		return "", false, fmt.Errorf("build workload client: %w", err)
	}
	node := &corev1.Node{}
	if err := workloadClient.Get(ctx, client.ObjectKey{Name: machine.Status.NodeRef.Name}, node); err != nil {
		if apierrors.IsNotFound(err) {
			return "", false, nil
		}
		return "", false, fmt.Errorf("get workload Node: %w", err)
	}
	ready := false
	for _, condition := range node.Status.Conditions {
		if condition.Type == corev1.NodeReady {
			ready = condition.Status == corev1.ConditionTrue
			break
		}
	}
	return node.Status.NodeInfo.BootID, ready, nil
}
