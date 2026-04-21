package nico

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"golang.org/x/oauth2"
	"golang.org/x/oauth2/clientcredentials"
	corev1 "k8s.io/api/core/v1"
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
)

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
