// SPDX-FileCopyrightText: Copyright (c) 2026 NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"flag"
	"testing"
)

func TestManagerOptionsWatchScopeDefaults(t *testing.T) {
	opts := managerOptions{}
	fs := flag.NewFlagSet("test", flag.ContinueOnError)
	opts.BindFlags(fs)
	if err := fs.Parse(nil); err != nil {
		t.Fatalf("parse flags: %v", err)
	}

	if opts.watchNamespace != "" {
		t.Fatalf("watchNamespace = %q, want empty", opts.watchNamespace)
	}
	if opts.watchFilterValue != "" {
		t.Fatalf("watchFilterValue = %q, want empty", opts.watchFilterValue)
	}
	if got := managerCacheOptions(opts.watchNamespace).DefaultNamespaces; got != nil {
		t.Fatalf("DefaultNamespaces = %#v, want nil", got)
	}
}

func TestManagerOptionsWatchScope(t *testing.T) {
	opts := managerOptions{}
	fs := flag.NewFlagSet("test", flag.ContinueOnError)
	opts.BindFlags(fs)
	if err := fs.Parse([]string{"--namespace", "team-a", "--watch-filter", "capnico-a"}); err != nil {
		t.Fatalf("parse flags: %v", err)
	}

	if opts.watchNamespace != "team-a" {
		t.Fatalf("watchNamespace = %q, want team-a", opts.watchNamespace)
	}
	if opts.watchFilterValue != "capnico-a" {
		t.Fatalf("watchFilterValue = %q, want capnico-a", opts.watchFilterValue)
	}
	defaultNamespaces := managerCacheOptions(opts.watchNamespace).DefaultNamespaces
	if len(defaultNamespaces) != 1 {
		t.Fatalf("DefaultNamespaces = %#v, want one namespace", defaultNamespaces)
	}
	if _, ok := defaultNamespaces["team-a"]; !ok {
		t.Fatalf("DefaultNamespaces = %#v, want team-a", defaultNamespaces)
	}
}
