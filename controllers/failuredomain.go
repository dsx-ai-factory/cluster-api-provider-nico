// SPDX-FileCopyrightText: Copyright (c) 2026 NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package controllers

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"time"
	"unicode/utf8"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"

	clusterv1 "sigs.k8s.io/cluster-api/api/core/v1beta2"
	"sigs.k8s.io/cluster-api/util/conditions"

	infrav1 "github.com/dsx-ai-factory/cluster-api-provider-nico/api/v1alpha1"
	"github.com/dsx-ai-factory/cluster-api-provider-nico/internal/nico"

	nicosdk "github.com/NVIDIA/infra-controller/rest-api/sdk/standard"
)

const (
	maxFailureDomains          = 100
	maxFailureDomainNameLength = 256

	failureDomainCapabilityWait = 10 * time.Minute
)

// toFailureDomains converts NICo domains to the Cluster API contract shape,
// marking them control-plane eligible because NICo exposes no eligibility metadata.
func toFailureDomains(domains []nico.FailureDomain) []clusterv1.FailureDomain {
	byName := make(map[string]nico.FailureDomain, len(domains))
	for _, domain := range domains {
		if domain.Name == "" || utf8.RuneCountInString(domain.Name) > maxFailureDomainNameLength {
			continue
		}
		if _, exists := byName[domain.Name]; !exists {
			byName[domain.Name] = domain
		}
	}

	names := make([]string, 0, len(byName))
	for name := range byName {
		names = append(names, name)
	}
	sort.Strings(names)
	if len(names) > maxFailureDomains {
		names = names[:maxFailureDomains]
	}

	out := make([]clusterv1.FailureDomain, 0, len(names))
	for _, name := range names {
		domain := byName[name]
		controlPlane := true
		out = append(out, clusterv1.FailureDomain{
			Name:         domain.Name,
			ControlPlane: &controlPlane,
			Attributes:   domain.Attributes,
		})
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// setFailureDomainDrift records whether the machine NICo assigned is still in the
// requested domain. An instance NICo has not yet assigned a machine to is
// unplaced rather than misplaced, and a machine that requested no domain carries
// no condition at all.
//
// Drift does not fail provisioning: the instance is running, and marking it
// unavailable would invite a MachineHealthCheck to delete a healthy node.
func setFailureDomainDrift(ctx context.Context, nicoMachine *infrav1.NicoMachine, instance *nicosdk.Instance, labelKey, requestedDomain, observedDomain string) {
	machineID := instance.GetMachineId()
	if requestedDomain == "" {
		conditions.Delete(nicoMachine, infrav1.FailureDomainDriftedCondition)
		return
	}
	if labelKey == "" {
		conditions.Set(nicoMachine, metav1.Condition{
			Type:    infrav1.FailureDomainDriftedCondition,
			Status:  metav1.ConditionUnknown,
			Reason:  infrav1.FailureDomainVerificationFailedReason,
			Message: fmt.Sprintf("owner Machine requests failure domain %q but the NicoCluster sets no failureDomainLabelKey", requestedDomain),
		})
		return
	}
	if machineID == "" || observedDomain == requestedDomain {
		conditions.Set(nicoMachine, metav1.Condition{
			Type:   infrav1.FailureDomainDriftedCondition,
			Status: metav1.ConditionFalse,
			Reason: infrav1.FailureDomainStableReason,
		})
		return
	}

	message := fmt.Sprintf("assigned machine %q is in failure domain %q, requested %q",
		machineID, observedDomain, requestedDomain)
	ctrl.LoggerFrom(ctx).Error(nico.ErrFailureDomainMismatch, message, "machineID", machineID)
	conditions.Set(nicoMachine, metav1.Condition{
		Type:    infrav1.FailureDomainDriftedCondition,
		Status:  metav1.ConditionTrue,
		Reason:  infrav1.FailureDomainDriftedReason,
		Message: message,
	})
}

// failureDomainPlacementFailure reports whether err is a placement failure for a
// requested domain, and how the reconciler should back off. Unrelated create
// errors keep the generic InstanceCreateFailedReason.
func failureDomainPlacementFailure(err error, failureDomain string) (string, time.Duration, bool) {
	if failureDomain == "" {
		return "", 0, false
	}
	switch {
	case errors.Is(err, nico.ErrFailureDomainUnavailable):
		// Only this reason is in capacityWaitReasons; the branches below are not
		// capacity waits and must not defer workers.
		return infrav1.FailureDomainUnavailableReason, instanceTypeUnavailableWait, true
	case errors.Is(err, nico.ErrFailureDomainCapabilityRequired):
		// NICo grants the capability out of band and nothing this controller
		// watches changes when it does, so retry slowly rather than terminally.
		return infrav1.FailureDomainPlacementFailedReason, failureDomainCapabilityWait, true
	case errors.Is(err, nico.ErrFailureDomainMismatch),
		errors.Is(err, nico.ErrPlacementLabelKeyUnset):
		return infrav1.FailureDomainPlacementFailedReason, 0, true
	default:
		return "", 0, false
	}
}
