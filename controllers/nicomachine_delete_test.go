// SPDX-FileCopyrightText: Copyright (c) 2026 NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package controllers

import (
	"context"
	"net/http/httptest"

	"github.com/onsi/ginkgo/v2"
	"github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	clusterv1 "sigs.k8s.io/cluster-api/api/core/v1beta2"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	crfake "sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	infrav1 "github.com/dsx-ai-factory/cluster-api-provider-nico/api/v1alpha1"
	"github.com/dsx-ai-factory/cluster-api-provider-nico/internal/fake"
	"github.com/dsx-ai-factory/cluster-api-provider-nico/internal/nico"
	nicomachine "github.com/dsx-ai-factory/cluster-api-provider-nico/internal/nicomachine"

	nicosdk "github.com/NVIDIA/infra-controller/rest-api/sdk/standard"
)

// The envtest delete cases cannot reach an empty status.instanceID, because their
// initial reconcile adopts by name before the delete step runs.
var _ = ginkgo.Describe("NicoMachine deletion with an empty status.instanceID", func() {
	const (
		orgID     = "org-1"
		clusterID = "cluster-1"
		machineID = "machine-1"
		vpcID     = "vpc-1"
		siteID    = "site-1"
	)
	key := client.ObjectKey{Namespace: testNamespace, Name: testMachine}

	ownedLabels := map[string]string{clusterv1.ClusterNameLabel: clusterID, instanceMachineNameLabel: machineID, instanceNamespaceLabel: testNamespace}

	seedInstance := func(server *fake.Server, site string, labels map[string]string) {
		instance := nicosdk.NewInstance()
		instance.SetId("instance-1")
		instance.SetName(machineID)
		instance.SetVpcId(vpcID)
		instance.SetSiteId(site)
		instance.SetStatus(nicosdk.INSTANCESTATUS_READY)
		instance.SetLabels(labels)
		server.SeedInstance(orgID, instance, 0)
	}

	// newReconciler takes a Secret name unique to each spec, because the shared
	// client cache keys on the Secret and the fake client repeats resourceVersions.
	newReconciler := func(endpoint, secretName string, bootstrapped, withSecret bool) (*NicoMachineReconciler, client.Client) {
		machine := &clusterv1.Machine{
			ObjectMeta: metav1.ObjectMeta{Name: machineID, Namespace: testNamespace, Labels: map[string]string{clusterv1.ClusterNameLabel: clusterID}},
			Spec:       clusterv1.MachineSpec{ClusterName: clusterID},
		}
		if bootstrapped {
			machine.Spec.Bootstrap.DataSecretName = new("bootstrap-1")
		}
		now := metav1.Now()
		objects := []client.Object{
			machine,
			&clusterv1.Cluster{
				ObjectMeta: metav1.ObjectMeta{Name: clusterID, Namespace: testNamespace},
				Spec: clusterv1.ClusterSpec{InfrastructureRef: clusterv1.ContractVersionedObjectReference{
					APIGroup: infrav1.GroupVersion.Group, Kind: "NicoCluster", Name: "nico-1",
				}},
			},
			&infrav1.NicoCluster{
				ObjectMeta: metav1.ObjectMeta{Name: "nico-1", Namespace: testNamespace},
				Spec:       infrav1.NicoClusterSpec{SiteID: siteID, IdentityRef: infrav1.NicoClusterIdentityReference{Name: secretName}},
			},
			&infrav1.NicoMachine{
				ObjectMeta: metav1.ObjectMeta{
					Name: testMachine, Namespace: testNamespace, DeletionTimestamp: &now, Finalizers: []string{nicoMachineFinalizer},
					OwnerReferences: []metav1.OwnerReference{{APIVersion: clusterv1.GroupVersion.String(), Kind: "Machine", Name: machineID, UID: "machine-uid", Controller: new(true)}},
				},
				Spec: infrav1.NicoMachineSpec{VPCID: vpcID},
			},
		}
		if withSecret {
			objects = append(objects, &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{Name: secretName, Namespace: testNamespace},
				Data:       map[string][]byte{"endpoint": []byte(endpoint), "orgID": []byte(orgID), "token": []byte("synthetic-token")},
			})
		}
		scheme := newScheme()
		c := crfake.NewClientBuilder().WithScheme(scheme).WithObjects(objects...).WithStatusSubresource(&infrav1.NicoMachine{}).Build()
		return &NicoMachineReconciler{Client: c, Scheme: scheme}, c
	}

	setProviderID := func(ctx context.Context, c client.Client, providerID string) {
		nicoMachine := &infrav1.NicoMachine{}
		gomega.Expect(c.Get(ctx, key, nicoMachine)).To(gomega.Succeed())
		nicoMachine.Spec.ProviderID = providerID
		gomega.Expect(c.Update(ctx, nicoMachine)).To(gomega.Succeed())
	}

	gone := func(ctx context.Context, c client.Client) bool {
		return apierrors.IsNotFound(c.Get(ctx, key, &infrav1.NicoMachine{}))
	}

	ginkgo.It("releases an instance whose create outlived its status patch", func(ctx ginkgo.SpecContext) {
		server := fake.New()
		httpServer := httptest.NewServer(server.Handler())
		ginkgo.DeferCleanup(httpServer.Close)
		seedInstance(server, siteID, ownedLabels)
		r, c := newReconciler(httpServer.URL, "creds-orphan", true, true)

		_, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: key})
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		nicoMachine := &infrav1.NicoMachine{}
		gomega.Expect(c.Get(ctx, key, nicoMachine)).To(gomega.Succeed())
		gomega.Expect(nicoMachine.Status.InstanceID).To(gomega.Equal("instance-1"))
		gomega.Expect(controllerutil.ContainsFinalizer(nicoMachine, nicoMachineFinalizer)).To(gomega.BeTrue())

		gomega.Eventually(func(g gomega.Gomega) {
			_, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: key})
			g.Expect(err).NotTo(gomega.HaveOccurred())
			g.Expect(gone(ctx, c)).To(gomega.BeTrue())
		}).WithTimeout(timeout).Should(gomega.Succeed())
		gomega.Expect(server.InstanceCount()).To(gomega.BeZero())
	})

	ginkgo.It("leaves another namespace's instance with the same Cluster and Machine names", func(ctx ginkgo.SpecContext) {
		server := fake.New()
		httpServer := httptest.NewServer(server.Handler())
		ginkgo.DeferCleanup(httpServer.Close)
		seedInstance(server, siteID, map[string]string{clusterv1.ClusterNameLabel: clusterID, instanceMachineNameLabel: machineID, instanceNamespaceLabel: "other-namespace"})
		r, c := newReconciler(httpServer.URL, "creds-other-namespace", true, true)

		_, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: key})
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		gomega.Expect(gone(ctx, c)).To(gomega.BeTrue())
		gomega.Expect(server.InstanceCount()).To(gomega.Equal(1))
	})

	ginkgo.It("leaves an owned-looking instance that spec.providerID does not name", func(ctx ginkgo.SpecContext) {
		server := fake.New()
		httpServer := httptest.NewServer(server.Handler())
		ginkgo.DeferCleanup(httpServer.Close)
		seedInstance(server, siteID, ownedLabels)
		r, c := newReconciler(httpServer.URL, "creds-other-provider-id", true, true)
		setProviderID(ctx, c, nico.ProviderID("imported-instance"))

		_, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: key})
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		gomega.Expect(gone(ctx, c)).To(gomega.BeTrue())
		gomega.Expect(server.InstanceCount()).To(gomega.Equal(1))
	})

	ginkgo.It("recovers an instance when only spec.providerID was saved", func(ctx ginkgo.SpecContext) {
		server := fake.New()
		httpServer := httptest.NewServer(server.Handler())
		ginkgo.DeferCleanup(httpServer.Close)
		seedInstance(server, siteID, ownedLabels)
		r, c := newReconciler(httpServer.URL, "creds-same-provider-id", true, true)
		setProviderID(ctx, c, nico.ProviderID("instance-1"))

		_, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: key})
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		nicoMachine := &infrav1.NicoMachine{}
		gomega.Expect(c.Get(ctx, key, nicoMachine)).To(gomega.Succeed())
		gomega.Expect(nicoMachine.Status.InstanceID).To(gomega.Equal("instance-1"))
	})

	ginkgo.It("finds an instance at its VPC's site when that differs from the configured site", func(ctx ginkgo.SpecContext) {
		server := fake.New()
		httpServer := httptest.NewServer(server.Handler())
		ginkgo.DeferCleanup(httpServer.Close)
		seedInstance(server, "vpc-site", ownedLabels)
		r, c := newReconciler(httpServer.URL, "creds-vpc-site", true, true)

		_, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: key})
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		nicoMachine := &infrav1.NicoMachine{}
		gomega.Expect(c.Get(ctx, key, nicoMachine)).To(gomega.Succeed())
		gomega.Expect(nicoMachine.Status.InstanceID).To(gomega.Equal("instance-1"))
	})

	ginkgo.It("stamps ownership labels over conflicting spec.labels", func() {
		machine := &infrav1.NicoMachine{ObjectMeta: metav1.ObjectMeta{Namespace: testNamespace}, Spec: infrav1.NicoMachineSpec{
			VPCID:      vpcID,
			Interfaces: []infrav1.NicoMachineInterface{{SubnetID: "subnet-1"}},
			Labels:     map[string]string{clusterv1.ClusterNameLabel: "other", instanceMachineNameLabel: "other", instanceNamespaceLabel: "other", "team": "a"},
		}}

		req, err := buildInstanceCreateRequest(machineID, "tenant-1", machine, clusterID, "#cloud-config", nicomachine.InstanceTypeCapabilities{}, false)
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		gomega.Expect(req.GetLabels()).To(gomega.Equal(map[string]string{
			clusterv1.ClusterNameLabel: clusterID, instanceMachineNameLabel: machineID, instanceNamespaceLabel: testNamespace, "team": "a",
		}))
	})

	ginkgo.It("leaves a same-named instance without this machine's labels", func(ctx ginkgo.SpecContext) {
		server := fake.New()
		httpServer := httptest.NewServer(server.Handler())
		ginkgo.DeferCleanup(httpServer.Close)
		seedInstance(server, siteID, nil)
		r, c := newReconciler(httpServer.URL, "creds-foreign", true, true)

		_, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: key})
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		gomega.Expect(gone(ctx, c)).To(gomega.BeTrue())
		gomega.Expect(server.InstanceCount()).To(gomega.Equal(1))
	})

	ginkgo.It("completes when NICo has no matching instance", func(ctx ginkgo.SpecContext) {
		server := fake.New()
		httpServer := httptest.NewServer(server.Handler())
		ginkgo.DeferCleanup(httpServer.Close)
		r, c := newReconciler(httpServer.URL, "creds-none", true, true)

		_, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: key})
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		gomega.Expect(gone(ctx, c)).To(gomega.BeTrue())
		gomega.Expect(server.InstanceCount()).To(gomega.BeZero())
	})

	ginkgo.It("needs no credentials when bootstrap data was never set", func(ctx ginkgo.SpecContext) {
		r, c := newReconciler("", "creds-absent-unbootstrapped", false, false)

		_, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: key})
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		gomega.Expect(gone(ctx, c)).To(gomega.BeTrue())
	})

	ginkgo.It("holds the finalizer when a create may have run and credentials are missing", func(ctx ginkgo.SpecContext) {
		r, c := newReconciler("", "creds-absent-bootstrapped", true, false)

		_, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: key})
		gomega.Expect(err).To(gomega.HaveOccurred())
		gomega.Expect(gone(ctx, c)).To(gomega.BeFalse())
	})
})
