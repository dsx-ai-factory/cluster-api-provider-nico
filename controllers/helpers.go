// SPDX-FileCopyrightText: Copyright (c) 2026 NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package controllers

import (
	"context"
	"errors"
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

	infrav1 "github.com/dsx-ai-factory/cluster-api-provider-nico/api/v1alpha1"
	"github.com/dsx-ai-factory/cluster-api-provider-nico/internal/nico"

	nicosdk "github.com/NVIDIA/infra-controller/rest-api/sdk/standard"
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

// credentialsSource is the object a NicoCluster's NICo credentials come from: a
// NicoIdentity, which names a Secret, or a Secret named directly or by default.
type credentialsSource struct {
	identity bool
	key      types.NamespacedName
}

// credentialsSourceFor returns the source a NicoCluster's identityRef names, or
// the provider default Secret when it names none. A reference it can't use is
// an error, never a reason to fall back to the default.
func credentialsSourceFor(nicoCluster *infrav1.NicoCluster, providerCreds types.NamespacedName) (credentialsSource, error) {
	ref := nicoCluster.Spec.IdentityRef
	named := types.NamespacedName{Namespace: nicoCluster.Namespace, Name: ref.Name}
	switch {
	case ref.Kind != "" && ref.Name == "":
		return credentialsSource{}, fmt.Errorf("identityRef sets kind %q without a name", ref.Kind)
	case ref.Kind == infrav1.NicoClusterIdentityKindNicoIdentity:
		return credentialsSource{identity: true, key: named}, nil
	case ref.Kind != "" && ref.Kind != infrav1.NicoClusterIdentityKindSecret:
		return credentialsSource{}, fmt.Errorf("identityRef kind %q is not supported", ref.Kind)
	case ref.Name != "":
		return credentialsSource{key: named}, nil
	case providerCreds.Namespace != "" && providerCreds.Name != "":
		return credentialsSource{key: providerCreds}, nil
	}
	return credentialsSource{}, apierrors.NewNotFound(corev1.Resource("secrets"), "")
}

// credentialsSecretKey returns the Secret a NicoCluster names directly, or the
// provider default. It reports false for a cluster that names a NicoIdentity,
// whose Secret is known only by reading the Identity.
func credentialsSecretKey(nicoCluster *infrav1.NicoCluster, providerCreds types.NamespacedName) (types.NamespacedName, bool) {
	source, err := credentialsSourceFor(nicoCluster, providerCreds)
	if err != nil || source.identity {
		return types.NamespacedName{}, false
	}
	return source.key, true
}

// credentialsSecretIndexer indexes a NicoCluster by the Secret its NICo
// credentials come from, named directly or by default. A cluster that names a
// NicoIdentity gets no entry.
func credentialsSecretIndexer(providerCreds types.NamespacedName) crclient.IndexerFunc {
	return func(object crclient.Object) []string {
		secretKey, ok := credentialsSecretKey(object.(*infrav1.NicoCluster), providerCreds)
		if !ok {
			return nil
		}
		return []string{secretKey.String()}
	}
}

// nicoClustersForSecret lists the NicoClusters whose credentials come from the
// Secret, through the controller's credentials-Secret index. Clusters in any
// namespace can use the provider-level Secret; any other Secret serves only its
// own namespace.
func nicoClustersForSecret(ctx context.Context, c crclient.Reader, secretKey, providerCreds types.NamespacedName, secretIndex string) ([]infrav1.NicoCluster, error) {
	listOptions := []crclient.ListOption{crclient.MatchingFields{secretIndex: secretKey.String()}}
	if secretKey != providerCreds {
		listOptions = append(listOptions, crclient.InNamespace(secretKey.Namespace))
	}
	var nicoClusters infrav1.NicoClusterList
	if err := c.List(ctx, &nicoClusters, listOptions...); err != nil {
		return nil, err
	}
	return nicoClusters.Items, nil
}

// credentialsIdentityIndexer indexes a NicoCluster by the NicoIdentity its
// identityRef names. Other clusters get no entry.
func credentialsIdentityIndexer(object crclient.Object) []string {
	source, err := credentialsSourceFor(object.(*infrav1.NicoCluster), types.NamespacedName{})
	if err != nil || !source.identity {
		return nil
	}
	return []string{source.key.String()}
}

// nicoClustersForIdentity lists the NicoClusters whose identityRef names the
// NicoIdentity, through the controller's Identity index.
func nicoClustersForIdentity(ctx context.Context, c crclient.Reader, identityKey types.NamespacedName, identityIndex string) ([]infrav1.NicoCluster, error) {
	var nicoClusters infrav1.NicoClusterList
	if err := c.List(ctx, &nicoClusters, crclient.InNamespace(identityKey.Namespace),
		crclient.MatchingFields{identityIndex: identityKey.String()}); err != nil {
		return nil, err
	}
	return nicoClusters.Items, nil
}

// nicoClustersThroughIdentities lists the NicoClusters that take their
// credentials from the Secret through a NicoIdentity: the clusters naming any
// Identity in the Secret's namespace whose credentialsRef names the Secret.
func nicoClustersThroughIdentities(ctx context.Context, c crclient.Reader, secretKey types.NamespacedName, identityIndex string) ([]infrav1.NicoCluster, error) {
	var identities infrav1.NicoIdentityList
	if err := c.List(ctx, &identities, crclient.InNamespace(secretKey.Namespace)); err != nil {
		return nil, err
	}
	var nicoClusters []infrav1.NicoCluster
	for i := range identities.Items {
		if identities.Items[i].Spec.CredentialsRef.Name != secretKey.Name {
			continue
		}
		referencing, err := nicoClustersForIdentity(ctx, c, crclient.ObjectKeyFromObject(&identities.Items[i]), identityIndex)
		if err != nil {
			return nil, err
		}
		nicoClusters = append(nicoClusters, referencing...)
	}
	return nicoClusters, nil
}

func nicoClientForCluster(ctx context.Context, c crclient.Client, nicoCluster *infrav1.NicoCluster, providerCreds types.NamespacedName) (nico.API, error) {
	source, err := credentialsSourceFor(nicoCluster, providerCreds)
	if err != nil {
		return nil, err
	}
	secretKey := source.key
	if source.identity {
		if secretKey, err = identitySecretKey(ctx, c, source.key); err != nil {
			return nil, err
		}
	}
	return nicoClientForSecret(ctx, c, secretKey)
}

// identitySecretKey returns the Secret a NicoIdentity names, in the Identity's
// namespace. It ignores the Identity's status, so an Identity that hasn't passed
// a check still supplies credentials.
func identitySecretKey(ctx context.Context, c crclient.Client, identityKey types.NamespacedName) (types.NamespacedName, error) {
	var identity infrav1.NicoIdentity
	if err := c.Get(ctx, identityKey, &identity); err != nil {
		return types.NamespacedName{}, fmt.Errorf("getting NicoIdentity %s: %w", identityKey, err)
	}
	return types.NamespacedName{Namespace: identity.Namespace, Name: identity.Spec.CredentialsRef.Name}, nil
}

// credentialsNotFoundReason names the condition reason for a NotFound error from
// nicoClientForCluster: the NicoIdentity is missing, or the Secret is.
func credentialsNotFoundReason(err error) string {
	var status apierrors.APIStatus
	if errors.As(err, &status) && status.Status().Details != nil && status.Status().Details.Group == infrav1.GroupVersion.Group {
		return infrav1.WaitingForNicoIdentityReason
	}
	return infrav1.WaitingForIdentitySecretReason
}

func powerControlClientForCluster(ctx context.Context, c crclient.Client, nicoCluster *infrav1.NicoCluster, instanceClient nico.API) (nico.API, error) {
	if nicoCluster.Spec.PowerControlIdentityRef.Name == "" {
		return instanceClient, nil
	}
	return nicoClientForSecret(ctx, c, types.NamespacedName{
		Namespace: nicoCluster.Namespace,
		Name:      nicoCluster.Spec.PowerControlIdentityRef.Name,
	})
}

func nicoClientForSecret(ctx context.Context, c crclient.Client, secretKey types.NamespacedName) (nico.API, error) {
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

// normalizeLabelValue makes Forge/NICo display names safe for NVKE label values
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
	infrav1.InstanceTypeUnavailableReason:  {},
	infrav1.InstanceCreateFailedReason:     {},
	infrav1.FailureDomainUnavailableReason: {},
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
