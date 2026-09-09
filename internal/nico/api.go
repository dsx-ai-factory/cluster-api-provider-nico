// SPDX-FileCopyrightText: Copyright (c) 2026 NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package nico

import (
	"context"

	nicosdk "github.com/NVIDIA/infra-controller/rest-api/sdk/standard"
)

// API is the NICo surface used by CAPNICo reconcilers.
type API interface {
	ValidateReadiness(ctx context.Context) error
	ResolveTenantID(ctx context.Context) (string, error)
	GetInstanceTypeWithAllocationStats(ctx context.Context, instanceTypeID string) (*nicosdk.InstanceType, error)
	CreateInstance(ctx context.Context, req nicosdk.InstanceCreateRequest) (*nicosdk.Instance, error)
	DeleteInstance(ctx context.Context, instanceID string, healthIssue *nicosdk.MachineHealthIssue) error
	TriggerInstanceReboot(ctx context.Context, instanceID string) (*nicosdk.Instance, error)
	ApplyInstanceLabels(ctx context.Context, instanceID string, labels map[string]string) (*nicosdk.Instance, error)
	GetInstance(ctx context.Context, instanceID string) (*nicosdk.Instance, error)
	GetSite(ctx context.Context, siteID string) (*nicosdk.Site, error)
	GetVPC(ctx context.Context, vpcID string) (*nicosdk.VPC, error)
	FindInstanceByName(ctx context.Context, lookup InstanceLookup) (*nicosdk.Instance, error)
}
