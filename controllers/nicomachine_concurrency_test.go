// SPDX-FileCopyrightText: Copyright (c) 2026 NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package controllers

import (
	"fmt"
	"net/http/httptest"
	"sync"

	"github.com/onsi/ginkgo/v2"
	"github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clusterv1 "sigs.k8s.io/cluster-api/api/core/v1beta2"
	"sigs.k8s.io/cluster-api/util/conditions"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	fakeclient "sigs.k8s.io/controller-runtime/pkg/client/fake"

	infrav1 "github.com/dsx-ai-factory/cluster-api-provider-nico/api/v1alpha1"
	"github.com/dsx-ai-factory/cluster-api-provider-nico/internal/fake"
	"github.com/dsx-ai-factory/cluster-api-provider-nico/internal/nico"
	"github.com/dsx-ai-factory/cluster-api-provider-nico/pkg/test/fixture"
)

// This spec reconciles more NicoMachines than their failure domain has free
// machines, all at once, as --nicomachine-concurrency workers do. A controller
// reconciles one object in one worker, so each goroutine owns one NicoMachine.
var _ = ginkgo.It("creates concurrently in one failure domain without sharing or leaking instances", func(ctx ginkgo.SpecContext) {
	const (
		namespace    = "default"
		nicoMachines = 12
		freeMachines = 4
		maxPasses    = 10
	)

	server := fake.New()
	gomega.Expect(seedFakeResources(&fixture.Case{}, server)).To(gomega.Succeed())
	seed := fmt.Sprintf(`
instance_types:
  resources:
  - org: org-1
    resource:
      allocationStats: {total: %[1]d, unused: %[1]d, unusedUsable: %[1]d, used: 0}
      id: type-1
machines:
  resources:
`, nicoMachines)
	for i := range freeMachines {
		seed += fmt.Sprintf(`  - org: org-1
    resource:
      id: machine-%d
      instanceTypeId: type-1
      labels: {failure_domain: fd-a}
      siteId: site-1
`, i)
	}
	gomega.Expect(server.SeedFromYAML(seed)).To(gomega.Succeed())
	endpoint := httptest.NewServer(server.Handler())
	ginkgo.DeferCleanup(endpoint.Close)

	scheme := runtime.NewScheme()
	utilruntime.Must(corev1.AddToScheme(scheme))
	utilruntime.Must(clusterv1.AddToScheme(scheme))
	utilruntime.Must(infrav1.AddToScheme(scheme))

	objects := make([]client.Object, 0, 3+2*nicoMachines)
	objects = append(objects,
		&corev1.Secret{
			Namespace: namespace, Name: "nico-creds",
			Data: map[string][]byte{
				nico.SecretKeyEndpoint: []byte(endpoint.URL),
				nico.SecretKeyOrgID:    []byte("org-1"),
				nico.SecretKeyToken:    []byte("test-token"),
			},
		},
		&corev1.Secret{
			Namespace: namespace, Name: "bootstrap",
			Data: map[string][]byte{"value": []byte("#cloud-config\n")},
		},
		&clusterv1.Cluster{Namespace: namespace, Name: "cluster-1"},
	)
	keys := make([]types.NamespacedName, nicoMachines)
	for i := range nicoMachines {
		name := fmt.Sprintf("machine-%d", i)
		keys[i] = types.NamespacedName{Namespace: namespace, Name: name}
		labels := map[string]string{clusterv1.ClusterNameLabel: "cluster-1"}
		objects = append(objects,
			&clusterv1.Machine{
				Namespace: namespace, Name: name, Labels: labels,
				Spec: clusterv1.MachineSpec{
					ClusterName:   "cluster-1",
					FailureDomain: "fd-a",
					Bootstrap:     clusterv1.Bootstrap{DataSecretName: new("bootstrap")},
				},
			},
			&infrav1.NicoMachine{
				Namespace: namespace, Name: name, Labels: labels,
				OwnerReferences: []metav1.OwnerReference{{
					APIVersion: clusterv1.GroupVersion.String(), Kind: "Machine", Name: name, UID: types.UID(name),
				}},
				Spec: infrav1.NicoMachineSpec{
					SiteID:                "site-1",
					IdentityRef:           corev1.LocalObjectReference{Name: "nico-creds"},
					VPCID:                 "vpc-1",
					InstanceTypeID:        "type-1",
					FailureDomainLabelKey: "failure_domain",
					Interfaces:            []infrav1.NicoMachineInterface{{SubnetID: "subnet-1"}},
				},
			},
		)
	}

	defaultNicoClientCache = nico.NewClientCache()
	reconciler := &NicoMachineReconciler{
		Client: fakeclient.NewClientBuilder().
			WithScheme(scheme).
			WithObjects(objects...).
			WithStatusSubresource(&infrav1.NicoMachine{}).
			Build(),
		Scheme: scheme,
	}

	// A NicoMachine settles when it holds an instance or NICo refused its
	// failure domain. Each pass before that makes progress or fails the test.
	settled := func(key types.NamespacedName) (bool, error) {
		var nicoMachine infrav1.NicoMachine
		if err := reconciler.Get(ctx, key, &nicoMachine); err != nil {
			return false, err
		}
		provisioned := conditions.Get(&nicoMachine, infrav1.MachineProvisionedCondition)
		return nicoMachine.Status.InstanceID != "" ||
			(provisioned != nil && provisioned.Reason == infrav1.FailureDomainUnavailableReason), nil
	}

	start := make(chan struct{})
	errs := make(chan error, nicoMachines)
	var wg sync.WaitGroup
	for _, key := range keys {
		wg.Go(func() {
			<-start
			for range maxPasses {
				if _, err := reconciler.Reconcile(ctx, ctrl.Request{NamespacedName: key}); err != nil {
					errs <- fmt.Errorf("reconcile %s: %w", key, err)
					return
				}
				if done, err := settled(key); err != nil || done {
					if err != nil {
						errs <- fmt.Errorf("read %s: %w", key, err)
					}
					return
				}
			}
			errs <- fmt.Errorf("%s did not settle in %d passes", key, maxPasses)
		})
	}
	close(start)
	wg.Wait()
	close(errs)
	var failures []error
	for err := range errs {
		failures = append(failures, err)
	}
	gomega.Expect(failures).To(gomega.BeEmpty())

	var list infrav1.NicoMachineList
	gomega.Expect(reconciler.List(ctx, &list, client.InNamespace(namespace))).To(gomega.Succeed())
	instanceIDs := map[string]struct{}{}
	provisioned := 0
	for i := range list.Items {
		if id := list.Items[i].Status.InstanceID; id != "" {
			instanceIDs[id] = struct{}{}
			provisioned++
		}
	}
	// The NicoMachines without an instance settled as refused. Distinct
	// instance IDs mean no instance is held twice.
	gomega.Expect(provisioned).To(gomega.Equal(freeMachines))
	gomega.Expect(instanceIDs).To(gomega.HaveLen(freeMachines))
	// Every instance NICo created is recorded on a NicoMachine: none leaked.
	gomega.Expect(server.InstanceCount()).To(gomega.Equal(freeMachines))
})
