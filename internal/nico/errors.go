// SPDX-FileCopyrightText: Copyright (c) 2026 NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package nico

import (
	"errors"
	"fmt"
	"net/http"
	"strings"
)

var (
	ErrAlreadyExists = errors.New("resource already exists")
	ErrConflict      = errors.New("resource conflict")
	ErrNotFound      = errors.New("resource not found")
	ErrUnauthorized  = errors.New("request unauthorized")
	ErrBadRequest    = errors.New("request invalid")

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

func normalizeError(resp *http.Response, err error) error {
	if err == nil {
		return nil
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
