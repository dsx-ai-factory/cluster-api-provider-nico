// SPDX-FileCopyrightText: Copyright (c) 2026 NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

// Package docker runs fake NICo instances as kindest/node containers.
package docker

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"sigs.k8s.io/yaml"
)

const (
	defaultNetwork = "kind"
	commandTimeout = 90 * time.Second

	controlPlaneLabel = "capnico-fake/control-plane"
)

// Backend runs instances as containers on the kind Docker network.
type Backend struct {
	run func(context.Context, io.Reader, ...string) ([]byte, error)

	// Image is the node image to run.
	Image string
	// Network is the Docker network the container joins. It defaults to the kind network.
	Network string
	// ControlPlaneHostname is the network alias assigned to the control-plane container.
	ControlPlaneHostname string
}

type cloudConfigFile struct {
	Path        string `json:"path"`
	Content     string `json:"content"`
	Permissions string `json:"permissions"`
	Owner       string `json:"owner"`
}

type cloudConfig struct {
	Hostname   string            `json:"hostname"`
	WriteFiles []cloudConfigFile `json:"write_files"`
	RunCmd     []string          `json:"runcmd"`
}

func (b *Backend) network() string {
	if b.Network != "" {
		return b.Network
	}
	return defaultNetwork
}

func (b *Backend) Create(ctx context.Context, instanceID, machineName, userData string, labels map[string]string) error {
	name := containerName(instanceID)

	args := []string{
		"create", "--pull=never", "--name", name, "--hostname", machineName,
		"--privileged", "--cgroupns=private",
		"--security-opt", "seccomp=unconfined", "--security-opt", "apparmor=unconfined",
		"--tmpfs", "/tmp", "--tmpfs", "/run", "--network", b.network(),
		"--volume", "/var", "--volume", "/lib/modules:/lib/modules:ro",
	}
	if labels[controlPlaneLabel] == "true" {
		if b.ControlPlaneHostname == "" {
			return fmt.Errorf("control-plane hostname is required")
		}
		args = append(args, "--network-alias", b.ControlPlaneHostname)
	}
	args = append(args, b.Image)
	output, err := b.docker(ctx, nil, args...)
	if err != nil {
		return fmt.Errorf("create node container: %w", err)
	}
	containerID := strings.TrimSpace(string(output))
	if _, err := b.docker(ctx, nil, "start", containerID); err != nil {
		_, _ = b.docker(context.WithoutCancel(ctx), nil, "rm", "--force", "--volumes", containerID)
		return fmt.Errorf("start node container: %w", err)
	}

	// Run bootstrap asynchronously so instance readiness remains independent of kubeadm.
	go func() {
		if err := b.waitForContainerRuntime(context.WithoutCancel(ctx), containerID); err != nil {
			log.Printf("fake/docker: wait for container runtime in %s: %v", name, err)
			return
		}
		if err := b.applyCloudConfig(context.WithoutCancel(ctx), containerID, userData); err != nil {
			log.Printf("fake/docker: apply cloud-config for %s: %v", name, err)
		}
	}()

	return nil
}

func (b *Backend) waitForContainerRuntime(ctx context.Context, containerID string) error {
	return b.execIn(ctx, containerID, nil, "sh", "-c",
		`i=0; while [ "$i" -lt 60 ]; do crictl info >/dev/null 2>&1 && exit 0; i=$((i + 1)); sleep 1; done; exit 1`)
}

// Ready reports whether the container is running, independently of kubeadm bootstrap.
func (b *Backend) Ready(ctx context.Context, instanceID string) (bool, error) {
	running, err := b.docker(ctx, nil, "container", "inspect", "--format", "{{.State.Running}}", containerName(instanceID))
	if err != nil {
		return false, nil
	}
	return strings.TrimSpace(string(running)) == "true", nil
}

func (b *Backend) Delete(ctx context.Context, instanceID string) error {
	name := containerName(instanceID)
	_, _ = b.docker(ctx, nil, "stop", "--timeout", "10", name)
	_, err := b.docker(ctx, nil, "rm", "--force", "--volumes", name)
	if err != nil && !isMissing(err) {
		return fmt.Errorf("remove node container: %w", err)
	}
	return nil
}

func containerName(instanceID string) string {
	return "capnico-fake-" + instanceID
}

func (b *Backend) applyCloudConfig(ctx context.Context, containerID, userData string) error {
	userData = strings.TrimSpace(userData)
	if userData == "" {
		return nil
	}
	userData = strings.TrimPrefix(userData, "#cloud-config\n")
	userData = strings.TrimPrefix(userData, "#cloud-config\r\n")

	var cfg cloudConfig
	if err := yaml.Unmarshal([]byte(userData), &cfg); err != nil {
		return fmt.Errorf("parse cloud-config userData: %w", err)
	}
	if cfg.Hostname != "" {
		if err := b.execIn(ctx, containerID, nil, "hostname", cfg.Hostname); err != nil {
			return fmt.Errorf("set hostname: %w", err)
		}
	}

	for _, file := range cfg.WriteFiles {
		if strings.TrimSpace(file.Path) == "" {
			continue
		}
		if err := b.execIn(ctx, containerID, nil, "mkdir", "-p", filepath.Dir(file.Path)); err != nil {
			return fmt.Errorf("create directory for %s: %w", file.Path, err)
		}
		if err := b.execIn(ctx, containerID, strings.NewReader(file.Content), "sh", "-c", "cat > "+singleQuote(file.Path)); err != nil {
			return fmt.Errorf("write %s: %w", file.Path, err)
		}
		if perms := strings.TrimSpace(file.Permissions); perms != "" {
			if err := b.execIn(ctx, containerID, nil, "chmod", perms, file.Path); err != nil {
				return fmt.Errorf("chmod %s: %w", file.Path, err)
			}
		}
		if owner := strings.TrimSpace(file.Owner); owner != "" {
			if err := b.execIn(ctx, containerID, nil, "chown", owner, file.Path); err != nil {
				return fmt.Errorf("chown %s: %w", file.Path, err)
			}
		}
	}

	commands := make([]string, 0, len(cfg.RunCmd))
	for _, command := range cfg.RunCmd {
		if strings.TrimSpace(command) == "" {
			continue
		}
		commands = append(commands, command)
	}
	if len(commands) == 0 {
		return nil
	}

	// Keep the container's root cgroup empty while kubelet starts.
	script := "set -e\n" + strings.Join(commands, "\n")
	if err := b.execIn(ctx, containerID, nil, "systemd-run", "--quiet", "--unit=capnico-bootstrap",
		"--property=Type=exec", "--", "sh", "-lc", script); err != nil {
		return fmt.Errorf("start cloud-config commands: %w", err)
	}
	return nil
}

func (b *Backend) execIn(ctx context.Context, containerID string, stdin io.Reader, cmd ...string) error {
	args := []string{"exec"}
	if stdin != nil {
		args = append(args, "-i")
	}
	args = append(args, containerID)
	args = append(args, cmd...)
	_, err := b.docker(ctx, stdin, args...)
	return err
}

func (b *Backend) docker(ctx context.Context, stdin io.Reader, args ...string) ([]byte, error) {
	if b.run != nil {
		return b.run(ctx, stdin, args...)
	}
	return runDocker(ctx, stdin, args...)
}

type commandError struct {
	operation string
	stderr    string
	err       error
}

func (e *commandError) Error() string {
	return fmt.Sprintf("docker %s: %v: %s", e.operation, e.err, e.stderr)
}

func (e *commandError) Unwrap() error { return e.err }

func isMissing(err error) bool {
	var commandErr *commandError
	return errors.As(err, &commandErr) &&
		(strings.Contains(commandErr.stderr, "No such container:") || strings.Contains(commandErr.stderr, "No such object:"))
}

func runDocker(ctx context.Context, stdin io.Reader, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, commandTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "docker", args...)
	cmd.Stdin = stdin
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			err = ctx.Err()
		}
		return nil, &commandError{operation: args[0], stderr: strings.TrimSpace(stderr.String()), err: err}
	}
	return stdout.Bytes(), nil
}

func singleQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
