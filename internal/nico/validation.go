// SPDX-FileCopyrightText: Copyright (c) 2026 NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package nico

import (
	"context"
	"crypto/tls"
	"errors"
	"net/http"
	"net/url"
	"time"

	"golang.org/x/oauth2"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// DefaultCredentialValidationTimeout bounds one validation attempt unless the
// manager configures another value.
const DefaultCredentialValidationTimeout = 30 * time.Second

// CredentialValidation contains only safe diagnostics for an Identity observation.
// Success proves authentication and a current-tenant read, not provisioning permissions
// or that the credentials belong to an intended principal. It contains no Secret data.
type CredentialValidation struct {
	Status  metav1.ConditionStatus `json:"status"`
	Reason  string                 `json:"reason"`
	Message string                 `json:"message"`
}

// ValidateCredentials checks one Secret snapshot without provisioning or using cached
// clients, tokens, or tenant IDs. Timeout bounds the whole attempt, from client
// construction through the current-tenant read; an earlier parent deadline still applies.
// The caller owns Secret selection and status publication. Raw client errors must not
// cross this boundary because they can contain credentials.
func ValidateCredentials(ctx context.Context, secret *corev1.Secret, timeout time.Duration) CredentialValidation {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	if ctx.Err() != nil {
		return validationFailure(ctx, ctx.Err(), "Credential validation")
	}

	cfg, err := LoadSecretConfig(secret)
	if err != nil {
		return invalidCredentialConfiguration("Credential configuration is missing required fields or contains incompatible or invalid values.")
	}
	if !validCredentialURL(cfg.Endpoint) || (cfg.TokenURL != "" && !validCredentialURL(cfg.TokenURL)) {
		return invalidCredentialConfiguration("Credential endpoint configuration requires absolute HTTP or HTTPS URLs.")
	}
	httpClient, err := cfg.HTTPClient()
	if err != nil {
		return invalidCredentialConfiguration("Credential TLS configuration does not contain a usable CA bundle.")
	}
	// Close the underlying transport, including when the SDK wraps it for apiName.
	defer httpClient.CloseIdleConnections()
	client, err := newClient(ctx, cfg, httpClient)
	if err != nil {
		return invalidCredentialConfiguration("Credential client configuration is invalid.")
	}

	// OAuth retains the construction context; both operations share this deadline.
	authCtx, err := client.authCtx(ctx)
	if ctx.Err() != nil {
		return validationFailure(ctx, ctx.Err(), "Token acquisition")
	}
	if err != nil {
		var rejection *oauth2.RetrieveError
		if errors.As(err, &rejection) && rejection.Response != nil && rejection.ErrorCode == "invalid_client" &&
			(rejection.Response.StatusCode == http.StatusBadRequest || rejection.Response.StatusCode == http.StatusUnauthorized) {
			return CredentialValidation{metav1.ConditionFalse, "AuthenticationFailed", "Token issuer rejected the client credentials."}
		}
		return validationFailure(ctx, err, "Token acquisition")
	}

	_, err = client.getCurrentTenantID(authCtx)
	if ctx.Err() != nil {
		return validationFailure(ctx, ctx.Err(), "Current-tenant lookup")
	}
	switch {
	case errors.Is(err, ErrForbidden):
		// ErrForbidden also matches ErrUnauthorized; preserve the more specific evidence.
		return CredentialValidation{metav1.ConditionFalse, "AccessDenied", "Current-tenant lookup was forbidden."}
	case errors.Is(err, ErrUnauthorized):
		return CredentialValidation{metav1.ConditionFalse, "AuthenticationFailed", "Current-tenant lookup rejected the access token."}
	case err != nil:
		return validationFailure(ctx, err, "Current-tenant lookup")
	}

	message := "Authentication and current-tenant lookup succeeded."
	if cfg.InsecureSkipTLSVerify {
		message += " TLS certificate verification is disabled."
	}
	return CredentialValidation{metav1.ConditionTrue, "ValidationSucceeded", message}
}

func validCredentialURL(raw string) bool {
	u, err := url.Parse(raw)
	return err == nil && u.Hostname() != "" && (u.Scheme == "http" || u.Scheme == "https")
}

func invalidCredentialConfiguration(message string) CredentialValidation {
	return CredentialValidation{metav1.ConditionFalse, "InvalidConfiguration", message}
}

func validationFailure(ctx context.Context, err error, operation string) CredentialValidation {
	message := operation + " could not establish credential health."
	var verificationError *tls.CertificateVerificationError
	switch {
	case errors.Is(ctx.Err(), context.DeadlineExceeded), errors.Is(err, context.DeadlineExceeded):
		message = operation + " exceeded the validation deadline."
	case errors.Is(ctx.Err(), context.Canceled), errors.Is(err, context.Canceled):
		message = operation + " was canceled."
	case errors.As(err, &verificationError):
		message = operation + " failed TLS certificate verification."
	}
	return CredentialValidation{metav1.ConditionUnknown, "ValidationFailed", message}
}
