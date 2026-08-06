package nico

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"
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

var _ API = (*Client)(nil)

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

// GetInstanceTypeWithAllocationStats fetches an instance type with tenant-visible allocation stats.
func (c *Client) GetInstanceTypeWithAllocationStats(ctx context.Context, instanceTypeID string) (*nicosdk.InstanceType, error) {
	authCtx, err := c.authCtx(ctx)
	if err != nil {
		return nil, err
	}
	instanceType, resp, err := c.api.InstanceTypeAPI.
		GetInstanceType(authCtx, c.orgID, instanceTypeID).
		IncludeAllocationStats(true).
		Execute()
	if err != nil {
		return nil, normalizeError(resp, err)
	}
	return instanceType, nil
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

// DeleteInstance deletes a NICo instance. When healthIssue is non-nil it is
// forwarded to the API as machine health context for the repair workflow.
func (c *Client) DeleteInstance(ctx context.Context, instanceID string, healthIssue *nicosdk.MachineHealthIssue) error {
	authCtx, err := c.authCtx(ctx)
	if err != nil {
		return err
	}
	req := c.api.InstanceAPI.DeleteInstance(authCtx, c.orgID, instanceID)
	if healthIssue != nil {
		deleteReq := nicosdk.NewInstanceDeleteRequest()
		deleteReq.SetMachineHealthIssue(*healthIssue)
		req = req.InstanceDeleteRequest(*deleteReq)
	}
	resp, err := req.Execute()
	return normalizeError(resp, err)
}

// TriggerInstanceReboot requests a power cycle for the NICo instance.
func (c *Client) TriggerInstanceReboot(ctx context.Context, instanceID string) (*nicosdk.Instance, error) {
	authCtx, err := c.authCtx(ctx)
	if err != nil {
		return nil, err
	}

	req := nicosdk.NewInstanceUpdateRequest()
	req.SetTriggerReboot(true)
	instance, resp, err := c.api.InstanceAPI.UpdateInstance(authCtx, c.orgID, instanceID).
		InstanceUpdateRequest(*req).
		Execute()
	if err != nil {
		return nil, normalizeError(resp, err)
	}
	return instance, nil
}

// ApplyInstanceLabels applies labels to a NICo instance. CAPNICo uses this
// after create because machine-id and observed topology are not all known when
// the initial create request is built.
func (c *Client) ApplyInstanceLabels(ctx context.Context, instanceID string, labels map[string]string) (*nicosdk.Instance, error) {
	authCtx, err := c.authCtx(ctx)
	if err != nil {
		return nil, err
	}

	req := nicosdk.NewInstanceUpdateRequest()
	req.SetLabels(labels)
	instance, resp, err := c.api.InstanceAPI.UpdateInstance(authCtx, c.orgID, instanceID).
		InstanceUpdateRequest(*req).
		Execute()
	if err != nil {
		return nil, normalizeError(resp, err)
	}
	return instance, nil
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

// GetSite fetches a NICo site by ID.
func (c *Client) GetSite(ctx context.Context, siteID string) (*nicosdk.Site, error) {
	authCtx, err := c.authCtx(ctx)
	if err != nil {
		return nil, err
	}
	tenantID, err := c.ResolveTenantID(ctx)
	if err != nil {
		return nil, err
	}
	for pageNumber := int32(1); ; pageNumber++ {
		sites, resp, err := c.api.SiteAPI.GetAllSite(authCtx, c.orgID).
			TenantId(tenantID).
			PageNumber(pageNumber).
			PageSize(pageSize).
			Execute()
		if err != nil {
			return nil, normalizeError(resp, err)
		}
		for i := range sites {
			if sites[i].GetId() == siteID {
				return &sites[i], nil
			}
		}
		if len(sites) < pageSize {
			return nil, ErrNotFound
		}
	}
}

// GetVPC fetches a NICo VPC by ID.
func (c *Client) GetVPC(ctx context.Context, vpcID string) (*nicosdk.VPC, error) {
	authCtx, err := c.authCtx(ctx)
	if err != nil {
		return nil, err
	}
	vpc, resp, err := c.api.VPCAPI.GetVpc(authCtx, c.orgID, vpcID).Execute()
	if err != nil {
		return nil, normalizeError(resp, err)
	}
	return vpc, nil
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

func InstanceID(providerID string) (string, error) {
	if !strings.HasPrefix(providerID, ProviderIDPrefix) {
		return "", fmt.Errorf("providerID must start with %q", ProviderIDPrefix)
	}

	instanceID := strings.TrimPrefix(providerID, ProviderIDPrefix)
	if instanceID == "" {
		return "", fmt.Errorf("providerID must include an instance ID")
	}

	return instanceID, nil
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
