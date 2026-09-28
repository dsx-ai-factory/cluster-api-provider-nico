// SPDX-FileCopyrightText: Copyright (c) 2026 NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package controllers

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	nicosdk "github.com/NVIDIA/infra-controller/rest-api/sdk/standard"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/events"
	clusterv1 "sigs.k8s.io/cluster-api/api/core/v1beta2"
	"sigs.k8s.io/cluster-api/util/conditions"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	crfake "sigs.k8s.io/controller-runtime/pkg/client/fake"

	infrav1 "github.com/dsx-ai-factory/cluster-api-provider-nico/api/v1alpha1"
	nicofake "github.com/dsx-ai-factory/cluster-api-provider-nico/internal/fake"
	"github.com/dsx-ai-factory/cluster-api-provider-nico/internal/nico"
)

type rebootHarness struct {
	controller  *NicoMachineReconciler
	kube        client.Client
	workload    client.Client
	nico        *nicofake.Server
	events      *events.FakeRecorder
	instance    nico.API
	cluster     *clusterv1.Cluster
	nicoCluster *infrav1.NicoCluster
}

func newRebootHarness(t *testing.T, annotation string) *rebootHarness {
	t.Helper()
	defaultNicoClientCache = nico.NewClientCache()
	server := nicofake.New()
	server.SeedToken("synthetic-token")
	machine := nicosdk.NewMachine()
	machine.SetId("physical-1")
	server.SeedMachine("provider-org", *machine)
	instance := nicosdk.NewInstance()
	instance.SetId("instance-1")
	instance.SetMachineId("physical-1")
	instance.SetStatus(nicosdk.INSTANCESTATUS_READY)
	server.SeedInstance("tenant-org", *instance)
	httpServer := httptest.NewServer(server.Handler())
	t.Cleanup(httpServer.Close)

	scheme := runtime.NewScheme()
	if err := corev1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	if err := clusterv1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	if err := infrav1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	owner := &clusterv1.Machine{ObjectMeta: metav1.ObjectMeta{
		Namespace: "default", Name: "machine-1", Annotations: map[string]string{annotation: "requested"},
		Labels: map[string]string{clusterv1.ClusterNameLabel: "cluster-1"},
	}, Spec: clusterv1.MachineSpec{ClusterName: "cluster-1"}}
	owner.Status.NodeRef.Name = "node-1"
	cluster := &clusterv1.Cluster{ObjectMeta: metav1.ObjectMeta{Namespace: "default", Name: "cluster-1"},
		Spec: clusterv1.ClusterSpec{InfrastructureRef: clusterv1.ContractVersionedObjectReference{
			APIGroup: infrav1.GroupVersion.Group, Kind: "NicoCluster", Name: "nico-cluster",
		}}}
	nicoCluster := &infrav1.NicoCluster{ObjectMeta: metav1.ObjectMeta{Namespace: "default", Name: "nico-cluster"},
		Spec: infrav1.NicoClusterSpec{IdentityRef: corev1.LocalObjectReference{Name: "tenant-identity"}}}
	nicoCluster.Spec.PowerControlIdentityRef.Name = "provider-power"
	nicoMachine := &infrav1.NicoMachine{ObjectMeta: metav1.ObjectMeta{
		Namespace: "default", Name: "nico-machine", Finalizers: []string{nicoMachineFinalizer},
		OwnerReferences: []metav1.OwnerReference{{APIVersion: clusterv1.GroupVersion.String(), Kind: "Machine", Name: owner.Name, UID: owner.UID}},
	}, Spec: infrav1.NicoMachineSpec{ProviderID: "nico://instance-1"}}
	nicoMachine.Status.InstanceID = "instance-1"
	nicoMachine.Status.MachineID = "physical-1"
	credential := func(name, org string) *corev1.Secret {
		return &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Namespace: "default", Name: name}, Data: map[string][]byte{
			"endpoint": []byte(httpServer.URL), "orgID": []byte(org), "token": []byte("synthetic-token"),
		}}
	}
	kube := crfake.NewClientBuilder().WithScheme(scheme).
		WithStatusSubresource(&infrav1.NicoMachine{}, &clusterv1.Machine{}).
		WithObjects(owner, cluster, nicoCluster, nicoMachine,
			credential("tenant-identity", "tenant-org"), credential("provider-power", "provider-org"),
			&corev1.Secret{ObjectMeta: metav1.ObjectMeta{Namespace: "default", Name: "cluster-1-kubeconfig"}, Data: map[string][]byte{"value": []byte("synthetic-kubeconfig")}},
		).Build()
	node := &corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: "node-1"}}
	node.Status.NodeInfo.BootID = "boot-before"
	node.Status.Conditions = []corev1.NodeCondition{{Type: corev1.NodeReady, Status: corev1.ConditionTrue}}
	workload := crfake.NewClientBuilder().WithScheme(scheme).WithStatusSubresource(&corev1.Node{}).WithObjects(node).Build()
	instanceClient, err := nico.NewClient(t.Context(), nico.SecretConfig{Endpoint: httpServer.URL, OrgID: "tenant-org", Token: "synthetic-token"})
	if err != nil {
		t.Fatal(err)
	}
	controller := &NicoMachineReconciler{
		Client: kube, APIReader: kube, Scheme: scheme,
		ProviderConfig:        nico.ProviderConfig{RebootAnnotation: nico.DefaultRebootAnnotation},
		WorkloadClientFactory: fixedWorkloadClientFactory(workload),
	}
	eventRecorder := events.NewFakeRecorder(20)
	controller.Recorder = eventRecorder
	return &rebootHarness{controller: controller, kube: kube, workload: workload, nico: server, events: eventRecorder, instance: instanceClient, cluster: cluster, nicoCluster: nicoCluster}
}

func (h *rebootHarness) step(t *testing.T) *infrav1.NicoMachine {
	return h.stepOnInstance(t, "instance-1")
}

func (h *rebootHarness) stepOnInstance(t *testing.T, instanceID string) *infrav1.NicoMachine {
	t.Helper()
	ctx := t.Context()
	owner := &clusterv1.Machine{}
	if err := h.kube.Get(ctx, client.ObjectKey{Namespace: "default", Name: "machine-1"}, owner); err != nil {
		t.Fatal(err)
	}
	machine := &infrav1.NicoMachine{}
	if err := h.kube.Get(ctx, client.ObjectKey{Namespace: "default", Name: "nico-machine"}, machine); err != nil {
		t.Fatal(err)
	}
	_, handled, err := h.controller.reconcileReboot(ctx, owner, h.cluster, h.nicoCluster, machine, h.instance, instanceID)
	if err != nil {
		t.Fatal(err)
	}
	if !handled {
		t.Fatal("reboot request was not handled")
	}
	if err := h.kube.Get(ctx, client.ObjectKeyFromObject(machine), machine); err != nil {
		t.Fatal(err)
	}
	return machine
}

func (h *rebootHarness) expire(t *testing.T) {
	t.Helper()
	machine := &infrav1.NicoMachine{}
	key := client.ObjectKey{Namespace: "default", Name: "nico-machine"}
	if err := h.kube.Get(t.Context(), key, machine); err != nil {
		t.Fatal(err)
	}
	deadline := metav1.NewTime(time.Now().Add(-time.Minute))
	machine.Status.Reboot.Deadline = &deadline
	if err := h.kube.Status().Update(t.Context(), machine); err != nil {
		t.Fatal(err)
	}
}

func (h *rebootHarness) setBoot(t *testing.T, bootID string, ready bool) {
	t.Helper()
	node := &corev1.Node{}
	if err := h.workload.Get(t.Context(), client.ObjectKey{Name: "node-1"}, node); err != nil {
		t.Fatal(err)
	}
	node.Status.NodeInfo.BootID = bootID
	status := corev1.ConditionFalse
	if ready {
		status = corev1.ConditionTrue
	}
	node.Status.Conditions = []corev1.NodeCondition{{Type: corev1.NodeReady, Status: status}}
	if err := h.workload.Status().Update(t.Context(), node); err != nil {
		t.Fatal(err)
	}
}

func TestRebootGracefulSuccessAndRetry(t *testing.T) {
	h := newRebootHarness(t, nico.DefaultRebootAnnotation)
	if got := h.step(t).Status.Reboot.Phase; got != rebootPhasePrepared {
		t.Fatalf("phase = %q", got)
	}
	h.setBoot(t, "boot-between", true)
	if got := h.step(t).Status.Reboot; got.Phase != rebootPhaseGracefulDispatched || got.BootID != "boot-between" {
		t.Fatalf("reboot baseline = %+v", got)
	}
	h.step(t) // Reconciliation retry must not send another GracefulRestart.
	h.setBoot(t, "boot-after", true)
	if got := h.step(t).Status.Reboot; got.Phase != rebootPhaseCompleted || got.Path != "graceful" {
		t.Fatalf("reboot status = %+v", got)
	}
	if h.nico.PowerControlCalls() != 1 || h.nico.InstanceRebootCount("tenant-org", "instance-1") != 0 {
		t.Fatalf("power calls = %d, hard reboots = %d", h.nico.PowerControlCalls(), h.nico.InstanceRebootCount("tenant-org", "instance-1"))
	}
	owner := &clusterv1.Machine{}
	if err := h.kube.Get(t.Context(), client.ObjectKey{Namespace: "default", Name: "machine-1"}, owner); err != nil {
		t.Fatal(err)
	}
	if owner.Annotations[nico.DefaultRebootAnnotation] != "" {
		t.Fatal("request annotation was not removed")
	}
}

func TestRebootFallbackOnTimeoutAndRejection(t *testing.T) {
	for _, tc := range []struct {
		name   string
		status int
	}{{"timeout", 0}, {"rejected", http.StatusForbidden}} {
		t.Run(tc.name, func(t *testing.T) {
			h := newRebootHarness(t, nico.DefaultRebootAnnotation)
			h.nico.SetPowerControlStatus(tc.status)
			h.step(t)
			state := h.step(t)
			if tc.status == 0 {
				h.expire(t)
				state = h.step(t)
			}
			if got := state.Status.Reboot.Phase; got != rebootPhaseHardDispatched {
				t.Fatalf("phase = %q", got)
			}
			if got := h.step(t).Status.Reboot; got.Phase != rebootPhaseCompleted || got.Path != "hard" {
				t.Fatalf("reboot status = %+v", got)
			}
			if h.nico.PowerControlCalls() != 1 || h.nico.InstanceRebootCount("tenant-org", "instance-1") != 1 {
				t.Fatalf("power calls = %d, hard reboots = %d", h.nico.PowerControlCalls(), h.nico.InstanceRebootCount("tenant-org", "instance-1"))
			}
			foundFallback := false
			for len(h.events.Events) > 0 {
				if strings.Contains(<-h.events.Events, "HardRebootFallback") {
					foundFallback = true
				}
			}
			if !foundFallback {
				t.Fatal("hard fallback event was not emitted")
			}
		})
	}
}

func TestExplicitRebootModes(t *testing.T) {
	t.Run("hard", func(t *testing.T) {
		h := newRebootHarness(t, hardRebootAnnotation)
		h.step(t)
		h.step(t)
		if got := h.step(t).Status.Reboot; got.Phase != rebootPhaseCompleted || got.Path != "hard" {
			t.Fatalf("reboot status = %+v", got)
		}
		if h.nico.PowerControlCalls() != 0 || h.nico.InstanceRebootCount("tenant-org", "instance-1") != 1 {
			t.Fatal("hard mode did not use the instance API exactly once")
		}
	})
	t.Run("hard rejection", func(t *testing.T) {
		h := newRebootHarness(t, hardRebootAnnotation)
		h.step(t)
		if got := h.stepOnInstance(t, "missing-instance").Status.Reboot; got.Phase != rebootPhaseCompleted || got.Path != "none" {
			t.Fatalf("reboot status = %+v", got)
		}
	})
	t.Run("soft-only timeout", func(t *testing.T) {
		h := newRebootHarness(t, softRebootAnnotation)
		h.step(t)
		h.step(t)
		h.expire(t)
		if got := h.step(t).Status.Reboot; got.Phase != rebootPhaseCompleted || got.Path != "graceful" {
			t.Fatalf("reboot status = %+v", got)
		}
		if h.nico.InstanceRebootCount("tenant-org", "instance-1") != 0 {
			t.Fatal("graceful-only request sent a hard fallback")
		}
	})
	t.Run("soft-only rejection", func(t *testing.T) {
		h := newRebootHarness(t, softRebootAnnotation)
		h.nico.SetPowerControlStatus(http.StatusForbidden)
		h.step(t)
		if got := h.step(t).Status.Reboot; got.Phase != rebootPhaseCompleted || got.Path != "none" {
			t.Fatalf("reboot status = %+v", got)
		}
		if h.nico.InstanceRebootCount("tenant-org", "instance-1") != 0 {
			t.Fatal("graceful-only rejection sent a hard fallback")
		}
	})
}

func TestRebootBootObservedDoesNotHardFallback(t *testing.T) {
	h := newRebootHarness(t, nico.DefaultRebootAnnotation)
	h.step(t)
	h.step(t)
	h.expire(t)
	h.setBoot(t, "boot-after", false)
	if got := h.step(t).Status.Reboot.Phase; got != rebootPhaseCompleted {
		t.Fatalf("phase = %q", got)
	}
	if h.nico.InstanceRebootCount("tenant-org", "instance-1") != 0 {
		t.Fatal("hard fallback interrupted observed host recovery")
	}
}

func TestRebootPreparedDispatchIsNotRepeated(t *testing.T) {
	h := newRebootHarness(t, nico.DefaultRebootAnnotation)
	h.step(t)
	machine := &infrav1.NicoMachine{}
	key := client.ObjectKey{Namespace: "default", Name: "nico-machine"}
	if err := h.kube.Get(t.Context(), key, machine); err != nil {
		t.Fatal(err)
	}
	machine.Status.Reboot.Phase = rebootPhaseGracefulDispatched
	deadline := metav1.NewTime(time.Now().Add(rebootRecoveryTimeout))
	machine.Status.Reboot.Deadline = &deadline
	if err := h.kube.Status().Update(t.Context(), machine); err != nil {
		t.Fatal(err)
	}
	h.step(t)
	if h.nico.PowerControlCalls() != 0 {
		t.Fatal("uncertain graceful action was repeated")
	}
}

func TestRebootUnknownGracefulResultWaitsBeforeFallback(t *testing.T) {
	h := newRebootHarness(t, nico.DefaultRebootAnnotation)
	h.nico.SetPowerControlStatus(http.StatusInternalServerError)
	h.step(t)
	h.step(t)
	if got := h.step(t).Status.Reboot.Phase; got != rebootPhaseGracefulDispatched {
		t.Fatalf("phase before deadline = %q", got)
	}
	if h.nico.InstanceRebootCount("tenant-org", "instance-1") != 0 {
		t.Fatal("hard fallback occurred before deadline after an uncertain result")
	}
	h.expire(t)
	h.step(t)
	if h.nico.PowerControlCalls() != 1 || h.nico.InstanceRebootCount("tenant-org", "instance-1") != 1 {
		t.Fatal("uncertain graceful result was repeated or hard fallback was missed")
	}
}

func TestRebootMissingProviderCredentialFallsBack(t *testing.T) {
	h := newRebootHarness(t, nico.DefaultRebootAnnotation)
	h.nicoCluster.Spec.PowerControlIdentityRef.Name = "missing-provider-power"
	h.step(t)
	if got := h.step(t).Status.Reboot.Phase; got != rebootPhaseHardDispatched {
		t.Fatalf("phase = %q", got)
	}
	if h.nico.PowerControlCalls() != 0 || h.nico.InstanceRebootCount("tenant-org", "instance-1") != 1 {
		t.Fatal("missing provider credential did not use the tenant hard fallback")
	}
}

func TestRebootConflictingAnnotationsAndReapplication(t *testing.T) {
	h := newRebootHarness(t, hardRebootAnnotation)
	h.step(t)
	owner := &clusterv1.Machine{}
	key := client.ObjectKey{Namespace: "default", Name: "machine-1"}
	if err := h.kube.Get(t.Context(), key, owner); err != nil {
		t.Fatal(err)
	}
	owner.Annotations[softRebootAnnotation] = "also requested"
	if err := h.kube.Update(t.Context(), owner); err != nil {
		t.Fatal(err)
	}
	h.step(t)
	if h.nico.PowerControlCalls() != 0 || h.nico.InstanceRebootCount("tenant-org", "instance-1") != 0 {
		t.Fatal("conflicting reboot annotations caused an action")
	}
	if err := h.kube.Get(t.Context(), key, owner); err != nil {
		t.Fatal(err)
	}
	delete(owner.Annotations, softRebootAnnotation)
	if err := h.kube.Update(t.Context(), owner); err != nil {
		t.Fatal(err)
	}
	h.step(t)
	h.step(t)
	if err := h.kube.Get(t.Context(), key, owner); err != nil {
		t.Fatal(err)
	}
	if owner.Annotations == nil {
		owner.Annotations = map[string]string{}
	}
	owner.Annotations[hardRebootAnnotation] = "requested"
	if err := h.kube.Update(t.Context(), owner); err != nil {
		t.Fatal(err)
	}
	h.step(t)
	h.step(t)
	h.step(t)
	if got := h.nico.InstanceRebootCount("tenant-org", "instance-1"); got != 2 {
		t.Fatalf("hard reboot calls after a new application = %d, want 2", got)
	}
}

func TestRebootRemovedBeforeDispatch(t *testing.T) {
	h := newRebootHarness(t, hardRebootAnnotation)
	h.step(t)
	owner := &clusterv1.Machine{}
	key := client.ObjectKey{Namespace: "default", Name: "machine-1"}
	if err := h.kube.Get(t.Context(), key, owner); err != nil {
		t.Fatal(err)
	}
	delete(owner.Annotations, hardRebootAnnotation)
	if err := h.kube.Update(t.Context(), owner); err != nil {
		t.Fatal(err)
	}
	if got := h.step(t).Status.Reboot; got.Phase != rebootPhaseCompleted || got.Path != "none" {
		t.Fatalf("cancelled reboot status = %+v", got)
	}
	if h.nico.InstanceRebootCount("tenant-org", "instance-1") != 0 {
		t.Fatal("reboot sent after annotation removal")
	}
}

func TestRebootThroughFullReconcile(t *testing.T) {
	h := newRebootHarness(t, nico.DefaultRebootAnnotation)
	key := client.ObjectKey{Namespace: "default", Name: "nico-machine"}
	for range 2 {
		if _, err := h.controller.Reconcile(t.Context(), ctrl.Request{NamespacedName: key}); err != nil {
			t.Fatalf("reconcile graceful dispatch: %v", err)
		}
	}
	if got := h.nico.PowerControlCalls(); got != 1 {
		t.Fatalf("graceful power calls = %d, want 1", got)
	}
	h.setBoot(t, "boot-after", true)
	if _, err := h.controller.Reconcile(t.Context(), ctrl.Request{NamespacedName: key}); err != nil {
		t.Fatalf("reconcile graceful recovery: %v", err)
	}
	machine := &infrav1.NicoMachine{}
	if err := h.kube.Get(t.Context(), key, machine); err != nil {
		t.Fatal(err)
	}
	if got := machine.Status.Reboot; got.Phase != rebootPhaseCompleted || got.Path != rebootPathGraceful {
		t.Fatalf("reboot status = %+v", got)
	}
	if paused := conditions.Get(machine, clusterv1.PausedCondition); paused == nil || paused.Status != metav1.ConditionFalse {
		t.Fatalf("paused condition after reboot = %+v, all conditions = %+v", paused, machine.Status.Conditions)
	}
	if machine.Annotations[lastRebootTriggeredAnnotation] == "" {
		t.Fatal("last reboot triggered annotation was not preserved")
	}
}

func TestHardRebootThroughFullReconcile(t *testing.T) {
	h := newRebootHarness(t, hardRebootAnnotation)
	key := client.ObjectKey{Namespace: "default", Name: "nico-machine"}
	for range 3 {
		if _, err := h.controller.Reconcile(t.Context(), ctrl.Request{NamespacedName: key}); err != nil {
			t.Fatalf("reconcile hard reboot: %v", err)
		}
	}
	machine := &infrav1.NicoMachine{}
	if err := h.kube.Get(t.Context(), key, machine); err != nil {
		t.Fatal(err)
	}
	if machine.Status.Reboot.Phase != rebootPhaseCompleted || machine.Annotations[lastRebootTriggeredAnnotation] == "" {
		t.Fatalf("hard reboot status or timestamp missing: %+v, %v", machine.Status.Reboot, machine.Annotations)
	}
}
