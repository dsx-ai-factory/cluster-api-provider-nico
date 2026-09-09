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

func credentialsSecretKey(nicoCluster *infrav1.NicoCluster, providerCreds types.NamespacedName) (types.NamespacedName, bool) {
	if nicoCluster.Spec.IdentityRef.Name != "" {
		return types.NamespacedName{Namespace: nicoCluster.Namespace, Name: nicoCluster.Spec.IdentityRef.Name}, true
	}
	if providerCreds.Namespace != "" && providerCreds.Name != "" {
		return providerCreds, true
	}
	return types.NamespacedName{}, false
}

func nicoClientForCluster(ctx context.Context, c crclient.Client, nicoCluster *infrav1.NicoCluster, providerCreds types.NamespacedName) (nico.API, error) {
	secretKey, ok := credentialsSecretKey(nicoCluster, providerCreds)
	if !ok {
		return nil, apierrors.NewNotFound(corev1.Resource("secrets"), "")
	}

	var identitySecret corev1.Secret
	if err := c.Get(ctx, secretKey, &identitySecret); err != nil {
		return nil, fmt.Errorf("getting nico credentials secret %s: %w", secretKey, err)
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

// isControlPlaneNicoMachine reports whether a NicoMachine backs a control-plane Machine
func isControlPlaneNicoMachine(nicoMachine *infrav1.NicoMachine) bool {
	_, ok := nicoMachine.Labels[clusterv1.MachineControlPlaneLabel]
	return ok
}

// capacityWaitReasons are the MachineProvisioned-condition reasons that mean a control-plane
// NicoMachine is waiting specifically on instance-type capacity
var capacityWaitReasons = map[string]struct{}{
	infrav1.InstanceTypeUnavailableReason: {},
	infrav1.InstanceCreateFailedReason:    {},
}

// isWaitingForCapacity reports whether nicoMachine should receive capacity priority
func isWaitingForCapacity(nicoMachine *infrav1.NicoMachine) bool {
	pausedCondition := apimeta.FindStatusCondition(nicoMachine.Status.Conditions, clusterv1.PausedCondition)
	if pausedCondition != nil && pausedCondition.Status == metav1.ConditionTrue {
		return false
	}

	provisioned := apimeta.FindStatusCondition(nicoMachine.Status.Conditions, infrav1.MachineProvisionedCondition)
	if provisioned == nil {
		return true
	}
	if provisioned.Status != metav1.ConditionFalse {
		return false
	}
	_, waiting := capacityWaitReasons[provisioned.Reason]
	return waiting
}

// countControlPlaneWaitingForInstanceType counts control-plane NicoMachines that
// still need to claim a NICo instance of instanceTypeID, excluding self
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
		// Fresh machines receive initial priority. After their first reconcile,
		// only machines demonstrably waiting on capacity continue to count.
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
