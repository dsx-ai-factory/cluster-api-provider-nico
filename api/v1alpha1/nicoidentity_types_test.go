// SPDX-FileCopyrightText: Copyright (c) 2026 NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package v1alpha1

import (
	"encoding/json"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	apiequality "k8s.io/apimachinery/pkg/api/equality"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
)

func TestNicoIdentityAPI(t *testing.T) {
	ctx := t.Context()
	environment := &envtest.Environment{
		CRDDirectoryPaths:     []string{filepath.Join("..", "..", "config", "crd", "bases")},
		ErrorIfCRDPathMissing: true,
		UseExistingCluster:    new(false),
	}
	config, err := environment.Start()
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, environment.Stop()) })
	scheme := runtime.NewScheme()
	require.NoError(t, corev1.AddToScheme(scheme))
	require.NoError(t, AddToScheme(scheme))
	c, err := client.New(config, client.Options{Scheme: scheme})
	require.NoError(t, err)
	namespace := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "identity-api"}}
	require.NoError(t, c.Create(ctx, namespace))

	// Accepted objects' initial generation and absent status, finalizers and
	// owners are asserted by the nicoidentity-create-ready controller golden.
	t.Run("admission", func(t *testing.T) {
		create := func(spec map[string]any) error {
			object := &unstructured.Unstructured{Object: map[string]any{
				"apiVersion": GroupVersion.String(),
				"kind":       "NicoIdentity",
				"metadata":   map[string]any{"generateName": "identity-", "namespace": namespace.Name},
			}}
			if spec != nil {
				object.Object["spec"] = spec
			}
			return c.Create(ctx, object)
		}
		reference := func(name string) map[string]any {
			return map[string]any{"credentialsRef": map[string]any{"name": name}}
		}
		requireRejected := func(t *testing.T, names ...string) {
			t.Helper()
			for _, name := range names {
				err := create(reference(name))
				require.True(t, apierrors.IsInvalid(err), "name %q: expected admission rejection, got %v", name, err)
			}
		}

		cases := []struct {
			name  string
			spec  map[string]any
			valid bool
		}{
			{name: "missing spec"},
			{name: "missing reference", spec: map[string]any{}},
			{name: "missing name", spec: map[string]any{"credentialsRef": map[string]any{}}},
			{name: "null reference", spec: map[string]any{"credentialsRef": nil}},
			{name: "valid 253-character name", spec: reference(strings.Repeat("a", 253)), valid: true},
			{name: "invalid empty name", spec: reference("")},
			{name: "invalid 254-character name", spec: reference(strings.Repeat("a", 254))},
		}
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				err := create(tc.spec)
				if !tc.valid {
					require.True(t, apierrors.IsInvalid(err), "expected admission rejection, got %v", err)
					return
				}
				require.NoError(t, err)
			})
		}

		t.Run("accepts valid Secret-name forms", func(t *testing.T) {
			for _, name := range []string{"nico-credentials", "nico.credentials", "0"} {
				require.NoError(t, create(reference(name)), "name %q", name)
			}
		})
		t.Run("rejects characters outside lowercase alphanumerics, dash and dot", func(t *testing.T) {
			requireRejected(t, "Nico", "nico_credentials", "nico/credentials")
		})
		t.Run("rejects empty dot-separated segments", func(t *testing.T) {
			requireRejected(t, ".nico", "nico.", "nico..credentials")
		})
	})

	newIdentity := func(name string) *NicoIdentity {
		return &NicoIdentity{
			ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace.Name},
			Spec:       NicoIdentitySpec{CredentialsRef: NicoIdentityCredentialsReference{Name: "nico-credentials"}},
		}
	}
	checked := metav1.NewTime(time.Date(2026, 9, 23, 12, 5, 0, 0, time.UTC))
	ready := metav1.Condition{
		Type: "Ready", Status: metav1.ConditionUnknown, ObservedGeneration: 1,
		LastTransitionTime: metav1.NewTime(checked.Add(-time.Minute)),
		Reason:             "FutureValidationReason", Message: "A future diagnostic remains admissible.",
	}

	t.Run("status isolation and mutable reference", func(t *testing.T) {
		object := newIdentity("status-isolation")
		object.Status = NicoIdentityStatus{Conditions: []metav1.Condition{ready}, LastCheckedTime: &checked}
		require.NoError(t, c.Create(ctx, object))
		key := client.ObjectKeyFromObject(object)
		require.NoError(t, c.Get(ctx, key, object))
		require.Empty(t, object.Status.Conditions, "creation must not supply an observation")
		require.Nil(t, object.Status.LastCheckedTime)

		object.Spec.CredentialsRef.Name = "replacement-credentials"
		object.Status.Conditions = []metav1.Condition{ready}
		require.NoError(t, c.Update(ctx, object))
		require.NoError(t, c.Get(ctx, key, object))
		require.EqualValues(t, 2, object.Generation)
		require.Empty(t, object.Status.Conditions, "ordinary updates must not write status")

		condition := ready
		condition.ObservedGeneration = object.Generation
		status := NicoIdentityStatus{Conditions: []metav1.Condition{condition}, LastCheckedTime: &checked}
		object.Status = status
		object.Spec.CredentialsRef.Name = "must-not-be-written-through-status"
		require.NoError(t, c.Status().Update(ctx, object))
		require.NoError(t, c.Get(ctx, key, object))
		require.Equal(t, "replacement-credentials", object.Spec.CredentialsRef.Name)
		require.EqualValues(t, 2, object.Generation, "status must not advance desired generation")
		require.True(t, apiequality.Semantic.DeepEqual(status, object.Status), "status was not preserved: %+v", object.Status)

		object.Spec.CredentialsRef.Name = "rotated-reference"
		object.Status = NicoIdentityStatus{}
		require.NoError(t, c.Update(ctx, object))
		require.NoError(t, c.Get(ctx, key, object))
		require.EqualValues(t, 3, object.Generation)
		require.True(t, apiequality.Semantic.DeepEqual(status, object.Status), "a spec edit must preserve the old generation's observation")

		var identities NicoIdentityList
		require.NoError(t, c.List(ctx, &identities, client.InNamespace(namespace.Name)))
		require.True(t, slices.ContainsFunc(identities.Items, func(identity NicoIdentity) bool { return identity.UID == object.UID }))
		require.NoError(t, c.Delete(ctx, object))
		require.True(t, apierrors.IsNotFound(c.Get(ctx, key, &NicoIdentity{})))
	})

	t.Run("conditions merge by type and retain future reasons", func(t *testing.T) {
		object := newIdentity("condition-ownership")
		require.NoError(t, c.Create(ctx, object))
		applyCondition := func(owner string, condition metav1.Condition) {
			t.Helper()
			body, err := json.Marshal(map[string]any{
				"apiVersion": GroupVersion.String(), "kind": "NicoIdentity",
				"metadata": map[string]any{"name": object.Name, "namespace": object.Namespace},
				"status":   NicoIdentityStatus{Conditions: []metav1.Condition{condition}},
			})
			require.NoError(t, err)
			require.NoError(t, c.Status().Patch(ctx, object, client.RawPatch(types.ApplyPatchType, body), client.FieldOwner(owner)))
		}
		applyCondition("baseline-observer", ready)
		additional := ready
		additional.Type = "FutureDetail"
		additional.Status = metav1.ConditionTrue
		applyCondition("additional-observer", additional)
		require.NoError(t, c.Get(ctx, client.ObjectKeyFromObject(object), object))
		require.Len(t, object.Status.Conditions, 2)
		require.True(t, apiequality.Semantic.DeepEqual(&ready, apimeta.FindStatusCondition(object.Status.Conditions, "Ready")))
		require.True(t, apiequality.Semantic.DeepEqual(&additional, apimeta.FindStatusCondition(object.Status.Conditions, "FutureDetail")))

		updated := ready
		updated.Status = metav1.ConditionFalse
		updated.LastTransitionTime = checked
		applyCondition("baseline-observer", updated)
		require.NoError(t, c.Get(ctx, client.ObjectKeyFromObject(object), object))
		require.Len(t, object.Status.Conditions, 2)
		require.True(t, apiequality.Semantic.DeepEqual(&updated, apimeta.FindStatusCondition(object.Status.Conditions, "Ready")))
		require.True(t, apiequality.Semantic.DeepEqual(&additional, apimeta.FindStatusCondition(object.Status.Conditions, "FutureDetail")))
		require.EqualValues(t, 1, object.Generation)

		object.Status.Conditions = []metav1.Condition{updated, updated}
		require.True(t, apierrors.IsInvalid(c.Status().Update(ctx, object)), "condition types must be unique")
	})
}
