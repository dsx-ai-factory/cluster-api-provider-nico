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

	nicosdk "github.com/NVIDIA/infra-controller/rest-api/sdk/standard"
	"sigs.k8s.io/yaml"
)

const (
	// ReadyAfterPolls is how many reads an instance stays in a transitional
	// state before reporting its next state.
	ReadyAfterPolls       = 2
	defaultIPAddress      = "10.0.0.10"
	defaultInstanceTypeID = "type-1"
	defaultOrgID          = "org-1"
	defaultSiteID         = "site-1"
	defaultTenantID       = "tenant-1"
	defaultVPCID          = "vpc-1"
	statusTerminated      = "Terminated"
)

type operation string

const (
	operationCreateInstance operation = "create-instance"
	operationUpdateInstance operation = "update-instance"
	operationDeleteInstance operation = "delete-instance"
	operationMachinePower   operation = "machine-power"
)

type instanceRecord struct {
	org         string
	instance    nicosdk.Instance
	polls       int
	rebootCount int
}

type requestRecord struct {
	Operation  operation                           `json:"operation"`
	Org        string                              `json:"org"`
	InstanceID string                              `json:"instance_id,omitempty"`
	MachineID  string                              `json:"machine_id,omitempty"`
	Create     *nicosdk.InstanceCreateRequest      `json:"create,omitempty"`
	Update     *nicosdk.InstanceUpdateRequest      `json:"update,omitempty"`
	Delete     *nicosdk.InstanceDeleteRequest      `json:"delete,omitempty"`
	Power      *nicosdk.MachinePowerControlRequest `json:"power,omitempty"`
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

type machineDump struct {
	Org      string          `json:"org"`
	Resource nicosdk.Machine `json:"resource"`
}

type siteDump struct {
	Org      string       `json:"org"`
	Resource nicosdk.Site `json:"resource"`
}

type vpcDump struct {
	Org      string      `json:"org"`
	Resource nicosdk.VPC `json:"resource"`
}

type resourceCollection[T any] struct {
	StatusCode int `json:"statusCode,omitempty"`
	Resources  T   `json:"resources,omitempty"`
}

type serverSeed struct {
	Tenants       resourceCollection[[]tenantDump]       `json:"tenants"`
	InstanceTypes resourceCollection[[]instanceTypeDump] `json:"instance_types"`
	Instances     resourceCollection[[]instanceDump]     `json:"instances"`
	Machines      resourceCollection[[]machineDump]      `json:"machines"`
	Sites         resourceCollection[[]siteDump]         `json:"sites"`
	VPCs          resourceCollection[[]vpcDump]          `json:"vpcs"`
	Requests      []requestRecord                        `json:"requests"`
}

type serverDump struct {
	Tenants       []tenantDump       `json:"tenants"`
	InstanceTypes []instanceTypeDump `json:"instance_types"`
	Instances     []instanceDump     `json:"instances"`
	Machines      []machineDump      `json:"machines,omitempty"`
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

	tenants       resourceCollection[map[string]*nicosdk.Tenant]
	instanceTypes resourceCollection[map[string]*nicosdk.InstanceType]
	instances     resourceCollection[map[string]*instanceRecord]
	machines      resourceCollection[map[string]*nicosdk.Machine]
	sites         resourceCollection[map[string]*nicosdk.Site]
	vpcs          resourceCollection[map[string]*nicosdk.VPC]

	requests []requestRecord

	nextID             int
	powerControlStatus int
	powerControlCalls  int
}

// New returns a Server seeded with the resources needed by the worked example.
func New() *Server {
	s := &Server{
		tenants:       resourceCollection[map[string]*nicosdk.Tenant]{Resources: map[string]*nicosdk.Tenant{}},
		instanceTypes: resourceCollection[map[string]*nicosdk.InstanceType]{Resources: map[string]*nicosdk.InstanceType{}},
		instances:     resourceCollection[map[string]*instanceRecord]{Resources: map[string]*instanceRecord{}},
		machines:      resourceCollection[map[string]*nicosdk.Machine]{Resources: map[string]*nicosdk.Machine{}},
		sites:         resourceCollection[map[string]*nicosdk.Site]{Resources: map[string]*nicosdk.Site{}},
		vpcs:          resourceCollection[map[string]*nicosdk.VPC]{Resources: map[string]*nicosdk.VPC{}},
	}

	tenant := nicosdk.NewTenant()
	tenant.SetId(defaultTenantID)
	tenant.SetOrg(defaultOrgID)
	s.SeedTenant(defaultOrgID, tenant, 0)

	instanceType := nicosdk.NewInstanceType()
	instanceType.SetId(defaultInstanceTypeID)
	allocation := nicosdk.NewInstanceTypeAllocationStats()
	allocation.SetTotal(1)
	allocation.SetUnused(1)
	allocation.SetUnusedUsable(1)
	allocation.SetUsed(0)
	instanceType.SetAllocationStats(*allocation)
	s.SeedInstanceType(defaultOrgID, instanceType, 0)

	site := nicosdk.NewSite()
	site.SetId(defaultSiteID)
	site.SetName("fake-site")
	site.SetOrg(defaultOrgID)
	s.SeedSite(defaultOrgID, site, 0)

	vpc := nicosdk.NewVPC()
	vpc.SetId(defaultVPCID)
	vpc.SetName("fake-vpc")
	vpc.SetOrg(defaultOrgID)
	vpc.SetTenantId(defaultTenantID)
	vpc.SetSiteId(defaultSiteID)
	s.SeedVPC(defaultOrgID, vpc, 0)

	return s
}

// Handler returns the HTTP surface used by controller envtests.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()

	// OAuth2 token surface.
	mux.HandleFunc("POST "+tokenPath, s.issueToken)

	// Controller-used NICo surface.
	mux.HandleFunc("GET /v2/org/{org}/nico/tenant/current", handle(s, &s.tenants, s.getCurrentTenant))
	mux.HandleFunc("GET /v2/org/{org}/nico/instance/type/{instanceTypeID}", handle(s, &s.instanceTypes, s.getInstanceType))
	mux.HandleFunc("POST /v2/org/{org}/nico/instance", handle(s, &s.instances, s.createInstance))
	mux.HandleFunc("GET /v2/org/{org}/nico/instance", handle(s, &s.instances, s.listInstances))
	mux.HandleFunc("GET /v2/org/{org}/nico/instance/{instanceID}", handle(s, &s.instances, s.getInstance))
	mux.HandleFunc("PATCH /v2/org/{org}/nico/instance/{instanceID}", handle(s, &s.instances, s.updateInstance))
	mux.HandleFunc("DELETE /v2/org/{org}/nico/instance/{instanceID}", handle(s, &s.instances, s.deleteInstance))
	mux.HandleFunc("GET /v2/org/{org}/nico/machine", handle(s, &s.machines, s.listMachines))
	mux.HandleFunc("GET /v2/org/{org}/nico/machine/{machineID}", handle(s, &s.machines, s.getMachine))
	mux.HandleFunc("PATCH /v2/org/{org}/nico/machine/{machineID}/power", handle(s, &s.machines, s.powerControlMachine))
	mux.HandleFunc("GET /v2/org/{org}/nico/site", handle(s, &s.sites, s.listSites))
	mux.HandleFunc("GET /v2/org/{org}/nico/vpc/{vpcID}", handle(s, &s.vpcs, s.getVPC))

	return s.authenticate(mux)
}

func handle[T any](s *Server, collection *resourceCollection[T], handler http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		status := collection.StatusCode
		s.mu.Unlock()
		if status != 0 {
			writeError(w, status, http.StatusText(status))
			return
		}
		handler(w, r)
	}
}

// SeedTenant adds or replaces the current tenant for org.
func (s *Server) SeedTenant(org string, tenant *nicosdk.Tenant, statusCode int) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.tenants.StatusCode = statusCode
	if tenant == nil {
		return
	}
	copy := *tenant
	if copy.GetOrg() == "" {
		copy.SetOrg(org)
	}
	s.tenants.Resources[org] = &copy
}

// SeedInstanceType adds or replaces an instance type visible to org.
func (s *Server) SeedInstanceType(org string, instanceType *nicosdk.InstanceType, statusCode int) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.instanceTypes.StatusCode = statusCode
	if instanceType == nil {
		return
	}
	copy := cloneInstanceType(*instanceType)
	s.instanceTypes.Resources[resourceKey(org, copy.GetId())] = &copy
}

// SeedInstance adds or replaces an instance visible to org.
func (s *Server) SeedInstance(org string, instance *nicosdk.Instance, statusCode int) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.instances.StatusCode = statusCode
	if instance == nil {
		return
	}
	copy := cloneInstance(*instance)
	s.instances.Resources[resourceKey(org, copy.GetId())] = &instanceRecord{org: org, instance: copy}
}

// SeedMachine adds or replaces a machine visible to org.
func (s *Server) SeedMachine(org string, machine *nicosdk.Machine, statusCode int) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.machines.StatusCode = statusCode
	if machine == nil {
		return
	}
	copy := cloneMachine(*machine)
	s.machines.Resources[resourceKey(org, copy.GetId())] = &copy
}

// SeedSite adds or replaces a site visible to org.
func (s *Server) SeedSite(org string, site *nicosdk.Site, statusCode int) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.sites.StatusCode = statusCode
	if site == nil {
		return
	}
	copy := *site
	if copy.GetOrg() == "" {
		copy.SetOrg(org)
	}
	s.sites.Resources[resourceKey(org, copy.GetId())] = &copy
}

// SeedVPC adds or replaces a VPC visible to org.
func (s *Server) SeedVPC(org string, vpc *nicosdk.VPC, statusCode int) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.vpcs.StatusCode = statusCode
	if vpc == nil {
		return
	}
	copy := *vpc
	copy.Labels = maps.Clone(vpc.Labels)
	if copy.GetOrg() == "" {
		copy.SetOrg(org)
	}
	s.vpcs.Resources[resourceKey(org, copy.GetId())] = &copy
}

// SeedFromYAML adds resources declared by a controller fixture. The input uses
// the resource sections emitted by Dump; requests are observations and cannot
// be seeded.
func (s *Server) SeedFromYAML(input string) error {
	var seed serverSeed
	if err := yaml.Unmarshal([]byte(input), &seed); err != nil {
		return fmt.Errorf("decode fake server resources: %w", err)
	}
	if len(seed.Requests) != 0 {
		return fmt.Errorf("fake server requests cannot be seeded")
	}

	if len(seed.Tenants.Resources) == 0 {
		s.SeedTenant("", nil, seed.Tenants.StatusCode)
	}
	for _, resource := range seed.Tenants.Resources {
		if resource.Org == "" || resource.Resource.GetId() == "" {
			return fmt.Errorf("seeded tenant requires org and resource.id")
		}
		s.SeedTenant(resource.Org, &resource.Resource, seed.Tenants.StatusCode)
	}
	if len(seed.InstanceTypes.Resources) == 0 {
		s.SeedInstanceType("", nil, seed.InstanceTypes.StatusCode)
	}
	for _, resource := range seed.InstanceTypes.Resources {
		if resource.Org == "" || resource.Resource.GetId() == "" {
			return fmt.Errorf("seeded instance type requires org and resource.id")
		}
		s.SeedInstanceType(resource.Org, &resource.Resource, seed.InstanceTypes.StatusCode)
	}
	if len(seed.Instances.Resources) == 0 {
		s.SeedInstance("", nil, seed.Instances.StatusCode)
	}
	for _, resource := range seed.Instances.Resources {
		if resource.Org == "" || resource.Resource.GetId() == "" {
			return fmt.Errorf("seeded instance requires org and resource.id")
		}
		s.SeedInstance(resource.Org, &resource.Resource, seed.Instances.StatusCode)
	}
	if len(seed.Machines.Resources) == 0 {
		s.SeedMachine("", nil, seed.Machines.StatusCode)
	}
	for _, resource := range seed.Machines.Resources {
		if resource.Org == "" || resource.Resource.GetId() == "" {
			return fmt.Errorf("seeded machine requires org and resource.id")
		}
		s.SeedMachine(resource.Org, &resource.Resource, seed.Machines.StatusCode)
	}
	if len(seed.Sites.Resources) == 0 {
		s.SeedSite("", nil, seed.Sites.StatusCode)
	}
	for _, resource := range seed.Sites.Resources {
		if resource.Org == "" || resource.Resource.GetId() == "" {
			return fmt.Errorf("seeded site requires org and resource.id")
		}
		s.SeedSite(resource.Org, &resource.Resource, seed.Sites.StatusCode)
	}
	if len(seed.VPCs.Resources) == 0 {
		s.SeedVPC("", nil, seed.VPCs.StatusCode)
	}
	for _, resource := range seed.VPCs.Resources {
		if resource.Org == "" || resource.Resource.GetId() == "" {
			return fmt.Errorf("seeded VPC requires org and resource.id")
		}
		s.SeedVPC(resource.Org, &resource.Resource, seed.VPCs.StatusCode)
	}
	return nil
}

// InstanceCount returns the number of instances that have not terminated.
func (s *Server) InstanceCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()

	count := 0
	for _, record := range s.instances.Resources {
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
		Tenants:       make([]tenantDump, 0, len(s.tenants.Resources)),
		InstanceTypes: make([]instanceTypeDump, 0, len(s.instanceTypes.Resources)),
		Instances:     make([]instanceDump, 0, len(s.instances.Resources)),
		Machines:      make([]machineDump, 0, len(s.machines.Resources)),
		Sites:         make([]siteDump, 0, len(s.sites.Resources)),
		VPCs:          make([]vpcDump, 0, len(s.vpcs.Resources)),
		Requests:      deduplicateRequests(s.requests),
	}
	for org, tenant := range s.tenants.Resources {
		dump.Tenants = append(dump.Tenants, tenantDump{Org: org, Resource: *tenant})
	}
	for key, instanceType := range s.instanceTypes.Resources {
		dump.InstanceTypes = append(dump.InstanceTypes, instanceTypeDump{Org: orgFromKey(key), Resource: cloneInstanceType(*instanceType)})
	}
	for _, record := range s.instances.Resources {
		dump.Instances = append(dump.Instances, instanceDump{Org: record.org, Resource: cloneInstance(record.instance), RebootCount: record.rebootCount})
	}
	for key, machine := range s.machines.Resources {
		dump.Machines = append(dump.Machines, machineDump{Org: orgFromKey(key), Resource: cloneMachine(*machine)})
	}
	for key, site := range s.sites.Resources {
		dump.Sites = append(dump.Sites, siteDump{Org: orgFromKey(key), Resource: *site})
	}
	for key, vpc := range s.vpcs.Resources {
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
	slices.SortFunc(dump.Machines, func(a, b machineDump) int {
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
	tenant, ok := s.tenants.Resources[r.PathValue("org")]
	s.mu.Unlock()
	if !ok {
		writeError(w, http.StatusNotFound, "current tenant not found")
		return
	}
	writeJSON(w, http.StatusOK, tenant)
}

func (s *Server) listMachines(w http.ResponseWriter, r *http.Request) {
	org := r.PathValue("org")
	query := r.URL.Query()
	s.mu.Lock()
	machines := make([]nicosdk.Machine, 0, len(s.machines.Resources))
	for key, machine := range s.machines.Resources {
		if orgFromKey(key) != org || (query.Get("siteId") != "" && machine.GetSiteId() != query.Get("siteId")) {
			continue
		}
		if query.Get("hasInstanceType") == "true" && machine.GetInstanceTypeId() == "" {
			continue
		}
		machines = append(machines, cloneMachine(*machine))
	}
	s.mu.Unlock()
	slices.SortFunc(machines, func(a, b nicosdk.Machine) int {
		return strings.Compare(a.GetId(), b.GetId())
	})
	machines = paginate(machines, query.Get("pageNumber"), query.Get("pageSize"))
	writeJSON(w, http.StatusOK, machines)
}

func (s *Server) getInstanceType(w http.ResponseWriter, r *http.Request) {
	org, instanceTypeID := r.PathValue("org"), r.PathValue("instanceTypeID")
	s.mu.Lock()
	instanceType, ok := s.instanceTypes.Resources[resourceKey(org, instanceTypeID)]
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

type apiFailure struct {
	status  int
	message string
}

// selectMachineForSelector resolves the machine a machine label selector places
// an instance on, mirroring NICo's rejection messages. It must be called with
// s.mu held. A nil machine and nil failure mean the request carried no selector.
func (s *Server) selectMachineForSelector(org string, request *nicosdk.InstanceCreateRequest, vpc *nicosdk.VPC) (*nicosdk.Machine, *apiFailure) {
	selector := request.GetMachineLabelSelector()
	if len(selector) == 0 {
		return nil, nil
	}

	if request.HasMachineId() {
		machine, ok := s.machines.Resources[resourceKey(org, request.GetMachineId())]
		if !ok || !matchesLabels(machine.GetLabels(), selector) {
			return nil, &apiFailure{http.StatusBadRequest, "Machine specified in request does not match machineLabelSelector"}
		}
		if machine.GetInstanceId() != "" {
			return nil, &apiFailure{http.StatusBadRequest, "Machine is assigned to an Instance"}
		}
		return machine, nil
	}

	candidates := make([]*nicosdk.Machine, 0)
	for key, machine := range s.machines.Resources {
		if orgFromKey(key) != org ||
			machine.GetSiteId() != vpc.GetSiteId() ||
			machine.GetInstanceTypeId() != request.GetInstanceTypeId() ||
			machine.GetInstanceId() != "" ||
			!matchesLabels(machine.GetLabels(), selector) {
			continue
		}
		candidates = append(candidates, machine)
	}
	if len(candidates) == 0 {
		return nil, &apiFailure{http.StatusBadRequest, "No Machines are available for specified Instance Type"}
	}
	slices.SortFunc(candidates, func(a, b *nicosdk.Machine) int {
		return strings.Compare(a.GetId(), b.GetId())
	})
	return candidates[0], nil
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
	tenant, tenantOK := s.tenants.Resources[org]
	vpc, vpcOK := s.vpcs.Resources[resourceKey(org, request.GetVpcId())]
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
		if _, ok := s.instanceTypes.Resources[resourceKey(org, request.GetInstanceTypeId())]; !ok {
			s.mu.Unlock()
			writeError(w, http.StatusBadRequest, "instance type not found")
			return
		}
	}
	for _, existing := range s.instances.Resources {
		if existing.org == org && existing.instance.GetName() == request.GetName() && !statusEqual(existing.instance.GetStatus(), statusTerminated) {
			s.mu.Unlock()
			writeError(w, http.StatusConflict, "instance already exists")
			return
		}
	}

	assignedMachine, failure := s.selectMachineForSelector(org, &request, vpc)
	if failure != nil {
		s.mu.Unlock()
		writeError(w, failure.status, failure.message)
		return
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
	if assignedMachine != nil {
		instance.SetMachineId(assignedMachine.GetId())
		assignedMachine.SetInstanceId(id)
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
	s.instances.Resources[resourceKey(org, id)] = record
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
	instances := make([]nicosdk.Instance, 0, len(s.instances.Resources))
	for _, record := range s.instances.Resources {
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
	record, ok := s.instances.Resources[resourceKey(org, instanceID)]
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

func (s *Server) getMachine(w http.ResponseWriter, r *http.Request) {
	org, machineID := r.PathValue("org"), r.PathValue("machineID")
	s.mu.Lock()
	machine, ok := s.machines.Resources[resourceKey(org, machineID)]
	var response nicosdk.Machine
	if ok {
		response = cloneMachine(*machine)
	}
	s.mu.Unlock()
	if !ok {
		writeError(w, http.StatusNotFound, "machine not found")
		return
	}
	writeJSON(w, http.StatusOK, &response)
}

// SetPowerControlStatus makes Machine power actions fail with the given HTTP
// status. Zero restores the normal accepted response.
func (s *Server) SetPowerControlStatus(status int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.powerControlStatus = status
}

// PowerControlCalls counts all valid Machine power requests, including rejected ones.
func (s *Server) PowerControlCalls() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.powerControlCalls
}

// InstanceRebootCount counts accepted hard reboots for one Instance.
func (s *Server) InstanceRebootCount(org, instanceID string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	if record := s.instances.Resources[resourceKey(org, instanceID)]; record != nil {
		return record.rebootCount
	}
	return 0
}

func (s *Server) powerControlMachine(w http.ResponseWriter, r *http.Request) {
	var request nicosdk.MachinePowerControlRequest
	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		writeError(w, http.StatusBadRequest, "malformed machine power request")
		return
	}
	if request.GetAction() != "GracefulRestart" || !request.GetAcknowledgeAttachedInstance() {
		writeError(w, http.StatusBadRequest, "GracefulRestart and acknowledgeAttachedInstance are required")
		return
	}
	org, machineID := r.PathValue("org"), r.PathValue("machineID")
	s.mu.Lock()
	_, ok := s.machines.Resources[resourceKey(org, machineID)]
	status := s.powerControlStatus
	s.powerControlCalls++
	if ok && status == 0 {
		requestCopy := request
		s.requests = append(s.requests, requestRecord{
			Operation: operationMachinePower,
			Org:       org,
			MachineID: machineID,
			Power:     &requestCopy,
		})
	}
	s.mu.Unlock()
	if !ok {
		writeError(w, http.StatusNotFound, "machine not found")
		return
	}
	if status != 0 {
		writeError(w, status, "machine power request rejected")
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]string{"message": ""})
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
	record, ok := s.instances.Resources[resourceKey(org, instanceID)]
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
	record, ok := s.instances.Resources[resourceKey(org, instanceID)]
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
	tenant, tenantOK := s.tenants.Resources[org]
	sites := make([]nicosdk.Site, 0, len(s.sites.Resources))
	if tenantOK && (query.Get("tenantId") == "" || query.Get("tenantId") == tenant.GetId()) {
		for key, site := range s.sites.Resources {
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
	vpc, ok := s.vpcs.Resources[resourceKey(org, vpcID)]
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
			if machine := s.machines.Resources[resourceKey(record.org, record.instance.GetMachineId())]; machine != nil &&
				machine.GetInstanceId() == record.instance.GetId() {
				machine.SetInstanceIdNil()
			}
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

func matchesLabels(labels, selector map[string]string) bool {
	for key, expected := range selector {
		actual, ok := labels[key]
		if !ok || actual != expected {
			return false
		}
	}
	return true
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

func cloneMachine(machine nicosdk.Machine) nicosdk.Machine {
	copy := machine
	copy.Labels = maps.Clone(machine.Labels)
	copy.MachineCapabilities = slices.Clone(machine.MachineCapabilities)
	copy.MachineInterfaces = slices.Clone(machine.MachineInterfaces)
	copy.AssociatedDpuMachineIds = slices.Clone(machine.AssociatedDpuMachineIds)
	copy.StatusHistory = slices.Clone(machine.StatusHistory)
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
