// SPDX-FileCopyrightText: Copyright (c) 2026 NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package nico

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
)

var (
	ErrAlreadyExists = errors.New("resource already exists")
	ErrConflict      = errors.New("resource conflict")
	ErrNotFound      = errors.New("resource not found")
	ErrUnauthorized  = errors.New("request unauthorized")
	ErrBadRequest    = errors.New("request invalid")

	// ErrTLSVerifyFailed indicates NICo's TLS certificate could not be verified.
	ErrTLSVerifyFailed = errors.New("NICo TLS certificate verification failed")
	// ErrEndpointUnreachable indicates a NICo network failure before an HTTP response.
	ErrEndpointUnreachable = errors.New("NICo API endpoint could not be reached")

	// ErrForbidden means NICo authenticated the identity and refused the action.
	// A 403 also satisfies ErrUnauthorized; only ErrForbidden distinguishes a
	// denied privilege from a credential NICo would not accept at all.
	ErrForbidden = errors.New("request forbidden")

	// ErrFailureDomainUnavailable means NICo rejected placement in the requested domain.
	ErrFailureDomainUnavailable = errors.New("requested failure domain unavailable")
	// ErrFailureDomainMismatch means a selected or assigned machine is in a different domain.
	ErrFailureDomainMismatch = errors.New("machine failure domain does not match requested domain")
	// ErrFailureDomainCapabilityRequired means NICo requires targeted instance creation for placement.
	ErrFailureDomainCapabilityRequired = errors.New("requested failure domain requires targeted instance creation capability")
)

type openAPIError interface {
	error
	Body() []byte
	Model() any
}

// connectionError keeps transport details out of messages while preserving the cause.
type connectionError struct {
	kind  error
	cause error
}

func (e *connectionError) Error() string        { return e.kind.Error() }
func (e *connectionError) Unwrap() error        { return e.cause }
func (e *connectionError) Is(target error) bool { return target == e.kind }

func normalizeConnectionError(ctx context.Context, err error) error {
	if ctx.Err() != nil || errors.Is(err, context.Canceled) {
		return err
	}

	var verificationError *tls.CertificateVerificationError
	var unknownAuthority x509.UnknownAuthorityError
	var hostnameError x509.HostnameError
	var invalidCertificate x509.CertificateInvalidError
	var systemRootsError x509.SystemRootsError
	if errors.As(err, &verificationError) || errors.As(err, &unknownAuthority) ||
		errors.As(err, &hostnameError) || errors.As(err, &invalidCertificate) || errors.As(err, &systemRootsError) {
		return &connectionError{kind: ErrTLSVerifyFailed, cause: err}
	}

	cause := err
	if urlError, ok := errors.AsType[*url.Error](err); ok {
		cause = urlError.Err
	}
	var networkError net.Error
	if errors.As(cause, &networkError) || errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
		return &connectionError{kind: ErrEndpointUnreachable, cause: err}
	}
	return err
}

func normalizeError(ctx context.Context, resp *http.Response, err error) error {
	if err == nil {
		return nil
	}
	if resp == nil {
		err = normalizeConnectionError(ctx, err)
	}

	var apiErr openAPIError
	if !errors.As(err, &apiErr) {
		return err
	}

	message := strings.TrimSpace(err.Error())
	if body := strings.TrimSpace(string(apiErr.Body())); body != "" {
		message += ": " + body
	}

	statusCode := 0
	if resp != nil {
		statusCode = resp.StatusCode
	}

	switch statusCode {
	case http.StatusUnauthorized:
		return fmt.Errorf("%w: %s", ErrUnauthorized, message)
	case http.StatusForbidden:
		return fmt.Errorf("%w: %w: %s", ErrUnauthorized, ErrForbidden, message)
	case http.StatusNotFound:
		return fmt.Errorf("%w: %s", ErrNotFound, message)
	case http.StatusBadRequest:
		return fmt.Errorf("%w: %s", ErrBadRequest, message)
	case http.StatusConflict:
		// Typed ErrAlreadyExists, which is a special case of conflict error.
		if strings.Contains(strings.ToLower(message), "already exists") {
			return fmt.Errorf("%w: %s", ErrAlreadyExists, message)
		}
		return fmt.Errorf("%w: %s", ErrConflict, message)
	default:
		return fmt.Errorf("%s", message)
	}
}
