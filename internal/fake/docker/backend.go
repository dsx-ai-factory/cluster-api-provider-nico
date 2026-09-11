// SPDX-FileCopyrightText: Copyright (c) 2026 NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

// Package docker implements a fake.Backend that runs each instance as a real
// kindest/node container and applies the create request's userData to it, so
// a bootstrap provider's rendered cloud-init actually executes somewhere.
package docker

import (
	"context"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"

	"sigs.k8s.io/yaml"
)

const (
	defaultImage   = "kindest/node:v1.31.0"
	defaultNetwork = "kind"
)

// Backend runs instances as containers on a Docker network shared with a kind
// management cluster's nodes.
type Backend struct {
	// Image is the node image to run. Defaults to defaultImage.
	Image string
	// Network is the Docker network the container joins. Defaults to
	// defaultNetwork, the network `kind create cluster` creates.
	Network string
}

type cloudConfigFile struct {
	Path        string `json:"path"`
	Content     string `json:"content"`
	Permissions string `json:"permissions"`
	Owner       string `json:"owner"`
}

type cloudConfig struct {
	WriteFiles []cloudConfigFile `json:"write_files"`
	RunCmd     []string          `json:"runcmd"`
}

func (b *Backend) image() string {
	if b.Image != "" {
		return b.Image
	}
	return defaultImage
}

func (b *Backend) network() string {
	if b.Network != "" {
		return b.Network
	}
	return defaultNetwork
}

func (b *Backend) Create(ctx context.Context, instanceID, userData string) error {
	name := containerName(instanceID)
	args := []string{
		"run", "-d",
		"--name", name,
		"--hostname", name,
		"--privileged",
		"--security-opt", "seccomp=unconfined",
		"--security-opt", "apparmor=unconfined",
		"--tmpfs", "/tmp",
		"--tmpfs", "/run",
		"--network", b.network(),
		"--volume", "/lib/modules:/lib/modules:ro",
		b.image(),
	}
	if _, err := runDocker(ctx, args...); err != nil {
		return fmt.Errorf("create node container: %w", err)
	}

	if err := applyCloudConfig(ctx, name, userData); err != nil {
		_, _ = runDocker(context.WithoutCancel(ctx), "rm", "--force", name)
		return err
	}
	return nil
}

// Ready reports whether the container is up, matching NICo: an instance is
// READY once the machine exists, independent of whether kubeadm has joined it
// to a cluster yet. KubeadmControlPlane observes bootstrap success separately.
func (b *Backend) Ready(ctx context.Context, instanceID string) (bool, error) {
	name := containerName(instanceID)
	running, err := runDocker(ctx, "inspect", "-f", "{{.State.Running}}", name)
	if err != nil {
		return false, nil
	}
	return running == "true", nil
}

func (b *Backend) Delete(ctx context.Context, instanceID string) error {
	name := containerName(instanceID)
	_, _ = runDocker(ctx, "stop", "--time", "10", name)
	if _, err := runDocker(ctx, "rm", "--force", name); err != nil {
		return fmt.Errorf("remove node container: %w", err)
	}
	return nil
}

func containerName(instanceID string) string {
	return "capnico-fake-" + instanceID
}

// applyCloudConfig supports the write_files/runcmd subset of cloud-config,
// same as CWE's nico-mock.
func applyCloudConfig(ctx context.Context, containerName, userData string) error {
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

	for _, file := range cfg.WriteFiles {
		if strings.TrimSpace(file.Path) == "" {
			continue
		}
		if err := execIn(ctx, containerName, "mkdir", "-p", filepath.Dir(file.Path)); err != nil {
			return fmt.Errorf("create directory for %s: %w", file.Path, err)
		}
		if err := writeFile(ctx, containerName, file.Path, file.Content); err != nil {
			return fmt.Errorf("write %s: %w", file.Path, err)
		}
		if perms := strings.TrimSpace(file.Permissions); perms != "" {
			if err := execIn(ctx, containerName, "chmod", perms, file.Path); err != nil {
				return fmt.Errorf("chmod %s: %w", file.Path, err)
			}
		}
		if owner := strings.TrimSpace(file.Owner); owner != "" {
			if err := execIn(ctx, containerName, "chown", owner, file.Path); err != nil {
				return fmt.Errorf("chown %s: %w", file.Path, err)
			}
		}
	}

	for _, command := range cfg.RunCmd {
		if strings.TrimSpace(command) == "" {
			continue
		}
		if err := execIn(ctx, containerName, "sh", "-lc", command); err != nil {
			return fmt.Errorf("run cloud-config command %q: %w", command, err)
		}
	}
	return nil
}

// writeFile uses `docker exec` + stdin rather than `docker cp`, which cannot
// write into the tmpfs mounts (/run, /tmp) CABPK writes files under.
func writeFile(ctx context.Context, containerName, targetPath, content string) error {
	cmd := exec.CommandContext(ctx, "docker", "exec", "-i", containerName, "sh", "-c", "cat > "+singleQuote(targetPath))
	cmd.Stdin = strings.NewReader(content)
	output, err := cmd.CombinedOutput()
	if err != nil {
		trimmed := strings.TrimSpace(string(output))
		if trimmed == "" {
			trimmed = err.Error()
		}
		return fmt.Errorf("write file into container: %s", trimmed)
	}
	return nil
}

func singleQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

func execIn(ctx context.Context, containerName string, args ...string) error {
	full := append([]string{"exec", containerName}, args...)
	_, err := runDocker(ctx, full...)
	return err
}

func runDocker(ctx context.Context, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "docker", args...)
	output, err := cmd.CombinedOutput()
	trimmed := strings.TrimSpace(string(output))
	if err != nil {
		if trimmed == "" {
			trimmed = err.Error()
		}
		return "", fmt.Errorf("docker %s: %s", strings.Join(args, " "), trimmed)
	}
	return trimmed, nil
}
