// SPDX-FileCopyrightText: Copyright (c) 2026 NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package controllers

import (
	"context"
	"fmt"
	"slices"
	"sort"
	"time"

	"github.com/onsi/ginkgo/v2"
	"github.com/onsi/gomega"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/config"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"
	"sigs.k8s.io/yaml"

	infrav1 "github.com/dsx-ai-factory/cluster-api-provider-nico/api/v1alpha1"
	"github.com/dsx-ai-factory/cluster-api-provider-nico/internal/fake"
	"github.com/dsx-ai-factory/cluster-api-provider-nico/internal/nico"
	"github.com/dsx-ai-factory/cluster-api-provider-nico/pkg/test/fixture"
)

// testIdentity is the Identity whose generation each case's steps track. The
// reconciler checks every Identity in a case.
const testIdentity = "nico-identity"

func nicoIdentityCaseSet(description, dirPrefix string, defineSteps func(*fixture.Case, fixture.CaseSet)) fixture.CaseSet {
	return fixture.CaseSet{
		Description:          description,
		DirPrefix:            dirPrefix,
		MaskExpectedMetadata: true,
		SchemeFn:             newScheme,
		EnvironmentFn:        newEnvironment,
		CompareObjects: func() []client.ObjectList {
			return []client.ObjectList{&infrav1.NicoIdentityList{}}
		},
		Setup: func(ctx ginkgo.SpecContext, tc *fixture.Case, _ fixture.CaseSet) {
			tc.Client = client.WithFieldOwner(tc.Client, "capnico-envtest")
			gomega.Expect(tc.CreateObjects(ctx)).To(gomega.Succeed())
			gomega.Expect(applyNicoIdentityStatusFixture(ctx, tc)).To(gomega.Succeed())

			server := fake.New()
			gomega.Expect(seedFakeResources(tc, server)).To(gomega.Succeed())
			// The Identity controller never writes to NICo or Secrets; both goldens show that.
			tc.AddGolden("expected_nico.yaml", func(context.Context) (string, error) {
				return server.Dump()
			})
			tc.AddGolden("expected_secrets.yaml", func(ctx context.Context) (string, error) {
				return dumpSecretKeys(ctx, tc.Client)
			})

			endpoint := startFake(server)
			gomega.Expect(pointIdentitySecretsAtFake(ctx, tc.Client, endpoint)).To(gomega.Succeed())
			startIdentityReconciler(ctx, tc)
		},
		DefineSteps: defineSteps,
	}
}

// IMPORTANT: Read docs/writing-tests.md. There is ZERO reason that you should
// have to add or update a case set.
// Represents an Identity at generation 1 because reconciliation only writes status.
var _ = fixture.DescribeCaseSet(nicoIdentityCaseSet(
	"NicoIdentity create reconciliation",
	"nicoidentity-create-",
	func(tc *fixture.Case, _ fixture.CaseSet) {
		ginkgo.It("checks every NicoIdentity", func(ctx ginkgo.SpecContext) {
			expectNicoIdentityChecked(ctx, tc, 1)
		})
	},
))

// IMPORTANT: Read docs/writing-tests.md. There is ZERO reason that you should
// have to add or update a case set.
// Represents an Identity at generation 2 after a spec update.
var _ = fixture.DescribeCaseSet(nicoIdentityCaseSet(
	"NicoIdentity update reconciliation",
	"nicoidentity-update-",
	func(tc *fixture.Case, _ fixture.CaseSet) {
		ginkgo.It("checks the initial NicoIdentity", func(ctx ginkgo.SpecContext) {
			expectNicoIdentityChecked(ctx, tc, 1)
		})

		ginkgo.It("applies the NicoIdentity update", func(ctx ginkgo.SpecContext) {
			gomega.Expect(tc.PatchObjects(ctx, "input_update.yaml")).To(gomega.Succeed())
		})

		ginkgo.It("checks the updated NicoIdentity", func(ctx ginkgo.SpecContext) {
			expectNicoIdentityChecked(ctx, tc, 2)
		})
	},
))

// IMPORTANT: Read docs/writing-tests.md. There is ZERO reason that you should
// have to add or update a case set.
var _ = fixture.DescribeCaseSet(nicoIdentityCaseSet(
	"NicoIdentity delete reconciliation",
	"nicoidentity-delete-",
	func(tc *fixture.Case, _ fixture.CaseSet) {
		var deletedObjects []client.Object

		ginkgo.It("checks the initial NicoIdentity", func(ctx ginkgo.SpecContext) {
			expectNicoIdentityChecked(ctx, tc, 1)
		})

		ginkgo.It("deletes the selected objects", func(ctx ginkgo.SpecContext) {
			var err error
			deletedObjects, err = tc.DeleteObjects(ctx, "input_delete.yaml")
			gomega.Expect(err).NotTo(gomega.HaveOccurred())
		})

		ginkgo.It("reconciles the object deletion", func(ctx ginkgo.SpecContext) {
			gomega.Eventually(func(g gomega.Gomega) {
				for _, object := range deletedObjects {
					actual := object.DeepCopyObject().(client.Object)
					err := tc.Client.Get(ctx, client.ObjectKeyFromObject(object), actual)
					g.Expect(apierrors.IsNotFound(err)).To(gomega.BeTrue())
				}
			}).WithTimeout(timeout).WithPolling(time.Second).Should(gomega.Succeed())
		})
	},
))

// expectNicoIdentityChecked waits until testIdentity is at generation and every
// Identity in the case has a completed check of its current generation. A
// completed check can be Unknown, so it waits for Ready's observedGeneration
// and lastCheckedTime rather than a Ready value.
func expectNicoIdentityChecked(ctx context.Context, tc *fixture.Case, generation int64) {
	gomega.Eventually(func(g gomega.Gomega) {
		tracked := &infrav1.NicoIdentity{}
		g.Expect(tc.Client.Get(ctx, client.ObjectKey{Namespace: testNamespace, Name: testIdentity}, tracked)).To(gomega.Succeed())
		g.Expect(tracked.Generation).To(gomega.Equal(generation))

		identities := &infrav1.NicoIdentityList{}
		g.Expect(tc.Client.List(ctx, identities)).To(gomega.Succeed())
		for i := range identities.Items {
			identity := &identities.Items[i]
			ready := meta.FindStatusCondition(identity.Status.Conditions, nicoIdentityReadyCondition)
			g.Expect(ready).NotTo(gomega.BeNil(), "%s/%s has no Ready condition", identity.Namespace, identity.Name)
			g.Expect(ready.ObservedGeneration).To(gomega.Equal(identity.Generation))

			// Goldens normalize the check time, so prove here that it exists and is plausible.
			checked := identity.Status.LastCheckedTime
			g.Expect(checked).NotTo(gomega.BeNil())
			g.Expect(checked.Before(&ready.LastTransitionTime)).To(gomega.BeFalse())
			g.Expect(checked.Time).To(gomega.BeTemporally("<=", time.Now()))
		}
	}).WithTimeout(timeout).WithPolling(time.Second).Should(gomega.Succeed())
}

// startIdentityReconciler runs only the Identity controller. Its provider
// default is nico-creds in the case namespace, which the Identity controller
// does not use: every Identity is checked against the Secret it names.
func startIdentityReconciler(ctx ginkgo.SpecContext, tc *fixture.Case) {
	mgr, err := manager.New(tc.Config, manager.Options{
		Scheme:  tc.Scheme,
		Metrics: metricsserver.Options{BindAddress: "0"},
		// Cases share a process, so the controller names repeat.
		Controller: config.Controller{SkipNameValidation: new(true)},
	})
	gomega.Expect(err).NotTo(gomega.HaveOccurred())

	gomega.Expect((&NicoIdentityReconciler{
		Client: mgr.GetClient(),
		ProviderConfig: nico.ProviderConfig{
			Credentials: types.NamespacedName{Namespace: testNamespace, Name: "nico-creds"},
		},
	}).SetupWithManager(ctx, mgr)).To(gomega.Succeed())

	tc.StartManager(ctx, mgr)
}

// applyNicoIdentityStatusFixture writes status that exists before the
// controller starts, such as a condition owned by another writer.
func applyNicoIdentityStatusFixture(ctx context.Context, tc *fixture.Case) error {
	input, ok := tc.Input("input_nicoidentity_status.yaml")
	if !ok {
		return nil
	}

	desired := &infrav1.NicoIdentity{}
	if err := yaml.Unmarshal([]byte(input), desired); err != nil {
		return fmt.Errorf("decode input_nicoidentity_status.yaml: %w", err)
	}
	identity := &infrav1.NicoIdentity{}
	if err := tc.Client.Get(ctx, client.ObjectKeyFromObject(desired), identity); err != nil {
		return fmt.Errorf("get NicoIdentity for status fixture: %w", err)
	}
	identity.Status = desired.Status
	if err := tc.Client.Status().Update(ctx, identity); err != nil {
		return fmt.Errorf("apply NicoIdentity status fixture: %w", err)
	}
	return nil
}

type secretKeys struct {
	Name string   `json:"name"`
	Type string   `json:"type"`
	Keys []string `json:"keys"`
}

// dumpSecretKeys lists the case Secrets by name and key, omitting values such
// as the fake endpoint's port and credentials.
func dumpSecretKeys(ctx context.Context, c client.Client) (string, error) {
	secrets := &corev1.SecretList{}
	if err := c.List(ctx, secrets, client.InNamespace(testNamespace)); err != nil {
		return "", fmt.Errorf("list Secrets: %w", err)
	}

	items := make([]secretKeys, 0, len(secrets.Items))
	for i := range secrets.Items {
		keys := make([]string, 0, len(secrets.Items[i].Data))
		for key := range secrets.Items[i].Data {
			keys = append(keys, key)
		}
		slices.Sort(keys)
		items = append(items, secretKeys{Name: secrets.Items[i].Name, Type: string(secrets.Items[i].Type), Keys: keys})
	}
	sort.Slice(items, func(i, j int) bool { return items[i].Name < items[j].Name })

	out, err := yaml.Marshal(items)
	if err != nil {
		return "", fmt.Errorf("encode Secrets: %w", err)
	}
	return string(out), nil
}
