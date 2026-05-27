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
)

type openAPIError interface {
	error
	Body() []byte
	Model() interface{}
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
	case http.StatusUnauthorized, http.StatusForbidden:
		return fmt.Errorf("%w: %s", ErrUnauthorized, message)
	case http.StatusNotFound:
		return fmt.Errorf("%w: %s", ErrNotFound, message)
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
