// SPDX-FileCopyrightText: Copyright (c) 2026 NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package nico

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"flag"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"golang.org/x/oauth2"
	"golang.org/x/oauth2/clientcredentials"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
)

const (
	SecretKeyEndpoint              = "endpoint"
	SecretKeyOrgID                 = "orgID"
	SecretKeyToken                 = "token"
	SecretKeyTokenURL              = "tokenURL"
	SecretKeyClientID              = "clientID"
	SecretKeyClientSecret          = "clientSecret"
	SecretKeyScope                 = "scope"
	SecretKeyScopes                = "scopes"
	SecretKeyCA                    = "ca.crt"
	SecretKeyInsecureSkipTLSVerify = "insecureSkipTLSVerify"
	SecretKeyAPIName               = "apiName"
	defaultHTTPClientTimeout       = 30 * time.Second
	DefaultRebootAnnotation        = "nico.nvidia.com/reboot"
	DefaultRepairAnnotation        = "nico.nvidia.com/machine-health-issue"
)

// ProviderConfig holds provider-wide settings.
type ProviderConfig struct {
	// Credentials points at the provider-level NICo credentials Secret used as
	// a fallback when a NicoCluster does not set spec.identityRef. A zero value
	// means no provider-level Secret is configured.
	Credentials types.NamespacedName
	// RebootAnnotation is the CAPI Machine annotation key used to request a NICo instance reboot.
	RebootAnnotation string

	// RepairAnnotation is the annotation key the controller watches on the owner
	// CAPI Machine during deletion. When the annotation is present its value is
	// forwarded to the NICo API as the MachineHealthIssue summary, signalling
	// that the underlying hardware should be flagged for repair rather than
	// returned to the available pool. An empty string disables the behaviour.
	RepairAnnotation string
}

// BindFlags binds the provider-level configuration to fs. The current values of
// p's fields are used as the flag defaults, so callers can pre-seed defaults by
// constructing p before calling BindFlags.
func (p *ProviderConfig) BindFlags(fs *flag.FlagSet) {
	if p.RebootAnnotation == "" {
		p.RebootAnnotation = DefaultRebootAnnotation
	}
	if p.RepairAnnotation == "" {
		p.RepairAnnotation = DefaultRepairAnnotation
	}
	fs.StringVar(&p.Credentials.Namespace, "provider-credentials-namespace", p.Credentials.Namespace,
		"Namespace of the provider-level NICo credentials Secret used when a NicoCluster does not set spec.identityRef.")
	fs.StringVar(&p.Credentials.Name, "provider-credentials-secret-name", p.Credentials.Name,
		"Name of the provider-level NICo credentials Secret used when a NicoCluster does not set spec.identityRef.")
	fs.StringVar(&p.RebootAnnotation, "reboot-annotation", p.RebootAnnotation,
		"CAPI Machine annotation key used to request a NICo instance reboot.")
	fs.StringVar(&p.RepairAnnotation, "repair-annotation", p.RepairAnnotation,
		"Annotation key on the owner CAPI Machine whose presence triggers a repair flag on the NICo instance before deletion. The annotation value is used as the health-issue summary. Leave empty to disable.")
}

// SecretConfig contains NICo API connection settings loaded from a Secret.
type SecretConfig struct {
	Endpoint              string
	OrgID                 string
	Token                 string
	TokenURL              string
	ClientID              string
	ClientSecret          string
	Scopes                []string
	CA                    []byte
	InsecureSkipTLSVerify bool
	APIName               string
}

// LoadSecretConfig loads NICo client settings from a Secret.
func LoadSecretConfig(secret *corev1.Secret) (SecretConfig, error) {
	if secret == nil {
		return SecretConfig{}, fmt.Errorf("identity secret is nil")
	}

	cfg := SecretConfig{
		Endpoint:     string(secret.Data[SecretKeyEndpoint]),
		OrgID:        string(secret.Data[SecretKeyOrgID]),
		Token:        string(secret.Data[SecretKeyToken]),
		TokenURL:     string(secret.Data[SecretKeyTokenURL]),
		ClientID:     string(secret.Data[SecretKeyClientID]),
		ClientSecret: string(secret.Data[SecretKeyClientSecret]),
		CA:           secret.Data[SecretKeyCA],
		APIName:      string(secret.Data[SecretKeyAPIName]),
	}
	if cfg.Endpoint == "" {
		return SecretConfig{}, fmt.Errorf("identity secret missing %q", SecretKeyEndpoint)
	}
	if cfg.OrgID == "" {
		return SecretConfig{}, fmt.Errorf("identity secret missing %q", SecretKeyOrgID)
	}

	scope := string(secret.Data[SecretKeyScope])
	if scope == "" {
		scope = string(secret.Data[SecretKeyScopes])
	}
	if scope != "" {
		cfg.Scopes = strings.Fields(scope)
	}

	hasStaticToken := cfg.Token != ""
	hasOAuthClientCredentials := cfg.TokenURL != "" || cfg.ClientID != "" || cfg.ClientSecret != ""

	switch {
	case hasStaticToken && hasOAuthClientCredentials:
		return SecretConfig{}, fmt.Errorf("identity secret must configure either %q or OAuth client credentials, not both", SecretKeyToken)
	case hasStaticToken:
	case hasOAuthClientCredentials:
		if cfg.TokenURL == "" {
			return SecretConfig{}, fmt.Errorf("identity secret missing %q", SecretKeyTokenURL)
		}
		if cfg.ClientID == "" {
			return SecretConfig{}, fmt.Errorf("identity secret missing %q", SecretKeyClientID)
		}
		if cfg.ClientSecret == "" {
			return SecretConfig{}, fmt.Errorf("identity secret missing %q", SecretKeyClientSecret)
		}
	default:
		return SecretConfig{}, fmt.Errorf("identity secret must include either %q or the OAuth client credentials fields %q, %q, and %q", SecretKeyToken, SecretKeyTokenURL, SecretKeyClientID, SecretKeyClientSecret)
	}

	if raw, ok := secret.Data[SecretKeyInsecureSkipTLSVerify]; ok && len(raw) > 0 {
		value, err := strconv.ParseBool(string(raw))
		if err != nil {
			return SecretConfig{}, fmt.Errorf("invalid %q value: %w", SecretKeyInsecureSkipTLSVerify, err)
		}
		cfg.InsecureSkipTLSVerify = value
	}

	return cfg, nil
}

func (c SecretConfig) HTTPClient() (*http.Client, error) {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.TLSClientConfig = &tls.Config{
		InsecureSkipVerify: c.InsecureSkipTLSVerify, //nolint:gosec
		MinVersion:         tls.VersionTLS12,
	}

	if len(c.CA) > 0 {
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM(c.CA) {
			return nil, fmt.Errorf("identity secret contains an invalid CA bundle")
		}
		transport.TLSClientConfig.RootCAs = pool
	}

	return &http.Client{
		Timeout:   defaultHTTPClientTimeout,
		Transport: transport,
	}, nil
}

func (c SecretConfig) TokenSource(ctx context.Context, httpClient *http.Client) (oauth2.TokenSource, error) {
	if c.Token != "" {
		return oauth2.StaticTokenSource(&oauth2.Token{
			AccessToken: c.Token,
			TokenType:   "Bearer",
		}), nil
	}

	if _, err := url.Parse(c.TokenURL); err != nil {
		return nil, fmt.Errorf("invalid %q value: %w", SecretKeyTokenURL, err)
	}

	cc := clientcredentials.Config{
		ClientID:     c.ClientID,
		ClientSecret: c.ClientSecret,
		Scopes:       c.Scopes,
		TokenURL:     c.TokenURL,
		AuthStyle:    oauth2.AuthStyleInHeader,
	}

	tokenCtx := context.WithValue(ctx, oauth2.HTTPClient, httpClient)
	return oauth2.ReuseTokenSource(nil, cc.TokenSource(tokenCtx)), nil
}
