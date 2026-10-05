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
	"sigs.k8s.io/cluster-api/util/predicates"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/predicate"

	infrav1 "github.com/dsx-ai-factory/cluster-api-provider-nico/api/v1alpha1"
	"github.com/dsx-ai-factory/cluster-api-provider-nico/internal/nico"
)

const (
	nicoIdentityReadyCondition = "Ready"

	// defaultIdentityCheckInterval is the longest nominal delay after each completed check.
	defaultIdentityCheckInterval = 5 * time.Minute
	// identitySupersededRequeue retries soon after discarding a result for changed inputs.
	identitySupersededRequeue = time.Second

	identityCredentialsNotFoundReason = "CredentialsNotFound"
	identitySecretReadFailedReason    = "SecretReadFailed"
)

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
	// CheckInterval is the longest delay after each completed check. Zero means five minutes.
	CheckInterval time.Duration
}

// +kubebuilder:rbac:groups=infrastructure.cluster.x-k8s.io,resources=nicoidentities,verbs=get;list;watch
// +kubebuilder:rbac:groups=infrastructure.cluster.x-k8s.io,resources=nicoidentities/status,verbs=get;update;patch
// +kubebuilder:rbac:groups="",resources=secrets,verbs=get

func (r *NicoIdentityReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := ctrl.LoggerFrom(ctx)

	tested := &infrav1.NicoIdentity{}
	if err := r.APIReader.Get(ctx, req.NamespacedName, tested); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}
	if !tested.DeletionTimestamp.IsZero() {
		return ctrl.Result{}, nil
	}

	result, revision := r.check(ctx, tested)
	completed := metav1.Now()
	if ctx.Err() != nil {
		// The manager is stopping; a canceled check is not an observation.
		return ctrl.Result{}, nil
	}

	current, superseded, err := r.superseded(ctx, tested, revision)
	if err != nil {
		return ctrl.Result{}, err
	}
	if superseded {
		log.V(1).Info("discarded NicoIdentity check for changed inputs", "reason", result.Reason)
		return ctrl.Result{RequeueAfter: identitySupersededRequeue}, nil
	}

	meta.SetStatusCondition(&current.Status.Conditions, metav1.Condition{
		Type:               nicoIdentityReadyCondition,
		Status:             result.Status,
		ObservedGeneration: tested.Generation,
		LastTransitionTime: completed,
		Reason:             result.Reason,
		Message:            result.Message,
	})
	current.Status.LastCheckedTime = &completed
	if err := r.Status().Update(ctx, current); err != nil {
		return ctrl.Result{}, fmt.Errorf("publish NicoIdentity status: %w", err)
	}

	log.Info("checked NicoIdentity credentials", "ready", result.Status, "reason", result.Reason)
	return ctrl.Result{RequeueAfter: r.recheckAfter(current)}, nil
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
// Identity for an optimistic status update otherwise.
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

// secretRevision identifies the Secret state a check used without retaining its contents.
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
func (r *NicoIdentityReconciler) recheckAfter(identity *infrav1.NicoIdentity) time.Duration {
	interval := defaultIdentityCheckInterval
	if r.CheckInterval > 0 {
		interval = r.CheckInterval
	}
	return interval - deterministicJitter(string(identity.UID), interval/5)
}

func (r *NicoIdentityReconciler) validationTimeout() time.Duration {
	if r.ProviderConfig.IdentityValidationTimeout > 0 {
		return r.ProviderConfig.IdentityValidationTimeout
	}
	return nico.DefaultCredentialValidationTimeout
}

// SetupWithManager watches every Identity in the manager's cache scope, narrowed
// only by the watch-filter label. Status-only updates, including this
// controller's own lastCheckedTime writes, do not trigger checks; the timed
// requeue does. It returns ErrNicoIdentityCRDMissing when the API server does
// not serve NicoIdentity.
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

	predicateLog := ctrl.LoggerFrom(ctx).WithValues("controller", "NicoIdentity")
	return ctrl.NewControllerManagedBy(mgr).
		For(&infrav1.NicoIdentity{}, builder.WithPredicates(
			predicates.ResourceHasFilterLabel(mgr.GetScheme(), predicateLog, r.WatchFilterValue),
			// A label change can bring an Identity into the watch filter.
			predicate.Or(predicate.GenerationChangedPredicate{}, predicate.LabelChangedPredicate{}),
		)).
		Complete(r)
}
