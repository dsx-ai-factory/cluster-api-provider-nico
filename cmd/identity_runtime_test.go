// SPDX-FileCopyrightText: Copyright (c) 2026 NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package main

// Runtime scenarios for the NicoIdentity controller. Each test starts the
// manager through bindFlags, managerOptions and setupReconcilers against
// envtest and the authenticated HTTP fake, then changes its inputs while it
// runs. Settled states are compared with goldens under
// testdata/identity-runtime. Timing, request counts, stale results that must
// never appear and unchanged timestamps are asserted directly, because a
// masked snapshot cannot show them.

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	nicosdk "github.com/NVIDIA/infra-controller/rest-api/sdk/standard"
	"github.com/onsi/gomega"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/rest"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
	ctrlmetrics "sigs.k8s.io/controller-runtime/pkg/metrics"
	"sigs.k8s.io/yaml"

	infrav1 "github.com/dsx-ai-factory/cluster-api-provider-nico/api/v1alpha1"
	"github.com/dsx-ai-factory/cluster-api-provider-nico/internal/fake"
	"github.com/dsx-ai-factory/cluster-api-provider-nico/internal/nico"
	"github.com/dsx-ai-factory/cluster-api-provider-nico/pkg/test/fixture"
)

const (
	// runtimeCheckInterval replaces the five-minute recheck so scenarios run in seconds.
	runtimeCheckInterval = 2 * time.Second
	runtimeTimeout       = 30 * time.Second
	runtimeNamespace     = "capnico-system"
	runtimeSecretName    = "nico-credentials"
	runtimeClientID      = "identity-client"
	runtimeClientSecret  = "identity-secret"
	runtimeOrg           = "org-1"
	runtimeManagerUser   = "capnico-runtime-manager"
	identityController   = "nicoidentity"
)

var (
	runtimeIdentity = types.NamespacedName{Namespace: runtimeNamespace, Name: "nico-default"}
	runtimeSecret   = types.NamespacedName{Namespace: runtimeNamespace, Name: runtimeSecretName}
)

// R1: the controller rechecks once per interval, and its own status writes
// cause no extra checks.
func TestIdentityRuntimeRechecksWithoutHotLoop(t *testing.T) {
	rt := newIdentityRuntime(t)
	rt.createIdentity()
	rt.startManager(rt.config)
	first := rt.waitForReady(metav1.ConditionTrue, "ValidationSucceeded")

	const checks = 4
	require.EventuallyWithT(t, func(collect *assert.CollectT) {
		_, reads := rt.backend.counts()
		assert.GreaterOrEqual(collect, len(reads), checks)
	}, checks*runtimeCheckInterval+runtimeTimeout, 100*time.Millisecond)

	// Every check takes one fresh token and one tenant read. A token request can
	// precede its tenant read when the counts are taken.
	tokens, reads := rt.backend.counts()
	assert.Contains(t, []int{len(reads), len(reads) + 1}, tokens)
	for i := 1; i < len(reads); i++ {
		assert.GreaterOrEqual(t, reads[i].Sub(reads[i-1]), runtimeCheckInterval,
			"tenant read %d followed read %d within the check interval", i, i-1)
	}

	// The check time advances while an unchanged Ready keeps its transition time.
	current := rt.getIdentity()
	assert.True(t, current.Status.LastCheckedTime.After(first.Status.LastCheckedTime.Time))
	assert.True(t, readyCondition(first).LastTransitionTime.Equal(&readyCondition(current).LastTransitionTime))
	rt.expectGoldens("recheck")
}

// R2: issuer-side revocation and the delivery of rotated credentials both
// reach status at a scheduled check, without a manager restart.
func TestIdentityRuntimeFollowsCredentialRotation(t *testing.T) {
	rt := newIdentityRuntime(t)
	rt.createIdentity()
	rt.startManager(rt.config)
	rt.waitForReady(metav1.ConditionTrue, "ValidationSucceeded")

	// The issuer accepts only the rotated secret; tokens it already issued stay valid.
	rt.server.SeedClient(runtimeClientID, "rotated-secret")
	rt.waitForReady(metav1.ConditionFalse, "AuthenticationFailed")
	rt.expectGoldens("rotation-revoked")

	rt.setClientSecret("rotated-secret")
	rt.waitForReady(metav1.ConditionTrue, "ValidationSucceeded")
	rt.expectGoldens("rotation-recovered")
}

// R3: deleting and recreating the Secret or the Identity recovers at a later
// check, and a recreated Identity gets its own observation.
func TestIdentityRuntimeRecoversFromDeletion(t *testing.T) {
	rt := newIdentityRuntime(t)
	rt.createIdentity()
	rt.startManager(rt.config)
	rt.waitForReady(metav1.ConditionTrue, "ValidationSucceeded")

	require.NoError(t, rt.admin.Delete(t.Context(), rt.secret(runtimeClientSecret)))
	rt.waitForReady(metav1.ConditionFalse, "CredentialsNotFound")
	rt.expectGoldens("secret-deleted")

	require.NoError(t, rt.admin.Create(t.Context(), rt.secret(runtimeClientSecret)))
	rt.waitForReady(metav1.ConditionTrue, "ValidationSucceeded")
	rt.expectGoldens("secret-recreated")

	previous := rt.getIdentity()
	require.NoError(t, rt.admin.Delete(t.Context(), previous))
	require.EventuallyWithT(t, func(collect *assert.CollectT) {
		err := rt.admin.Get(t.Context(), runtimeIdentity, &infrav1.NicoIdentity{})
		assert.True(collect, apierrors.IsNotFound(err))
	}, runtimeTimeout, 100*time.Millisecond)
	rt.createIdentity()

	recreated := rt.waitForReady(metav1.ConditionTrue, "ValidationSucceeded")
	assert.NotEqual(t, previous.UID, recreated.UID)
	assert.Equal(t, int64(1), readyCondition(recreated).ObservedGeneration)
	rt.expectGoldens("identity-recreated")
}

// R4: a check whose Secret is replaced while it is in flight is discarded,
// so its stale success is never published.
func TestIdentityRuntimeDiscardsResultForReplacedSecret(t *testing.T) {
	rt := newIdentityRuntime(t)
	readies := rt.recordReady()
	held, release := rt.backend.holdTenantReads()
	rt.createIdentity()
	rt.startManager(rt.config)

	rt.waitHeld(held)
	rt.setClientSecret("replacement-secret")
	release()

	rt.waitForReady(metav1.ConditionFalse, "AuthenticationFailed")
	for _, ready := range readies() {
		assert.NotEqual(t, metav1.ConditionTrue, ready.Status, "a superseded success was published: %+v", ready)
	}
	rt.expectGoldens("superseded-secret")
}

// R4: a check whose Identity spec changes while it is in flight is discarded,
// and the final status carries the new generation and result.
func TestIdentityRuntimeDiscardsResultForChangedSpec(t *testing.T) {
	rt := newIdentityRuntime(t)
	readies := rt.recordReady()
	held, release := rt.backend.holdTenantReads()
	rt.createIdentity()
	rt.startManager(rt.config)

	rt.waitHeld(held)
	identity := rt.getIdentity()
	identity.Spec.CredentialsRef.Name = "other-credentials"
	require.NoError(t, rt.admin.Update(t.Context(), identity))
	release()

	current := rt.waitForReady(metav1.ConditionFalse, "InvalidConfiguration")
	assert.Equal(t, int64(2), current.Generation)
	assert.Equal(t, int64(2), readyCondition(current).ObservedGeneration)
	for _, ready := range readies() {
		assert.NotEqual(t, int64(1), ready.ObservedGeneration, "a superseded generation-1 result was published: %+v", ready)
	}
	rt.expectGoldens("superseded-spec")
}

// R5: when RBAC denies the Secret read, the check completes as Unknown,
// retries on schedule and never calls NICo.
func TestIdentityRuntimeReportsSecretReadDenial(t *testing.T) {
	rt := newIdentityRuntime(t)
	rt.createIdentity()
	config := rt.restrictedConfig("", "secrets", "get")

	restricted, err := client.New(config, client.Options{Scheme: scheme})
	require.NoError(t, err)
	err = restricted.Get(t.Context(), runtimeSecret, &corev1.Secret{})
	require.True(t, apierrors.IsForbidden(err), "the manager identity can still read the Secret: %v", err)

	rt.startManager(config)
	first := rt.waitForReady(metav1.ConditionUnknown, "SecretReadFailed")
	require.NotNil(t, first.Status.LastCheckedTime)
	require.EventuallyWithT(t, func(collect *assert.CollectT) {
		current := rt.getIdentity()
		assert.True(collect, current.Status.LastCheckedTime.After(first.Status.LastCheckedTime.Time))
	}, 2*runtimeCheckInterval+runtimeTimeout, 100*time.Millisecond)

	tokens, reads := rt.backend.counts()
	assert.Zero(t, tokens)
	assert.Empty(t, reads)
	rt.expectGoldens("secret-read-denied")
}

// R6: when RBAC denies the status write, the reconcile fails and the
// previously persisted result and check time stay unchanged.
func TestIdentityRuntimeKeepsStatusWhenWriteFails(t *testing.T) {
	rt := newIdentityRuntime(t)
	rt.createIdentity()
	previous := metav1.NewTime(time.Date(2026, time.January, 2, 3, 4, 5, 0, time.UTC))
	identity := rt.getIdentity()
	identity.Status = infrav1.NicoIdentityStatus{
		Conditions: []metav1.Condition{{
			Type:               "Ready",
			Status:             metav1.ConditionTrue,
			ObservedGeneration: 1,
			LastTransitionTime: previous,
			Reason:             "ValidationSucceeded",
			Message:            "Authentication and current-tenant lookup succeeded.",
		}},
		LastCheckedTime: &previous,
	}
	require.NoError(t, rt.admin.Status().Update(t.Context(), identity))

	// A publishable check would now report AuthenticationFailed.
	rt.server.SeedClient(runtimeClientID, "rotated-secret")
	config := rt.restrictedConfig("infrastructure.cluster.x-k8s.io", "nicoidentities/status", "update", "patch")
	errorsBefore := reconcileErrors(t, identityController)
	rt.startManager(config)

	require.EventuallyWithT(t, func(collect *assert.CollectT) {
		tokens, _ := rt.backend.counts()
		assert.GreaterOrEqual(collect, tokens, 2)
		assert.GreaterOrEqual(collect, reconcileErrors(t, identityController)-errorsBefore, 2.0)
	}, runtimeTimeout, 100*time.Millisecond)

	current := rt.getIdentity()
	ready := readyCondition(current)
	assert.Equal(t, metav1.ConditionTrue, ready.Status)
	assert.Equal(t, "ValidationSucceeded", ready.Reason)
	assert.True(t, previous.Equal(&ready.LastTransitionTime))
	assert.True(t, previous.Equal(current.Status.LastCheckedTime))
	rt.expectGoldens("status-write-denied")
}

// identityRuntime is one envtest API server, the authenticated fake behind a
// recording handler, and the selected Identity's namespace and Secret.
type identityRuntime struct {
	t           *testing.T
	environment *envtest.Environment
	config      *rest.Config
	admin       client.WithWatch
	server      *fake.Server
	backend     *recordingBackend
	endpoint    string
}

func newIdentityRuntime(t *testing.T) *identityRuntime {
	t.Helper()
	environment := &envtest.Environment{
		CRDDirectoryPaths:     []string{filepath.Join("..", "config", "crd", "bases"), capiCRDPath(t)},
		ErrorIfCRDPathMissing: true,
	}
	config, err := environment.Start()
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, environment.Stop()) })
	admin, err := client.NewWithWatch(config, client.Options{Scheme: scheme})
	require.NoError(t, err)

	server := fake.New()
	server.SeedClient(runtimeClientID, runtimeClientSecret)
	tenant := nicosdk.NewTenant()
	tenant.SetId("tenant-1")
	tenant.SetOrg(runtimeOrg)
	server.SeedTenant(runtimeOrg, *tenant)
	backend := &recordingBackend{handler: server.Handler()}
	endpoint := httptest.NewServer(backend)
	t.Cleanup(endpoint.Close)

	rt := &identityRuntime{
		t:           t,
		environment: environment,
		config:      config,
		admin:       admin,
		server:      server,
		backend:     backend,
		endpoint:    endpoint.URL,
	}
	require.NoError(t, admin.Create(t.Context(), &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: runtimeNamespace}}))
	require.NoError(t, admin.Create(t.Context(), rt.secret(runtimeClientSecret)))
	return rt
}

// startManager runs the full manager as config's user, selecting the runtime
// Identity and rechecking every runtimeCheckInterval.
func (rt *identityRuntime) startManager(config *rest.Config) {
	rt.t.Helper()
	mgr, cfg := newTestManager(rt.t, config, runtimeNamespace, "--provider-identity-name="+runtimeIdentity.Name)
	cfg.identityCheckInterval = runtimeCheckInterval
	require.NoError(rt.t, setupReconcilers(rt.t.Context(), mgr, cfg))
	startManager(rt.t, mgr)
}

func (rt *identityRuntime) secret(clientSecret string) *corev1.Secret {
	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Namespace: runtimeSecret.Namespace, Name: runtimeSecret.Name},
		StringData: map[string]string{
			nico.SecretKeyEndpoint:     rt.endpoint,
			nico.SecretKeyOrgID:        runtimeOrg,
			nico.SecretKeyTokenURL:     rt.endpoint + "/token",
			nico.SecretKeyClientID:     runtimeClientID,
			nico.SecretKeyClientSecret: clientSecret,
		},
	}
}

func (rt *identityRuntime) setClientSecret(clientSecret string) {
	rt.t.Helper()
	secret := &corev1.Secret{}
	require.NoError(rt.t, rt.admin.Get(rt.t.Context(), runtimeSecret, secret))
	secret.Data[nico.SecretKeyClientSecret] = []byte(clientSecret)
	require.NoError(rt.t, rt.admin.Update(rt.t.Context(), secret))
}

func (rt *identityRuntime) createIdentity() {
	rt.t.Helper()
	require.NoError(rt.t, rt.admin.Create(rt.t.Context(), newIdentity(runtimeIdentity)))
}

func (rt *identityRuntime) getIdentity() *infrav1.NicoIdentity {
	rt.t.Helper()
	identity := &infrav1.NicoIdentity{}
	require.NoError(rt.t, rt.admin.Get(rt.t.Context(), runtimeIdentity, identity))
	return identity
}

// waitForReady waits for a completed check at the current generation with the
// given outcome and returns the Identity.
func (rt *identityRuntime) waitForReady(status metav1.ConditionStatus, reason string) *infrav1.NicoIdentity {
	rt.t.Helper()
	var identity *infrav1.NicoIdentity
	require.EventuallyWithT(rt.t, func(collect *assert.CollectT) {
		current := &infrav1.NicoIdentity{}
		if !assert.NoError(collect, rt.admin.Get(rt.t.Context(), runtimeIdentity, current)) {
			return
		}
		ready := readyCondition(current)
		if !assert.NotNil(collect, ready) {
			return
		}
		assert.Equal(collect, status, ready.Status)
		assert.Equal(collect, reason, ready.Reason)
		assert.Equal(collect, current.Generation, ready.ObservedGeneration)
		if assert.NotNil(collect, current.Status.LastCheckedTime) {
			assert.False(collect, current.Status.LastCheckedTime.Before(&ready.LastTransitionTime))
		}
		identity = current
	}, runtimeTimeout, 100*time.Millisecond)
	return identity
}

func (rt *identityRuntime) waitHeld(held <-chan struct{}) {
	rt.t.Helper()
	select {
	case <-held:
	case <-time.After(runtimeTimeout):
		rt.t.Fatal("no tenant read reached the fake")
	}
}

// recordReady watches the runtime Identity and returns every Ready condition
// it has published so far, including ones a later write replaced.
func (rt *identityRuntime) recordReady() func() []metav1.Condition {
	rt.t.Helper()
	watcher, err := rt.admin.Watch(rt.t.Context(), &infrav1.NicoIdentityList{}, client.InNamespace(runtimeNamespace))
	require.NoError(rt.t, err)
	rt.t.Cleanup(watcher.Stop)

	var mu sync.Mutex
	var readies []metav1.Condition
	go func() {
		for event := range watcher.ResultChan() {
			identity, ok := event.Object.(*infrav1.NicoIdentity)
			if !ok || event.Type == watch.Deleted || identity.Name != runtimeIdentity.Name {
				continue
			}
			if ready := readyCondition(identity); ready != nil {
				mu.Lock()
				readies = append(readies, *ready)
				mu.Unlock()
			}
		}
	}()
	return func() []metav1.Condition {
		mu.Lock()
		defer mu.Unlock()
		return slices.Clone(readies)
	}
}

// restrictedConfig binds a new user to the generated manager ClusterRole minus
// the denied verbs on one resource, and returns that user's client config.
func (rt *identityRuntime) restrictedConfig(group, resource string, denied ...string) *rest.Config {
	rt.t.Helper()
	content, err := os.ReadFile(filepath.Join("..", "config", "rbac", "role.yaml"))
	require.NoError(rt.t, err)
	role := &rbacv1.ClusterRole{}
	require.NoError(rt.t, yaml.Unmarshal(content, role))
	rules, removed := withoutVerbs(role.Rules, group, resource, denied...)
	require.ElementsMatch(rt.t, denied, removed, "the generated manager role does not grant every denied verb")

	ctx := rt.t.Context()
	require.NoError(rt.t, rt.admin.Create(ctx, &rbacv1.ClusterRole{
		ObjectMeta: metav1.ObjectMeta{Name: runtimeManagerUser},
		Rules:      rules,
	}))
	require.NoError(rt.t, rt.admin.Create(ctx, &rbacv1.ClusterRoleBinding{
		ObjectMeta: metav1.ObjectMeta{Name: runtimeManagerUser},
		RoleRef:    rbacv1.RoleRef{APIGroup: rbacv1.GroupName, Kind: "ClusterRole", Name: runtimeManagerUser},
		Subjects:   []rbacv1.Subject{{APIGroup: rbacv1.GroupName, Kind: rbacv1.UserKind, Name: runtimeManagerUser}},
	}))
	user, err := rt.environment.AddUser(envtest.User{Name: runtimeManagerUser}, nil)
	require.NoError(rt.t, err)
	return user.Config()
}

// expectGoldens compares the Identities and the fake's state with
// testdata/identity-runtime/<name>. The fake dump lists NICo writes; the
// Identity controller must make none.
func (rt *identityRuntime) expectGoldens(name string) {
	rt.t.Helper()
	dir := filepath.Join("testdata", "identity-runtime", name)
	g := gomega.NewWithT(rt.t)
	objects, err := fixture.Snapshot(rt.t.Context(), rt.admin, scheme, &infrav1.NicoIdentityList{})
	require.NoError(rt.t, err)
	fixture.ExpectGolden(g, filepath.Join(dir, "expected_objects.yaml"), objects)
	dump, err := rt.server.Dump()
	require.NoError(rt.t, err)
	fixture.ExpectGolden(g, filepath.Join(dir, "expected_nico.yaml"), dump)
}

// withoutVerbs removes the denied verbs on one group and resource. A rule that
// also grants other resources is split, so their permissions are unchanged.
func withoutVerbs(rules []rbacv1.PolicyRule, group, resource string, denied ...string) ([]rbacv1.PolicyRule, []string) {
	var kept []rbacv1.PolicyRule
	var removed []string
	for _, rule := range rules {
		if !slices.Equal(rule.APIGroups, []string{group}) || !slices.Contains(rule.Resources, resource) {
			kept = append(kept, rule)
			continue
		}
		others := slices.DeleteFunc(slices.Clone(rule.Resources), func(r string) bool { return r == resource })
		if len(others) > 0 {
			split := *rule.DeepCopy()
			split.Resources = others
			kept = append(kept, split)
		}
		verbs := slices.DeleteFunc(slices.Clone(rule.Verbs), func(verb string) bool {
			if slices.Contains(denied, verb) {
				removed = append(removed, verb)
				return true
			}
			return false
		})
		if len(verbs) > 0 {
			narrowed := *rule.DeepCopy()
			narrowed.Resources = []string{resource}
			narrowed.Verbs = verbs
			kept = append(kept, narrowed)
		}
	}
	return kept, removed
}

func readyCondition(identity *infrav1.NicoIdentity) *metav1.Condition {
	return meta.FindStatusCondition(identity.Status.Conditions, "Ready")
}

// reconcileErrors reads controller-runtime's reconcile error counter. Managers
// in this package share the counter, so tests compare before and after.
func reconcileErrors(t *testing.T, controller string) float64 {
	t.Helper()
	families, err := ctrlmetrics.Registry.Gather()
	require.NoError(t, err)
	for _, family := range families {
		if family.GetName() != "controller_runtime_reconcile_errors_total" {
			continue
		}
		for _, metric := range family.GetMetric() {
			for _, label := range metric.GetLabel() {
				if label.GetName() == "controller" && label.GetValue() == controller {
					return metric.GetCounter().GetValue()
				}
			}
		}
	}
	return 0
}

// recordingBackend counts every token and tenant request the fake receives,
// including rejected ones, and can hold tenant reads in flight.
type recordingBackend struct {
	handler http.Handler

	mu          sync.Mutex
	tokens      int
	tenantReads []time.Time
	hold        chan struct{}
	held        chan struct{}
}

func (b *recordingBackend) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch {
	case r.URL.Path == "/token":
		b.mu.Lock()
		b.tokens++
		b.mu.Unlock()
	case strings.HasSuffix(r.URL.Path, "/tenant/current"):
		b.mu.Lock()
		b.tenantReads = append(b.tenantReads, time.Now())
		hold, held := b.hold, b.held
		b.mu.Unlock()
		if hold != nil {
			held <- struct{}{}
			select {
			case <-hold:
			case <-r.Context().Done():
				return
			}
		}
	}
	b.handler.ServeHTTP(w, r)
}

// holdTenantReads makes tenant reads wait until release. Each held read sends
// on the returned channel.
func (b *recordingBackend) holdTenantReads() (<-chan struct{}, func()) {
	b.mu.Lock()
	defer b.mu.Unlock()
	hold := make(chan struct{})
	held := make(chan struct{}, 16)
	b.hold, b.held = hold, held
	return held, func() {
		b.mu.Lock()
		b.hold, b.held = nil, nil
		b.mu.Unlock()
		close(hold)
	}
}

func (b *recordingBackend) counts() (int, []time.Time) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.tokens, slices.Clone(b.tenantReads)
}
