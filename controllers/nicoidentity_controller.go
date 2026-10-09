// SPDX-FileCopyrightText: Copyright (c) 2026 NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package controllers

import (
	"context"
	"errors"
	"fmt"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/clock"
	"sigs.k8s.io/cluster-api/util/conditions"
	"sigs.k8s.io/cluster-api/util/labels"
	"sigs.k8s.io/cluster-api/util/patch"
	"sigs.k8s.io/cluster-api/util/predicates"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	infrav1 "github.com/dsx-ai-factory/cluster-api-provider-nico/api/v1alpha1"
	"github.com/dsx-ai-factory/cluster-api-provider-nico/internal/nico"
)

const (
	nicoIdentityReadyCondition = "Ready"
	// nicoIdentityCredentialsRefIndex indexes Identities by the Secret they name.
	nicoIdentityCredentialsRefIndex = "nicoIdentityCredentialsRef"

	// identityCheckInterval is the longest nominal delay after each completed check.
	identityCheckInterval = 5 * time.Minute
	// identitySupersededRequeue retries soon after discarding a result for changed inputs.
	identitySupersededRequeue = time.Second

	identityCredentialsNotFoundReason = "CredentialsNotFound"
	identitySecretReadFailedReason    = "SecretReadFailed"
)

// nicoIdentityOwnedConditions are the conditions this controller writes. Patches
// keep its values for them and leave other writers' conditions alone.
var nicoIdentityOwnedConditions = []string{nicoIdentityReadyCondition}

// ErrNicoIdentityCRDMissing reports that the API server does not serve NicoIdentity.
var ErrNicoIdentityCRDMissing = errors.New("the NicoIdentity CRD is not installed")

// Secret states recorded for a check that did not read Secret contents.
const (
	secretRevisionAbsent     = "absent"
	secretRevisionUnreadable = "unreadable"
)

// NicoIdentityReconciler reports whether the Secret each NicoIdentity names
// passes NICo's baseline check. Like the cluster controller, it reconciles every
// Identity in the manager's cache scope. It never changes which credentials
// provisioning uses.
type NicoIdentityReconciler struct {
	client.Client
	// APIReader reads the Identity and its Secret directly from the API server.
	APIReader        client.Reader
	ProviderConfig   nico.ProviderConfig
	WatchFilterValue string
	// clock dates each completed check. SetupWithManager defaults it to the real clock.
	clock clock.PassiveClock
}

// +kubebuilder:rbac:groups=infrastructure.cluster.x-k8s.io,resources=nicoidentities,verbs=get;list;watch
// +kubebuilder:rbac:groups=infrastructure.cluster.x-k8s.io,resources=nicoidentities/status,verbs=get;update;patch
// +kubebuilder:rbac:groups="",resources=secrets,verbs=get;list;watch

func (r *NicoIdentityReconciler) Reconcile(ctx context.Context, req ctrl.Request) (_ ctrl.Result, retErr error) {
	log := ctrl.LoggerFrom(ctx)

	identity := &infrav1.NicoIdentity{}
	if err := r.APIReader.Get(ctx, req.NamespacedName, identity); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}
	if !identity.DeletionTimestamp.IsZero() {
		return ctrl.Result{}, nil
	}
	if !r.inWatchScope(identity) {
		// Scheduled rechecks bypass the event filter, so an Identity that left
		// the watch filter stops here and keeps its last status.
		log.V(1).Info("stopped checking NicoIdentity outside the watch filter")
		return ctrl.Result{}, nil
	}

	patchHelper, err := patch.NewHelper(identity, r.Client)
	if err != nil {
		return ctrl.Result{}, err
	}
	// A canceled or discarded check leaves identity unchanged, so nothing is patched.
	defer func() {
		if patchErr := patchHelper.Patch(ctx, identity, patch.WithOwnedConditions{Conditions: nicoIdentityOwnedConditions}); patchErr != nil {
			retErr = errors.Join(retErr, fmt.Errorf("publish NicoIdentity status: %w", patchErr))
		}
	}()

	result, revision := r.check(ctx, identity)
	completed := metav1.NewTime(r.clock.Now())
	if ctx.Err() != nil {
		// The manager is stopping; a canceled check is not an observation.
		return ctrl.Result{}, nil
	}

	current, superseded, err := r.superseded(ctx, identity, revision)
	if err != nil {
		return ctrl.Result{}, err
	}
	if current != nil && !r.inWatchScope(current) {
		log.V(1).Info("discarded NicoIdentity check after it left the watch filter", "reason", result.Reason)
		return ctrl.Result{}, nil
	}
	if superseded {
		log.V(1).Info("discarded NicoIdentity check for changed inputs", "reason", result.Reason)
		return ctrl.Result{RequeueAfter: identitySupersededRequeue}, nil
	}

	conditions.Set(identity, metav1.Condition{
		Type:               nicoIdentityReadyCondition,
		Status:             result.Status,
		LastTransitionTime: completed,
		Reason:             result.Reason,
		Message:            result.Message,
	})
	identity.Status.LastCheckedTime = &completed

	log.Info("checked NicoIdentity credentials", "ready", result.Status, "reason", result.Reason)
	return ctrl.Result{RequeueAfter: recheckAfter(identity)}, nil
}

// check validates the Secret the Identity names in its own namespace and
// reports which Secret state it used.
func (r *NicoIdentityReconciler) check(ctx context.Context, identity *infrav1.NicoIdentity) (nico.CredentialValidation, string) {
	secret := &corev1.Secret{}
	err := r.APIReader.Get(ctx, client.ObjectKey{Namespace: identity.Namespace, Name: identity.Spec.CredentialsRef.Name}, secret)
	revision := secretRevision(secret, err)
	switch revision {
	case secretRevisionAbsent:
		return nico.CredentialValidation{
			Status:  metav1.ConditionFalse,
			Reason:  identityCredentialsNotFoundReason,
			Message: "Credential Secret was not found.",
		}, revision
	case secretRevisionUnreadable:
		ctrl.LoggerFrom(ctx).Error(err, "failed to read NicoIdentity credential Secret")
		return nico.CredentialValidation{
			Status:  metav1.ConditionUnknown,
			Reason:  identitySecretReadFailedReason,
			Message: "Credential Secret could not be read.",
		}, revision
	}
	return nico.ValidateCredentials(ctx, secret, r.validationTimeout()), revision
}

// superseded rereads the Identity and, when the check used it, the Secret. It
// reports whether either changed during the check, and returns the current
// Identity otherwise so its watch-filter label can be checked again.
func (r *NicoIdentityReconciler) superseded(ctx context.Context, tested *infrav1.NicoIdentity, revision string) (*infrav1.NicoIdentity, bool, error) {
	current := &infrav1.NicoIdentity{}
	if err := r.APIReader.Get(ctx, client.ObjectKeyFromObject(tested), current); err != nil {
		if apierrors.IsNotFound(err) {
			return nil, true, nil
		}
		return nil, false, fmt.Errorf("reread NicoIdentity: %w", err)
	}
	if current.UID != tested.UID || current.Generation != tested.Generation || !current.DeletionTimestamp.IsZero() {
		return nil, true, nil
	}
	if revision == "" {
		return current, false, nil
	}

	secret := &corev1.Secret{}
	err := r.APIReader.Get(ctx, client.ObjectKey{Namespace: tested.Namespace, Name: tested.Spec.CredentialsRef.Name}, secret)
	return current, secretRevision(secret, err) != revision, nil
}

// secretRevision identifies the Secret state a check used without retaining its
// contents. Status records which Identity generation a result checked, but not
// which Secret revision. A result for a Secret rotated during the check would
// look current, with a check time after the rotation. Comparing revisions lets
// superseded discard that result, so the recheck the rotation queued publishes
// instead.
func secretRevision(secret *corev1.Secret, err error) string {
	switch {
	case err == nil:
		return string(secret.UID) + "/" + secret.ResourceVersion
	case apierrors.IsNotFound(err):
		return secretRevisionAbsent
	default:
		return secretRevisionUnreadable
	}
}

// recheckAfter spreads Identities across the last fifth of the check interval,
// as the cluster controller spreads clusters. It subtracts the jitter rather
// than adding it, so no Identity's nominal interval exceeds five minutes, the
// age at which readers treat an observation as stale.
func recheckAfter(identity *infrav1.NicoIdentity) time.Duration {
	return identityCheckInterval - deterministicJitter(string(identity.UID), identityCheckInterval/5)
}

func (r *NicoIdentityReconciler) validationTimeout() time.Duration {
	if r.ProviderConfig.IdentityValidationTimeout > 0 {
		return r.ProviderConfig.IdentityValidationTimeout
	}
	return nico.DefaultCredentialValidationTimeout
}

// SetupWithManager watches every Identity in the manager's cache scope, narrowed
// only by the watch-filter label, and the Secrets they name. Status-only
// updates, including this controller's own lastCheckedTime writes, do not
// trigger checks; Secret changes and the timed requeue do. It returns
// ErrNicoIdentityCRDMissing when the API server does not serve NicoIdentity.
func (r *NicoIdentityReconciler) SetupWithManager(ctx context.Context, mgr ctrl.Manager) error {
	gvk := infrav1.GroupVersion.WithKind("NicoIdentity")
	if _, err := mgr.GetRESTMapper().RESTMapping(gvk.GroupKind(), gvk.Version); err != nil {
		if meta.IsNoMatchError(err) {
			return fmt.Errorf("%w: %w", ErrNicoIdentityCRDMissing, err)
		}
		return fmt.Errorf("discover the NicoIdentity API: %w", err)
	}
	if r.APIReader == nil {
		r.APIReader = mgr.GetAPIReader()
	}
	if r.clock == nil {
		r.clock = clock.RealClock{}
	}

	if err := mgr.GetFieldIndexer().IndexField(ctx, &infrav1.NicoIdentity{}, nicoIdentityCredentialsRefIndex, func(object client.Object) []string {
		return []string{object.(*infrav1.NicoIdentity).Spec.CredentialsRef.Name}
	}); err != nil {
		return fmt.Errorf("index NicoIdentities by credentials Secret: %w", err)
	}

	// The watch filter applies to Identities only. As a global event filter it
	// would also drop events for Secrets that lack the label.
	predicateLog := ctrl.LoggerFrom(ctx).WithValues("controller", "NicoIdentity")
	return ctrl.NewControllerManagedBy(mgr).
		For(&infrav1.NicoIdentity{}, builder.WithPredicates(
			predicates.ResourceHasFilterLabel(mgr.GetScheme(), predicateLog, r.WatchFilterValue),
			// Each check writes lastCheckedTime, so status-only updates must not
			// start another check. Spec changes bump the generation. Label changes
			// don't, but an Identity that gains the watch-filter label needs a check.
			predicate.Or(predicate.GenerationChangedPredicate{}, predicate.LabelChangedPredicate{}),
		)).
		Watches(&corev1.Secret{}, handler.EnqueueRequestsFromMapFunc(r.identitiesForSecret)).
		Complete(r)
}

// identitiesForSecret maps a Secret event to the Identities in its namespace
// that name it and pass the watch filter.
func (r *NicoIdentityReconciler) identitiesForSecret(ctx context.Context, secret client.Object) []reconcile.Request {
	identities := &infrav1.NicoIdentityList{}
	if err := r.List(ctx, identities,
		client.InNamespace(secret.GetNamespace()),
		client.MatchingFields{nicoIdentityCredentialsRefIndex: secret.GetName()},
	); err != nil {
		ctrl.LoggerFrom(ctx).Error(err, "failed to list NicoIdentities for credentials Secret", "secret", client.ObjectKeyFromObject(secret))
		return nil
	}

	requests := make([]reconcile.Request, 0, len(identities.Items))
	for i := range identities.Items {
		identity := &identities.Items[i]
		if !r.inWatchScope(identity) {
			continue
		}
		requests = append(requests, reconcile.Request{NamespacedName: client.ObjectKeyFromObject(identity)})
	}
	return requests
}

// inWatchScope reports whether the Identity carries the manager's watch-filter
// label. Without a filter, every Identity is in scope.
func (r *NicoIdentityReconciler) inWatchScope(identity *infrav1.NicoIdentity) bool {
	return r.WatchFilterValue == "" || labels.HasWatchLabel(identity, r.WatchFilterValue)
}
