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
	"k8s.io/apimachinery/pkg/types"
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

// nicoClientFactory builds a NICo API client from an already-loaded SecretConfig.
// When unset on a reconciler, defaultNicoClientCache.GetOrCreate is used.
// Secret resolution and LoadSecretConfig always run on the production path first.
type nicoClientFactory func(ctx context.Context, secret *corev1.Secret, cfg nico.SecretConfig) (nico.API, error)

func nicoClientForCluster(ctx context.Context, c crclient.Client, nicoCluster *infrav1.NicoCluster, providerCreds types.NamespacedName, factory nicoClientFactory) (nico.API, error) {
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

	if factory != nil {
		return factory(ctx, &identitySecret, secretConfig)
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
