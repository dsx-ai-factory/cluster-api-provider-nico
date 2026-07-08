package nico

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestClientGetSite(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got, want := r.URL.Path, "/v2/org/test-org/carbide/site/site-1"; got != want {
			t.Fatalf("request path = %q, want %q", got, want)
		}
		if got, want := r.Header.Get("Authorization"), "Bearer static-token"; got != want {
			t.Fatalf("Authorization = %q, want %q", got, want)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"site-1","name":"Site / West"}`))
	}))
	defer server.Close()

	client := newStaticTokenClient(t, server.URL)
	site, err := client.GetSite(context.Background(), "site-1")
	if err != nil {
		t.Fatalf("GetSite() error = %v", err)
	}
	if got, want := site.GetName(), "Site / West"; got != want {
		t.Fatalf("site name = %q, want raw name %q", got, want)
	}
}

func TestClientGetVPC(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got, want := r.URL.Path, "/v2/org/test-org/carbide/vpc/vpc-1"; got != want {
			t.Fatalf("request path = %q, want %q", got, want)
		}
		if got, want := r.Header.Get("Authorization"), "Bearer static-token"; got != want {
			t.Fatalf("Authorization = %q, want %q", got, want)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"vpc-1","name":"VPC / Production"}`))
	}))
	defer server.Close()

	client := newStaticTokenClient(t, server.URL)
	vpc, err := client.GetVPC(context.Background(), "vpc-1")
	if err != nil {
		t.Fatalf("GetVPC() error = %v", err)
	}
	if got, want := vpc.GetName(), "VPC / Production"; got != want {
		t.Fatalf("VPC name = %q, want raw name %q", got, want)
	}
}

func newStaticTokenClient(t *testing.T, endpoint string) *Client {
	t.Helper()
	client, err := NewClient(context.Background(), SecretConfig{
		Endpoint: endpoint,
		OrgID:    "test-org",
		Token:    "static-token",
	})
	if err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}
	return client
}
