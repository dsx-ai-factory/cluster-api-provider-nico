// SPDX-FileCopyrightText: Copyright (c) 2026 NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

// Package docker runs fake NICo instances as kindest/node containers.
package docker

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log"
	"path/filepath"
	"strings"

	"github.com/moby/moby/api/pkg/stdcopy"
	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/network"
	"github.com/moby/moby/client"
	"sigs.k8s.io/yaml"
)

const (
	defaultNetwork = "kind"

	controlPlaneLabel = "capnico-fake/control-plane"
)

// Backend runs instances as containers on the kind Docker network.
type Backend struct {
	Client *client.Client

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

func (b *Backend) Create(ctx context.Context, instanceID, userData string, labels map[string]string) error {
	name := containerName(instanceID)

	options := client.ContainerCreateOptions{
		Name: name,
		Config: &container.Config{
			Hostname: name,
			Image:    b.Image,
			Volumes:  map[string]struct{}{"/var": {}},
		},
		HostConfig: &container.HostConfig{
			Privileged:   true,
			CgroupnsMode: container.CgroupnsModePrivate,
			SecurityOpt:  []string{"seccomp=unconfined", "apparmor=unconfined"},
			Tmpfs:        map[string]string{"/tmp": "", "/run": ""},
			NetworkMode:  container.NetworkMode(b.network()),
			Binds:        []string{"/lib/modules:/lib/modules:ro"},
		},
	}

	if labels[controlPlaneLabel] == "true" {
		if b.ControlPlaneHostname == "" {
			return fmt.Errorf("control-plane hostname is required")
		}
		options.NetworkingConfig = &network.NetworkingConfig{
			EndpointsConfig: map[string]*network.EndpointSettings{
				b.network(): {
					Aliases: []string{b.ControlPlaneHostname},
				},
			},
		}
	}

	created, err := b.Client.ContainerCreate(ctx, options)
	if err != nil {
		return fmt.Errorf("create node container: %w", err)
	}
	if _, err := b.Client.ContainerStart(ctx, created.ID, client.ContainerStartOptions{}); err != nil {
		_, _ = b.Client.ContainerRemove(ctx, created.ID, client.ContainerRemoveOptions{Force: true})
		return fmt.Errorf("start node container: %w", err)
	}

	// Run bootstrap asynchronously so instance readiness remains independent of kubeadm.
	go func() {
		if err := b.waitForContainerRuntime(context.WithoutCancel(ctx), created.ID); err != nil {
			log.Printf("fake/docker: wait for container runtime in %s: %v", name, err)
			return
		}
		if err := b.applyCloudConfig(context.WithoutCancel(ctx), created.ID, userData); err != nil {
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
	result, err := b.Client.ContainerInspect(ctx, containerName(instanceID), client.ContainerInspectOptions{})
	if err != nil {
		return false, nil
	}
	return result.Container.State != nil && result.Container.State.Running, nil
}

func (b *Backend) Delete(ctx context.Context, instanceID string) error {
	name := containerName(instanceID)
	timeout := 10
	_, _ = b.Client.ContainerStop(ctx, name, client.ContainerStopOptions{Timeout: &timeout})
	if _, err := b.Client.ContainerRemove(ctx, name, client.ContainerRemoveOptions{Force: true}); err != nil {
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
	created, err := b.Client.ExecCreate(ctx, containerID, client.ExecCreateOptions{
		Cmd:          cmd,
		AttachStdin:  stdin != nil,
		AttachStdout: true,
		AttachStderr: true,
	})
	if err != nil {
		return fmt.Errorf("create exec: %w", err)
	}

	attached, err := b.Client.ExecAttach(ctx, created.ID, client.ExecAttachOptions{})
	if err != nil {
		return fmt.Errorf("attach exec: %w", err)
	}
	defer attached.Close()

	if stdin != nil {
		go func() {
			_, _ = io.Copy(attached.Conn, stdin)
			_ = attached.CloseWrite()
		}()
	}

	var stdout, stderr bytes.Buffer
	if _, err := stdcopy.StdCopy(&stdout, &stderr, attached.Reader); err != nil {
		return fmt.Errorf("read exec output: %w", err)
	}

	inspected, err := b.Client.ExecInspect(ctx, created.ID, client.ExecInspectOptions{})
	if err != nil {
		return fmt.Errorf("inspect exec: %w", err)
	}
	if inspected.ExitCode != 0 {
		output := strings.TrimSpace(stdout.String() + stderr.String())
		return fmt.Errorf("exec %q exited %d: %s", cmd, inspected.ExitCode, output)
	}
	return nil
}

func singleQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
