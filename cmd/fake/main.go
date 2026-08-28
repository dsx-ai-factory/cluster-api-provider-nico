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
	"time"

	"github.com/NVIDIA/cluster-api-provider-nico/internal/fake"
)

func main() {
	addr := flag.String("addr", ":8090", "The address the fake endpoint binds to.")
	flag.Parse()

	endpoint := fake.New()

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
