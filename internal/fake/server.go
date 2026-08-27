// SPDX-FileCopyrightText: Copyright (c) 2026 NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

// Package fake implements a self-contained stand-in for the NICo API. Tests run
// the production client against Handler without requiring a NICo deployment.
//
// The served surface mirrors the published NICo SDK models and wire format for
// the operations used by the controllers. Provider tests should extend this
// HTTP surface instead of bypassing serialization with an injected Go client.
package fake

import (
	"encoding/json"
	"fmt"
	"io"
	"maps"
	"net/http"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"sync"

	nicosdk "github.com/NVIDIA/ncx-infra-controller-rest/sdk/standard"
	"sigs.k8s.io/yaml"
)

const (
	// ReadyAfterPolls is how many reads an instance stays in a transitional
	// state before reporting its next state.
	ReadyAfterPolls  = 2
	defaultIPAddress = "10.0.0.10"
	statusTerminated = "Terminated"
)

type operation string

const (
	operationCreateInstance operation = "create-instance"
	operationUpdateInstance operation = "update-instance"
	operationDeleteInstance operation = "delete-instance"
)

type instanceRecord struct {
	org         string
	instance    nicosdk.Instance
	polls       int
	rebootCount int
}

type requestRecord struct {
	Operation  operation                      `json:"operation"`
	Org        string                         `json:"org"`
	InstanceID string                         `json:"instance_id,omitempty"`
	Create     *nicosdk.InstanceCreateRequest `json:"create,omitempty"`
	Update     *nicosdk.InstanceUpdateRequest `json:"update,omitempty"`
	Delete     *nicosdk.InstanceDeleteRequest `json:"delete,omitempty"`
}

type tenantDump struct {
	Org      string         `json:"org"`
	Resource nicosdk.Tenant `json:"resource"`
}

type instanceTypeDump struct {
	Org      string               `json:"org"`
	Resource nicosdk.InstanceType `json:"resource"`
}

type instanceDump struct {
	Org         string           `json:"org"`
	Resource    nicosdk.Instance `json:"resource"`
	RebootCount int              `json:"reboot_count,omitempty"`
}

type siteDump struct {
	Org      string       `json:"org"`
	Resource nicosdk.Site `json:"resource"`
}

type vpcDump struct {
	Org      string      `json:"org"`
	Resource nicosdk.VPC `json:"resource"`
}

type serverDump struct {
	Tenants       []tenantDump       `json:"tenants"`
	InstanceTypes []instanceTypeDump `json:"instance_types"`
	Instances     []instanceDump     `json:"instances"`
	Sites         []siteDump         `json:"sites"`
	VPCs          []vpcDump          `json:"vpcs"`
	Requests      []requestRecord    `json:"requests"`
}

// Server owns the fake's in-memory state and serves the OAuth2 and NICo API
// routes. All state is isolated to one Server so controller cases can run in
// parallel without sharing external resources.
type Server struct {
	mu sync.Mutex

	// Authentication state is configured through SeedToken or SeedClient. See
	// auth.go. With neither configured the handler permits unauthenticated API
	// calls so callers can opt into the authentication mode they need to test.
	staticToken  string
	clientID     string
	clientSecret string

	tokenRequests int

	tenants       map[string]*nicosdk.Tenant
	instanceTypes map[string]*nicosdk.InstanceType
	instances     map[string]*instanceRecord
	sites         map[string]*nicosdk.Site
	vpcs          map[string]*nicosdk.VPC

	requests []requestRecord

	nextID int
}

// New returns an isolated fake with no NICo resources. Each test case seeds the
// tenant and resources it owns explicitly.
func New() *Server {
	return &Server{
		tenants:       map[string]*nicosdk.Tenant{},
		instanceTypes: map[string]*nicosdk.InstanceType{},
		instances:     map[string]*instanceRecord{},
		sites:         map[string]*nicosdk.Site{},
		vpcs:          map[string]*nicosdk.VPC{},
	}
}

// Handler returns the HTTP surface used by controller envtests.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()

	// OAuth2 token surface.
	mux.HandleFunc("POST "+tokenPath, s.issueToken)

	// Controller-used NICo surface.
	mux.HandleFunc("GET /v2/org/{org}/carbide/tenant/current", s.getCurrentTenant)
	mux.HandleFunc("GET /v2/org/{org}/carbide/instance/type/{instanceTypeID}", s.getInstanceType)
	mux.HandleFunc("POST /v2/org/{org}/carbide/instance", s.createInstance)
	mux.HandleFunc("GET /v2/org/{org}/carbide/instance", s.listInstances)
	mux.HandleFunc("GET /v2/org/{org}/carbide/instance/{instanceID}", s.getInstance)
	mux.HandleFunc("PATCH /v2/org/{org}/carbide/instance/{instanceID}", s.updateInstance)
	mux.HandleFunc("DELETE /v2/org/{org}/carbide/instance/{instanceID}", s.deleteInstance)
	mux.HandleFunc("GET /v2/org/{org}/carbide/site", s.listSites)
	mux.HandleFunc("GET /v2/org/{org}/carbide/vpc/{vpcID}", s.getVPC)

	return s.authenticate(mux)
}

// SeedTenant adds or replaces the current tenant for org.
func (s *Server) SeedTenant(org string, tenant nicosdk.Tenant) {
	s.mu.Lock()
	defer s.mu.Unlock()

	copy := tenant
	if copy.GetOrg() == "" {
		copy.SetOrg(org)
	}
	s.tenants[org] = &copy
}

// SeedInstanceType adds or replaces an instance type visible to org.
func (s *Server) SeedInstanceType(org string, instanceType nicosdk.InstanceType) {
	s.mu.Lock()
	defer s.mu.Unlock()

	copy := cloneInstanceType(instanceType)
	s.instanceTypes[resourceKey(org, copy.GetId())] = &copy
}

// SeedInstance adds or replaces an instance visible to org.
func (s *Server) SeedInstance(org string, instance nicosdk.Instance) {
	s.mu.Lock()
	defer s.mu.Unlock()

	copy := cloneInstance(instance)
	s.instances[resourceKey(org, copy.GetId())] = &instanceRecord{org: org, instance: copy}
}

// SeedSite adds or replaces a site visible to org.
func (s *Server) SeedSite(org string, site nicosdk.Site) {
	s.mu.Lock()
	defer s.mu.Unlock()

	copy := site
	if copy.GetOrg() == "" {
		copy.SetOrg(org)
	}
	s.sites[resourceKey(org, copy.GetId())] = &copy
}

// SeedVPC adds or replaces a VPC visible to org.
func (s *Server) SeedVPC(org string, vpc nicosdk.VPC) {
	s.mu.Lock()
	defer s.mu.Unlock()

	copy := vpc
	copy.Labels = maps.Clone(vpc.Labels)
	if copy.GetOrg() == "" {
		copy.SetOrg(org)
	}
	s.vpcs[resourceKey(org, copy.GetId())] = &copy
}

// SeedFromYAML adds resources declared by a controller fixture. The input uses
// the resource sections emitted by Dump; requests are observations and cannot
// be seeded.
func (s *Server) SeedFromYAML(input string) error {
	var seed serverDump
	if err := yaml.Unmarshal([]byte(input), &seed); err != nil {
		return fmt.Errorf("decode fake server resources: %w", err)
	}
	if len(seed.Requests) != 0 {
		return fmt.Errorf("fake server requests cannot be seeded")
	}

	for _, resource := range seed.Tenants {
		if resource.Org == "" || resource.Resource.GetId() == "" {
			return fmt.Errorf("seeded tenant requires org and resource.id")
		}
		s.SeedTenant(resource.Org, resource.Resource)
	}
	for _, resource := range seed.InstanceTypes {
		if resource.Org == "" || resource.Resource.GetId() == "" {
			return fmt.Errorf("seeded instance type requires org and resource.id")
		}
		s.SeedInstanceType(resource.Org, resource.Resource)
	}
	for _, resource := range seed.Instances {
		if resource.Org == "" || resource.Resource.GetId() == "" {
			return fmt.Errorf("seeded instance requires org and resource.id")
		}
		s.SeedInstance(resource.Org, resource.Resource)
	}
	for _, resource := range seed.Sites {
		if resource.Org == "" || resource.Resource.GetId() == "" {
			return fmt.Errorf("seeded site requires org and resource.id")
		}
		s.SeedSite(resource.Org, resource.Resource)
	}
	for _, resource := range seed.VPCs {
		if resource.Org == "" || resource.Resource.GetId() == "" {
			return fmt.Errorf("seeded VPC requires org and resource.id")
		}
		s.SeedVPC(resource.Org, resource.Resource)
	}
	return nil
}

// InstanceCount returns the number of instances that have not terminated.
func (s *Server) InstanceCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()

	count := 0
	for _, record := range s.instances {
		if !statusEqual(record.instance.GetStatus(), statusTerminated) {
			count++
		}
	}
	return count
}

// Dump returns a deterministic YAML representation of external NICo state and
// write requests. Read polling is excluded so reconciliation timing cannot make
// golden files nondeterministic.
func (s *Server) Dump() (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	dump := serverDump{
		Tenants:       make([]tenantDump, 0, len(s.tenants)),
		InstanceTypes: make([]instanceTypeDump, 0, len(s.instanceTypes)),
		Instances:     make([]instanceDump, 0, len(s.instances)),
		Sites:         make([]siteDump, 0, len(s.sites)),
		VPCs:          make([]vpcDump, 0, len(s.vpcs)),
		Requests:      deduplicateRequests(s.requests),
	}
	for org, tenant := range s.tenants {
		dump.Tenants = append(dump.Tenants, tenantDump{Org: org, Resource: *tenant})
	}
	for key, instanceType := range s.instanceTypes {
		dump.InstanceTypes = append(dump.InstanceTypes, instanceTypeDump{Org: orgFromKey(key), Resource: cloneInstanceType(*instanceType)})
	}
	for _, record := range s.instances {
		dump.Instances = append(dump.Instances, instanceDump{Org: record.org, Resource: cloneInstance(record.instance), RebootCount: record.rebootCount})
	}
	for key, site := range s.sites {
		dump.Sites = append(dump.Sites, siteDump{Org: orgFromKey(key), Resource: *site})
	}
	for key, vpc := range s.vpcs {
		copy := *vpc
		copy.Labels = maps.Clone(vpc.Labels)
		dump.VPCs = append(dump.VPCs, vpcDump{Org: orgFromKey(key), Resource: copy})
	}

	slices.SortFunc(dump.Tenants, func(a, b tenantDump) int {
		return compareResource(a.Org, a.Resource.GetId(), b.Org, b.Resource.GetId())
	})
	slices.SortFunc(dump.InstanceTypes, func(a, b instanceTypeDump) int {
		return compareResource(a.Org, a.Resource.GetId(), b.Org, b.Resource.GetId())
	})
	slices.SortFunc(dump.Instances, func(a, b instanceDump) int {
		return compareResource(a.Org, a.Resource.GetId(), b.Org, b.Resource.GetId())
	})
	slices.SortFunc(dump.Sites, func(a, b siteDump) int { return compareResource(a.Org, a.Resource.GetId(), b.Org, b.Resource.GetId()) })
	slices.SortFunc(dump.VPCs, func(a, b vpcDump) int { return compareResource(a.Org, a.Resource.GetId(), b.Org, b.Resource.GetId()) })

	content, err := yaml.Marshal(dump)
	if err != nil {
		return "", fmt.Errorf("marshal fake server state: %w", err)
	}
	return string(content), nil
}

func deduplicateRequests(requests []requestRecord) []requestRecord {
	deduplicated := make([]requestRecord, 0, len(requests))
	for _, request := range requests {
		if slices.ContainsFunc(deduplicated, func(existing requestRecord) bool {
			return reflect.DeepEqual(existing, request)
		}) {
			continue
		}
		deduplicated = append(deduplicated, request)
	}
	return deduplicated
}

func (s *Server) getCurrentTenant(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	tenant, ok := s.tenants[r.PathValue("org")]
	s.mu.Unlock()
	if !ok {
		writeError(w, http.StatusNotFound, "current tenant not found")
		return
	}
	writeJSON(w, http.StatusOK, tenant)
}

func (s *Server) getInstanceType(w http.ResponseWriter, r *http.Request) {
	org, instanceTypeID := r.PathValue("org"), r.PathValue("instanceTypeID")
	s.mu.Lock()
	instanceType, ok := s.instanceTypes[resourceKey(org, instanceTypeID)]
	if ok {
		copy := cloneInstanceType(*instanceType)
		instanceType = &copy
	}
	s.mu.Unlock()
	if !ok {
		writeError(w, http.StatusNotFound, "instance type not found")
		return
	}
	if r.URL.Query().Get("includeAllocationStats") != "true" {
		instanceType.AllocationStats = nil
	}
	writeJSON(w, http.StatusOK, instanceType)
}

func (s *Server) createInstance(w http.ResponseWriter, r *http.Request) {
	var request nicosdk.InstanceCreateRequest
	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		writeError(w, http.StatusBadRequest, "malformed instance create request")
		return
	}
	if request.GetName() == "" || request.GetTenantId() == "" || request.GetVpcId() == "" || len(request.GetInterfaces()) == 0 {
		writeError(w, http.StatusBadRequest, "name, tenantId, vpcId, and interfaces are required")
		return
	}
	if request.HasInstanceTypeId() == request.HasMachineId() {
		writeError(w, http.StatusBadRequest, "exactly one of instanceTypeId or machineId is required")
		return
	}

	org := r.PathValue("org")
	s.mu.Lock()
	tenant, tenantOK := s.tenants[org]
	vpc, vpcOK := s.vpcs[resourceKey(org, request.GetVpcId())]
	if !tenantOK || tenant.GetId() != request.GetTenantId() {
		s.mu.Unlock()
		writeError(w, http.StatusBadRequest, "tenantId does not match the current tenant")
		return
	}
	if !vpcOK {
		s.mu.Unlock()
		writeError(w, http.StatusBadRequest, "vpc not found")
		return
	}
	if request.HasInstanceTypeId() {
		if _, ok := s.instanceTypes[resourceKey(org, request.GetInstanceTypeId())]; !ok {
			s.mu.Unlock()
			writeError(w, http.StatusBadRequest, "instance type not found")
			return
		}
	}
	for _, existing := range s.instances {
		if existing.org == org && existing.instance.GetName() == request.GetName() && !statusEqual(existing.instance.GetStatus(), statusTerminated) {
			s.mu.Unlock()
			writeError(w, http.StatusConflict, "instance already exists")
			return
		}
	}

	id := s.mintID("instance")
	instance := nicosdk.NewInstance()
	instance.SetId(id)
	instance.SetName(request.GetName())
	instance.SetTenantId(request.GetTenantId())
	instance.SetVpcId(request.GetVpcId())
	instance.SetSiteId(vpc.GetSiteId())
	instance.SetStatus(nicosdk.INSTANCESTATUS_PENDING)
	if request.HasInstanceTypeId() {
		instance.SetInstanceTypeId(request.GetInstanceTypeId())
	}
	if request.HasMachineId() {
		instance.SetMachineId(request.GetMachineId())
	}
	if request.HasLabels() {
		instance.SetLabels(maps.Clone(request.GetLabels()))
	}
	interfaces, err := instanceInterfaces(request.GetInterfaces())
	if err != nil {
		s.mu.Unlock()
		writeError(w, http.StatusBadRequest, "invalid instance interface request")
		return
	}
	instance.SetInterfaces(interfaces)

	record := &instanceRecord{org: org, instance: *instance}
	s.instances[resourceKey(org, id)] = record
	requestCopy := request
	s.requests = append(s.requests, requestRecord{
		Operation:  operationCreateInstance,
		Org:        org,
		InstanceID: id,
		Create:     &requestCopy,
	})
	response := cloneInstance(record.instance)
	s.mu.Unlock()

	writeJSON(w, http.StatusCreated, &response)
}

func (s *Server) listInstances(w http.ResponseWriter, r *http.Request) {
	org := r.PathValue("org")
	query := r.URL.Query()
	s.mu.Lock()
	instances := make([]nicosdk.Instance, 0, len(s.instances))
	for _, record := range s.instances {
		if record.org != org || !matchesInstanceQuery(&record.instance, query.Get("name"), query.Get("vpcId"), query.Get("siteId")) {
			continue
		}
		instances = append(instances, cloneInstance(record.instance))
	}
	s.mu.Unlock()
	slices.SortFunc(instances, func(a, b nicosdk.Instance) int { return strings.Compare(a.GetId(), b.GetId()) })
	instances = paginate(instances, query.Get("pageNumber"), query.Get("pageSize"))
	writeJSON(w, http.StatusOK, instances)
}

func (s *Server) getInstance(w http.ResponseWriter, r *http.Request) {
	org, instanceID := r.PathValue("org"), r.PathValue("instanceID")
	s.mu.Lock()
	record, ok := s.instances[resourceKey(org, instanceID)]
	if ok {
		s.advanceInstance(record)
	}
	var response nicosdk.Instance
	if ok {
		response = cloneInstance(record.instance)
	}
	s.mu.Unlock()
	if !ok {
		writeError(w, http.StatusNotFound, "instance not found")
		return
	}
	writeJSON(w, http.StatusOK, &response)
}

func (s *Server) updateInstance(w http.ResponseWriter, r *http.Request) {
	var request nicosdk.InstanceUpdateRequest
	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		writeError(w, http.StatusBadRequest, "malformed instance update request")
		return
	}
	if request.Labels == nil && !request.GetTriggerReboot() {
		writeError(w, http.StatusBadRequest, "labels or triggerReboot is required")
		return
	}
	org, instanceID := r.PathValue("org"), r.PathValue("instanceID")
	s.mu.Lock()
	record, ok := s.instances[resourceKey(org, instanceID)]
	if !ok {
		s.mu.Unlock()
		writeError(w, http.StatusNotFound, "instance not found")
		return
	}
	if request.Labels != nil {
		record.instance.SetLabels(maps.Clone(request.Labels))
	}
	if request.GetTriggerReboot() {
		record.rebootCount++
	}
	requestCopy := request
	s.requests = append(s.requests, requestRecord{
		Operation:  operationUpdateInstance,
		Org:        org,
		InstanceID: instanceID,
		Update:     &requestCopy,
	})
	response := cloneInstance(record.instance)
	s.mu.Unlock()

	writeJSON(w, http.StatusOK, &response)
}

func (s *Server) deleteInstance(w http.ResponseWriter, r *http.Request) {
	request, err := decodeDeleteRequest(r.Body)
	if err != nil {
		writeError(w, http.StatusBadRequest, "malformed instance delete request")
		return
	}

	org, instanceID := r.PathValue("org"), r.PathValue("instanceID")
	s.mu.Lock()
	record, ok := s.instances[resourceKey(org, instanceID)]
	if !ok {
		s.mu.Unlock()
		writeError(w, http.StatusNotFound, "instance not found")
		return
	}
	record.instance.SetStatus(nicosdk.INSTANCESTATUS_TERMINATING)
	record.polls = 0
	s.requests = append(s.requests, requestRecord{
		Operation:  operationDeleteInstance,
		Org:        org,
		InstanceID: instanceID,
		Delete:     request,
	})
	s.mu.Unlock()

	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) listSites(w http.ResponseWriter, r *http.Request) {
	org := r.PathValue("org")
	query := r.URL.Query()
	s.mu.Lock()
	tenant, tenantOK := s.tenants[org]
	sites := make([]nicosdk.Site, 0, len(s.sites))
	if tenantOK && (query.Get("tenantId") == "" || query.Get("tenantId") == tenant.GetId()) {
		for key, site := range s.sites {
			if orgFromKey(key) == org {
				sites = append(sites, *site)
			}
		}
	}
	s.mu.Unlock()
	slices.SortFunc(sites, func(a, b nicosdk.Site) int { return strings.Compare(a.GetId(), b.GetId()) })
	sites = paginate(sites, query.Get("pageNumber"), query.Get("pageSize"))
	writeJSON(w, http.StatusOK, sites)
}

func (s *Server) getVPC(w http.ResponseWriter, r *http.Request) {
	org, vpcID := r.PathValue("org"), r.PathValue("vpcID")
	s.mu.Lock()
	vpc, ok := s.vpcs[resourceKey(org, vpcID)]
	s.mu.Unlock()
	if !ok {
		writeError(w, http.StatusNotFound, "vpc not found")
		return
	}
	writeJSON(w, http.StatusOK, vpc)
}

func (s *Server) advanceInstance(record *instanceRecord) {
	status := record.instance.GetStatus()
	switch {
	case statusEqual(status, string(nicosdk.INSTANCESTATUS_PENDING)):
		record.polls++
		if record.polls >= ReadyAfterPolls {
			record.instance.SetStatus(nicosdk.INSTANCESTATUS_READY)
			assignInstanceAddresses(&record.instance, defaultIPAddress)
		}
	case statusEqual(status, string(nicosdk.INSTANCESTATUS_TERMINATING)):
		record.polls++
		if record.polls >= ReadyAfterPolls {
			record.instance.SetStatus(nicosdk.InstanceStatus(statusTerminated))
			clearInstanceAddresses(&record.instance)
		}
	}
}

func (s *Server) mintID(prefix string) string {
	s.nextID++
	return fmt.Sprintf("%s-%08x", prefix, s.nextID)
}

func resourceKey(org, id string) string {
	return org + "\x00" + id
}

func orgFromKey(key string) string {
	org, _, _ := strings.Cut(key, "\x00")
	return org
}

func compareResource(aOrg, aID, bOrg, bID string) int {
	if compared := strings.Compare(aOrg, bOrg); compared != 0 {
		return compared
	}
	return strings.Compare(aID, bID)
}

func statusEqual(actual nicosdk.InstanceStatus, expected string) bool {
	return strings.EqualFold(string(actual), expected)
}

func matchesInstanceQuery(instance *nicosdk.Instance, name, vpcID, siteID string) bool {
	return (name == "" || instance.GetName() == name) &&
		(vpcID == "" || instance.GetVpcId() == vpcID) &&
		(siteID == "" || instance.GetSiteId() == siteID)
}

func instanceInterfaces(requests []nicosdk.InterfaceCreateRequest) ([]nicosdk.Interface, error) {
	interfaces := make([]nicosdk.Interface, 0, len(requests))
	for i := range requests {
		encoded, err := json.Marshal(requests[i])
		if err != nil {
			return nil, fmt.Errorf("marshal interface %d: %w", i, err)
		}
		var response nicosdk.Interface
		if err := json.Unmarshal(encoded, &response); err != nil {
			return nil, fmt.Errorf("decode interface %d: %w", i, err)
		}
		interfaces = append(interfaces, response)
	}
	return interfaces, nil
}

func assignInstanceAddresses(instance *nicosdk.Instance, address string) {
	interfaces := slices.Clone(instance.GetInterfaces())
	for i := range interfaces {
		if len(interfaces[i].GetIpAddresses()) == 0 {
			interfaces[i].SetIpAddresses([]string{address})
		}
	}
	instance.SetInterfaces(interfaces)
}

func clearInstanceAddresses(instance *nicosdk.Instance) {
	interfaces := slices.Clone(instance.GetInterfaces())
	for i := range interfaces {
		interfaces[i].SetIpAddresses(nil)
	}
	instance.SetInterfaces(interfaces)
}

func decodeDeleteRequest(body io.Reader) (*nicosdk.InstanceDeleteRequest, error) {
	content, err := io.ReadAll(body)
	if err != nil {
		return nil, err
	}
	if len(strings.TrimSpace(string(content))) == 0 || strings.TrimSpace(string(content)) == "null" {
		return nil, nil
	}
	var request nicosdk.InstanceDeleteRequest
	if err := json.Unmarshal(content, &request); err != nil {
		return nil, err
	}
	return &request, nil
}

func paginate[T any](items []T, rawPage, rawSize string) []T {
	page, size := 1, len(items)
	if parsed, err := strconv.Atoi(rawPage); err == nil && parsed > 0 {
		page = parsed
	}
	if parsed, err := strconv.Atoi(rawSize); err == nil && parsed > 0 {
		size = parsed
	}
	if size == 0 {
		return items
	}
	start := (page - 1) * size
	if start >= len(items) {
		return []T{}
	}
	return items[start:min(start+size, len(items))]
}

func cloneInstance(instance nicosdk.Instance) nicosdk.Instance {
	copy := instance
	copy.Labels = maps.Clone(instance.Labels)
	copy.Interfaces = slices.Clone(instance.Interfaces)
	copy.InfinibandInterfaces = slices.Clone(instance.InfinibandInterfaces)
	copy.NvLinkInterfaces = slices.Clone(instance.NvLinkInterfaces)
	copy.SecondaryVpcIds = slices.Clone(instance.SecondaryVpcIds)
	copy.SshKeyGroupIds = slices.Clone(instance.SshKeyGroupIds)
	return copy
}

func cloneInstanceType(instanceType nicosdk.InstanceType) nicosdk.InstanceType {
	copy := instanceType
	copy.Labels = maps.Clone(instanceType.Labels)
	copy.MachineCapabilities = slices.Clone(instanceType.MachineCapabilities)
	copy.MachineInstanceTypes = slices.Clone(instanceType.MachineInstanceTypes)
	if instanceType.AllocationStats != nil {
		stats := *instanceType.AllocationStats
		copy.AllocationStats = &stats
	}
	return copy
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	// The status line is already on the wire, so a failed encode can only be
	// observed by the client as a truncated response and retried.
	_ = json.NewEncoder(w).Encode(body)
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": message})
}
