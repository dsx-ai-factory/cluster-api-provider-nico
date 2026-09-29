// SPDX-FileCopyrightText: Copyright (c) 2026 NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package docker

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/moby/moby/client"
)

func TestDeleteRemovesVolumes(t *testing.T) {
	var removeQuery string
	backend := newTestBackend(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodDelete {
			removeQuery = r.URL.RawQuery
		}
		w.WriteHeader(http.StatusNoContent)
	})

	if err := backend.Delete(t.Context(), "instance-1"); err != nil {
		t.Fatalf("Delete() error = %v", err)
	}
	if !strings.Contains(removeQuery, "v=1") {
		t.Fatalf("container remove query = %q, want anonymous volumes removed", removeQuery)
	}
}

func TestDeleteTreatsMissingContainerAsDeleted(t *testing.T) {
	backend := newTestBackend(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"message":"No such container"}`))
	})

	if err := backend.Delete(t.Context(), "instance-1"); err != nil {
		t.Fatalf("Delete() error = %v, want nil for a missing container", err)
	}
}

func TestDeleteReturnsOtherRemoveErrors(t *testing.T) {
	backend := newTestBackend(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodDelete {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"message":"removal failed"}`))
	})

	if err := backend.Delete(t.Context(), "instance-1"); err == nil {
		t.Fatal("Delete() error = nil, want the remove failure")
	}
}

func newTestBackend(t *testing.T, handler http.HandlerFunc) *Backend {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)

	dockerClient, err := client.New(client.WithHost("tcp://"+strings.TrimPrefix(server.URL, "http://")), client.WithAPIVersion("1.52"))
	if err != nil {
		t.Fatalf("create Docker client: %v", err)
	}
	t.Cleanup(func() { _ = dockerClient.Close() })
	return &Backend{Client: dockerClient}
}
