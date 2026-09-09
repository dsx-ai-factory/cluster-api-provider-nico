// SPDX-FileCopyrightText: Copyright (c) 2026 NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package nico

import (
	"context"
	"errors"
	"sort"

	nicosdk "github.com/NVIDIA/infra-controller/rest-api/sdk/standard"
)

// machineOrderByID pins a total order across pages so a Machine cannot move
// between pages while the list is being walked.
const machineOrderByID = "ID_ASC"

// ErrPlacementLabelKeyUnset means a failure domain was requested without a
// Machine label key to match it against.
var ErrPlacementLabelKeyUnset = errors.New("failure domain requested without a machine label key")

// ListFailureDomains returns the distinct failure domains labelled on the site's
// Machines, sorted by name. labelKey selects the Machine label to read; an empty
// labelKey disables discovery and returns no domains. Only Machines that have an
// Instance Type are considered, since placement selects within one.
//
// Do not narrow this by Machine status or assignment. The published set feeds
// Cluster.status.failureDomains, so a domain that vanished while its Machines
// were busy would put the control-plane machines there out of failure domain.
func (c *Client) ListFailureDomains(ctx context.Context, siteID, labelKey string) ([]FailureDomain, error) {
	if labelKey == "" {
		return nil, nil
	}

	authCtx, err := c.authCtx(ctx)
	if err != nil {
		return nil, err
	}

	names := map[string]struct{}{}
	for page := int32(1); ; page++ {
		machines, resp, err := c.api.MachineAPI.
			GetAllMachine(authCtx, c.orgID).
			SiteId(siteID).
			HasInstanceType(true).
			OrderBy(machineOrderByID).
			PageNumber(page).
			PageSize(pageSize).
			Execute()
		if err != nil {
			return nil, normalizeError(resp, err)
		}
		for i := range machines {
			if name := machines[i].GetLabels()[labelKey]; name != "" {
				names[name] = struct{}{}
			}
		}
		if len(machines) < pageSize {
			break
		}
	}

	sortedNames := make([]string, 0, len(names))
	for name := range names {
		sortedNames = append(sortedNames, name)
	}
	sort.Strings(sortedNames)

	domains := make([]FailureDomain, 0, len(sortedNames))
	for _, name := range sortedNames {
		domains = append(domains, FailureDomain{Name: name})
	}
	return domains, nil
}

// applyInstancePlacement constrains req to placement's failure domain. It fails
// rather than sending an unconstrained create when a domain was requested but no
// label key is configured to express it.
func applyInstancePlacement(req *nicosdk.InstanceCreateRequest, placement InstancePlacement) error {
	if placement.FailureDomain == "" {
		return nil
	}
	if placement.LabelKey == "" {
		return ErrPlacementLabelKeyUnset
	}

	req.SetMachineLabelSelector(map[string]string{
		placement.LabelKey: placement.FailureDomain,
	})
	return nil
}

// MachineFailureDomain returns the failure domain labelled on machine, or the
// empty string when machine is nil, labelKey is empty, or the label is absent.
func MachineFailureDomain(machine *nicosdk.Machine, labelKey string) string {
	if machine == nil || labelKey == "" {
		return ""
	}
	return machine.GetLabels()[labelKey]
}
