// SPDX-FileCopyrightText: Copyright (c) 2026 NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package controllers

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	clusterv1 "sigs.k8s.io/cluster-api/api/core/v1beta2"
	"sigs.k8s.io/cluster-api/util/conditions"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	infrav1 "github.com/NVIDIA/cluster-api-provider-nico/api/v1alpha1"
	"github.com/NVIDIA/cluster-api-provider-nico/internal/nico"
)

func TestNicoClusterReconciler_InvalidIdentitySecret(t *testing.T) {
	scheme := runtime.NewScheme()
	require.NoError(t, corev1.AddToScheme(scheme))
	require.NoError(t, clusterv1.AddToScheme(scheme))
	require.NoError(t, infrav1.AddToScheme(scheme))

	cluster := &clusterv1.Cluster{
		ObjectMeta: metav1.ObjectMeta{Namespace: "ns", Name: "cluster-1", UID: "cluster-uid"},
	}
	nicoCluster := &infrav1.NicoCluster{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "ns",
			Name:      "nico-1",
			OwnerReferences: []metav1.OwnerReference{{
				APIVersion: clusterv1.GroupVersion.String(),
				Kind:       "Cluster",
				Name:       cluster.Name,
				UID:        cluster.UID,
			}},
		},
		Spec: infrav1.NicoClusterSpec{
			IdentityRef: corev1.LocalObjectReference{Name: "nico-creds"},
		},
	}
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Namespace: "ns", Name: "nico-creds"},
		Data:       map[string][]byte{nico.SecretKeyEndpoint: []byte("https://nico.example")},
	}

	c := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(cluster, nicoCluster, secret).
		WithStatusSubresource(nicoCluster).
		Build()

	r := &NicoClusterReconciler{Client: c, Scheme: scheme}
	req := ctrl.Request{NamespacedName: types.NamespacedName{Namespace: "ns", Name: "nico-1"}}

	// EnsurePausedCondition may patch and requeue before identity handling.
	_, err := r.Reconcile(context.Background(), req)
	require.NoError(t, err)

	_, err = r.Reconcile(context.Background(), req)
	require.Error(t, err)

	var updated infrav1.NicoCluster
	require.NoError(t, c.Get(context.Background(), types.NamespacedName{Namespace: "ns", Name: "nico-1"}, &updated))
	cond := conditions.Get(&updated, clusterv1.ReadyCondition)
	require.NotNil(t, cond)
	assert.Equal(t, metav1.ConditionFalse, cond.Status)
	assert.Equal(t, infrav1.IdentityConfigurationFailedReason, cond.Reason)
	assert.False(t, updated.Status.Ready)
}
