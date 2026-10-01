// SPDX-FileCopyrightText: Copyright (c) 2026 NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"flag"
	"io"
	"maps"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	nicosdk "github.com/NVIDIA/infra-controller/rest-api/sdk/standard"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/rest"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"

	infrav1 "github.com/dsx-ai-factory/cluster-api-provider-nico/api/v1alpha1"
	"github.com/dsx-ai-factory/cluster-api-provider-nico/controllers"
	"github.com/dsx-ai-factory/cluster-api-provider-nico/internal/fake"
	"github.com/dsx-ai-factory/cluster-api-provider-nico/internal/nico"
)

func TestMain(m *testing.M) {
	// Managers log through controller-runtime; assertions carry the failure detail.
	ctrl.SetLogger(zap.New(zap.WriteTo(io.Discard)))
	os.Exit(m.Run())
}

func parseManagerFlags(t *testing.T, podNamespace string, args ...string) *managerConfig {
	t.Helper()
	fs := flag.NewFlagSet("manager", flag.ContinueOnError)
	cfg := bindFlags(fs, podNamespace)
	require.NoError(t, fs.Parse(args))
	require.NoError(t, cfg.provider.Validate())
	return cfg
}

func TestCredentialSelection(t *testing.T) {
	tests := []struct {
		name         string
		podNamespace string
		args         []string
		want         types.NamespacedName
	}{
		{
			name: "default",
			want: types.NamespacedName{Namespace: "capnico-system", Name: "nico-credentials"},
		},
		{
			name:         "POD_NAMESPACE",
			podNamespace: "provider-system",
			want:         types.NamespacedName{Namespace: "provider-system", Name: "nico-credentials"},
		},
		{
			name:         "explicit flags win",
			podNamespace: "provider-system",
			args: []string{
				"--provider-credentials-namespace=credentials",
				"--provider-credentials-secret-name=explicit-credentials",
			},
			want: types.NamespacedName{Namespace: "credentials", Name: "explicit-credentials"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			args := append([]string{"--provider-identity-name=nico-default"}, tc.args...)
			cfg := parseManagerFlags(t, tc.podNamespace, args...)
			require.Equal(t, tc.want, cfg.provider.Credentials)

			// The selected Identity is cached only in the default Secret's namespace.
			byObjects := cfg.managerOptions().Cache.ByObject
			require.Len(t, byObjects, 1)
			var byObject cache.ByObject
			for object, value := range byObjects {
				require.IsType(t, &infrav1.NicoIdentity{}, object)
				byObject = value
			}
			require.Equal(t, []string{tc.want.Namespace}, slices.Collect(maps.Keys(byObject.Namespaces)))
			require.Equal(t, "metadata.name=nico-default", byObject.Field.String())
		})
	}

	t.Run("disabled observation caches no Identities", func(t *testing.T) {
		cfg := parseManagerFlags(t, "")
		require.Empty(t, cfg.managerOptions().Cache.ByObject)
	})
}

func TestManagerWithoutIdentityCRD(t *testing.T) {
	providerCRDs, err := filepath.Glob(filepath.Join("..", "config", "crd", "bases", "*.yaml"))
	require.NoError(t, err)
	withoutIdentity := []string{capiCRDPath(t)}
	for _, path := range providerCRDs {
		if !strings.Contains(path, "nicoidentities") {
			withoutIdentity = append(withoutIdentity, path)
		}
	}
	require.Len(t, withoutIdentity, len(providerCRDs))
	config := startEnvironment(t, withoutIdentity)

	t.Run("disabled observation starts the manager", func(t *testing.T) {
		mgr, cfg := newTestManager(t, config, "")
		require.NoError(t, setupReconcilers(t.Context(), mgr, cfg))
		done := startManager(t, mgr)
		// Every controller must sync before CacheSyncTimeout, or Start returns an error.
		require.Never(t, func() bool {
			select {
			case err := <-done:
				t.Logf("manager stopped: %v", err)
				return true
			default:
				return false
			}
		}, 2*testCacheSyncTimeout, 100*time.Millisecond)
	})

	t.Run("enabled observation reports the missing CRD", func(t *testing.T) {
		cfg := parseManagerFlags(t, "", "--provider-identity-name=nico-default")
		_, err := ctrl.NewManager(config, cfg.managerOptions())
		require.ErrorIs(t, explainManagerError(cfg, err), controllers.ErrNicoIdentityCRDMissing)
	})
}

func TestManagerObservesSelectedIdentity(t *testing.T) {
	config := startEnvironment(t, []string{filepath.Join("..", "config", "crd", "bases"), capiCRDPath(t)})
	c, err := client.New(config, client.Options{Scheme: scheme})
	require.NoError(t, err)
	ctx := t.Context()

	server := fake.New()
	server.SeedClient("identity-client", "identity-secret")
	tenant := nicosdk.NewTenant()
	tenant.SetId("tenant-1")
	tenant.SetOrg("org-1")
	server.SeedTenant("org-1", *tenant)
	endpoint := httptest.NewServer(server.Handler())
	t.Cleanup(endpoint.Close)

	for _, namespace := range []string{"capnico-system", "tenants"} {
		require.NoError(t, c.Create(ctx, &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: namespace}}))
	}
	require.NoError(t, c.Create(ctx, &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Namespace: "capnico-system", Name: "nico-credentials"},
		StringData: map[string]string{
			nico.SecretKeyEndpoint:     endpoint.URL,
			nico.SecretKeyOrgID:        "org-1",
			nico.SecretKeyTokenURL:     endpoint.URL + "/token",
			nico.SecretKeyClientID:     "identity-client",
			nico.SecretKeyClientSecret: "identity-secret",
		},
	}))
	selected := types.NamespacedName{Namespace: "capnico-system", Name: "nico-default"}
	unselected := []types.NamespacedName{
		{Namespace: "capnico-system", Name: "unselected"},
		{Namespace: "tenants", Name: "nico-default"},
	}
	for _, key := range unselected {
		require.NoError(t, c.Create(ctx, newIdentity(key)))
	}
	unselectedObserved := func() bool {
		for _, key := range unselected {
			identity := &infrav1.NicoIdentity{}
			if err := c.Get(ctx, key, identity); err != nil {
				return true
			}
			if len(identity.Status.Conditions) > 0 || identity.Status.LastCheckedTime != nil {
				return true
			}
		}
		return false
	}

	// --namespace restricts cluster and machine watches to tenants; the selected
	// Identity lives with the default Secret in capnico-system.
	mgr, cfg := newTestManager(t, config, "capnico-system", "--namespace=tenants", "--provider-identity-name=nico-default")
	require.NoError(t, setupReconcilers(ctx, mgr, cfg))
	startManager(t, mgr)

	// Until the selected Identity exists, nothing is checked or reported.
	require.Never(t, func() bool {
		return server.TokenRequestCount() != 0 || unselectedObserved()
	}, 2*time.Second, 100*time.Millisecond)

	require.NoError(t, c.Create(ctx, newIdentity(selected)))
	require.EventuallyWithT(t, func(collect *assert.CollectT) {
		identity := &infrav1.NicoIdentity{}
		if !assert.NoError(collect, c.Get(ctx, selected, identity)) {
			return
		}
		ready := meta.FindStatusCondition(identity.Status.Conditions, "Ready")
		if !assert.NotNil(collect, ready) {
			return
		}
		assert.Equal(collect, metav1.ConditionTrue, ready.Status)
		assert.Equal(collect, "ValidationSucceeded", ready.Reason)
		assert.Equal(collect, identity.Generation, ready.ObservedGeneration)
		assert.NotNil(collect, identity.Status.LastCheckedTime)
	}, time.Minute, 100*time.Millisecond)

	// One fresh token for the one selected check; unselected objects get no
	// check, no backend call and no status.
	require.Never(t, func() bool {
		return server.TokenRequestCount() != 1 || unselectedObserved()
	}, 3*time.Second, 100*time.Millisecond)
}

func TestManagerAppliesValidationTimeout(t *testing.T) {
	config := startEnvironment(t, []string{filepath.Join("..", "config", "crd", "bases"), capiCRDPath(t)})
	c, err := client.New(config, client.Options{Scheme: scheme})
	require.NoError(t, err)
	ctx := t.Context()

	server := fake.New()
	server.SeedToken("static-token")
	tenant := nicosdk.NewTenant()
	tenant.SetId("tenant-1")
	tenant.SetOrg("org-1")
	server.SeedTenant("org-1", *tenant)
	// The tenant read outlasts the configured timeout but not the 30-second default.
	handler := server.Handler()
	endpoint := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/tenant/current") {
			select {
			case <-r.Context().Done():
				return
			case <-time.After(5 * time.Second):
			}
		}
		handler.ServeHTTP(w, r)
	}))
	t.Cleanup(endpoint.Close)

	require.NoError(t, c.Create(ctx, &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "capnico-system"}}))
	require.NoError(t, c.Create(ctx, &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Namespace: "capnico-system", Name: "nico-credentials"},
		StringData: map[string]string{
			nico.SecretKeyEndpoint: endpoint.URL,
			nico.SecretKeyOrgID:    "org-1",
			nico.SecretKeyToken:    "static-token",
		},
	}))
	selected := types.NamespacedName{Namespace: "capnico-system", Name: "nico-default"}
	require.NoError(t, c.Create(ctx, newIdentity(selected)))

	mgr, cfg := newTestManager(t, config, "capnico-system",
		"--provider-identity-name=nico-default", "--provider-identity-validation-timeout=500ms")
	require.NoError(t, setupReconcilers(ctx, mgr, cfg))
	startManager(t, mgr)

	require.EventuallyWithT(t, func(collect *assert.CollectT) {
		identity := &infrav1.NicoIdentity{}
		if !assert.NoError(collect, c.Get(ctx, selected, identity)) {
			return
		}
		ready := meta.FindStatusCondition(identity.Status.Conditions, "Ready")
		if !assert.NotNil(collect, ready) {
			return
		}
		assert.Equal(collect, metav1.ConditionUnknown, ready.Status)
		assert.Equal(collect, "ValidationFailed", ready.Reason)
		assert.Equal(collect, "Current-tenant lookup exceeded the validation deadline.", ready.Message)
	}, 30*time.Second, 100*time.Millisecond)
}

func newIdentity(key types.NamespacedName) *infrav1.NicoIdentity {
	return &infrav1.NicoIdentity{
		ObjectMeta: metav1.ObjectMeta{Namespace: key.Namespace, Name: key.Name},
		Spec: infrav1.NicoIdentitySpec{
			CredentialsRef: infrav1.NicoIdentityCredentialsReference{Name: "nico-credentials"},
		},
	}
}

const testCacheSyncTimeout = 5 * time.Second

func newTestManager(
	t *testing.T, config *rest.Config, podNamespace string, args ...string,
) (ctrl.Manager, *managerConfig) {
	t.Helper()
	args = append([]string{"--metrics-bind-address=0", "--health-probe-bind-address=0"}, args...)
	cfg := parseManagerFlags(t, podNamespace, args...)
	options := cfg.managerOptions()
	// Tests start several managers in one process, so controller names repeat.
	options.Controller.SkipNameValidation = new(true)
	options.Controller.CacheSyncTimeout = testCacheSyncTimeout
	mgr, err := ctrl.NewManager(config, options)
	require.NoError(t, err)
	return mgr, cfg
}

func startManager(t *testing.T, mgr ctrl.Manager) <-chan error {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- mgr.Start(ctx) }()
	t.Cleanup(func() {
		cancel()
		<-done
	})
	select {
	case <-mgr.Elected():
	case err := <-done:
		t.Fatalf("manager stopped before starting: %v", err)
	}
	return done
}

func startEnvironment(t *testing.T, crdPaths []string) *rest.Config {
	t.Helper()
	environment := &envtest.Environment{CRDDirectoryPaths: crdPaths, ErrorIfCRDPathMissing: true}
	config, err := environment.Start()
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, environment.Stop()) })
	return config
}

// capiCRDPath resolves the Cluster API CRDs from the module cache, matching the
// version go.mod builds against.
func capiCRDPath(t *testing.T) string {
	t.Helper()
	out, err := exec.Command("go", "list", "-m", "-f", "{{.Dir}}", "sigs.k8s.io/cluster-api").Output()
	require.NoError(t, err)
	return filepath.Join(strings.TrimSpace(string(out)), "config", "crd", "bases")
}
