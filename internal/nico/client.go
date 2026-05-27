package nico

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"sync"

	nicosdk "github.com/NVIDIA/ncx-infra-controller-rest/sdk/standard"
	"golang.org/x/oauth2"
)

const (
	ProviderIDPrefix = "nico://"
	pageSize         = 100
)

// Client wraps the generated NICo SDK with provider-specific helpers.
type Client struct {
	orgID       string
	tokenSource oauth2.TokenSource
	api         *nicosdk.APIClient
	tenantMu    sync.RWMutex
	tenantID    string
}

// InstanceLookup narrows idempotent instance lookups.
type InstanceLookup struct {
	Name   string
	VPCID  string
	SiteID string
}

// NewClient builds a NICo API client from Secret-backed config.
func NewClient(ctx context.Context, secretConfig SecretConfig) (*Client, error) {
	httpClient, err := secretConfig.HTTPClient()
	if err != nil {
		return nil, err
	}
	tokenSource, err := secretConfig.TokenSource(ctx, httpClient)
	if err != nil {
		return nil, err
	}

	cfg := nicosdk.NewConfiguration()
	cfg.HTTPClient = httpClient
	cfg.Servers = nicosdk.ServerConfigurations{
		{
			URL:         trimTrailingSlash(secretConfig.Endpoint),
			Description: "Configured NICo API endpoint",
		},
	}
	cfg.UserAgent = "cluster-api-provider-nico/0.1.0"

	// If the API name is set, we need to set it on the configuration.
	// Must be called after cfg.HTTPClient is set.
	if secretConfig.APIName != "" {
		cfg.SetAPIName(secretConfig.APIName)
	}

	return &Client{
		orgID:       secretConfig.OrgID,
		tokenSource: tokenSource,
		api:         nicosdk.NewAPIClient(cfg),
	}, nil
}

func (c *Client) authCtx(ctx context.Context) (context.Context, error) {
	token, err := c.tokenSource.Token()
	if err != nil {
		return nil, fmt.Errorf("get access token: %w", err)
	}
	return context.WithValue(ctx, nicosdk.ContextAccessToken, token.AccessToken), nil
}

// ResolveTenantID discovers the current tenant once and reuses it for the lifetime of the client.
func (c *Client) ResolveTenantID(ctx context.Context) (string, error) {
	c.tenantMu.RLock()
	if c.tenantID != "" {
		defer c.tenantMu.RUnlock()
		return c.tenantID, nil
	}
	c.tenantMu.RUnlock()

	c.tenantMu.Lock()
	defer c.tenantMu.Unlock()
	if c.tenantID != "" {
		return c.tenantID, nil
	}

	resolvedTenantID, err := c.GetCurrentTenantID(ctx)
	if err != nil {
		return "", err
	}
	c.tenantID = resolvedTenantID
	return c.tenantID, nil
}

// ValidateReadiness confirms the client can authenticate and resolve tenant context.
func (c *Client) ValidateReadiness(ctx context.Context) error {
	_, err := c.ResolveTenantID(ctx)
	return err
}

// GetCurrentTenantID returns the current tenant ID for the configured org.
func (c *Client) GetCurrentTenantID(ctx context.Context) (string, error) {
	authCtx, err := c.authCtx(ctx)
	if err != nil {
		return "", err
	}
	tenant, resp, err := c.api.TenantAPI.GetCurrentTenant(authCtx, c.orgID).Execute()
	if err != nil {
		return "", normalizeError(resp, err)
	}
	if tenant == nil || tenant.GetId() == "" {
		return "", fmt.Errorf("current tenant response did not include an id")
	}
	return tenant.GetId(), nil
}

// CreateInstance creates a NICo instance.
func (c *Client) CreateInstance(ctx context.Context, req nicosdk.InstanceCreateRequest) (*nicosdk.Instance, error) {
	authCtx, err := c.authCtx(ctx)
	if err != nil {
		return nil, err
	}
	instance, resp, err := c.api.InstanceAPI.
		CreateInstance(authCtx, c.orgID).
		InstanceCreateRequest(req).
		Execute()
	if err != nil {
		return nil, normalizeError(resp, err)
	}
	return instance, nil
}

// DeleteInstance deletes a NICo instance.
func (c *Client) DeleteInstance(ctx context.Context, instanceID string) error {
	authCtx, err := c.authCtx(ctx)
	if err != nil {
		return err
	}
	resp, err := c.api.InstanceAPI.DeleteInstance(authCtx, c.orgID, instanceID).Execute()
	return normalizeError(resp, err)
}

// GetInstance fetches a NICo instance by ID.
func (c *Client) GetInstance(ctx context.Context, instanceID string) (*nicosdk.Instance, error) {
	authCtx, err := c.authCtx(ctx)
	if err != nil {
		return nil, err
	}
	instance, resp, err := c.api.InstanceAPI.GetInstance(authCtx, c.orgID, instanceID).Execute()
	if err != nil {
		return nil, normalizeError(resp, err)
	}
	return instance, nil
}

// FindInstanceByName searches for a NICo instance by name and optional scoping fields.
func (c *Client) FindInstanceByName(ctx context.Context, lookup InstanceLookup) (*nicosdk.Instance, error) {
	authCtx, err := c.authCtx(ctx)
	if err != nil {
		return nil, err
	}
	req := c.api.InstanceAPI.
		GetAllInstance(authCtx, c.orgID).
		Name(lookup.Name).
		PageSize(pageSize)

	if lookup.VPCID != "" {
		req = req.VpcId(lookup.VPCID)
	}
	if lookup.SiteID != "" {
		req = req.SiteId(lookup.SiteID)
	}

	instances, resp, err := req.Execute()
	if err != nil {
		return nil, normalizeError(resp, err)
	}
	if len(instances) == 0 {
		return nil, ErrNotFound
	}
	return &instances[0], nil
}

func IsReady(instance *nicosdk.Instance) bool {
	return instance != nil && instance.Status != nil && *instance.Status == nicosdk.INSTANCESTATUS_READY
}

func ProviderID(instanceID string) string {
	return ProviderIDPrefix + instanceID
}

func trimTrailingSlash(in string) string {
	parsed, err := url.Parse(in)
	if err != nil || parsed.String() == "" {
		return in
	}
	for len(parsed.Path) > 1 && parsed.Path[len(parsed.Path)-1] == '/' {
		parsed.Path = parsed.Path[:len(parsed.Path)-1]
	}
	return parsed.String()
}

func IgnoreNotFound(err error) error {
	if err == nil || errors.Is(err, ErrNotFound) {
		return nil
	}
	return err
}
