// SPDX-FileCopyrightText: Copyright (c) 2026 NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package nico

import (
	"strings"

	nicosdk "github.com/NVIDIA/infra-controller/rest-api/sdk/standard"
)

const (
	InstanceStatusTerminating = "Terminating"
	InstanceStatusTerminated  = "Terminated"
)

// InstanceStatus returns the observed status, or Unknown when it is absent.
func InstanceStatus(instance *nicosdk.Instance) string {
	if instance == nil || instance.Status == nil {
		return "Unknown"
	}
	return string(*instance.Status)
}

// IsReady reports whether the instance is ready for use.
func IsReady(instance *nicosdk.Instance) bool {
	return strings.EqualFold(InstanceStatus(instance), string(nicosdk.INSTANCESTATUS_READY))
}

// IsTerminating reports whether instance teardown is in progress.
func IsTerminating(instance *nicosdk.Instance) bool {
	return strings.EqualFold(InstanceStatus(instance), InstanceStatusTerminating)
}

// IsTerminated reports whether the instance has been released. NICo retains
// terminated instance records, so a terminal record is equivalent to absence
// for deletion reconciliation.
func IsTerminated(instance *nicosdk.Instance) bool {
	return strings.EqualFold(InstanceStatus(instance), InstanceStatusTerminated)
}
