// SPDX-FileCopyrightText: Copyright (c) 2026 NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package fake

import (
	"net/http"
	"net/url"
	"strings"
)

const (
	tokenPath = "/token"

	grantTypeClientCredentials = "client_credentials"
	accessTokenLifetime        = 900
	clientAccessToken          = "fake-access-token"
)

// SeedToken configures the static bearer-token mode supported by CAPNICo. It
// replaces any client-credentials configuration because a production Secret
// cannot select both authentication modes.
func (s *Server) SeedToken(token string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.staticToken = token
	s.clientID = ""
	s.clientSecret = ""
}

// SeedClient configures the OAuth2 client-credentials mode supported by
// CAPNICo. It replaces any static bearer token.
func (s *Server) SeedClient(clientID, clientSecret string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.staticToken = ""
	s.clientID = clientID
	s.clientSecret = clientSecret
}

// TokenRequestCount reports how many OAuth2 tokens the fake has minted. This
// lets tests prove that the production token source caches tokens rather than
// requesting one for every NICo call.
func (s *Server) TokenRequestCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()

	return s.tokenRequests
}

func (s *Server) issueToken(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request")
		return
	}
	if r.PostFormValue("grant_type") != grantTypeClientCredentials {
		writeError(w, http.StatusBadRequest, "unsupported_grant_type")
		return
	}

	clientID, clientSecret, basicAuth := r.BasicAuth()
	if basicAuth {
		// The OAuth2 client encodes both Basic-auth fields before placing them in
		// the header, so decode them before comparing with the configured values.
		var idErr, secretErr error
		clientID, idErr = url.QueryUnescape(clientID)
		clientSecret, secretErr = url.QueryUnescape(clientSecret)
		if idErr != nil || secretErr != nil {
			writeError(w, http.StatusBadRequest, "invalid_request")
			return
		}
	} else {
		clientID = r.PostFormValue("client_id")
		clientSecret = r.PostFormValue("client_secret")
	}

	s.mu.Lock()
	seeded := s.clientID != "" || s.clientSecret != ""
	if !seeded || clientID != s.clientID || clientSecret != s.clientSecret {
		s.mu.Unlock()
		w.Header().Set("WWW-Authenticate", `Basic realm="nico"`)
		writeError(w, http.StatusUnauthorized, "invalid_client")
		return
	}
	s.tokenRequests++
	s.mu.Unlock()

	writeJSON(w, http.StatusOK, map[string]any{
		"access_token": clientAccessToken,
		"token_type":   "Bearer",
		"expires_in":   accessTokenLifetime,
		"scope":        r.PostFormValue("scope"),
	})
}

func (s *Server) authenticate(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == tokenPath {
			next.ServeHTTP(w, r)
			return
		}

		s.mu.Lock()
		staticToken := s.staticToken
		clientID, clientSecret := s.clientID, s.clientSecret
		s.mu.Unlock()

		expectedToken := staticToken
		if expectedToken == "" && (clientID != "" || clientSecret != "") {
			expectedToken = clientAccessToken
		}
		if expectedToken == "" {
			next.ServeHTTP(w, r)
			return
		}

		presented, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
		if !ok {
			w.Header().Set("WWW-Authenticate", `Bearer realm="nico"`)
			writeError(w, http.StatusUnauthorized, "an access token is required")
			return
		}
		if strings.TrimSpace(presented) != expectedToken {
			w.Header().Set("WWW-Authenticate", `Bearer realm="nico", error="invalid_token"`)
			writeError(w, http.StatusUnauthorized, "the access token is not valid")
			return
		}

		next.ServeHTTP(w, r)
	})
}
