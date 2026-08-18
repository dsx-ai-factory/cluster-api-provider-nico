// SPDX-FileCopyrightText: Copyright (c) 2026 NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package controllers

import (
	"context"
	"fmt"
	"hash/fnv"
	"maps"
	"net"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	clusterv1 "sigs.k8s.io/cluster-api/api/core/v1beta2"
	crclient "sigs.k8s.io/controller-runtime/pkg/client"

	infrav1 "github.com/NVIDIA/cluster-api-provider-nico/api/v1alpha1"
	"github.com/NVIDIA/cluster-api-provider-nico/internal/nico"

	nicosdk "github.com/NVIDIA/ncx-infra-controller-rest/sdk/standard"
)

var defaultNicoClientCache = nico.NewClientCache()

func bootstrapCloudConfigFromSecret(secret *corev1.Secret) (string, error) {
	if b, ok := secret.Data["value"]; ok && len(b) > 0 {
		return string(b), nil
	}
	return "", fmt.Errorf("bootstrap secret missing data key %q", "value")
}

func firstIPv4FromInstance(instance *nicosdk.Instance) string {
	if instance == nil {
		return ""
	}
	for _, iface := range instance.Interfaces {
		for _, ip := range iface.IpAddresses {
			parsed := net.ParseIP(ip)
			if parsed == nil {
				continue
			}
			if parsed.To4() != nil {
				return ip
			}
		}
	}
	for _, iface := range instance.Interfaces {
		if len(iface.IpAddresses) > 0 {
			return iface.IpAddresses[0]
		}
	}
	return ""
}

func nicoClientForCluster(ctx context.Context, c crclient.Client, nicoCluster *infrav1.NicoCluster, providerCreds types.NamespacedName) (nico.API, error) {
	var secretKey types.NamespacedName

	// Prefer the cluster-specific credentials Secret over the provider-level credentials Secret.
	switch {
	case nicoCluster.Spec.IdentityRef.Name != "":
		secretKey = types.NamespacedName{Namespace: nicoCluster.Namespace, Name: nicoCluster.Spec.IdentityRef.Name}
	case providerCreds.Namespace != "" && providerCreds.Name != "":
		secretKey = providerCreds
	default:
		return nil, apierrors.NewNotFound(corev1.Resource("secrets"), "")
	}

	var identitySecret corev1.Secret
	if err := c.Get(ctx, secretKey, &identitySecret); err != nil {
		return nil, err
	}

	secretConfig, err := nico.LoadSecretConfig(&identitySecret)
	if err != nil {
		return nil, err
	}

	return defaultNicoClientCache.GetOrCreate(ctx, &identitySecret, secretConfig)
}

func mergeLabels(labelSets ...map[string]string) map[string]string {
	out := map[string]string{}
	for _, set := range labelSets {
		maps.Copy(out, set)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// normalizeLabelValue makes Forge/NICo display names safe for NKE label values
// before those names are copied onto VM records as topology labels.
func normalizeLabelValue(value string) string {
	value = strings.ToLower(value)
	var builder strings.Builder
	lastSeparator := false
	for _, r := range value {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '_', r == '.', r == '-':
			if builder.Len() < 63 {
				builder.WriteRune(r)
				lastSeparator = r == '.' || r == '-' || r == '_'
			}
		case r == ' ' || r == '/' || r == '\\':
			if builder.Len() > 0 && !lastSeparator && builder.Len() < 63 {
				builder.WriteByte('-')
				lastSeparator = true
			}
		}
	}
	return strings.Trim(builder.String(), ".-_")
}

// deterministicJitter returns a stable whole-second jitter value for key within the given window.
func deterministicJitter(key string, window time.Duration) time.Duration {
	if window <= 0 {
		return 0
	}

	buckets := uint64(window / time.Second)
	if buckets == 0 {
		return 0
	}

	h := fnv.New64a()
	_, _ = h.Write([]byte(key))

	return time.Duration(h.Sum64()%buckets) * time.Second
}

// isControlPlaneNicoMachine reports whether a NicoMachine backs a control-plane
// Machine. CWE stamps the CAPI control-plane label on control-plane NicoMachines
// at create time, so no NKE-specific marker is needed.
func isControlPlaneNicoMachine(nicoMachine *infrav1.NicoMachine) bool {
	_, ok := nicoMachine.Labels[clusterv1.MachineControlPlaneLabel]
	return ok
}

// capacityWaitReasons are the MachineProvisioned-condition reasons that mean a control-plane
// NicoMachine is waiting specifically on instance-type capacity, and so is a
// machine that a freed allocation would actually unblock. Only these count
// towards the deferral tally.
//
// This is deliberately an allowlist rather than a blocklist of non-capacity
// wedge reasons. A blocklist has to enumerate every way a machine can be stuck,
// and any reason added to condition_consts.go later is silently treated as
// "waiting for capacity" until someone remembers to list it. Now that the gate
// blocks workers unconditionally rather than only when headroom is tight, that
// omission would stall every worker of the instance type indefinitely. An
// allowlist fails the safe way round: an unrecognised reason simply does not
// hold capacity.
//
// The cost is a narrow window. A control-plane machine that has not yet
// attempted a create carries no Ready condition and so does not reserve, and a
// worker reconciling inside that window can still take the instance. The
// control-plane machine then fails its own availability preflight, stamps
// InstanceTypeUnavailable, and is protected from that point on.
var capacityWaitReasons = map[string]struct{}{
	infrav1.InstanceTypeUnavailableReason: {},
	infrav1.InstanceCreateFailedReason:    {},
}

// isWaitingForCapacity reports whether nicoMachine is genuinely blocked on
// instance-type capacity: its MachineProvisioned condition is False for one of
// capacityWaitReasons and reconciliation is not paused.
//
// This reads MachineProvisioned rather than Ready. Ready became an aggregate in
// the v1beta2 status alignment (#100), so its reason is the summary
// NotReadyReason and the specific cause is only on MachineProvisioned. Keyed on
// Ready, no machine would ever match capacityWaitReasons, every control-plane
// machine would look idle, and the deferral would silently never fire.
//
// The paused check survives the move to an allowlist because a paused machine
// can still be carrying an InstanceTypeUnavailable condition stamped before the
// pause. A pause is operator-held for as long as intended -- a maintenance
// window, or a clusterctl move -- and the candidate list is deliberately
// cluster-wide, so a paused control-plane machine that kept contending would
// stall worker scale-out in every other cluster sharing its instance type.
// cluster-api's paused.EnsurePausedCondition patches clusterv1.PausedCondition
// onto the same Status.Conditions slice read here, so this needs no extra API
// call.
func isWaitingForCapacity(nicoMachine *infrav1.NicoMachine) bool {
	pausedCondition := apimeta.FindStatusCondition(nicoMachine.Status.Conditions, clusterv1.PausedCondition)
	if pausedCondition != nil && pausedCondition.Status == metav1.ConditionTrue {
		return false
	}

	provisioned := apimeta.FindStatusCondition(nicoMachine.Status.Conditions, infrav1.MachineProvisionedCondition)
	if provisioned == nil || provisioned.Status != metav1.ConditionFalse {
		return false
	}
	_, waiting := capacityWaitReasons[provisioned.Reason]
	return waiting
}

// countControlPlaneWaitingForInstanceType counts control-plane NicoMachines that
// still need to claim a NICo instance of instanceTypeID, excluding self. It also
// returns one "namespace/name" sample for the deferral message.
//
// Callers use this to hold back worker instance creates so a control-plane
// replacement gets first claim on scarce capacity during control-plane
// recycling.
//
// The list is cluster-wide because NICo instance-type capacity is a shared site
// pool: a worker in any namespace can consume the instance a control-plane
// machine is waiting for. Instance-type IDs are site-scoped, so matching on the
// ID alone already restricts the comparison to a single site. The manager runs an
// unscoped cache, so this reads the informer, not the API server.
func countControlPlaneWaitingForInstanceType(
	ctx context.Context,
	c crclient.Client,
	instanceTypeID string,
	self *infrav1.NicoMachine,
) (int, string, error) {
	if instanceTypeID == "" {
		return 0, "", nil
	}

	var machines infrav1.NicoMachineList
	if err := c.List(ctx, &machines); err != nil {
		return 0, "", fmt.Errorf("failed to list NicoMachines: %w", err)
	}

	count := 0
	sample := ""
	for i := range machines.Items {
		candidate := &machines.Items[i]

		if candidate.Namespace == self.Namespace && candidate.Name == self.Name {
			continue
		}
		if !isControlPlaneNicoMachine(candidate) {
			continue
		}
		if !candidate.DeletionTimestamp.IsZero() {
			continue
		}
		if candidate.Spec.InstanceTypeID != instanceTypeID {
			continue
		}
		// Already claiming or importing an instance, so no longer contending for
		// a free allocation. This mirrors the predicate Reconcile uses to decide
		// it must create an instance.
		if candidate.Status.InstanceID != "" || candidate.Spec.ProviderID != "" {
			continue
		}
		// Only machines demonstrably waiting on capacity count. Anything else --
		// no bootstrap data, an unresolvable identity secret, an invalid create
		// request, a paused cluster, or a failure mode added after this was
		// written -- cannot be unblocked by a freed allocation, and would
		// otherwise contend forever and stall every worker of this instance type.
		if !isWaitingForCapacity(candidate) {
			continue
		}

		if count == 0 {
			sample = candidate.Namespace + "/" + candidate.Name
		}
		count++
	}

	return count, sample, nil
}
