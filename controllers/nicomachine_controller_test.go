// SPDX-FileCopyrightText: Copyright (c) 2026 NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package controllers

import (
	"context"
	"time"

	"github.com/onsi/ginkgo/v2"
	"github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	clusterv1 "sigs.k8s.io/cluster-api/api/core/v1beta2"
	"sigs.k8s.io/cluster-api/util/conditions"
	"sigs.k8s.io/controller-runtime/pkg/client"

	infrav1 "github.com/dsx-ai-factory/cluster-api-provider-nico/api/v1alpha1"
	"github.com/dsx-ai-factory/cluster-api-provider-nico/internal/fake"
	"github.com/dsx-ai-factory/cluster-api-provider-nico/internal/test/fixtures"
)

const (
	timeout          = 60 * time.Second
	testMachine      = "nicomachine-1"
	testOwnerMachine = "machine-1"
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
			if tc.HasInput("input_workload_client_objects.yaml") {
				gomega.Expect(createWorkloadKubeconfigSecret(ctx, tc)).To(gomega.Succeed())
			}
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

				if tc.HasInput("input_workload_client_objects.yaml") {
					nodes := &corev1.NodeList{}
					g.Expect(tc.Client.List(ctx, nodes)).To(gomega.Succeed())
					g.Expect(nodes.Items).To(gomega.HaveLen(1))
					g.Expect(nodes.Items[0].Spec.ProviderID).To(gomega.Equal(nicoMachine.Spec.ProviderID))
				}
			}).WithTimeout(timeout).WithPolling(time.Second).Should(gomega.Succeed())

			if tc.HasInput("input_workload_client_objects.yaml") {
				assertNodeProviderIDReconciliation(ctx, tc)
			}
		})
	},
))

func assertNodeProviderIDReconciliation(ctx ginkgo.SpecContext, tc *fixtures.Case) {
	ginkgo.By("leaving an already matching providerID unchanged")
	machine := &clusterv1.Machine{}
	gomega.Expect(tc.Client.Get(ctx, client.ObjectKey{Namespace: testNamespace, Name: testOwnerMachine}, machine)).To(gomega.Succeed())
	cluster := &clusterv1.Cluster{}
	gomega.Expect(tc.Client.Get(ctx, client.ObjectKey{Namespace: testNamespace, Name: testWorkloadCluster}, cluster)).To(gomega.Succeed())
	nicoMachine := &infrav1.NicoMachine{}
	gomega.Expect(tc.Client.Get(ctx, client.ObjectKey{Namespace: testNamespace, Name: testMachine}, nicoMachine)).To(gomega.Succeed())

	reconciler := &NicoMachineReconciler{Client: tc.Client}
	node := &corev1.Node{}
	gomega.Expect(tc.Client.Get(ctx, client.ObjectKey{Name: machine.Name}, node)).To(gomega.Succeed())
	matchingResourceVersion := node.ResourceVersion

	result, err := reconciler.reconcileNodeProviderID(ctx, machine, cluster, nicoMachine)
	gomega.Expect(err).NotTo(gomega.HaveOccurred())
	gomega.Expect(result.IsZero()).To(gomega.BeTrue())
	gomega.Expect(tc.Client.Get(ctx, client.ObjectKey{Name: machine.Name}, node)).To(gomega.Succeed())
	gomega.Expect(node.ResourceVersion).To(gomega.Equal(matchingResourceVersion))

	ginkgo.By("waiting when the workload Node has not registered")
	gomega.Expect(tc.Client.Delete(ctx, node)).To(gomega.Succeed())
	result, err = reconciler.reconcileNodeProviderID(ctx, machine, cluster, nicoMachine)
	gomega.Expect(err).NotTo(gomega.HaveOccurred())
	gomega.Expect(result.RequeueAfter).To(gomega.Equal(machineRequeueFast))

	ginkgo.By("preserving a providerID owned by another controller")
	conflictingProviderID := "external://machine-1"
	node = &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{Name: machine.Name},
		Spec:       corev1.NodeSpec{ProviderID: conflictingProviderID},
	}
	gomega.Expect(tc.Client.Create(ctx, node)).To(gomega.Succeed())
	result, err = reconciler.reconcileNodeProviderID(ctx, machine, cluster, nicoMachine)
	gomega.Expect(err).To(gomega.MatchError(gomega.ContainSubstring("has providerID")))
	gomega.Expect(result.IsZero()).To(gomega.BeTrue())
	gomega.Expect(tc.Client.Get(ctx, client.ObjectKey{Name: machine.Name}, node)).To(gomega.Succeed())
	gomega.Expect(node.Spec.ProviderID).To(gomega.Equal(conflictingProviderID))

	ginkgo.By("leaving providerID ownership to an external CCM when opted out")
	if cluster.Annotations == nil {
		cluster.Annotations = map[string]string{}
	}
	cluster.Annotations[skipNodeProviderIDReconciliationAnnotation] = "true"
	result, err = reconciler.reconcileNodeProviderID(ctx, machine, cluster, nicoMachine)
	gomega.Expect(err).NotTo(gomega.HaveOccurred())
	gomega.Expect(result.IsZero()).To(gomega.BeTrue())
	gomega.Expect(tc.Client.Get(ctx, client.ObjectKey{Name: machine.Name}, node)).To(gomega.Succeed())
	gomega.Expect(node.Spec.ProviderID).To(gomega.Equal(conflictingProviderID))

	ginkgo.By("using Machine status nodeRef to select the workload Node")
	delete(cluster.Annotations, skipNodeProviderIDReconciliationAnnotation)
	nodeRefName := "node-ref-1"
	machine.Status.NodeRef = clusterv1.MachineNodeReference{Name: nodeRefName}
	nodeRefNode := &corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: nodeRefName}}
	gomega.Expect(tc.Client.Create(ctx, nodeRefNode)).To(gomega.Succeed())
	result, err = reconciler.reconcileNodeProviderID(ctx, machine, cluster, nicoMachine)
	gomega.Expect(err).NotTo(gomega.HaveOccurred())
	gomega.Expect(result.IsZero()).To(gomega.BeTrue())
	gomega.Expect(tc.Client.Get(ctx, client.ObjectKey{Name: nodeRefName}, nodeRefNode)).To(gomega.Succeed())
	gomega.Expect(nodeRefNode.Spec.ProviderID).To(gomega.Equal(nicoMachine.Spec.ProviderID))
	gomega.Expect(tc.Client.Get(ctx, client.ObjectKey{Name: machine.Name}, node)).To(gomega.Succeed())
	gomega.Expect(node.Spec.ProviderID).To(gomega.Equal(conflictingProviderID))

	ginkgo.By("rejecting exec credential plugins in workload kubeconfigs")
	execKubeconfig, err := workloadKubeconfigWithExecProvider(tc.Config.Host)
	gomega.Expect(err).NotTo(gomega.HaveOccurred())
	execCluster := client.ObjectKey{Namespace: testNamespace, Name: "exec-provider"}
	execKubeconfigSecret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Namespace: execCluster.Namespace, Name: execCluster.Name + "-kubeconfig"},
		Data:       map[string][]byte{workloadKubeconfigDataKey: execKubeconfig},
	}
	gomega.Expect(tc.Client.Create(ctx, execKubeconfigSecret)).To(gomega.Succeed())
	workloadClient, err := newWorkloadClusterClient(ctx, tc.Client, execCluster)
	gomega.Expect(workloadClient).To(gomega.BeNil())
	gomega.Expect(err).To(gomega.MatchError("workload cluster kubeconfig must not use an exec credential plugin"))
}

// IMPORTANT: Read docs/writing-tests.md. There is ZERO reason that you should
// have to add or update a case set.
// Represents an unprovisioned CR at generation 1 with no providerID.
var _ = fixtures.DescribeCaseSet(nicoMachineCaseSet(
	"NicoMachine create reconciliation ending not provisioned",
	"nicomachine-create-not-provisioned-",
	func(tc *fixtures.Case, _ fixtures.CaseSet) {
		ginkgo.It("reconciles the initial NicoMachine", func(ctx ginkgo.SpecContext) {
			gomega.Eventually(func(g gomega.Gomega) {
				nicoMachines := &infrav1.NicoMachineList{}
				g.Expect(tc.Client.List(ctx, nicoMachines)).To(gomega.Succeed())
				for i := range nicoMachines.Items {
					provisioned := conditions.Get(&nicoMachines.Items[i], infrav1.MachineProvisionedCondition)
					g.Expect(provisioned).NotTo(gomega.BeNil())
					g.Expect(provisioned.Status).To(gomega.Equal(metav1.ConditionFalse))
					g.Expect(provisioned.ObservedGeneration).To(gomega.Equal(nicoMachines.Items[i].Generation))
				}

				nicoMachine := &infrav1.NicoMachine{}
				g.Expect(tc.Client.Get(ctx, client.ObjectKey{Namespace: testNamespace, Name: testMachine}, nicoMachine)).To(gomega.Succeed())
				g.Expect(nicoMachine.Generation).To(gomega.Equal(int64(1)))
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
