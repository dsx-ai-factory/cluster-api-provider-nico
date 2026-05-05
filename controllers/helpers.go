package controllers

import (
	"context"
	"fmt"
	"net"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	crclient "sigs.k8s.io/controller-runtime/pkg/client"

	infrav1 "gitlab-master.nvidia.com/nke/cluster-api-provider-nico/api/v1alpha1"
	"gitlab-master.nvidia.com/nke/cluster-api-provider-nico/internal/nico"

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

func nicoClientForCluster(ctx context.Context, c crclient.Client, nicoCluster *infrav1.NicoCluster, providerCreds types.NamespacedName) (*nico.Client, error) {
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
		for k, v := range set {
			out[k] = v
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}
