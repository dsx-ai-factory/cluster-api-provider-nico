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
		cmd = exec.Command("kubectl", "apply", "-f", "test/e2e/testdata/fake-nico-api-docker.yaml")
		_, err = utils.Run(cmd)
		Expect(err).NotTo(HaveOccurred(), "Failed to deploy the fake NICo API")

		By("waiting for the fake NICo API to be available")
		cmd = exec.Command("kubectl", "wait", "deployment/fake-nico-api",
			"-n", "fake-nico-api", "--for", "condition=Available", "--timeout", "2m")
		_, err = utils.Run(cmd)
		Expect(err).NotTo(HaveOccurred(), "fake NICo API did not become available")

		By("applying the bootstrap cluster")
		cmd = exec.Command("kubectl", "apply", "-f", "test/e2e/testdata/cluster-bootstrap.yaml")
		_, err = utils.Run(cmd)
		Expect(err).NotTo(HaveOccurred(), "Failed to apply the bootstrap cluster")
	})

	AfterAll(func() {
		By("deleting the bootstrap cluster")
		cmd := exec.Command("kubectl", "delete", "-f", "test/e2e/testdata/cluster-bootstrap.yaml",
			"--ignore-not-found", "--wait=true", "--timeout=3m")
		_, _ = utils.Run(cmd)

		By("deleting the docker-backed fake NICo API")
		cmd = exec.Command("kubectl", "delete", "-f", "test/e2e/testdata/fake-nico-api-docker.yaml", "--ignore-not-found")
		_, _ = utils.Run(cmd)

		By("undeploying the controller-manager")
		cmd = exec.Command("make", "undeploy", "ignore-not-found=true")
		_, _ = utils.Run(cmd)

		By("uninstalling CRDs")
		cmd = exec.Command("make", "uninstall", "ignore-not-found=true")
		_, _ = utils.Run(cmd)
	})

	It("bootstraps a real workload cluster via KubeadmControlPlane", func() {
		By("waiting for the KubeadmControlPlane to report Ready")
		verifyControlPlaneReady := func(g Gomega) {
			cmd := exec.Command("kubectl", "get", "kubeadmcontrolplane", "capnico-e2e-control-plane",
				"-n", "capnico-e2e", "-o", "jsonpath={.status.conditions[?(@.type=='Ready')].status}")
			output, err := utils.Run(cmd)
			g.Expect(err).NotTo(HaveOccurred())
			g.Expect(output).To(Equal("True"), "KubeadmControlPlane not Ready")
		}
		Eventually(verifyControlPlaneReady, 10*time.Minute, 5*time.Second).Should(Succeed())

		By("waiting for the Cluster to report ControlPlaneReady")
		verifyClusterReady := func(g Gomega) {
			cmd := exec.Command("kubectl", "get", "cluster", "capnico-e2e",
				"-n", "capnico-e2e", "-o", "jsonpath={.status.controlPlaneReady}")
			output, err := utils.Run(cmd)
			g.Expect(err).NotTo(HaveOccurred())
			g.Expect(output).To(Equal("true"))
		}
		Eventually(verifyClusterReady, 2*time.Minute, 5*time.Second).Should(Succeed())

		By("waiting for the worker MachineDeployment to become Ready")
		verifyWorkersReady := func(g Gomega) {
			cmd := exec.Command("kubectl", "get", "machinedeployment", "capnico-e2e-workers",
				"-n", "capnico-e2e", "-o", "jsonpath={.status.readyReplicas}")
			output, err := utils.Run(cmd)
			g.Expect(err).NotTo(HaveOccurred())
			g.Expect(output).To(Equal("1"))
		}
		Eventually(verifyWorkersReady, 10*time.Minute, 5*time.Second).Should(Succeed())
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
