// SPDX-FileCopyrightText: Copyright (c) 2026 NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package nico

import (
	"context"

	nicosdk "github.com/NVIDIA/infra-controller/rest-api/sdk/standard"
)

// FailureDomain is a NICo-defined placement area, such as a stable group of racks.
// CapNICo treats the name as opaque; NICo owns the physical topology behind it.
type FailureDomain struct {
	Name       string
	Attributes map[string]string
}

// InstancePlacement carries the optional failure domain used to place an instance.
// LabelKey names the NICo Machine label the domain is matched against. Placing a
// non-empty FailureDomain with an empty LabelKey is rejected rather than sent as
// an unconstrained create.
type InstancePlacement struct {
	FailureDomain string
	LabelKey      string
}

// API is the NICo surface used by CAPNICo reconcilers.
type API interface {
	ValidateReadiness(ctx context.Context) error
	ResolveTenantID(ctx context.Context) (string, error)
	GetInstanceTypeWithAllocationStats(ctx context.Context, instanceTypeID string) (*nicosdk.InstanceType, error)
	ListFailureDomains(ctx context.Context, siteID, labelKey string) ([]FailureDomain, error)
	CreateInstance(ctx context.Context, req nicosdk.InstanceCreateRequest, placement InstancePlacement) (*nicosdk.Instance, error)
	DeleteInstance(ctx context.Context, instanceID string, healthIssue *nicosdk.MachineHealthIssue) error
	TriggerInstanceReboot(ctx context.Context, instanceID string) (*nicosdk.Instance, error)
	ApplyInstanceLabels(ctx context.Context, instanceID string, labels map[string]string) (*nicosdk.Instance, error)
	GetInstance(ctx context.Context, instanceID string) (*nicosdk.Instance, error)
	GetMachine(ctx context.Context, machineID string) (*nicosdk.Machine, error)
	GetSite(ctx context.Context, siteID string) (*nicosdk.Site, error)
	GetVPC(ctx context.Context, vpcID string) (*nicosdk.VPC, error)
	FindInstanceByName(ctx context.Context, lookup InstanceLookup) (*nicosdk.Instance, error)
}
