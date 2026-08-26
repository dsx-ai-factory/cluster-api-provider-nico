// SPDX-FileCopyrightText: Copyright (c) 2026 NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package controllers

import (
	"fmt"
	"time"

	"github.com/onsi/ginkgo/v2"
	"github.com/onsi/gomega"

	nicosdk "github.com/NVIDIA/ncx-infra-controller-rest/sdk/standard"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	clusterv1 "sigs.k8s.io/cluster-api/api/core/v1beta2"
	"sigs.k8s.io/cluster-api/util/conditions"
	"sigs.k8s.io/controller-runtime/pkg/client"

	infrav1 "github.com/NVIDIA/cluster-api-provider-nico/api/v1alpha1"
	nicofake "github.com/NVIDIA/cluster-api-provider-nico/internal/nico/fake"
	"github.com/NVIDIA/cluster-api-provider-nico/internal/test/fixtures"
)

var _ = fixtures.DescribeCaseSet(nicoMachineCaseSet(
	"NicoMachine create not ready",
	"nicomachine-create-not-ready",
	func(fakeAPI *nicofake.Client) {
		fakeAPI.CreateStatus = nicosdk.INSTANCESTATUS_PENDING
	},
	func(tc *fixtures.Case, _ fixtures.CaseSet) {
		var instanceID string

		ginkgo.It("reports InstanceNotReady while pending", func(ctx ginkgo.SpecContext) {
			gomega.Eventually(func(g gomega.Gomega) {
				assertNicoMachineProvisionedReason(g, ctx, tc.Client, "nicomachine-1", metav1.ConditionFalse, infrav1.InstanceNotReadyReason)
				var current infrav1.NicoMachine
				g.Expect(tc.Client.Get(ctx, client.ObjectKey{Namespace: "test-ns", Name: "nicomachine-1"}, &current)).To(gomega.Succeed())
				g.Expect(current.Status.InstanceID).NotTo(gomega.BeEmpty())
				instanceID = current.Status.InstanceID
			}).WithTimeout(30 * time.Second).WithPolling(time.Second).Should(gomega.Succeed())
		})

		ginkgo.It("becomes Ready after the instance is Ready", func(ctx ginkgo.SpecContext) {
			gomega.Expect(loadCaseFake(tc.Name).SetInstanceStatus(instanceID, nicosdk.INSTANCESTATUS_READY)).To(gomega.Succeed())
			// production requeues after machineRequeueSlow; kick to avoid waiting.
			kickMachine(ctx, tc.Client)

			gomega.Eventually(func(g gomega.Gomega) {
				assertNicoMachineReady(g, ctx, tc.Client, "nicomachine-1")
			}).WithTimeout(30 * time.Second).WithPolling(time.Second).Should(gomega.Succeed())
		})
	},
))

var _ = fixtures.DescribeCaseSet(nicoMachineCaseSet(
	"NicoMachine instance type unavailable",
	"nicomachine-instance-type-unavailable",
	func(fakeAPI *nicofake.Client) {
		fakeAPI.SeedInstanceType("type-1", 0)
	},
	func(tc *fixtures.Case, _ fixtures.CaseSet) {
		ginkgo.It("reports InstanceTypeUnavailable", func(ctx ginkgo.SpecContext) {
			gomega.Eventually(func(g gomega.Gomega) {
				assertNicoMachineProvisionedReason(g, ctx, tc.Client, "nicomachine-1", metav1.ConditionFalse, infrav1.InstanceTypeUnavailableReason)
			}).WithTimeout(30 * time.Second).WithPolling(time.Second).Should(gomega.Succeed())
			gomega.Expect(loadCaseFake(tc.Name).LastCreateRequest()).To(gomega.BeNil())
		})

		ginkgo.It("creates after capacity is available", func(ctx ginkgo.SpecContext) {
			loadCaseFake(tc.Name).SeedInstanceType("type-1", 1)
			// production requeues after instanceTypeUnavailableWait (2m); kick to avoid waiting.
			kickMachine(ctx, tc.Client)

			gomega.Eventually(func(g gomega.Gomega) {
				assertNicoMachineReady(g, ctx, tc.Client, "nicomachine-1")
			}).WithTimeout(30 * time.Second).WithPolling(time.Second).Should(gomega.Succeed())
			gomega.Expect(loadCaseFake(tc.Name).LastCreateRequest()).NotTo(gomega.BeNil())
		})
	},
))

var _ = fixtures.DescribeCaseSet(nicoMachineCaseSet(
	"NicoMachine create failed",
	"nicomachine-create-failed",
	func(fakeAPI *nicofake.Client) {
		fakeAPI.SetCreateErr(fmt.Errorf("inject create failure"))
	},
	func(tc *fixtures.Case, _ fixtures.CaseSet) {
		ginkgo.It("reports InstanceCreateFailed", func(ctx ginkgo.SpecContext) {
			gomega.Eventually(func(g gomega.Gomega) {
				assertNicoMachineProvisionedReason(g, ctx, tc.Client, "nicomachine-1", metav1.ConditionFalse, infrav1.InstanceCreateFailedReason)
			}).WithTimeout(30 * time.Second).WithPolling(time.Second).Should(gomega.Succeed())
			gomega.Expect(loadCaseFake(tc.Name).LastCreateRequest()).NotTo(gomega.BeNil())
			gomega.Expect(loadCaseFake(tc.Name).InstanceCount()).To(gomega.Equal(0))
		})
	},
))

var _ = fixtures.DescribeCaseSet(nicoMachineCaseSet(
	"NicoMachine delete instance missing",
	"nicomachine-delete-instance-missing",
	nil,
	func(tc *fixtures.Case, _ fixtures.CaseSet) {
		var instanceID string

		ginkgo.It("reconciles the NicoMachine to Ready", func(ctx ginkgo.SpecContext) {
			gomega.Eventually(func(g gomega.Gomega) {
				assertNicoMachineReady(g, ctx, tc.Client, "nicomachine-1")
				var current infrav1.NicoMachine
				g.Expect(tc.Client.Get(ctx, client.ObjectKey{Namespace: "test-ns", Name: "nicomachine-1"}, &current)).To(gomega.Succeed())
				instanceID = current.Status.InstanceID
			}).WithTimeout(30 * time.Second).WithPolling(time.Second).Should(gomega.Succeed())
		})

		ginkgo.It("deletes after the NICo instance is already gone", func(ctx ginkgo.SpecContext) {
			fakeAPI := loadCaseFake(tc.Name)
			fakeAPI.RemoveInstance(instanceID)

			var current infrav1.NicoMachine
			gomega.Expect(tc.Client.Get(ctx, client.ObjectKey{Namespace: "test-ns", Name: "nicomachine-1"}, &current)).To(gomega.Succeed())
			gomega.Expect(tc.Client.Delete(ctx, &current)).To(gomega.Succeed())

			gomega.Eventually(func(g gomega.Gomega) {
				err := tc.Client.Get(ctx, client.ObjectKey{Namespace: "test-ns", Name: "nicomachine-1"}, &infrav1.NicoMachine{})
				g.Expect(apierrors.IsNotFound(err)).To(gomega.BeTrue())
			}).WithTimeout(30 * time.Second).WithPolling(time.Second).Should(gomega.Succeed())
			gomega.Expect(fakeAPI.DeleteAttempts()).To(gomega.Equal(0))
			gomega.Expect(fakeAPI.LastDeleteInstanceID()).To(gomega.BeEmpty())
		})
	},
))

var _ = fixtures.DescribeCaseSet(nicoMachineCaseSet(
	"NicoMachine delete retry",
	"nicomachine-delete-retry",
	nil,
	func(tc *fixtures.Case, _ fixtures.CaseSet) {
		var instanceID string

		ginkgo.It("reconciles the NicoMachine to Ready", func(ctx ginkgo.SpecContext) {
			gomega.Eventually(func(g gomega.Gomega) {
				assertNicoMachineReady(g, ctx, tc.Client, "nicomachine-1")
				var current infrav1.NicoMachine
				g.Expect(tc.Client.Get(ctx, client.ObjectKey{Namespace: "test-ns", Name: "nicomachine-1"}, &current)).To(gomega.Succeed())
				instanceID = current.Status.InstanceID
			}).WithTimeout(30 * time.Second).WithPolling(time.Second).Should(gomega.Succeed())
		})

		ginkgo.It("keeps the finalizer when observing the instance fails", func(ctx ginkgo.SpecContext) {
			fakeAPI := loadCaseFake(tc.Name)
			fakeAPI.SetGetInstanceErr(fmt.Errorf("inject get failure"))

			var current infrav1.NicoMachine
			gomega.Expect(tc.Client.Get(ctx, client.ObjectKey{Namespace: "test-ns", Name: "nicomachine-1"}, &current)).To(gomega.Succeed())
			gomega.Expect(tc.Client.Delete(ctx, &current)).To(gomega.Succeed())

			gomega.Eventually(func(g gomega.Gomega) {
				g.Expect(fakeAPI.DeleteAttempts()).To(gomega.Equal(0))
				var still infrav1.NicoMachine
				g.Expect(tc.Client.Get(ctx, client.ObjectKey{Namespace: "test-ns", Name: "nicomachine-1"}, &still)).To(gomega.Succeed())
				g.Expect(still.DeletionTimestamp).NotTo(gomega.BeNil())
				g.Expect(still.Finalizers).To(gomega.ContainElement("infrastructure.cluster.x-k8s.io/nicomachine"))
			}).WithTimeout(30 * time.Second).WithPolling(time.Second).Should(gomega.Succeed())
		})

		ginkgo.It("keeps the finalizer when delete fails", func(ctx ginkgo.SpecContext) {
			fakeAPI := loadCaseFake(tc.Name)
			fakeAPI.SetDeleteErr(fmt.Errorf("inject delete failure"))
			fakeAPI.SetGetInstanceErr(nil)
			kickMachine(ctx, tc.Client)

			gomega.Eventually(func(g gomega.Gomega) {
				g.Expect(fakeAPI.DeleteAttempts()).To(gomega.BeNumerically(">=", 1))
				g.Expect(fakeAPI.LastDeleteInstanceID()).To(gomega.Equal(instanceID))
				var still infrav1.NicoMachine
				g.Expect(tc.Client.Get(ctx, client.ObjectKey{Namespace: "test-ns", Name: "nicomachine-1"}, &still)).To(gomega.Succeed())
				g.Expect(still.DeletionTimestamp).NotTo(gomega.BeNil())
				g.Expect(still.Finalizers).To(gomega.ContainElement("infrastructure.cluster.x-k8s.io/nicomachine"))
			}).WithTimeout(30 * time.Second).WithPolling(time.Second).Should(gomega.Succeed())
		})

		ginkgo.It("deletes after the failure clears", func(ctx ginkgo.SpecContext) {
			fakeAPI := loadCaseFake(tc.Name)
			attemptsBefore := fakeAPI.DeleteAttempts()
			fakeAPI.SetDeleteErr(nil)
			kickMachine(ctx, tc.Client)

			gomega.Eventually(fakeAPI.DeleteAttempts).WithTimeout(30 * time.Second).WithPolling(time.Second).Should(gomega.BeNumerically(">", attemptsBefore))
			_, err := fakeAPI.GetInstance(ctx, instanceID)
			gomega.Expect(err).NotTo(gomega.HaveOccurred())
			kickMachine(ctx, tc.Client)
			gomega.Eventually(func(g gomega.Gomega) {
				err := tc.Client.Get(ctx, client.ObjectKey{Namespace: "test-ns", Name: "nicomachine-1"}, &infrav1.NicoMachine{})
				g.Expect(apierrors.IsNotFound(err)).To(gomega.BeTrue())
			}).WithTimeout(30 * time.Second).WithPolling(time.Second).Should(gomega.Succeed())
		})
	},
))

var _ = fixtures.DescribeCaseSet(nicoMachineCaseSet(
	"NicoMachine paused",
	"nicomachine-paused",
	nil,
	func(tc *fixtures.Case, _ fixtures.CaseSet) {
		ginkgo.It("stays paused with no NICo create", func(ctx ginkgo.SpecContext) {
			gomega.Eventually(func(g gomega.Gomega) {
				var current infrav1.NicoMachine
				g.Expect(tc.Client.Get(ctx, client.ObjectKey{Namespace: "test-ns", Name: "nicomachine-1"}, &current)).To(gomega.Succeed())
				cond := conditions.Get(&current, clusterv1.PausedCondition)
				g.Expect(cond).NotTo(gomega.BeNil())
				g.Expect(cond.Status).To(gomega.Equal(metav1.ConditionTrue))
				assertConditionsObservedAtGeneration(g, current.GetGeneration(), current.Status.Conditions)
			}).WithTimeout(30 * time.Second).WithPolling(time.Second).Should(gomega.Succeed())

			gomega.Consistently(func(g gomega.Gomega) {
				g.Expect(loadCaseFake(tc.Name).LastCreateRequest()).To(gomega.BeNil())
			}).WithTimeout(5 * time.Second).WithPolling(time.Second).Should(gomega.Succeed())
		})

		ginkgo.It("reconciles after unpause", func(ctx ginkgo.SpecContext) {
			var cluster clusterv1.Cluster
			gomega.Expect(tc.Client.Get(ctx, client.ObjectKey{Namespace: "test-ns", Name: "cluster-1"}, &cluster)).To(gomega.Succeed())
			cluster.Spec.Paused = new(false)
			gomega.Expect(tc.Client.Update(ctx, &cluster)).To(gomega.Succeed())

			gomega.Eventually(func(g gomega.Gomega) {
				assertNicoMachineReady(g, ctx, tc.Client, "nicomachine-1")
			}).WithTimeout(30 * time.Second).WithPolling(time.Second).Should(gomega.Succeed())
			gomega.Expect(loadCaseFake(tc.Name).LastCreateRequest()).NotTo(gomega.BeNil())
		})
	},
))

var _ = fixtures.DescribeCaseSet(nicoMachineCaseSet(
	"NicoCluster delete blocked by machines",
	"nicocluster-delete-blocked",
	nil,
	func(tc *fixtures.Case, _ fixtures.CaseSet) {
		ginkgo.It("reconciles Ready", func(ctx ginkgo.SpecContext) {
			gomega.Eventually(func(g gomega.Gomega) {
				assertNicoMachineReady(g, ctx, tc.Client, "nicomachine-1")
				assertNicoClusterConditionReason(g, ctx, tc.Client, "nico-1", infrav1.NicoReadyCondition, metav1.ConditionTrue, infrav1.InfrastructureReadyReason)
			}).WithTimeout(30 * time.Second).WithPolling(time.Second).Should(gomega.Succeed())
		})

		ginkgo.It("blocks NicoCluster deletion while machines remain", func(ctx ginkgo.SpecContext) {
			var nicoCluster infrav1.NicoCluster
			gomega.Expect(tc.Client.Get(ctx, client.ObjectKey{Namespace: "test-ns", Name: "nico-1"}, &nicoCluster)).To(gomega.Succeed())
			gomega.Expect(tc.Client.Delete(ctx, &nicoCluster)).To(gomega.Succeed())

			gomega.Eventually(func(g gomega.Gomega) {
				assertNicoClusterConditionReason(g, ctx, tc.Client, "nico-1", clusterv1.DeletingCondition, metav1.ConditionTrue, infrav1.WaitingForNicoMachinesDeletionReason)
				var current infrav1.NicoCluster
				g.Expect(tc.Client.Get(ctx, client.ObjectKey{Namespace: "test-ns", Name: "nico-1"}, &current)).To(gomega.Succeed())
				g.Expect(current.Finalizers).To(gomega.ContainElement("infrastructure.cluster.x-k8s.io/nicocluster"))
			}).WithTimeout(30 * time.Second).WithPolling(time.Second).Should(gomega.Succeed())
		})

		ginkgo.It("clears the NicoCluster finalizer after machines are gone", func(ctx ginkgo.SpecContext) {
			var nicoMachine infrav1.NicoMachine
			gomega.Expect(tc.Client.Get(ctx, client.ObjectKey{Namespace: "test-ns", Name: "nicomachine-1"}, &nicoMachine)).To(gomega.Succeed())
			instanceID := nicoMachine.Status.InstanceID
			gomega.Expect(tc.Client.Delete(ctx, &nicoMachine)).To(gomega.Succeed())

			fakeAPI := loadCaseFake(tc.Name)
			gomega.Eventually(fakeAPI.DeleteAttempts).WithTimeout(30 * time.Second).WithPolling(time.Second).Should(gomega.BeNumerically(">=", 1))
			_, err := fakeAPI.GetInstance(ctx, instanceID)
			gomega.Expect(err).NotTo(gomega.HaveOccurred())
			kickMachine(ctx, tc.Client)

			gomega.Eventually(func(g gomega.Gomega) {
				err := tc.Client.Get(ctx, client.ObjectKey{Namespace: "test-ns", Name: "nicomachine-1"}, &infrav1.NicoMachine{})
				g.Expect(apierrors.IsNotFound(err)).To(gomega.BeTrue())
			}).WithTimeout(30 * time.Second).WithPolling(time.Second).Should(gomega.Succeed())

			// production requeues after clusterDeleteRequeue; kick to avoid waiting.
			kickNicoCluster(ctx, tc.Client, "nico-1")

			gomega.Eventually(func(g gomega.Gomega) {
				err := tc.Client.Get(ctx, client.ObjectKey{Namespace: "test-ns", Name: "nico-1"}, &infrav1.NicoCluster{})
				g.Expect(apierrors.IsNotFound(err)).To(gomega.BeTrue())
			}).WithTimeout(30 * time.Second).WithPolling(time.Second).Should(gomega.Succeed())
		})
	},
))
