// SPDX-FileCopyrightText: Copyright (c) 2026 NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package nico

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNormalizeErrorDistinguishesForbiddenFromUnauthorized(t *testing.T) {
	for _, tc := range []struct {
		name          string
		status        int
		wantForbidden bool
	}{
		{name: "credentials rejected", status: http.StatusUnauthorized, wantForbidden: false},
		{name: "privilege denied", status: http.StatusForbidden, wantForbidden: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				http.Error(w, "nope", tc.status)
			}))
			defer server.Close()

			_, err := newStaticTokenClient(t, server.URL).ListFailureDomains(context.Background(), "site-1", "failure_domain")

			require.Error(t, err)
			assert.ErrorIs(t, err, ErrUnauthorized, "both statuses stay compatible with the broader sentinel")
			assert.Equal(t, tc.wantForbidden, errors.Is(err, ErrForbidden),
				"only 403 reports the missing capability that means no failure domains exist")
		})
	}
}

func TestNormalizeErrorClassifiesSchemeMismatch(t *testing.T) {
	server := httptest.NewServer(http.NotFoundHandler())
	t.Cleanup(server.Close)
	endpoint := strings.Replace(server.URL, "http://", "https://", 1)

	_, err := newStaticTokenClient(t, endpoint).GetVPC(context.Background(), "vpc-1")

	require.ErrorIs(t, err, ErrEndpointUnreachable)
	assert.EqualError(t, err, "NICo API endpoint could not be reached")
}

func TestNormalizeErrorClassifiesConnectionFailures(t *testing.T) {
	ok := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{}`))
	})
	for _, tc := range []struct {
		name    string
		start   func() *httptest.Server
		expired bool
		want    error
	}{
		{
			name:  "untrusted certificate",
			start: func() *httptest.Server { return httptest.NewTLSServer(ok) },
			want:  ErrTLSVerifyFailed,
		},
		{
			name: "closed endpoint",
			start: func() *httptest.Server {
				server := httptest.NewServer(ok)
				server.Close()
				return server
			},
			want: ErrEndpointUnreachable,
		},
		{
			name: "connection reset",
			start: func() *httptest.Server {
				return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
					conn, _, err := w.(http.Hijacker).Hijack()
					if err == nil {
						_ = conn.Close()
					}
				}))
			},
			want: ErrEndpointUnreachable,
		},
		{
			name:    "expired context",
			start:   func() *httptest.Server { return httptest.NewServer(ok) },
			expired: true,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := tc.start()
			t.Cleanup(server.Close)
			ctx := context.Background()
			if tc.expired {
				var cancel context.CancelFunc
				ctx, cancel = context.WithDeadline(ctx, time.Now().Add(-time.Second))
				defer cancel()
			}

			_, err := newStaticTokenClient(t, server.URL).GetVPC(ctx, "vpc-1")

			require.Error(t, err)
			for _, sentinel := range []error{ErrTLSVerifyFailed, ErrEndpointUnreachable} {
				assert.Equal(t, sentinel == tc.want, errors.Is(err, sentinel), "sentinel %v", sentinel)
			}
			if tc.expired {
				assert.ErrorIs(t, err, context.DeadlineExceeded, "an expired reconcile must not read as an unreachable endpoint")
			}
			if tc.want != nil {
				assert.NotContains(t, err.Error(), server.URL, "classified errors must not expose the endpoint")
			}
		})
	}
}
