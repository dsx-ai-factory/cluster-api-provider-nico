// SPDX-FileCopyrightText: Copyright (c) 2026 NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package controllers

import (
	"context"
	"fmt"
	"net/http/httptest"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"

	"github.com/onsi/ginkgo/v2"
	"github.com/onsi/gomega"

	nicosdk "github.com/NVIDIA/ncx-infra-controller-rest/sdk/standard"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	clusterv1 "sigs.k8s.io/cluster-api/api/core/v1beta2"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/config"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"

	infrav1 "github.com/NVIDIA/cluster-api-provider-nico/api/v1alpha1"
	"github.com/NVIDIA/cluster-api-provider-nico/internal/fake"
	"github.com/NVIDIA/cluster-api-provider-nico/internal/nico"
	"github.com/NVIDIA/cluster-api-provider-nico/internal/test/fixtures"
)

const (
	// testNamespace is where every case's objects live.
	testNamespace = "default"
)

// caseFakes keeps each case's fake reachable from its assertions. Cases run in
// their own environment, so the state must not be shared between them.
var caseFakes sync.Map // case name -> *fake.Server

func seedFakeResources(tc *fixtures.Case, server *fake.Server) error {
	server.SeedToken("test-token")

	tenant := nicosdk.NewTenant()
	tenant.SetId("tenant-1")
	tenant.SetOrg("org-1")
	server.SeedTenant("org-1", *tenant)

	site := nicosdk.NewSite()
	site.SetId("site-1")
	site.SetName("fake-site")
	site.SetOrg("org-1")
	server.SeedSite("org-1", *site)

	vpc := nicosdk.NewVPC()
	vpc.SetId("vpc-1")
	vpc.SetName("fake-vpc")
	vpc.SetOrg("org-1")
	vpc.SetTenantId("tenant-1")
	vpc.SetSiteId("site-1")
	server.SeedVPC("org-1", *vpc)

	input, ok := tc.Input("input_nico_objects.yaml")
	if !ok {
		return nil
	}
	return server.SeedFromYAML(input)
}

// capiCRDPath resolves the Cluster API CRDs out of the module cache, so envtest
// validates against the same contract version go.mod builds against rather than
// a copy that can drift.
func capiCRDPath() string {
	out, err := exec.Command("go", "list", "-m", "-f", "{{.Dir}}", "sigs.k8s.io/cluster-api").Output()
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), "resolve the Cluster API module directory")

	return filepath.Join(strings.TrimSpace(string(out)), "config", "crd", "bases")
}

// newEnvironment builds an envtest environment carrying both this provider's
// CRDs and Cluster API's, because the reconcilers resolve their owning Machine
// and Cluster through the API server.
func newEnvironment(*fixtures.Case) *envtest.Environment {
	return &envtest.Environment{
		CRDDirectoryPaths:     []string{filepath.Join("..", "config", "crd", "bases"), capiCRDPath()},
		ErrorIfCRDPathMissing: true,
	}
}

func newScheme() *runtime.Scheme {
	scheme := runtime.NewScheme()
	gomega.Expect(corev1.AddToScheme(scheme)).To(gomega.Succeed())
	gomega.Expect(clusterv1.AddToScheme(scheme)).To(gomega.Succeed())
	gomega.Expect(infrav1.AddToScheme(scheme)).To(gomega.Succeed())

	return scheme
}

// wireOwnerReferences fills in the UIDs of owner references declared by name in
// the input files. A UID is only known once the owner exists, and the API
// server rejects a reference without one, so the references are declared with a
// placeholder UID and resolved here.
func wireOwnerReferences(ctx context.Context, c client.Client, scheme *runtime.Scheme) error {
	for _, list := range []client.ObjectList{
		&infrav1.NicoClusterList{},
		&infrav1.NicoMachineList{},
		&clusterv1.MachineList{},
	} {
		if err := c.List(ctx, list); err != nil {
			return fmt.Errorf("list %T: %w", list, err)
		}

		objects, err := extractItems(list, scheme)
		if err != nil {
			return err
		}

		for _, object := range objects {
			owners := object.GetOwnerReferences()
			if len(owners) == 0 {
				continue
			}

			changed := false
			for i := range owners {
				uid, err := resolveOwnerUID(ctx, c, scheme, object.GetNamespace(), owners[i])
				if err != nil {
					return err
				}
				if owners[i].UID != uid {
					owners[i].UID = uid
					changed = true
				}
			}
			if !changed {
				continue
			}

			object.SetOwnerReferences(owners)
			if err := c.Update(ctx, object); err != nil {
				return fmt.Errorf("wire owner references on %s: %w", client.ObjectKeyFromObject(object), err)
			}
		}
	}
	return nil
}

func extractItems(list client.ObjectList, scheme *runtime.Scheme) ([]client.Object, error) {
	switch typed := list.(type) {
	case *infrav1.NicoClusterList:
		return toObjects(typed.Items), nil
	case *infrav1.NicoMachineList:
		return toObjects(typed.Items), nil
	case *clusterv1.MachineList:
		return toObjects(typed.Items), nil
	default:
		return nil, fmt.Errorf("unsupported list %T for scheme %v", list, scheme.Name())
	}
}

func toObjects[T any, PT interface {
	*T
	client.Object
}](items []T) []client.Object {
	objects := make([]client.Object, 0, len(items))
	for i := range items {
		objects = append(objects, PT(&items[i]))
	}
	return objects
}

func resolveOwnerUID(ctx context.Context, c client.Client, scheme *runtime.Scheme, namespace string, owner metav1.OwnerReference) (types.UID, error) {
	gv, err := schema.ParseGroupVersion(owner.APIVersion)
	if err != nil {
		return "", fmt.Errorf("parse owner apiVersion %q: %w", owner.APIVersion, err)
	}

	object, err := scheme.New(gv.WithKind(owner.Kind))
	if err != nil {
		return "", fmt.Errorf("owner kind %s is not in the scheme: %w", owner.Kind, err)
	}

	typed, ok := object.(client.Object)
	if !ok {
		return "", fmt.Errorf("owner kind %s is not a Kubernetes object", owner.Kind)
	}
	if err := c.Get(ctx, client.ObjectKey{Namespace: namespace, Name: owner.Name}, typed); err != nil {
		return "", fmt.Errorf("read owner %s/%s: %w", owner.Kind, owner.Name, err)
	}
	return typed.GetUID(), nil
}

// startFake serves the given fake on a loopback port and returns its base URL,
// tearing it down when the case finishes.
func startFake(server *fake.Server) string {
	endpoint := httptest.NewServer(server.Handler())
	ginkgo.DeferCleanup(endpoint.Close)

	return endpoint.URL
}

func pointIdentitySecretAtFake(ctx context.Context, c client.Client, endpoint string) error {
	secret := &corev1.Secret{}
	if err := c.Get(ctx, client.ObjectKey{Namespace: testNamespace, Name: "nico-creds"}, secret); err != nil {
		return client.IgnoreNotFound(err)
	}
	if secret.Data == nil {
		secret.Data = map[string][]byte{}
	}
	secret.Data[nico.SecretKeyEndpoint] = []byte(endpoint)

	return c.Update(ctx, secret)
}

// startReconcilers runs both reconcilers against the case's API server.
func startReconcilers(ctx ginkgo.SpecContext, tc *fixtures.Case) {
	defaultNicoClientCache = nico.NewClientCache()
	mgr, err := manager.New(tc.Config, manager.Options{
		Scheme:  tc.Scheme,
		Metrics: metricsserver.Options{BindAddress: "0"},
		// Cases share a process, so the controller names repeat.
		Controller: config.Controller{SkipNameValidation: new(true)},
	})
	gomega.Expect(err).NotTo(gomega.HaveOccurred())

	gomega.Expect((&NicoClusterReconciler{Client: mgr.GetClient(), Scheme: tc.Scheme}).SetupWithManager(ctx, mgr)).To(gomega.Succeed())
	gomega.Expect((&NicoMachineReconciler{Client: mgr.GetClient(), Scheme: tc.Scheme}).SetupWithManager(ctx, mgr)).To(gomega.Succeed())

	tc.StartManager(ctx, mgr)
}
