// SPDX-FileCopyrightText: Copyright (c) 2026 NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

// Package docker implements a fake.Backend that runs each instance as a real
// kindest/node container and applies the create request's userData to it, so
// a bootstrap provider's rendered cloud-init actually executes somewhere.
package docker

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log"
	"net/netip"
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

	// controlPlaneLabel marks the create request for the single control-plane
	// instance, set via NicoMachineTemplate.spec.template.spec.labels in the
	// test manifest -- not a real CAPI or NICo convention.
	controlPlaneLabel = "capnico-fake/control-plane"

	// controlPlaneIP is a fixed address on the kind network. With only one
	// control-plane replica there's no floating-IP/failover scenario to
	// test, so the control-plane container gets this address directly
	// instead of standing up kube-vip for it.
	controlPlaneIP = "172.18.255.250"
)

// Backend runs instances as containers on a Docker network shared with a kind
// management cluster's nodes.
type Backend struct {
	Client *client.Client

	// Image is the node image to run.
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
			Env: []string{
				// containerd's default overlayfs snapshotter fails to mount
				// when it's already running on top of another overlayfs (the
				// host's own storage driver, e.g. Docker Desktop). kindest/node
				// ships fuse-overlayfs for exactly this nested case.
				"KIND_EXPERIMENTAL_CONTAINERD_SNAPSHOTTER=fuse-overlayfs",
			},
		},
		HostConfig: &container.HostConfig{
			Privileged:  true,
			SecurityOpt: []string{"seccomp=unconfined", "apparmor=unconfined"},
			Tmpfs:       map[string]string{"/tmp": "", "/run": ""},
			NetworkMode: container.NetworkMode(b.network()),
			Binds:       []string{"/lib/modules:/lib/modules:ro"},
		},
	}

	if labels[controlPlaneLabel] == "true" {
		options.NetworkingConfig = &network.NetworkingConfig{
			EndpointsConfig: map[string]*network.EndpointSettings{
				b.network(): {
					IPAMConfig: &network.EndpointIPAMConfig{
						IPv4Address: netip.MustParseAddr(controlPlaneIP),
					},
				},
			},
		}
	}

	created, err := b.Client.ContainerCreate(ctx, options)
	if err != nil {
		return fmt.Errorf("create node container: %w", err)
	}
	if _, err := b.Client.ContainerStart(ctx, created.ID, client.ContainerStartOptions{}); err != nil {
		return fmt.Errorf("start node container: %w", err)
	}

	// Real hardware's create-instance call returns once the machine is
	// powered on; cloud-init (including any kubeadm command in it) runs
	// afterward, on its own timeline, and can fail without un-creating the
	// instance. Applying it synchronously here would make Create() block on -
	// and fail on - kubeadm succeeding, defeating Ready() reporting readiness
	// independent of bootstrap state.
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

// Ready reports whether the container is up, matching NICo: an instance is
// READY once the machine exists, independent of whether kubeadm has joined it
// to a cluster yet. KubeadmControlPlane observes bootstrap success separately.
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

// applyCloudConfig supports the hostname/write_files/runcmd subset of cloud-config,
// same as CWE's nico-mock.
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

	for _, command := range cfg.RunCmd {
		if strings.TrimSpace(command) == "" {
			continue
		}
		if err := b.execIn(ctx, containerID, nil, "sh", "-lc", command); err != nil {
			return fmt.Errorf("run cloud-config command %q: %w", command, err)
		}
	}
	return nil
}

// execIn runs a command in containerID, optionally feeding it stdin. It fails
// on a nonzero exit code, same as shelling out to `docker exec` would.
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
