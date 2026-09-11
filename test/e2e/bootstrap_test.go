// SPDX-FileCopyrightText: Copyright (c) 2026 NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

//go:build e2e
// +build e2e

package e2e

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/NVIDIA/cluster-api-provider-nico/test/utils"
)

const fakeDockerImage = "fake-nico-api-server"

// kindContext returns the kubeconfig context `kind create cluster` sets for
// KIND_CLUSTER, so kubectl calls here never depend on whatever context
// happens to be ambient.
func kindContext() string {
	cluster := "kind"
	if v, ok := os.LookupEnv("KIND_CLUSTER"); ok {
		cluster = v
	}
	return "kind-" + cluster
}

// Proves CAPNico's controllers work with the kubeadm bootstrap and
// control-plane providers end to end: a real KubeadmControlPlane and
// MachineDeployment/KubeadmConfigTemplate, backed by instances that are real
// containers (see internal/fake/docker), not just NICo API call assertions.
var _ = Describe("Bootstrap", Ordered, func() {
	BeforeAll(func() {
		By("building the fake NICo image")
		cmd := exec.Command("docker", "build", "-f", "Dockerfile.fake", "-t", fakeDockerImage, ".")
		_, err := utils.Run(cmd)
		Expect(err).NotTo(HaveOccurred(), "Failed to build the fake NICo image")

		By("loading the fake NICo image on Kind")
		Expect(utils.LoadImageToKindClusterWithName(fakeDockerImage)).To(Succeed())

		By("installing Cluster API core, kubeadm bootstrap, and kubeadm control-plane providers")
		cmd = exec.Command("make", "capi-init")
		cmd.Env = append(os.Environ(), "CAPNICO_KUBECONFIG="+ambientKubeconfig())
		_, err = utils.Run(cmd)
		Expect(err).NotTo(HaveOccurred(), "Failed to install Cluster API")

		By("installing CRDs")
		cmd = exec.Command("make", "install")
		_, err = utils.Run(cmd)
		Expect(err).NotTo(HaveOccurred(), "Failed to install CRDs")

		By("deploying the controller-manager")
		cmd = exec.Command("make", "deploy", fmt.Sprintf("IMG=%s", managerImage))
		_, err = utils.Run(cmd)
		Expect(err).NotTo(HaveOccurred(), "Failed to deploy the controller-manager")

		By("deploying the docker-backed fake NICo API")
		cmd = exec.Command("kubectl", "--context", kindContext(),
			"apply", "-f", "test/e2e/testdata/fake-nico-api-docker.yaml")
		_, err = utils.Run(cmd)
		Expect(err).NotTo(HaveOccurred(), "Failed to deploy the fake NICo API")

		By("waiting for the fake NICo API to be available")
		cmd = exec.Command("kubectl", "--context", kindContext(), "wait", "deployment/fake-nico-api",
			"-n", "fake-nico-api", "--for", "condition=Available", "--timeout", "2m")
		_, err = utils.Run(cmd)
		Expect(err).NotTo(HaveOccurred(), "fake NICo API did not become available")

		By("applying the bootstrap cluster")
		cmd = exec.Command("kubectl", "--context", kindContext(), "apply", "-f", "test/e2e/testdata/cluster-bootstrap.yaml")
		_, err = utils.Run(cmd)
		Expect(err).NotTo(HaveOccurred(), "Failed to apply the bootstrap cluster")

		// The ClusterResourceSet in cluster-bootstrap.yaml references this
		// ConfigMap by name; it's created here, directly from the vendored
		// file, so that file stays the CNI manifest's single copy.
		By("creating the kindnet ConfigMap")
		cmd = exec.Command("kubectl", "--context", kindContext(), "create", "configmap", "cni-kindnet",
			"-n", "capnico-e2e", "--from-file=kindnet.yaml=test/e2e/testdata/kindnet.yaml")
		_, err = utils.Run(cmd)
		Expect(err).NotTo(HaveOccurred(), "Failed to create the kindnet ConfigMap")
	})

	AfterAll(func() {
		// Deleting the Cluster first, and waiting for it to actually be gone,
		// lets it cascade through Machines/NicoMachines while nico-credentials
		// still exists. NicoMachine's own deletion reconciliation needs that
		// Secret to call the fake API's delete-instance endpoint; a single
		// `kubectl delete -f` of the whole manifest deletes the Secret in the
		// same operation, racing it against machines that still need it and
		// leaving them stuck deleting forever (and their containers orphaned).
		By("deleting the workload cluster")
		cmd := exec.Command("kubectl", "--context", kindContext(), "delete", "cluster", "capnico-e2e",
			"-n", "capnico-e2e", "--ignore-not-found", "--wait=true", "--timeout=3m")
		_, _ = utils.Run(cmd)

		By("deleting the rest of the bootstrap manifest")
		cmd = exec.Command("kubectl", "--context", kindContext(), "delete", "-f", "test/e2e/testdata/cluster-bootstrap.yaml",
			"--ignore-not-found", "--wait=true", "--timeout=1m")
		_, _ = utils.Run(cmd)

		By("deleting the docker-backed fake NICo API")
		cmd = exec.Command("kubectl", "--context", kindContext(),
			"delete", "-f", "test/e2e/testdata/fake-nico-api-docker.yaml", "--ignore-not-found")
		_, _ = utils.Run(cmd)

		By("undeploying the controller-manager")
		cmd = exec.Command("make", "undeploy", "ignore-not-found=true")
		_, _ = utils.Run(cmd)

		By("uninstalling CRDs")
		cmd = exec.Command("make", "uninstall", "ignore-not-found=true")
		_, _ = utils.Run(cmd)
	})

	It("bootstraps a real workload cluster via KubeadmControlPlane", func() {
		// This CAPI version's v1beta2 conditions API has no "Ready" condition
		// type on KubeadmControlPlane, and no Cluster.status.controlPlaneReady
		// field -- both restructured. Initialized is a fast, focused signal
		// that kubeadm init itself succeeded.
		By("waiting for the KubeadmControlPlane to report Initialized")
		verifyControlPlaneInitialized := func(g Gomega) {
			cmd := exec.Command("kubectl", "--context", kindContext(), "get", "kubeadmcontrolplane", "capnico-e2e-control-plane",
				"-n", "capnico-e2e", "-o", "jsonpath={.status.conditions[?(@.type=='Initialized')].status}")
			output, err := utils.Run(cmd)
			g.Expect(err).NotTo(HaveOccurred())
			g.Expect(output).To(Equal("True"), "KubeadmControlPlane not Initialized")
		}
		Eventually(verifyControlPlaneInitialized, 10*time.Minute, 5*time.Second).Should(Succeed())

		// Cluster's Available condition aggregates both control-plane and
		// worker availability (it embeds ControlPlaneAvailable/
		// WorkersAvailable sub-conditions in its message), so this is the one
		// check that the whole thing -- not just kubeadm init -- worked: CNI
		// installed via the ClusterResourceSet, the node went Ready, and the
		// worker joined.
		By("waiting for the Cluster to report Available")
		verifyClusterAvailable := func(g Gomega) {
			cmd := exec.Command("kubectl", "--context", kindContext(), "get", "cluster", "capnico-e2e",
				"-n", "capnico-e2e", "-o", "jsonpath={.status.conditions[?(@.type=='Available')].status}")
			output, err := utils.Run(cmd)
			g.Expect(err).NotTo(HaveOccurred())
			g.Expect(output).To(Equal("True"), "Cluster not Available")
		}
		Eventually(verifyClusterAvailable, 10*time.Minute, 5*time.Second).Should(Succeed())
	})
})

// ambientKubeconfig mirrors the default `make capi-init` would use for a
// bare `kubectl`/`kind` invocation, so Cluster API installs onto the same
// cluster the rest of this suite already targets rather than the Tilt dev
// loop's dedicated kubeconfig.
func ambientKubeconfig() string {
	if kubeconfig := os.Getenv("KUBECONFIG"); kubeconfig != "" {
		return kubeconfig
	}
	home, err := os.UserHomeDir()
	Expect(err).NotTo(HaveOccurred())
	return filepath.Join(home, ".kube", "config")
}
