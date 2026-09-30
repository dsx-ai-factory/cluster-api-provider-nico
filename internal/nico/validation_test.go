// SPDX-FileCopyrightText: Copyright (c) 2026 NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package nico

import (
	"bytes"
	"context"
	"encoding/pem"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/onsi/gomega"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrllog "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	"sigs.k8s.io/yaml"

	"github.com/dsx-ai-factory/cluster-api-provider-nico/internal/fake"
	"github.com/dsx-ai-factory/cluster-api-provider-nico/internal/test/matchers"
)

func TestValidateCredentialsResults(t *testing.T) {
	cases := []struct {
		name      string
		oauth     bool
		fields    map[string]string
		path      string
		code      int
		body      string
		noRequest bool
	}{
		{name: "static-success"},
		{name: "oauth-success", oauth: true},
		{name: "oauth-secret-shape", oauth: true, fields: map[string]string{
			SecretKeyScope: "tenant:read", SecretKeyInsecureSkipTLSVerify: strconv.FormatBool(false), SecretKeyAPIName: "nico",
		}},
		{name: "unintended-valid-principal", fields: map[string]string{SecretKeyToken: "other-principal-token"}},
		{name: "missing-endpoint", fields: map[string]string{SecretKeyEndpoint: ""}, noRequest: true},
		{name: "missing-org", fields: map[string]string{SecretKeyOrgID: ""}, noRequest: true},
		{name: "missing-auth", fields: map[string]string{SecretKeyToken: ""}, noRequest: true},
		{name: "incomplete-oauth", oauth: true, fields: map[string]string{SecretKeyClientSecret: ""}, noRequest: true},
		{name: "mixed-auth", oauth: true, fields: map[string]string{SecretKeyToken: "synthetic-token"}, noRequest: true},
		{name: "invalid-tls-option", fields: map[string]string{SecretKeyInsecureSkipTLSVerify: "value-SENTINEL"}, noRequest: true},
		{name: "invalid-ca", fields: map[string]string{SecretKeyCA: "ca-SENTINEL"}, noRequest: true},
		{name: "relative-endpoint", fields: map[string]string{SecretKeyEndpoint: "/endpoint-SENTINEL"}, noRequest: true},
		{name: "invalid-endpoint", fields: map[string]string{SecretKeyEndpoint: "https://%endpoint-SENTINEL"}, noRequest: true},
		{name: "unsupported-endpoint", fields: map[string]string{SecretKeyEndpoint: "file:///endpoint-SENTINEL"}, noRequest: true},
		{name: "relative-token-url", oauth: true, fields: map[string]string{SecretKeyTokenURL: "/issuer-SENTINEL"}, noRequest: true},
		{name: "rejected-static", fields: map[string]string{SecretKeyToken: "rejected-SENTINEL"}},
		{name: "rejected-oauth", oauth: true, fields: map[string]string{SecretKeyClientSecret: "rejected-SENTINEL"}, path: "/token"},
		{name: "issuer-invalid-client-400", oauth: true, path: "/token", code: 400, body: `{"error":"invalid_client","error_description":"body-SENTINEL"}`},
		{name: "issuer-unrecognized-401", oauth: true, path: "/token", code: 401, body: `{"error":"issuer_error","error_description":"invalid_client body-SENTINEL"}`},
		{name: "issuer-forbidden", oauth: true, path: "/token", code: 403, body: `{"error":"invalid_client"}`},
		{name: "issuer-outage", oauth: true, path: "/token", code: 503, body: `{"error":"invalid_client","error_description":"body-SENTINEL"}`},
		{name: "issuer-malformed", oauth: true, path: "/token", code: 200, body: "body-SENTINEL"},
		{name: "issuer-no-token", oauth: true, path: "/token", code: 200, body: `{}`},
		{name: "issuer-malformed-401", oauth: true, path: "/token", code: 401, body: "invalid_client body-SENTINEL"},
		{name: "backend-rejected", oauth: true, code: 401, body: "body-SENTINEL"},
		{name: "backend-forbidden", code: 403, body: `{"message":"body-SENTINEL"}`},
		{name: "backend-forbidden-malformed", code: 403, body: "body-SENTINEL"},
		{name: "backend-outage", code: 503, body: `{"message":"unauthorized body-SENTINEL"}`},
		{name: "backend-bad-request", code: 400, body: `{"message":"body-SENTINEL"}`},
		{name: "backend-not-found", code: 404, body: `{"message":"body-SENTINEL"}`},
		{name: "backend-malformed", code: 200, body: "body-SENTINEL"},
		{name: "missing-tenant-id", code: 200, body: `{"name":"body-SENTINEL"}`},
		{name: "null-tenant", code: 200, body: `null`},
	}
	results := map[string]CredentialValidation{}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			api := fake.New()
			api.SeedToken("synthetic-token")
			if tc.oauth {
				api.SeedClient("client-id", "client-secret")
			}
			if tc.name == "unintended-valid-principal" {
				api.SeedToken("other-principal-token")
			}
			before, err := api.Dump()
			require.NoError(t, err)
			var requests []string
			var mu sync.Mutex
			handler := api.Handler()
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				mu.Lock()
				requests = append(requests, r.Method+" "+r.URL.Path)
				mu.Unlock()
				if tc.code != 0 && ((tc.path == "/token") == (r.URL.Path == "/token")) {
					w.Header().Set("Content-Type", "application/json")
					w.WriteHeader(tc.code)
					_, _ = io.WriteString(w, tc.body)
					return
				}
				handler.ServeHTTP(w, r)
			}))
			t.Cleanup(server.Close)
			secret := validationSecret(server.URL, tc.oauth)
			for key, value := range tc.fields {
				secret.Data[key] = []byte(value)
			}
			original := secret.DeepCopy()
			results[tc.name] = ValidateCredentials(t.Context(), secret)
			assert.Equal(t, original, secret, "validation must not mutate its Secret snapshot")
			var wantRequests []string
			if !tc.noRequest {
				if tc.oauth {
					wantRequests = append(wantRequests, "POST /token")
				}
				if tc.path != "/token" {
					wantRequests = append(wantRequests, "GET /v2/org/org-1/nico/tenant/current")
				}
			}
			mu.Lock()
			assert.Equal(t, wantRequests, requests, "only token issuance and the baseline read are permitted")
			mu.Unlock()
			after, err := api.Dump()
			require.NoError(t, err)
			assert.Equal(t, before, after)
		})
	}
	matchValidationGolden(t, "validation_results.yaml", results)
}

func TestValidateCredentialsTLSAndOptions(t *testing.T) {
	results := map[string]CredentialValidation{}
	for _, mode := range []string{"untrusted-backend", "untrusted-issuer", "custom-ca", "skip-verification"} {
		t.Run(mode, func(t *testing.T) {
			api := fake.New()
			api.SeedClient("client-id", "client-secret")
			handler := api.Handler()
			var paths, scopes []string
			var mu sync.Mutex
			server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				mu.Lock()
				paths = append(paths, r.Method+" "+r.URL.Path)
				if r.URL.Path == "/token" {
					assert.NoError(t, r.ParseForm())
					scopes = append(scopes, r.Form.Get("scope"))
				}
				mu.Unlock()
				r.URL.Path = strings.Replace(r.URL.Path, "/custom-api/", "/nico/", 1)
				handler.ServeHTTP(w, r)
			}))
			server.Config.ErrorLog = log.New(io.Discard, "", 0)
			server.StartTLS()
			t.Cleanup(server.Close)
			secret := validationSecret(server.URL, true)
			secret.Data[SecretKeyAPIName] = []byte("custom-api")
			secret.Data[SecretKeyScope] = []byte("tenant:read inventory:read")
			switch mode {
			case "untrusted-backend":
				plainIssuer := httptest.NewServer(api.Handler())
				t.Cleanup(plainIssuer.Close)
				secret.Data[SecretKeyTokenURL] = []byte(plainIssuer.URL + "/token")
			case "custom-ca":
				secret.Data[SecretKeyCA] = pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: server.Certificate().Raw})
			case "skip-verification":
				secret.Data[SecretKeyInsecureSkipTLSVerify] = []byte(strconv.FormatBool(true))
				secret.Data[SecretKeyScopes] = secret.Data[SecretKeyScope]
				delete(secret.Data, SecretKeyScope)
			}
			results[mode] = ValidateCredentials(t.Context(), secret)
			mu.Lock()
			defer mu.Unlock()
			if mode == "custom-ca" || mode == "skip-verification" {
				assert.Equal(t, []string{"POST /token", "GET /v2/org/org-1/custom-api/tenant/current"}, paths)
				assert.Equal(t, []string{"tenant:read inventory:read"}, scopes)
				assert.Equal(t, 1, api.TokenRequestCount())
			} else {
				assert.Empty(t, paths, "untrusted TLS must prevent application requests")
			}
		})
	}
	matchValidationGolden(t, "validation_tls.yaml", results)
}

func TestValidateCredentialsFreshness(t *testing.T) {
	api := fake.New()
	api.SeedClient("client-id", "client-secret")
	var requests []string
	var mu sync.Mutex
	handler := api.Handler()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		requests = append(requests, r.Method+" "+r.URL.Path)
		mu.Unlock()
		handler.ServeHTTP(w, r)
	}))
	t.Cleanup(server.Close)
	secret := validationSecret(server.URL, true)
	cfg, err := LoadSecretConfig(secret)
	require.NoError(t, err)
	cache := NewClientCache()
	cached, err := cache.GetOrCreate(t.Context(), secret, cfg)
	require.NoError(t, err)
	_, err = cached.ResolveTenantID(t.Context())
	require.NoError(t, err)
	assert.Equal(t, 1, api.TokenRequestCount())
	for range 2 {
		require.Equal(t, metav1.ConditionTrue, ValidateCredentials(t.Context(), secret).Status)
	}
	assert.Equal(t, 3, api.TokenRequestCount(), "each validation must mint a new token")

	// The fake still accepts its old bearer token after the issuer credentials change.
	api.SeedClient("client-id", "rotated-client-secret")
	result := ValidateCredentials(t.Context(), secret)
	assert.Equal(t, metav1.ConditionFalse, result.Status)
	assert.Equal(t, "AuthenticationFailed", result.Reason)
	again, err := cache.GetOrCreate(t.Context(), secret, cfg)
	require.NoError(t, err)
	assert.Same(t, cached, again)
	_, err = again.GetCurrentTenantID(t.Context())
	require.NoError(t, err, "ordinary clients still reuse the accepted old token")
	_, err = again.ResolveTenantID(t.Context())
	require.NoError(t, err)
	assert.Equal(t, 3, api.TokenRequestCount())
	mu.Lock()
	defer mu.Unlock()
	assert.Equal(t, []string{
		"POST /token", "GET /v2/org/org-1/nico/tenant/current",
		"POST /token", "GET /v2/org/org-1/nico/tenant/current",
		"POST /token", "GET /v2/org/org-1/nico/tenant/current",
		"POST /token", "GET /v2/org/org-1/nico/tenant/current",
	}, requests, "the rejected fresh check must stop at the issuer; cached tenant resolution adds no request")
}

func TestValidateCredentialsDeadlineAndCancellation(t *testing.T) {
	results := map[string]CredentialValidation{}
	for _, stage := range []string{"token", "tenant-after-slow-token"} {
		for _, cancellation := range []string{"deadline", "parent-deadline", "parent-cancel"} {
			name := stage + "-" + cancellation
			t.Run(name, func(t *testing.T) {
				api := fake.New()
				api.SeedClient("client-id", "client-secret")
				handler := api.Handler()
				entered, disconnected := make(chan struct{}), make(chan struct{})
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if stage == "tenant-after-slow-token" && r.URL.Path == "/token" {
						time.Sleep(500 * time.Millisecond)
						handler.ServeHTTP(w, r)
						return
					}
					// Drain the OAuth body so the server can observe the client disconnect.
					_, _ = io.Copy(io.Discard, r.Body)
					close(entered)
					<-r.Context().Done()
					close(disconnected)
				}))
				t.Cleanup(server.Close)
				start := time.Now()
				ctx, cancel := context.WithCancel(t.Context())
				defer cancel()
				budget := time.Second
				if cancellation == "parent-deadline" {
					var deadlineCancel context.CancelFunc
					ctx, deadlineCancel = context.WithTimeout(ctx, budget)
					defer deadlineCancel()
					budget = 5 * time.Second
				}
				if cancellation == "parent-cancel" {
					budget = 5 * time.Second
				}
				done := make(chan CredentialValidation, 1)
				go func() { done <- validateCredentials(ctx, validationSecret(server.URL, true), budget) }()
				select {
				case <-entered:
				case <-time.After(2 * time.Second):
					t.Fatal("validation did not reach the blocked operation")
				}
				if cancellation == "parent-cancel" {
					cancel()
				}
				select {
				case results[name] = <-done:
				case <-time.After(2 * time.Second):
					t.Fatal("validation ignored its deadline or parent cancellation")
				}
				if cancellation != "parent-cancel" {
					assert.GreaterOrEqual(t, time.Since(start), time.Second)
					assert.Less(t, time.Since(start), 1400*time.Millisecond, "token and backend must share the earlier deadline")
				}
				select {
				case <-disconnected:
				case <-time.After(time.Second):
					t.Fatal("HTTP operation outlived validation")
				}
			})
		}
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	results["already-canceled"] = ValidateCredentials(ctx, nil)
	matchValidationGolden(t, "validation_timing.yaml", results)
}

func TestValidateCredentialsSafeDiagnostics(t *testing.T) {
	var logs bytes.Buffer
	previous := log.Writer()
	log.SetOutput(&logs)
	t.Cleanup(func() { log.SetOutput(previous) })
	ctx := ctrllog.IntoContext(t.Context(), zap.New(zap.WriteTo(&logs)))
	for _, stage := range []string{"issuer", "backend", "issuer-rejected", "backend-rejected", "configuration", "unreachable-issuer", "unreachable-backend"} {
		t.Run(stage, func(t *testing.T) {
			api := fake.New()
			api.SeedClient("client-SENTINEL", "secret-SENTINEL")
			handler := api.Handler()
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if strings.HasPrefix(stage, "backend") && r.URL.Path == "/token" {
					handler.ServeHTTP(w, r)
					return
				}
				w.Header().Set("Content-Type", "application/json")
				code := http.StatusServiceUnavailable
				if strings.HasSuffix(stage, "-rejected") {
					code = http.StatusUnauthorized
				}
				w.WriteHeader(code)
				_, _ = io.WriteString(w, `{"error":"invalid_client","error_description":"body-SENTINEL secret-SENTINEL","access_token":"token-SENTINEL"}`)
			}))
			t.Cleanup(server.Close)
			secret := validationSecret(server.URL, true)
			secret.Data[SecretKeyEndpoint] = []byte(server.URL + "/endpoint-SENTINEL")
			secret.Data[SecretKeyTokenURL] = []byte(server.URL + "/token?issuer-SENTINEL")
			secret.Data[SecretKeyClientID] = []byte("client-SENTINEL")
			secret.Data[SecretKeyClientSecret] = []byte("secret-SENTINEL")
			secret.Data[SecretKeyOrgID] = []byte("org-SENTINEL")
			if stage == "configuration" {
				secret.Data[SecretKeyInsecureSkipTLSVerify] = []byte("option-SENTINEL")
			}
			if strings.HasPrefix(stage, "unreachable-") {
				server.Close()
				if stage == "unreachable-backend" {
					secret.Data[SecretKeyToken] = []byte("token-SENTINEL")
					delete(secret.Data, SecretKeyTokenURL)
					delete(secret.Data, SecretKeyClientID)
					delete(secret.Data, SecretKeyClientSecret)
				}
			}
			result := ValidateCredentials(ctx, secret)
			if strings.HasSuffix(stage, "-rejected") || stage == "configuration" {
				assert.Equal(t, metav1.ConditionFalse, result.Status)
			} else {
				assert.Equal(t, metav1.ConditionUnknown, result.Status)
				assert.Equal(t, "ValidationFailed", result.Reason)
			}
			assert.NotContains(t, fmt.Sprint(result), "SENTINEL")
			assert.NotContains(t, fmt.Sprint(result), server.URL)
			assert.Less(t, len(result.Message), 256)
		})
	}
	assert.NotContains(t, logs.String(), "SENTINEL")
	assert.Empty(t, logs.String(), "validation must not log raw SDK or OAuth errors")
}

func validationSecret(endpoint string, oauth bool) *corev1.Secret {
	data := map[string][]byte{
		SecretKeyEndpoint: []byte(endpoint), SecretKeyOrgID: []byte("org-1"),
	}
	if oauth {
		data[SecretKeyTokenURL] = []byte(endpoint + "/token")
		data[SecretKeyClientID] = []byte("client-id")
		data[SecretKeyClientSecret] = []byte("client-secret")
	} else {
		data[SecretKeyToken] = []byte("synthetic-token")
	}
	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Namespace: "default", Name: "credentials", ResourceVersion: "1"},
		Data:       data,
	}
}

func matchValidationGolden(t *testing.T, name string, results map[string]CredentialValidation) {
	t.Helper()
	actual, err := yaml.Marshal(results)
	require.NoError(t, err)
	path := filepath.Join("testdata", name)
	if os.Getenv("TESTUTIL_UPDATE_EXPECTED") == strconv.FormatBool(true) {
		require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o750))
		require.NoError(t, os.WriteFile(path, actual, 0o600))
		return
	}
	expected, err := os.ReadFile(path)
	require.NoError(t, err, "regenerate validation goldens with make test-update")
	gomega.NewWithT(t).Expect(string(actual)).To(matchers.MatchGolden(string(expected), path, name+" (actual)"))
}
