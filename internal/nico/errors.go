package nico

import (
	"errors"
	"fmt"
	"strings"

	nicosdk "github.com/NVIDIA/ncx-infra-controller-rest/sdk/standard"
)

var (
	ErrAlreadyExists = errors.New("resource already exists")
	ErrNotFound      = errors.New("resource not found")
	ErrUnauthorized  = errors.New("request unauthorized")
)

type openAPIError interface {
	error
	Body() []byte
	Model() interface{}
}

func normalizeError(err error) error {
	if err == nil {
		return nil
	}

	var apiErr openAPIError
	if !errors.As(err, &apiErr) {
		return err
	}

	message := strings.TrimSpace(err.Error())
	if model := apiErr.Model(); model != nil {
		switch typed := model.(type) {
		case nicosdk.CarbideAPIError:
			if typed.GetMessage() != "" {
				message = typed.GetMessage()
			}
		case *nicosdk.CarbideAPIError:
			if typed != nil && typed.GetMessage() != "" {
				message = typed.GetMessage()
			}
		}
	}
	if message == "" && len(apiErr.Body()) > 0 {
		message = string(apiErr.Body())
	}

	lower := strings.ToLower(message)
	switch {
	case strings.Contains(lower, "already exists"):
		return fmt.Errorf("%w: %s", ErrAlreadyExists, message)
	case strings.Contains(lower, "not found"):
		return fmt.Errorf("%w: %s", ErrNotFound, message)
	case strings.Contains(lower, "unauthorized"), strings.Contains(lower, "forbidden"), strings.HasPrefix(strings.ToLower(err.Error()), "403"):
		return fmt.Errorf("%w: %s", ErrUnauthorized, message)
	default:
		return fmt.Errorf("%s", message)
	}
}
