// SPDX-FileCopyrightText: Copyright (c) 2026 NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package controllers

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/onsi/ginkgo/v2"
	"github.com/onsi/gomega"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	clusterv1 "sigs.k8s.io/cluster-api/api/core/v1beta2"
	"sigs.k8s.io/cluster-api/util/conditions"
	"sigs.k8s.io/controller-runtime/pkg/client"
	crfake "sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	infrav1 "github.com/NVIDIA/cluster-api-provider-nico/api/v1alpha1"
	"github.com/NVIDIA/cluster-api-provider-nico/internal/fake"
	"github.com/NVIDIA/cluster-api-provider-nico/internal/nico"
	"github.com/NVIDIA/cluster-api-provider-nico/internal/test/fixtures"
)

const (
	timeout     = 60 * time.Second
	testMachine = "nicomachine-1"
)

func nicoMachineCaseSet(description, dirPrefix string, defineSteps func(*fixtures.Case, fixtures.CaseSet)) fixtures.CaseSet {
	return fixtures.CaseSet{
		Description:          description,
		DirPrefix:            dirPrefix,
		MaskExpectedMetadata: true,
		SchemeFn:             newScheme,
		EnvironmentFn:        newEnvironment,
		CompareObjects: func() []client.ObjectList {
			return []client.ObjectList{
				&infrav1.NicoClusterList{},
				&infrav1.NicoMachineList{},
			}
		},
		Setup: func(ctx ginkgo.SpecContext, tc *fixtures.Case, _ fixtures.CaseSet) {
			tc.Client = client.WithFieldOwner(tc.Client, "capnico-envtest")
			gomega.Expect(tc.CreateObjects(ctx)).To(gomega.Succeed())
			gomega.Expect(wireOwnerReferences(ctx, tc.Client, tc.Scheme)).To(gomega.Succeed())

			server := fake.New()
			caseFakes.Store(tc.Name, server)
			gomega.Expect(seedFakeResources(tc, server)).To(gomega.Succeed())
			tc.AddGolden("expected_nico.yaml", func(context.Context) (string, error) {
				return server.Dump()
			})

			endpoint := startFake(server)
			gomega.Expect(pointIdentitySecretAtFake(ctx, tc.Client, endpoint)).To(gomega.Succeed())
			startReconcilers(ctx, tc)
		},
		DefineSteps: defineSteps,
	}
}

// IMPORTANT: Read docs/writing-tests.md. There is ZERO reason that you should
// have to add or update a case set.
// Represents a provisioned CR at generation 2 after CAPNICo sets providerID.
var _ = fixtures.DescribeCaseSet(nicoMachineCaseSet(
	"NicoMachine create reconciliation ending provisioned",
	"nicomachine-create-provisioned-",
	func(tc *fixtures.Case, _ fixtures.CaseSet) {
		ginkgo.It("reconciles the initial NicoMachine", func(ctx ginkgo.SpecContext) {
			gomega.Eventually(func(g gomega.Gomega) {
				nicoMachine := &infrav1.NicoMachine{}
				g.Expect(tc.Client.Get(ctx, client.ObjectKey{Namespace: testNamespace, Name: testMachine}, nicoMachine)).To(gomega.Succeed())
				provisioned := conditions.Get(nicoMachine, infrav1.MachineProvisionedCondition)
				g.Expect(provisioned).NotTo(gomega.BeNil())
				g.Expect(provisioned.Status).NotTo(gomega.Equal(metav1.ConditionUnknown))
				g.Expect(nicoMachine.Generation).To(gomega.BeNumerically(">", 1))
				g.Expect(provisioned.ObservedGeneration).To(gomega.Equal(nicoMachine.Generation))
			}).WithTimeout(timeout).WithPolling(time.Second).Should(gomega.Succeed())
		})
	},
))

// IMPORTANT: Read docs/writing-tests.md. There is ZERO reason that you should
// have to add or update a case set.
// Represents an unprovisioned CR at generation 1 with no providerID.
var _ = fixtures.DescribeCaseSet(nicoMachineCaseSet(
	"NicoMachine create reconciliation ending not provisioned",
	"nicomachine-create-not-provisioned-",
	func(tc *fixtures.Case, _ fixtures.CaseSet) {
		ginkgo.It("reconciles the initial NicoMachine", func(ctx ginkgo.SpecContext) {
			gomega.Eventually(func(g gomega.Gomega) {
				nicoMachine := &infrav1.NicoMachine{}
				g.Expect(tc.Client.Get(ctx, client.ObjectKey{Namespace: testNamespace, Name: testMachine}, nicoMachine)).To(gomega.Succeed())
				provisioned := conditions.Get(nicoMachine, infrav1.MachineProvisionedCondition)
				g.Expect(provisioned).NotTo(gomega.BeNil())
				g.Expect(provisioned.Status).To(gomega.Equal(metav1.ConditionFalse))
				g.Expect(nicoMachine.Generation).To(gomega.Equal(int64(1)))
				g.Expect(provisioned.ObservedGeneration).To(gomega.Equal(nicoMachine.Generation))
			}).WithTimeout(timeout).WithPolling(time.Second).Should(gomega.Succeed())
		})
	},
))

// IMPORTANT: Read docs/writing-tests.md. There is ZERO reason that you should
// have to add or update a case set.
// Represents a provisioned CR at generation 2 after a spec update.
var _ = fixtures.DescribeCaseSet(nicoMachineCaseSet(
	"NicoMachine update reconciliation ending provisioned",
	"nicomachine-update-provisioned-",
	func(tc *fixtures.Case, _ fixtures.CaseSet) {
		ginkgo.It("reconciles the initial NicoMachine", func(ctx ginkgo.SpecContext) {
			gomega.Eventually(func(g gomega.Gomega) {
				nicoMachine := &infrav1.NicoMachine{}
				g.Expect(tc.Client.Get(ctx, client.ObjectKey{Namespace: testNamespace, Name: testMachine}, nicoMachine)).To(gomega.Succeed())
				provisioned := conditions.Get(nicoMachine, infrav1.MachineProvisionedCondition)
				g.Expect(provisioned).NotTo(gomega.BeNil())
				g.Expect(provisioned.Status).NotTo(gomega.Equal(metav1.ConditionUnknown))
				g.Expect(provisioned.ObservedGeneration).To(gomega.Equal(nicoMachine.Generation))
			}).WithTimeout(timeout).WithPolling(time.Second).Should(gomega.Succeed())
		})

		ginkgo.It("applies the NicoMachine update", func(ctx ginkgo.SpecContext) {
			gomega.Expect(tc.PatchObjects(ctx, "input_update.yaml")).To(gomega.Succeed())
		})

		ginkgo.It("reconciles the updated NicoMachine", func(ctx ginkgo.SpecContext) {
			gomega.Eventually(func(g gomega.Gomega) {
				nicoMachine := &infrav1.NicoMachine{}
				g.Expect(tc.Client.Get(ctx, client.ObjectKey{Namespace: testNamespace, Name: testMachine}, nicoMachine)).To(gomega.Succeed())

				provisioned := conditions.Get(nicoMachine, infrav1.MachineProvisionedCondition)
				g.Expect(provisioned).NotTo(gomega.BeNil())
				g.Expect(provisioned.Status).To(gomega.Equal(metav1.ConditionTrue))

				g.Expect(nicoMachine.Generation).To(gomega.BeNumerically(">", 2))
				g.Expect(provisioned.ObservedGeneration).To(gomega.Equal(nicoMachine.Generation))
			}).WithTimeout(timeout).WithPolling(time.Second).Should(gomega.Succeed())
		})
	},
))

// IMPORTANT: Read docs/writing-tests.md. There is ZERO reason that you should
// have to add or update a case set.
var _ = fixtures.DescribeCaseSet(nicoMachineCaseSet(
	"NicoMachine delete reconciliation",
	"nicomachine-delete-",
	func(tc *fixtures.Case, _ fixtures.CaseSet) {
		var deletedObjects []client.Object

		ginkgo.It("reconciles the initial NicoMachine", func(ctx ginkgo.SpecContext) {
			gomega.Eventually(func(g gomega.Gomega) {
				nicoMachine := &infrav1.NicoMachine{}
				g.Expect(tc.Client.Get(ctx, client.ObjectKey{Namespace: testNamespace, Name: testMachine}, nicoMachine)).To(gomega.Succeed())
				provisioned := conditions.Get(nicoMachine, infrav1.MachineProvisionedCondition)
				g.Expect(provisioned).NotTo(gomega.BeNil())
				g.Expect(provisioned.Status).NotTo(gomega.Equal(metav1.ConditionUnknown))
				g.Expect(provisioned.ObservedGeneration).To(gomega.Equal(nicoMachine.Generation))
			}).WithTimeout(timeout).WithPolling(time.Second).Should(gomega.Succeed())
		})

		ginkgo.It("deletes the selected objects", func(ctx ginkgo.SpecContext) {
			var err error
			deletedObjects, err = tc.DeleteObjects(ctx, "input_delete.yaml")
			gomega.Expect(err).NotTo(gomega.HaveOccurred())
		})

		ginkgo.It("reconciles the object deletion", func(ctx ginkgo.SpecContext) {
			gomega.Eventually(func(g gomega.Gomega) {
				for _, object := range deletedObjects {
					actual := object.DeepCopyObject().(client.Object)
					err := tc.Client.Get(ctx, client.ObjectKeyFromObject(object), actual)
					g.Expect(apierrors.IsNotFound(err)).To(gomega.BeTrue())
				}
			}).WithTimeout(timeout).WithPolling(time.Second).Should(gomega.Succeed())
		})
	},
))

// freshControlPlane is a control-plane NicoMachine that has not reconciled yet:
// it carries no conditions at all. Under the capacityWaitReasons allowlist it
// does not contend, so it never holds a worker back.
func freshControlPlane(namespace, name, instanceTypeID string) infrav1.NicoMachine {
	return infrav1.NicoMachine{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: namespace,
			Name:      name,
			UID:       types.UID(name + "-uid"),
			Labels:    map[string]string{clusterv1.MachineControlPlaneLabel: ""},
		},
		Spec: infrav1.NicoMachineSpec{InstanceTypeID: instanceTypeID},
	}
}

// controlPlaneWaiter is a control-plane NicoMachine demonstrably blocked on
// instance-type capacity, which is the only state that now contends for a free
// allocation. Tests that need a machine stuck for some other reason wrap this in
// withReadyFalse, which replaces the condition.
func controlPlaneWaiter(namespace, name, instanceTypeID string) infrav1.NicoMachine {
	return withProvisionedFalse(
		freshControlPlane(namespace, name, instanceTypeID),
		infrav1.InstanceTypeUnavailableReason)
}

// workerMachine returns an unclaimed worker NicoMachine wanting instanceTypeID.
func workerMachine(namespace, name, instanceTypeID string) infrav1.NicoMachine {
	return infrav1.NicoMachine{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: namespace,
			Name:      name,
			UID:       types.UID(name + "-uid"),
		},
		Spec: infrav1.NicoMachineSpec{InstanceTypeID: instanceTypeID},
	}
}

// withProvisionedFalse stamps a MachineProvisioned=False condition carrying
// reason onto machine, the way Reconcile's early-return paths do.
//
// MachineProvisioned, not Ready: since the v1beta2 status alignment (#100) Ready
// is an aggregate whose reason is the summary NotReadyReason, so the specific
// capacity reason isWaitingForCapacity matches on only lives here.
func withProvisionedFalse(machine infrav1.NicoMachine, reason string) infrav1.NicoMachine {
	machine.Status.Conditions = []metav1.Condition{{
		Type:               infrav1.MachineProvisionedCondition,
		Status:             metav1.ConditionFalse,
		Reason:             reason,
		Message:            "set by test",
		LastTransitionTime: metav1.Now(),
	}}
	return machine
}

// withPaused stamps the CAPI Paused condition on machine with the given status,
// mirroring what paused.EnsurePausedCondition patches onto the object.
func withPaused(machine infrav1.NicoMachine, status metav1.ConditionStatus) infrav1.NicoMachine {
	reason := clusterv1.PausedReason
	if status != metav1.ConditionTrue {
		reason = clusterv1.NotPausedReason
	}
	machine.Status.Conditions = append(machine.Status.Conditions, metav1.Condition{
		Type:               clusterv1.PausedCondition,
		Status:             status,
		Reason:             reason,
		Message:            "set by test",
		LastTransitionTime: metav1.Now(),
	})
	return machine
}

// readyCondition returns the Ready condition on machine, or nil when unset.
// provisionedCondition reads MachineProvisioned, which is what a deferral
// writes. Ready and Status.Ready are derived from it by setNicoMachineConditions
// later in the reconcile, so they are not observable from a direct unit call.
func provisionedCondition(machine *infrav1.NicoMachine) *metav1.Condition {
	for i := range machine.Status.Conditions {
		if machine.Status.Conditions[i].Type == infrav1.MachineProvisionedCondition {
			return &machine.Status.Conditions[i]
		}
	}
	return nil
}

// fakeMachineClient builds a fake client seeded with objs over the infra scheme.
func fakeMachineClient(t *testing.T, objs ...runtime.Object) client.Client {
	t.Helper()
	scheme := runtime.NewScheme()
	require.NoError(t, infrav1.AddToScheme(scheme))
	return crfake.NewClientBuilder().WithScheme(scheme).WithRuntimeObjects(objs...).Build()
}

func TestCountControlPlaneWaitingForInstanceType(t *testing.T) {
	const wantedType = "instance-type-a"
	const otherType = "instance-type-b"

	claimed := controlPlaneWaiter("ns-a", "cp-claimed", wantedType)
	claimed.Status.InstanceID = "instance-9"

	importing := controlPlaneWaiter("ns-a", "cp-importing", wantedType)
	importing.Spec.ProviderID = nico.ProviderID("instance-8")

	// The fake client rejects an object carrying a deletionTimestamp unless it
	// also has a finalizer, so set both.
	deleting := controlPlaneWaiter("ns-a", "cp-deleting", wantedType)
	deleting.DeletionTimestamp = new(metav1.Now())
	deleting.Finalizers = []string{nicoMachineFinalizer}

	// wedgedCP is a control-plane waiter parked on a non-capacity Ready reason.
	wedgedCP := func(reason string) infrav1.NicoMachine {
		return withProvisionedFalse(controlPlaneWaiter("ns-a", "cp-"+reason, wantedType), reason)
	}

	type parameters struct {
		existing   []infrav1.NicoMachine
		self       infrav1.NicoMachine
		wantCount  int
		wantSample string
	}

	tests := map[string]parameters{
		"no control-plane machines waiting": {
			existing:  []infrav1.NicoMachine{workerMachine("ns-a", "worker-other", wantedType)},
			self:      workerMachine("ns-a", "worker", wantedType),
			wantCount: 0,
		},
		"control-plane machine in another namespace still counts": {
			existing:   []infrav1.NicoMachine{controlPlaneWaiter("ns-b", "cp", wantedType)},
			self:       workerMachine("ns-a", "worker", wantedType),
			wantCount:  1,
			wantSample: "ns-b/cp",
		},
		"multiple control-plane machines are tallied": {
			existing: []infrav1.NicoMachine{
				controlPlaneWaiter("ns-a", "cp-1", wantedType),
				controlPlaneWaiter("ns-b", "cp-2", wantedType),
			},
			self:      workerMachine("ns-a", "worker", wantedType),
			wantCount: 2,
		},
		"different instance type does not count": {
			existing:  []infrav1.NicoMachine{controlPlaneWaiter("ns-a", "cp", otherType)},
			self:      workerMachine("ns-a", "worker", wantedType),
			wantCount: 0,
		},
		"control-plane machine that already claimed an instance does not count": {
			existing:  []infrav1.NicoMachine{claimed},
			self:      workerMachine("ns-a", "worker", wantedType),
			wantCount: 0,
		},
		"control-plane machine importing an instance does not count": {
			existing:  []infrav1.NicoMachine{importing},
			self:      workerMachine("ns-a", "worker", wantedType),
			wantCount: 0,
		},
		"control-plane machine under deletion does not count": {
			existing:  []infrav1.NicoMachine{deleting},
			self:      workerMachine("ns-a", "worker", wantedType),
			wantCount: 0,
		},
		"a control-plane machine never counts itself": {
			existing:  []infrav1.NicoMachine{controlPlaneWaiter("ns-a", "cp", wantedType)},
			self:      controlPlaneWaiter("ns-a", "cp", wantedType),
			wantCount: 0,
		},
		"a same-named control-plane machine in another namespace still counts": {
			existing:   []infrav1.NicoMachine{controlPlaneWaiter("ns-b", "cp", wantedType)},
			self:       workerMachine("ns-c", "cp", wantedType),
			wantCount:  1,
			wantSample: "ns-b/cp",
		},
		"a worker wanting a different instance type sees no waiters": {
			existing:  []infrav1.NicoMachine{controlPlaneWaiter("ns-a", "cp", wantedType)},
			self:      workerMachine("ns-a", "worker", otherType),
			wantCount: 0,
		},
		// A control-plane machine wedged on something capacity cannot fix must not
		// contend, or it stalls every worker of its instance type indefinitely.
		"a control-plane machine waiting for bootstrap data does not count": {
			existing:  []infrav1.NicoMachine{wedgedCP(infrav1.WaitingForBootstrapDataReason)},
			self:      workerMachine("ns-a", "worker", wantedType),
			wantCount: 0,
		},
		"a control-plane machine waiting for its identity secret does not count": {
			existing:  []infrav1.NicoMachine{wedgedCP(infrav1.WaitingForIdentitySecretReason)},
			self:      workerMachine("ns-a", "worker", wantedType),
			wantCount: 0,
		},
		"a control-plane machine with an invalid create request does not count": {
			existing:  []infrav1.NicoMachine{wedgedCP(infrav1.InstanceCreateRequestInvalidReason)},
			self:      workerMachine("ns-a", "worker", wantedType),
			wantCount: 0,
		},
		"a control-plane machine with invalid bootstrap data does not count": {
			existing:  []infrav1.NicoMachine{wedgedCP(infrav1.BootstrapDataInvalidReason)},
			self:      workerMachine("ns-a", "worker", wantedType),
			wantCount: 0,
		},
		// A paused control-plane machine cannot claim an instance for as long as the
		// operator holds the pause, and the tally is cluster-wide, so counting it
		// would let a pause in one cluster deadlock worker scale-out in another.
		"a paused control-plane machine does not count": {
			existing: []infrav1.NicoMachine{
				withPaused(controlPlaneWaiter("ns-a", "cp-paused", wantedType), metav1.ConditionTrue),
			},
			self:      workerMachine("ns-a", "worker", wantedType),
			wantCount: 0,
		},
		"a paused control-plane machine does not count even with an unrelated Ready reason": {
			existing: []infrav1.NicoMachine{
				withPaused(
					withProvisionedFalse(
						controlPlaneWaiter("ns-a", "cp-paused-scarce", wantedType),
						infrav1.InstanceTypeUnavailableReason),
					metav1.ConditionTrue),
			},
			self:      workerMachine("ns-a", "worker", wantedType),
			wantCount: 0,
		},
		// A resumed cluster must contend again, so only Paused=True is a wedge.
		"a control-plane machine with Paused=False still counts": {
			existing: []infrav1.NicoMachine{
				withPaused(controlPlaneWaiter("ns-a", "cp-resumed", wantedType), metav1.ConditionFalse),
			},
			self:       workerMachine("ns-a", "worker", wantedType),
			wantCount:  1,
			wantSample: "ns-a/cp-resumed",
		},
		// The tally is an allowlist of capacity reasons, so a control-plane machine
		// that has not reconciled yet does not contend. It starts contending as
		// soon as its own availability preflight stamps InstanceTypeUnavailable.
		// This is the deliberate trade for not having to enumerate every
		// non-capacity failure mode, now that a single waiter blocks workers
		// outright.
		"a control-plane machine with no conditions yet does not count": {
			existing:  []infrav1.NicoMachine{freshControlPlane("ns-a", "cp-fresh", wantedType)},
			self:      workerMachine("ns-a", "worker", wantedType),
			wantCount: 0,
		},
		"a control-plane machine blocked on capacity still counts": {
			existing: []infrav1.NicoMachine{
				withProvisionedFalse(
					controlPlaneWaiter("ns-a", "cp-scarce", wantedType),
					infrav1.InstanceTypeUnavailableReason),
			},
			self:       workerMachine("ns-a", "worker", wantedType),
			wantCount:  1,
			wantSample: "ns-a/cp-scarce",
		},
		"a control-plane machine failing its create still counts": {
			existing: []infrav1.NicoMachine{
				withProvisionedFalse(
					controlPlaneWaiter("ns-a", "cp-failing", wantedType),
					infrav1.InstanceCreateFailedReason),
			},
			self:       workerMachine("ns-a", "worker", wantedType),
			wantCount:  1,
			wantSample: "ns-a/cp-failing",
		},
	}

	for name, params := range tests {
		t.Run(name, func(t *testing.T) {
			objects := make([]runtime.Object, 0, len(params.existing))
			for i := range params.existing {
				objects = append(objects, &params.existing[i])
			}
			c := fakeMachineClient(t, objects...)

			got, sample, err := countControlPlaneWaitingForInstanceType(
				context.Background(), c, params.self.Spec.InstanceTypeID, &params.self)

			require.NoError(t, err)
			assert.Equal(t, params.wantCount, got)
			if params.wantSample != "" {
				assert.Equal(t, params.wantSample, sample)
			}
		})
	}
}

func TestIsControlPlaneNicoMachine(t *testing.T) {
	cp := controlPlaneWaiter("ns-a", "cp", "instance-type-a")
	worker := workerMachine("ns-a", "worker", "instance-type-a")

	assert.True(t, isControlPlaneNicoMachine(&cp))
	assert.False(t, isControlPlaneNicoMachine(&worker))
}

// TestShouldDeferForControlPlane covers the gate that decides whether a worker
// instance create waits for a control-plane machine. It is deliberately
// unconditional: headroom is not consulted, because NICo's unusedUsable does not
// drop until a create lands and two independent reconciles would otherwise both
// read the same free allocation and both take it.
func TestShouldDeferForControlPlane(t *testing.T) {
	tests := map[string]struct {
		controlPlaneWaiting int
		want                bool
	}{
		"no control-plane waiting never defers": {controlPlaneWaiting: 0, want: false},
		"a single waiter defers":                {controlPlaneWaiting: 1, want: true},
		"several waiters defer":                 {controlPlaneWaiting: 5, want: true},
		"a negative tally is treated as none":   {controlPlaneWaiting: -1, want: false},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			assert.Equal(t, tc.want, shouldDeferForControlPlane(tc.controlPlaneWaiting))
		})
	}
}

// TestDeferForControlPlanePriority exercises the gate through the reconciler
// method rather than the pure predicate, so an inverted decision, a missing
// condition or a swallowed lookup error is caught.
//
// There is deliberately no envtest case for the deferral. The gate only engages
// when a control-plane NicoMachine is already settled into a waiting-on-capacity
// state, which means seeding its MachineProvisioned condition -- and the fixture
// framework creates objects with Client.Create, which drops status. Expressing
// it would need either a new case set (docs/writing-tests.md forbids one for a
// provider-specific sub-flow) or a status-seeding hook in
// internal/test/fixtures, which is a wider change than this feature warrants.
// The tests here and in TestCountControlPlaneWaitingForInstanceType cover the
// decision; the create-vs-defer transition belongs in E2E.
func TestDeferForControlPlanePriority(t *testing.T) {
	const wantedType = "instance-type-a"

	t.Run("worker defers when there is no headroom", func(t *testing.T) {
		cp := controlPlaneWaiter("ns-cp", "cp-1", wantedType)
		worker := workerMachine("ns-w", "worker-1", wantedType)
		reconciler := &NicoMachineReconciler{Client: fakeMachineClient(t, &cp)}
		deferred, err := reconciler.deferForControlPlanePriority(context.Background(), &worker, 1)

		require.NoError(t, err)
		assert.True(t, deferred, "worker must defer while a control-plane machine waits on the same type")
		condition := provisionedCondition(&worker)
		require.NotNil(t, condition, "MachineProvisioned condition must be set on deferral")
		assert.Equal(t, metav1.ConditionFalse, condition.Status)
		assert.Equal(t, infrav1.ControlPlanePriorityDeferredReason, condition.Reason)
		assert.Contains(t, condition.Message, "ns-cp/cp-1")
	})

	// The headroom rule this replaces would have let both workers through here.
	// unusedUsable is a NICo reading that does not drop until a create lands, so
	// with 50 reported free and one control-plane machine waiting, every worker
	// reconciling in that window saw headroom and proceeded.
	t.Run("worker defers even when NICo reports ample headroom", func(t *testing.T) {
		cp := controlPlaneWaiter("ns-cp", "cp-1", wantedType)
		worker := workerMachine("ns-w", "worker-1", wantedType)

		reconciler := &NicoMachineReconciler{Client: fakeMachineClient(t, &cp)}
		deferred, err := reconciler.deferForControlPlanePriority(context.Background(), &worker, 50)

		require.NoError(t, err)
		assert.True(t, deferred, "a waiting control-plane machine defers workers regardless of reported headroom")

		condition := provisionedCondition(&worker)
		require.NotNil(t, condition)
		assert.Equal(t, infrav1.ControlPlanePriorityDeferredReason, condition.Reason)
	})

	t.Run("a control-plane machine never defers to another", func(t *testing.T) {
		other := controlPlaneWaiter("ns-cp", "cp-2", wantedType)
		self := freshControlPlane("ns-cp", "cp-1", wantedType)

		reconciler := &NicoMachineReconciler{Client: fakeMachineClient(t, &other)}
		deferred, err := reconciler.deferForControlPlanePriority(context.Background(), &self, 1)

		require.NoError(t, err)
		assert.False(t, deferred)
		assert.Nil(t, provisionedCondition(&self))
	})

	t.Run("worker proceeds when no control-plane machine is waiting", func(t *testing.T) {
		otherWorker := workerMachine("ns-w", "worker-2", wantedType)
		worker := workerMachine("ns-w", "worker-1", wantedType)

		reconciler := &NicoMachineReconciler{Client: fakeMachineClient(t, &otherWorker)}
		deferred, err := reconciler.deferForControlPlanePriority(context.Background(), &worker, 1)

		require.NoError(t, err)
		assert.False(t, deferred)
		assert.Nil(t, provisionedCondition(&worker))
	})

	// Wedge bound: a control-plane machine parked on a non-capacity
	// reason can never consume the allocation, so it must not hold the worker.
	t.Run("worker proceeds when the control-plane machine is wedged", func(t *testing.T) {
		wedged := withProvisionedFalse(
			controlPlaneWaiter("ns-cp", "cp-wedged", wantedType),
			infrav1.WaitingForBootstrapDataReason)
		worker := workerMachine("ns-w", "worker-1", wantedType)

		reconciler := &NicoMachineReconciler{Client: fakeMachineClient(t, &wedged)}
		deferred, err := reconciler.deferForControlPlanePriority(context.Background(), &worker, 1)

		require.NoError(t, err)
		assert.False(t, deferred, "a wedged control-plane machine must not stall workers")
		assert.Nil(t, provisionedCondition(&worker))
	})

	// A pause is operator-held, so a paused control-plane machine must not hold a
	// worker -- in this cluster or, since the tally is cluster-wide, any other.
	t.Run("worker proceeds when the only control-plane machine is paused", func(t *testing.T) {
		paused := withPaused(controlPlaneWaiter("ns-cp", "cp-paused", wantedType), metav1.ConditionTrue)
		worker := workerMachine("ns-w", "worker-1", wantedType)

		reconciler := &NicoMachineReconciler{Client: fakeMachineClient(t, &paused)}
		deferred, err := reconciler.deferForControlPlanePriority(context.Background(), &worker, 1)

		require.NoError(t, err)
		assert.False(t, deferred, "a paused control-plane machine must not stall workers")
		assert.Nil(t, provisionedCondition(&worker))
	})

	// Under the allowlist a control-plane machine that has not reconciled yet
	// does not contend: it has not shown that capacity is what it is missing.
	// It starts contending one reconcile later, once its own availability
	// preflight stamps InstanceTypeUnavailable.
	t.Run("worker proceeds for a control-plane machine with no conditions yet", func(t *testing.T) {
		fresh := freshControlPlane("ns-cp", "cp-fresh", wantedType)
		worker := workerMachine("ns-w", "worker-1", wantedType)

		reconciler := &NicoMachineReconciler{Client: fakeMachineClient(t, &fresh)}
		deferred, err := reconciler.deferForControlPlanePriority(context.Background(), &worker, 1)

		require.NoError(t, err)
		assert.False(t, deferred, "a machine that has not reconciled yet does not hold capacity")
		assert.Nil(t, provisionedCondition(&worker))
	})

	t.Run("an empty instance type never defers", func(t *testing.T) {
		cp := controlPlaneWaiter("ns-cp", "cp-1", "")
		worker := workerMachine("ns-w", "worker-1", "")

		reconciler := &NicoMachineReconciler{Client: fakeMachineClient(t, &cp)}
		deferred, err := reconciler.deferForControlPlanePriority(context.Background(), &worker, 1)

		require.NoError(t, err)
		assert.False(t, deferred)
	})

	t.Run("a list failure surfaces an error and writes no condition", func(t *testing.T) {
		worker := workerMachine("ns-w", "worker-1", wantedType)

		scheme := runtime.NewScheme()
		require.NoError(t, infrav1.AddToScheme(scheme))
		failing := crfake.NewClientBuilder().WithScheme(scheme).WithInterceptorFuncs(interceptor.Funcs{
			List: func(_ context.Context, _ client.WithWatch, _ client.ObjectList, _ ...client.ListOption) error {
				return errors.New("informer unavailable")
			},
		}).Build()

		reconciler := &NicoMachineReconciler{Client: failing}
		deferred, err := reconciler.deferForControlPlanePriority(context.Background(), &worker, 1)

		require.Error(t, err)
		assert.False(t, deferred)
		assert.Nil(t, provisionedCondition(&worker), "no condition is written on lookup failure")
	})
}
