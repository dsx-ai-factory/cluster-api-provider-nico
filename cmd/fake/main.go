// SPDX-FileCopyrightText: Copyright (c) 2026 NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

// Command fake runs the self-contained fake NICo endpoint. The provider uses
// the production client against this server; only the endpoint differs.
package main

import (
	"errors"
	"flag"
	"log"
	"net/http"
	"net/netip"
	"os"
	"time"

	"github.com/moby/moby/client"

	"github.com/dsx-ai-factory/cluster-api-provider-nico/internal/fake"
	fakedocker "github.com/dsx-ai-factory/cluster-api-provider-nico/internal/fake/docker"
)

func main() {
	addr := flag.String("addr", ":8090", "The address the fake endpoint binds to.")
	seed := flag.String("seed", "", "Path to a YAML file of resources to seed into the endpoint.")
	backend := flag.String("backend", "", `Instance backend: "" (in-memory, default) or "docker".`)
	dockerImage := flag.String("docker-image", "", "Node image used by the Docker backend.")
	dockerControlPlaneIP := flag.String("docker-control-plane-ip", "", "Control-plane IP used by the Docker backend.")
	flag.Parse()

	endpoint := fake.New()

	switch *backend {
	case "":
	case "docker":
		if *dockerImage == "" {
			log.Fatal("--docker-image is required with --backend=docker")
		}
		controlPlaneIP, err := netip.ParseAddr(*dockerControlPlaneIP)
		if err != nil || !controlPlaneIP.Is4() {
			log.Fatal("--docker-control-plane-ip must be a valid IPv4 address with --backend=docker")
		}
		dockerClient, err := client.New(client.FromEnv)
		if err != nil {
			log.Fatalf("create docker client: %v", err)
		}
		endpoint.SetBackend(&fakedocker.Backend{
			Client:         dockerClient,
			Image:          *dockerImage,
			ControlPlaneIP: controlPlaneIP,
		})
		log.Print("using the docker backend: instances run as real containers")
	default:
		log.Fatalf("unknown backend %q", *backend)
	}

	if *seed != "" {
		contents, err := os.ReadFile(*seed)
		if err != nil {
			log.Fatalf("read seed file: %v", err)
		}
		if err := endpoint.SeedFromYAML(string(contents)); err != nil {
			log.Fatalf("seed from %s: %v", *seed, err)
		}
		log.Printf("seeded resources from %s", *seed)
	}

	clientID, clientSecret := os.Getenv("FAKE_CLIENT_ID"), os.Getenv("FAKE_CLIENT_SECRET")
	switch {
	case (clientID == "") != (clientSecret == ""):
		log.Fatal("set both FAKE_CLIENT_ID and FAKE_CLIENT_SECRET, or neither")
	case clientID != "":
		endpoint.SeedClient(clientID, clientSecret)
		log.Printf("requiring an access token minted for client %q", clientID)
	}

	server := &http.Server{
		Addr:              *addr,
		Handler:           fake.LogRequests(endpoint.Handler()),
		ReadHeaderTimeout: 10 * time.Second,
	}

	log.Printf("fake NICo endpoint listening on %s", *addr)
	if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatalf("fake endpoint failed: %v", err)
	}
}
