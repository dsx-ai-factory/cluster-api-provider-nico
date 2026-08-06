// Package fake provides an in-memory nico.API for controller tests.
package fake

import (
	"context"
	"fmt"
	"sync"

	nicosdk "github.com/NVIDIA/ncx-infra-controller-rest/sdk/standard"

	"gitlab-master.nvidia.com/nke/cluster-api-provider-nico/internal/nico"
)

var _ nico.API = (*Client)(nil)

// Client is a thread-safe in-memory NICo stand-in.
type Client struct {
	mu sync.Mutex

	TenantID      string
	DefaultSiteID string
	DefaultIP     string

	// ValidateErr, when set, is returned from ValidateReadiness and ResolveTenantID.
	ValidateErr error

	lastCreateRequest *nicosdk.InstanceCreateRequest
	lastAppliedLabels map[string]string

	instances     map[string]*nicosdk.Instance
	sites         map[string]*nicosdk.Site
	vpcs          map[string]*nicosdk.VPC
	instanceTypes map[string]*nicosdk.InstanceType
	nextID        int
}

// New returns a Client seeded with a default site.
func New() *Client {
	c := &Client{
		TenantID:      "tenant-1",
		DefaultSiteID: "site-1",
		DefaultIP:     "10.0.0.10",
		instances:     map[string]*nicosdk.Instance{},
		sites:         map[string]*nicosdk.Site{},
		vpcs:          map[string]*nicosdk.VPC{},
		instanceTypes: map[string]*nicosdk.InstanceType{},
	}
	site := nicosdk.NewSite()
	site.SetId(c.DefaultSiteID)
	site.SetName("fake-site")
	c.sites[c.DefaultSiteID] = site
	return c
}

// SeedVPC registers a VPC returned by GetVPC.
func (c *Client) SeedVPC(id, name string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	vpc := nicosdk.NewVPC()
	vpc.SetId(id)
	vpc.SetName(name)
	c.vpcs[id] = vpc
}

// SeedInstance registers an instance returned by GetInstance and FindInstanceByName.
func (c *Client) SeedInstance(inst *nicosdk.Instance) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.instances[inst.GetId()] = cloneInstance(inst)
}

// InstanceCount returns the number of tracked instances.
func (c *Client) InstanceCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.instances)
}

// LastCreateRequest returns a copy of the most recent CreateInstance argument.
func (c *Client) LastCreateRequest() *nicosdk.InstanceCreateRequest {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.lastCreateRequest == nil {
		return nil
	}
	copied := *c.lastCreateRequest
	return &copied
}

// LastAppliedLabels returns a copy of the most recent ApplyInstanceLabels map.
func (c *Client) LastAppliedLabels() map[string]string {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.lastAppliedLabels == nil {
		return nil
	}
	copied := make(map[string]string, len(c.lastAppliedLabels))
	for k, v := range c.lastAppliedLabels {
		copied[k] = v
	}
	return copied
}

// SeedInstanceType registers an instance type with unusedUsable capacity.
func (c *Client) SeedInstanceType(id string, unusedUsable int32) {
	c.mu.Lock()
	defer c.mu.Unlock()
	it := nicosdk.NewInstanceType()
	it.SetId(id)
	stats := nicosdk.NewInstanceTypeAllocationStats()
	stats.SetUnusedUsable(unusedUsable)
	stats.SetTotal(unusedUsable)
	stats.SetUnused(unusedUsable)
	stats.SetUsed(0)
	it.SetAllocationStats(*stats)
	c.instanceTypes[id] = it
}

func (c *Client) ValidateReadiness(ctx context.Context) error {
	_, err := c.ResolveTenantID(ctx)
	return err
}

func (c *Client) ResolveTenantID(context.Context) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.ValidateErr != nil {
		return "", c.ValidateErr
	}
	return c.TenantID, nil
}

func (c *Client) GetInstanceTypeWithAllocationStats(_ context.Context, instanceTypeID string) (*nicosdk.InstanceType, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	it, ok := c.instanceTypes[instanceTypeID]
	if !ok {
		return nil, fmt.Errorf("%w: instance type %q", nico.ErrNotFound, instanceTypeID)
	}
	return cloneInstanceType(it), nil
}

func (c *Client) CreateInstance(_ context.Context, req nicosdk.InstanceCreateRequest) (*nicosdk.Instance, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	copied := req
	c.lastCreateRequest = &copied

	for _, existing := range c.instances {
		if existing.GetName() == req.GetName() {
			return nil, fmt.Errorf("%w: instance %q", nico.ErrAlreadyExists, req.GetName())
		}
	}

	c.nextID++
	id := fmt.Sprintf("instance-%d", c.nextID)
	inst := nicosdk.NewInstance()
	inst.SetId(id)
	inst.SetName(req.GetName())
	inst.SetTenantId(req.GetTenantId())
	inst.SetVpcId(req.GetVpcId())
	inst.SetSiteId(c.DefaultSiteID)
	inst.SetStatus(nicosdk.INSTANCESTATUS_READY)
	if req.HasInstanceTypeId() {
		inst.SetInstanceTypeId(req.GetInstanceTypeId())
	}
	if req.HasMachineId() {
		inst.SetMachineId(req.GetMachineId())
	}
	if req.HasLabels() {
		inst.SetLabels(req.GetLabels())
	}

	iface := nicosdk.NewInterface()
	iface.SetIpAddresses([]string{c.DefaultIP})
	inst.SetInterfaces([]nicosdk.Interface{*iface})

	c.instances[id] = inst
	return cloneInstance(inst), nil
}

func (c *Client) DeleteInstance(_ context.Context, instanceID string, _ *nicosdk.MachineHealthIssue) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, ok := c.instances[instanceID]; !ok {
		return fmt.Errorf("%w: instance %q", nico.ErrNotFound, instanceID)
	}
	delete(c.instances, instanceID)
	return nil
}

func (c *Client) TriggerInstanceReboot(_ context.Context, instanceID string) (*nicosdk.Instance, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	inst, ok := c.instances[instanceID]
	if !ok {
		return nil, fmt.Errorf("%w: instance %q", nico.ErrNotFound, instanceID)
	}
	return cloneInstance(inst), nil
}

func (c *Client) ApplyInstanceLabels(_ context.Context, instanceID string, labels map[string]string) (*nicosdk.Instance, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	inst, ok := c.instances[instanceID]
	if !ok {
		return nil, fmt.Errorf("%w: instance %q", nico.ErrNotFound, instanceID)
	}
	copiedLabels := make(map[string]string, len(labels))
	for k, v := range labels {
		copiedLabels[k] = v
	}
	c.lastAppliedLabels = copiedLabels

	merged := map[string]string{}
	for k, v := range inst.GetLabels() {
		merged[k] = v
	}
	for k, v := range labels {
		merged[k] = v
	}
	inst.SetLabels(merged)
	return cloneInstance(inst), nil
}

func (c *Client) GetInstance(_ context.Context, instanceID string) (*nicosdk.Instance, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	inst, ok := c.instances[instanceID]
	if !ok {
		return nil, fmt.Errorf("%w: instance %q", nico.ErrNotFound, instanceID)
	}
	return cloneInstance(inst), nil
}

func (c *Client) GetSite(_ context.Context, siteID string) (*nicosdk.Site, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	site, ok := c.sites[siteID]
	if !ok {
		return nil, fmt.Errorf("%w: site %q", nico.ErrNotFound, siteID)
	}
	out := *site
	return &out, nil
}

func (c *Client) GetVPC(_ context.Context, vpcID string) (*nicosdk.VPC, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	vpc, ok := c.vpcs[vpcID]
	if !ok {
		return nil, fmt.Errorf("%w: vpc %q", nico.ErrNotFound, vpcID)
	}
	out := *vpc
	return &out, nil
}

func (c *Client) FindInstanceByName(_ context.Context, lookup nico.InstanceLookup) (*nicosdk.Instance, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, inst := range c.instances {
		if inst.GetName() != lookup.Name {
			continue
		}
		if lookup.VPCID != "" && inst.GetVpcId() != lookup.VPCID {
			continue
		}
		if lookup.SiteID != "" && inst.GetSiteId() != lookup.SiteID {
			continue
		}
		return cloneInstance(inst), nil
	}
	return nil, fmt.Errorf("%w: instance name %q", nico.ErrNotFound, lookup.Name)
}

func cloneInstance(in *nicosdk.Instance) *nicosdk.Instance {
	if in == nil {
		return nil
	}
	out := *in
	if in.Status != nil {
		status := *in.Status
		out.Status = &status
	}
	if in.Labels != nil {
		labels := map[string]string{}
		for k, v := range in.Labels {
			labels[k] = v
		}
		out.Labels = labels
	}
	if in.Interfaces != nil {
		ifaces := make([]nicosdk.Interface, len(in.Interfaces))
		copy(ifaces, in.Interfaces)
		out.Interfaces = ifaces
	}
	return &out
}

func cloneInstanceType(in *nicosdk.InstanceType) *nicosdk.InstanceType {
	if in == nil {
		return nil
	}
	out := *in
	if in.AllocationStats != nil {
		stats := *in.AllocationStats
		out.AllocationStats = &stats
	}
	return &out
}
