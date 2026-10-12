// SPDX-FileCopyrightText: Copyright (c) 2026 NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

//go:build e2e

// Package installation qualifies NicoIdentity through the provider's real
// installation paths: the Helm chart and the clusterctl release bundle,
// installed into a Kind management cluster that already runs Cluster API.
// Run `make setup-test-e2e` first, then `make test-e2e-installation`.
package installation

import (
	"fmt"
	"os"
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/dsx-ai-factory/cluster-api-provider-nico/test/utils"
)

func TestInstallation(t *testing.T) {
	RegisterFailHandler(Fail)
	_, _ = fmt.Fprintf(GinkgoWriter, "Starting NicoIdentity installation suite against %s\n", kubeContext())
	RunSpecs(t, "installation suite")
}

var _ = BeforeSuite(func() {
	// clusterctl would otherwise record a version check under the home directory.
	Expect(os.Setenv("CLUSTERCTL_DISABLE_VERSIONCHECK", "true")).To(Succeed())

	By("building and loading the manager image")
	_, err := utils.Run(execCommand("make", "docker-build", "IMG="+managerImage))
	Expect(err).NotTo(HaveOccurred(), "Failed to build the manager image")
	Expect(utils.LoadImageToKindClusterWithName(managerImage)).To(Succeed())

	By("building and loading the fake NICo image")
	_, err = utils.Run(execCommand("docker", "build", "-f", "Dockerfile.fake", "-t", fakeImage, "."))
	Expect(err).NotTo(HaveOccurred(), "Failed to build the fake NICo image")
	Expect(utils.LoadImageToKindClusterWithName(fakeImage)).To(Succeed())

	By("waiting for Cluster API, which the manager's cluster and machine controllers watch")
	for _, deployment := range capiDeployments {
		_, err = kubectl("wait", "deployment/"+deployment.name, "-n", deployment.namespace,
			"--for", "condition=Available", "--timeout", "3m")
		Expect(err).NotTo(HaveOccurred(), "%s did not become available", deployment.name)
	}

	By("deploying the fake NICo API shared by every installation")
	_, err = kubectl("apply", "-f", fakeManifest)
	Expect(err).NotTo(HaveOccurred())
	_, err = kubectl("wait", "deployment/fake-nico-api", "-n", fakeNamespace,
		"--for", "condition=Available", "--timeout", "2m")
	Expect(err).NotTo(HaveOccurred(), "fake NICo API did not become available")

	By("deploying an endpoint that accepts token requests and never answers")
	_, err = kubectl("apply", "-f", hangManifest)
	Expect(err).NotTo(HaveOccurred())
	_, err = kubectl("wait", "deployment/hang", "-n", hangNamespace,
		"--for", "condition=Available", "--timeout", "2m")
	Expect(err).NotTo(HaveOccurred(), "hanging endpoint did not become available")
})

var _ = AfterSuite(func() {
	By("removing the shared fake NICo API and hanging endpoint")
	_, _ = kubectl("delete", "-f", hangManifest, "--ignore-not-found", "--wait=true", "--timeout=1m")
	_, _ = kubectl("delete", "-f", fakeManifest, "--ignore-not-found", "--wait=true", "--timeout=1m")
})
