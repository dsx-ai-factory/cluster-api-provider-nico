// SPDX-FileCopyrightText: Copyright (c) 2026 NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package nico

import (
	"strings"
	"testing"
)

func TestInjectHostnameCloudConfigPreservesCloudConfigHeader(t *testing.T) {
	input := "#cloud-config\nhostname: old\n"

	got, err := InjectHostnameCloudConfig(input, "new-host")
	if err != nil {
		t.Fatalf("InjectHostnameCloudConfig() error = %v", err)
	}

	if !strings.HasPrefix(got, "#cloud-config\n") {
		t.Fatalf("expected output to keep #cloud-config header, got: %q", got)
	}
	if !strings.Contains(got, "hostname: new-host") {
		t.Fatalf("expected hostname to be updated, got: %q", got)
	}
}

func TestInjectHostnameCloudConfigPreservesTemplateHeader(t *testing.T) {
	input := "## template: jinja\n#cloud-config\nhostname: old\n"
	expectedHeader := "## template: jinja\n#cloud-config\n"

	got, err := InjectHostnameCloudConfig(input, "new-host")
	if err != nil {
		t.Fatalf("InjectHostnameCloudConfig() error = %v", err)
	}

	if !strings.HasPrefix(got, expectedHeader) {
		t.Fatalf("expected output to keep template header, got: %q", got)
	}
	if !strings.Contains(got, "hostname: new-host") {
		t.Fatalf("expected hostname to be updated, got: %q", got)
	}
}

func TestInjectHostnameCloudConfigPrependsHostnameFields(t *testing.T) {
	input := "#cloud-config\nusers: []\n"

	got, err := InjectHostnameCloudConfig(input, "new-host")
	if err != nil {
		t.Fatalf("InjectHostnameCloudConfig() error = %v", err)
	}

	hostnameIdx := strings.Index(got, "hostname: new-host")
	preserveIdx := strings.Index(got, "preserve_hostname: false")
	manageIdx := strings.Index(got, "manage_etc_hosts: true")
	usersIdx := strings.Index(got, "users: []")

	if hostnameIdx == -1 || preserveIdx == -1 || manageIdx == -1 || usersIdx == -1 {
		t.Fatalf("missing expected fields in output: %q", got)
	}
	if hostnameIdx >= preserveIdx || preserveIdx >= manageIdx || manageIdx >= usersIdx {
		t.Fatalf("expected hostname fields to be first and ordered, got: %q", got)
	}
}
