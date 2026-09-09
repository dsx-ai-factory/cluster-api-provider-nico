// SPDX-FileCopyrightText: Copyright (c) 2026 NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package nico

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

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
