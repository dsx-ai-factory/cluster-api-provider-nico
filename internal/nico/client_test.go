package nico

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestClientGetSite(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got, want := r.Header.Get("Authorization"), testBearerToken; got != want {
			t.Fatalf("Authorization = %q, want %q", got, want)
		}
		switch r.URL.Path {
		case testTenantPath:
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"id":"tenant-1"}`))
		case "/v2/org/test-org/carbide/site":
			if got, want := r.URL.Query().Get("tenantId"), testTenantID; got != want {
				t.Fatalf("tenantId = %q, want %q", got, want)
			}
			if got, want := r.URL.Query().Get("pageSize"), "100"; got != want {
				t.Fatalf("pageSize = %q, want %q", got, want)
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`[{"id":"other-site","name":"Other"},{"id":"site-1","name":"Site / West"}]`))
		default:
			t.Fatalf("request path = %q, want current tenant or tenant-scoped sites", r.URL.Path)
		}
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
		if got, want := r.Header.Get("Authorization"), testBearerToken; got != want {
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

func TestClientGetSiteQueriesAdditionalPagesUntilFound(t *testing.T) {
	var sitePageRequests []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got, want := r.Header.Get("Authorization"), testBearerToken; got != want {
			t.Fatalf("Authorization = %q, want %q", got, want)
		}
		switch r.URL.Path {
		case testTenantPath:
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"id":"tenant-1"}`))
		case "/v2/org/test-org/carbide/site":
			if got, want := r.URL.Query().Get("tenantId"), testTenantID; got != want {
				t.Fatalf("tenantId = %q, want %q", got, want)
			}
			if got, want := r.URL.Query().Get("pageSize"), "100"; got != want {
				t.Fatalf("pageSize = %q, want %q", got, want)
			}
			page := r.URL.Query().Get("pageNumber")
			sitePageRequests = append(sitePageRequests, page)
			w.Header().Set("Content-Type", "application/json")
			switch page {
			case "1":
				_, _ = w.Write([]byte("[" + strings.TrimSuffix(strings.Repeat(`{"id":"other-site"},`, pageSize), ",") + "]"))
			case "2":
				_, _ = w.Write([]byte(`[{"id":"site-101","name":"Site / East"}]`))
			default:
				t.Fatalf("unexpected pageNumber %q", page)
			}
		default:
			t.Fatalf("request path = %q, want current tenant or tenant-scoped sites", r.URL.Path)
		}
	}))
	defer server.Close()

	client := newStaticTokenClient(t, server.URL)
	site, err := client.GetSite(context.Background(), "site-101")
	if err != nil {
		t.Fatalf("GetSite() error = %v", err)
	}
	if got, want := site.GetName(), "Site / East"; got != want {
		t.Fatalf("site name = %q, want raw name %q", got, want)
	}
	if got, want := strings.Join(sitePageRequests, ","), "1,2"; got != want {
		t.Fatalf("site page requests = %q, want %q", got, want)
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
