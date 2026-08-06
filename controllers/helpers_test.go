package controllers

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	infrav1 "gitlab-master.nvidia.com/nke/cluster-api-provider-nico/api/v1alpha1"
	"gitlab-master.nvidia.com/nke/cluster-api-provider-nico/internal/nico"
	nicofake "gitlab-master.nvidia.com/nke/cluster-api-provider-nico/internal/nico/fake"
)

func TestNicoClientForCluster(t *testing.T) {
	scheme := runtime.NewScheme()
	require.NoError(t, corev1.AddToScheme(scheme))
	require.NoError(t, infrav1.AddToScheme(scheme))

	nicoCluster := &infrav1.NicoCluster{
		ObjectMeta: metav1.ObjectMeta{Namespace: "ns", Name: "nico-1"},
		Spec: infrav1.NicoClusterSpec{
			IdentityRef: corev1.LocalObjectReference{Name: "nico-creds"},
		},
	}

	t.Run("missing secret returns NotFound", func(t *testing.T) {
		c := fake.NewClientBuilder().WithScheme(scheme).Build()
		_, err := nicoClientForCluster(context.Background(), c, nicoCluster, types.NamespacedName{}, nil)
		require.Error(t, err)
		require.True(t, apierrors.IsNotFound(err))
	})

	t.Run("invalid secret fails LoadSecretConfig", func(t *testing.T) {
		secret := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{Namespace: "ns", Name: "nico-creds"},
			Data:       map[string][]byte{nico.SecretKeyEndpoint: []byte("https://nico.example")},
		}
		c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(secret).Build()
		_, err := nicoClientForCluster(context.Background(), c, nicoCluster, types.NamespacedName{}, nil)
		require.Error(t, err)
		require.False(t, apierrors.IsNotFound(err))
	})

	t.Run("valid secret uses factory after LoadSecretConfig", func(t *testing.T) {
		secret := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{Namespace: "ns", Name: "nico-creds"},
			Data: map[string][]byte{
				nico.SecretKeyEndpoint: []byte("https://nico.example"),
				nico.SecretKeyOrgID:    []byte("org-1"),
				nico.SecretKeyToken:    []byte("test-token"),
			},
		}
		c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(secret).Build()
		want := nicofake.New()
		var sawCfg nico.SecretConfig
		got, err := nicoClientForCluster(context.Background(), c, nicoCluster, types.NamespacedName{},
			func(_ context.Context, _ *corev1.Secret, cfg nico.SecretConfig) (nico.API, error) {
				sawCfg = cfg
				return want, nil
			},
		)
		require.NoError(t, err)
		require.Equal(t, want, got)
		require.Equal(t, "https://nico.example", sawCfg.Endpoint)
		require.Equal(t, "org-1", sawCfg.OrgID)
		require.Equal(t, "test-token", sawCfg.Token)
	})
}
