// SPDX-FileCopyrightText: Copyright (c) 2026 NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"flag"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
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
	clusterv1 "sigs.k8s.io/cluster-api/api/core/v1beta2"
	ctrl "sigs.k8s.io/controller-runtime"
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
	cfg := parseManagerFlags(t, "")
	require.Equal(t, types.NamespacedName{Namespace: "capnico-system", Name: "nico-credentials"},
		cfg.provider.Credentials, "without POD_NAMESPACE or flags")

	cfg = parseManagerFlags(t, "provider-system")
	require.Equal(t, types.NamespacedName{Namespace: "provider-system", Name: "nico-credentials"},
		cfg.provider.Credentials, "POD_NAMESPACE sets the default namespace")

	cfg = parseManagerFlags(t, "provider-system",
		"--provider-credentials-namespace=credentials",
		"--provider-credentials-secret-name=explicit-credentials",
	)
	require.Equal(t, types.NamespacedName{Namespace: "credentials", Name: "explicit-credentials"},
		cfg.provider.Credentials, "explicit flags win over POD_NAMESPACE")
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

	// The Identity controller is skipped, so no controller waits on an informer
	// for the missing kind.
	mgr, cfg := newTestManager(t, config, "")
	require.NoError(t, setupReconcilers(t.Context(), mgr, cfg))
	run := startManager(t, mgr)
	// Every controller must sync before CacheSyncTimeout, or Start returns an error.
	require.Never(t, func() bool {
		select {
		case <-run.done:
			t.Logf("manager stopped: %v", run.err)
			return true
		default:
			return false
		}
	}, 2*testCacheSyncTimeout, 100*time.Millisecond)

	reconciler := &controllers.NicoIdentityReconciler{Client: mgr.GetClient()}
	require.ErrorIs(t, reconciler.SetupWithManager(t.Context(), mgr), controllers.ErrNicoIdentityCRDMissing)
}

// TestManagerObservesIdentities shows the manager checking Identities the way it
// reconciles NicoClusters: every namespace by default, only --namespace when
// set, and only labeled Identities when --watch-filter is set. Subtests share
// one API server, so each uses its own namespaces and deletes its Identities.
func TestManagerObservesIdentities(t *testing.T) {
	config := startEnvironment(t, []string{filepath.Join("..", "config", "crd", "bases"), capiCRDPath(t)})
	c, err := client.New(config, client.Options{Scheme: scheme})
	require.NoError(t, err)

	t.Run("every namespace by default", func(t *testing.T) {
		server, endpoint := newIdentityBackend(t)
		first := createScopedIdentity(t, c, endpoint, "every-a", nil)
		second := createScopedIdentity(t, c, endpoint, "every-b", nil)
		startIdentityManager(t, config)

		expectIdentitiesReady(t, c, first, second)
		expectOnlyChecked(t, c, server, 2)
	})

	t.Run("--namespace limits the scope", func(t *testing.T) {
		server, endpoint := newIdentityBackend(t)
		watched := createScopedIdentity(t, c, endpoint, "scope-in", nil)
		outside := createScopedIdentity(t, c, endpoint, "scope-out", nil)
		startIdentityManager(t, config, "--namespace="+watched.Namespace)

		expectIdentitiesReady(t, c, watched)
		expectOnlyChecked(t, c, server, 1, outside)
	})

	t.Run("--watch-filter limits the scope to labeled Identities", func(t *testing.T) {
		server, endpoint := newIdentityBackend(t)
		labeled := createScopedIdentity(t, c, endpoint, "filter-labeled", map[string]string{clusterv1.WatchLabel: "team-a"})
		unlabeled := createScopedIdentity(t, c, endpoint, "filter-unlabeled", nil)
		startIdentityManager(t, config, "--watch-filter=team-a")

		expectIdentitiesReady(t, c, labeled)
		expectOnlyChecked(t, c, server, 1, unlabeled)

		// Adding the label brings an Identity into the filter without a spec change.
		identity := &infrav1.NicoIdentity{}
		require.NoError(t, c.Get(t.Context(), unlabeled, identity))
		identity.Labels = map[string]string{clusterv1.WatchLabel: "team-a"}
		require.NoError(t, c.Update(t.Context(), identity))
		expectIdentitiesReady(t, c, unlabeled)
		expectOnlyChecked(t, c, server, 2)
	})
}

// newIdentityBackend serves a fake that accepts one OAuth client.
func newIdentityBackend(t *testing.T) (*fake.Server, string) {
	t.Helper()
	server := fake.New()
	server.SeedClient("identity-client", "identity-secret")
	tenant := nicosdk.NewTenant()
	tenant.SetId("tenant-1")
	tenant.SetOrg("org-1")
	server.SeedTenant("org-1", *tenant)
	endpoint := httptest.NewServer(server.Handler())
	t.Cleanup(endpoint.Close)
	return server, endpoint.URL
}

// createScopedIdentity creates a namespace holding a Secret for endpoint and an
// Identity naming it. The Identity is deleted after the subtest's manager stops.
func createScopedIdentity(
	t *testing.T, c client.Client, endpoint, namespace string, labels map[string]string,
) types.NamespacedName {
	t.Helper()
	ctx := t.Context()
	require.NoError(t, c.Create(ctx, &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: namespace}}))
	require.NoError(t, c.Create(ctx, &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Namespace: namespace, Name: "nico-credentials"},
		StringData: map[string]string{
			nico.SecretKeyEndpoint:     endpoint,
			nico.SecretKeyOrgID:        "org-1",
			nico.SecretKeyTokenURL:     endpoint + "/token",
			nico.SecretKeyClientID:     "identity-client",
			nico.SecretKeyClientSecret: "identity-secret",
		},
	}))
	key := types.NamespacedName{Namespace: namespace, Name: "nico-default"}
	identity := newIdentity(key)
	identity.Labels = labels
	require.NoError(t, c.Create(ctx, identity))
	t.Cleanup(func() { require.NoError(t, client.IgnoreNotFound(c.Delete(context.Background(), identity))) })
	return key
}

func startIdentityManager(t *testing.T, config *rest.Config, args ...string) {
	t.Helper()
	mgr, cfg := newTestManager(t, config, "capnico-system", args...)
	require.NoError(t, setupReconcilers(t.Context(), mgr, cfg))
	startManager(t, mgr)
}

func expectIdentitiesReady(t *testing.T, c client.Client, keys ...types.NamespacedName) {
	t.Helper()
	require.EventuallyWithT(t, func(collect *assert.CollectT) {
		for _, key := range keys {
			identity := &infrav1.NicoIdentity{}
			if !assert.NoError(collect, c.Get(t.Context(), key, identity)) {
				continue
			}
			ready := meta.FindStatusCondition(identity.Status.Conditions, "Ready")
			if !assert.NotNil(collect, ready, "%s has no Ready condition", key) {
				continue
			}
			assert.Equal(collect, metav1.ConditionTrue, ready.Status)
			assert.Equal(collect, "ValidationSucceeded", ready.Reason)
			assert.Equal(collect, identity.Generation, ready.ObservedGeneration)
			assert.NotNil(collect, identity.Status.LastCheckedTime)
		}
	}, time.Minute, 100*time.Millisecond)
}

// expectOnlyChecked shows that the fake minted exactly tokens fresh tokens, one
// per in-scope check, and that the ignored Identities get no status.
func expectOnlyChecked(
	t *testing.T, c client.Client, server *fake.Server, tokens int, ignored ...types.NamespacedName,
) {
	t.Helper()
	require.Never(t, func() bool {
		if server.TokenRequestCount() != tokens {
			return true
		}
		for _, key := range ignored {
			identity := &infrav1.NicoIdentity{}
			if err := c.Get(t.Context(), key, identity); err != nil {
				return true
			}
			if len(identity.Status.Conditions) > 0 || identity.Status.LastCheckedTime != nil {
				return true
			}
		}
		return false
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
	key := types.NamespacedName{Namespace: "capnico-system", Name: "nico-default"}
	require.NoError(t, c.Create(ctx, newIdentity(key)))

	mgr, cfg := newTestManager(t, config, "capnico-system", "--provider-identity-validation-timeout=500ms")
	require.NoError(t, setupReconcilers(ctx, mgr, cfg))
	startManager(t, mgr)

	require.EventuallyWithT(t, func(collect *assert.CollectT) {
		identity := &infrav1.NicoIdentity{}
		if !assert.NoError(collect, c.Get(ctx, key, identity)) {
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

// managerStopTimeout bounds how long cleanup waits for a canceled manager, so a
// manager that never stops fails the test instead of hanging it.
const managerStopTimeout = time.Minute

// managerRun reports a started manager's exit. done closes when Start returns,
// so any number of waiters observe the exit; err is Start's result and may be
// read once done is closed.
type managerRun struct {
	done chan struct{}
	err  error
}

func startManager(t *testing.T, mgr ctrl.Manager) *managerRun {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	run := &managerRun{done: make(chan struct{})}
	go func() {
		run.err = mgr.Start(ctx)
		close(run.done)
	}()
	t.Cleanup(func() {
		cancel()
		select {
		case <-run.done:
		case <-time.After(managerStopTimeout):
			t.Errorf("manager did not stop within %s", managerStopTimeout)
		}
	})
	select {
	case <-mgr.Elected():
	case <-run.done:
		t.Fatalf("manager stopped before starting: %v", run.err)
	}
	return run
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
