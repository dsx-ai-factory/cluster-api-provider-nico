// SPDX-FileCopyrightText: Copyright (c) 2026 NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package docker

import (
	"context"
	"errors"
	"io"
	"slices"
	"strings"
	"testing"
)

func TestDeleteRemovesVolumes(t *testing.T) {
	var removeArgs []string
	backend := &Backend{run: func(_ context.Context, _ io.Reader, args ...string) ([]byte, error) {
		if args[0] == "rm" {
			removeArgs = slices.Clone(args)
		}
		return nil, nil
	}}
	if err := backend.Delete(t.Context(), "instance-1"); err != nil {
		t.Fatalf("Delete() error = %v", err)
	}
	if !slices.Contains(removeArgs, "--volumes") || !slices.Contains(removeArgs, "--force") {
		t.Fatalf("remove arguments = %q, want forced removal with anonymous volumes", removeArgs)
	}
}

func TestDeleteTreatsMissingContainerAsDeleted(t *testing.T) {
	for _, message := range []string{"No such container: capnico-fake-instance-1", "No such object: capnico-fake-instance-1"} {
		t.Run(message, func(t *testing.T) {
			backend := &Backend{run: func(_ context.Context, _ io.Reader, args ...string) ([]byte, error) {
				return nil, &commandError{operation: args[0], stderr: message, err: errors.New("exit status 1")}
			}}
			if err := backend.Delete(t.Context(), "instance-1"); err != nil {
				t.Fatalf("Delete() error = %v, want nil for a missing container", err)
			}
		})
	}
}

func TestDeleteReturnsOtherRemoveErrors(t *testing.T) {
	failure := errors.New("removal failed")
	backend := &Backend{run: func(_ context.Context, _ io.Reader, args ...string) ([]byte, error) {
		return nil, failure
	}}
	if err := backend.Delete(t.Context(), "instance-1"); !errors.Is(err, failure) {
		t.Fatalf("Delete() error = %v, want the remove failure", err)
	}
}

func TestCreateRemovesContainerWhenStartFails(t *testing.T) {
	failure := errors.New("start failed")
	var removed bool
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	backend := &Backend{Image: "kindest/node:test", run: func(commandCtx context.Context, _ io.Reader, args ...string) ([]byte, error) {
		switch args[0] {
		case "create":
			return []byte("container-id\n"), nil
		case "start":
			cancel()
			return nil, failure
		case "rm":
			if commandCtx.Err() != nil {
				t.Fatal("cleanup inherited the canceled request context")
			}
			if args[len(args)-1] != "container-id" || !slices.Contains(args, "--volumes") {
				t.Fatalf("cleanup arguments = %q, want created container and volumes removed", args)
			}
			removed = true
		}
		return nil, nil
	}}
	if err := backend.Create(ctx, "instance-1", "machine-1", "", nil); !errors.Is(err, failure) {
		t.Fatalf("Create() error = %v, want the original start failure", err)
	}
	if !removed {
		t.Fatal("created container was not removed after startup failed")
	}
}

func TestReadyChecksContainerState(t *testing.T) {
	for _, running := range []bool{true, false} {
		backend := &Backend{run: func(_ context.Context, _ io.Reader, args ...string) ([]byte, error) {
			if args[0] != "container" || args[1] != "inspect" {
				t.Fatalf("unexpected readiness command: %q", args)
			}
			if running {
				return []byte("true\n"), nil
			}
			return []byte("false\n"), nil
		}}
		ready, err := backend.Ready(t.Context(), "instance-1")
		if err != nil || ready != running {
			t.Fatalf("Ready() = %v, %v, want %v, nil", ready, err, running)
		}
	}
}

func TestExecStreamsInputWithoutChangingArguments(t *testing.T) {
	command := "cat > '/run/bootstrap file'"
	backend := &Backend{run: func(_ context.Context, stdin io.Reader, args ...string) ([]byte, error) {
		want := []string{"exec", "-i", "container-id", "sh", "-c", command}
		if !slices.Equal(args, want) {
			t.Fatalf("exec arguments = %q, want %q", args, want)
		}
		contents, err := io.ReadAll(stdin)
		if err != nil || string(contents) != "bootstrap contents\n" {
			t.Fatalf("exec input = %q, %v", contents, err)
		}
		return nil, nil
	}}
	if err := backend.execIn(t.Context(), "container-id", strings.NewReader("bootstrap contents\n"), "sh", "-c", command); err != nil {
		t.Fatal(err)
	}
}
