// SPDX-FileCopyrightText: Copyright (c) 2026 NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

//go:build e2e
// +build e2e

package e2e

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/dsx-ai-factory/cluster-api-provider-nico/test/utils"
)

const (
	fakeDockerImage              = "fake-nico-api-server"
	bootstrapManifestPath        = "test/e2e/testdata/cluster-bootstrap.yaml"
	managerKustomizationPath     = "test/e2e/config/manager"
	kubernetesVersionPlaceholder = "${KUBERNETES_VERSION}"
)

const dockerSocketPatch = `
spec:
  template:
    spec:
      securityContext:
        runAsUser: 0
        runAsNonRoot: false
      containers:
      - name: api
        securityContext:
          readOnlyRootFilesystem: false
          runAsNonRoot: false
        env:
        - name: DOCKER_HOST
          value: unix:///var/run/docker.sock
        volumeMounts:
        - name: docker-sock
          mountPath: /var/run/docker.sock
      volumes:
      - name: docker-sock
        hostPath:
          path: /var/run/docker.sock
          type: Socket
`

func kindContext() string {
	cluster := "kind"
	if v, ok := os.LookupEnv("KIND_CLUSTER"); ok {
		cluster = v
	}
	return "kind-" + cluster
}

func renderBootstrapManifest() ([]byte, error) {
	version := os.Getenv("KUBERNETES_VERSION")
	if version == "" {
		return nil, fmt.Errorf("KUBERNETES_VERSION is required")
	}

	manifest, err := os.ReadFile(bootstrapManifestPath)
	if err != nil {
		return nil, fmt.Errorf("read bootstrap manifest: %w", err)
	}
	if !bytes.Contains(manifest, []byte(kubernetesVersionPlaceholder)) {
		return nil, fmt.Errorf("bootstrap manifest does not contain %s", kubernetesVersionPlaceholder)
	}

	return bytes.ReplaceAll(manifest, []byte(kubernetesVersionPlaceholder), []byte(version)), nil
}

var _ = Describe("Bootstrap", Ordered, func() {
	var bootstrapManifest []byte

	BeforeAll(func() {
		var err error
		bootstrapManifest, err = renderBootstrapManifest()
		Expect(err).NotTo(HaveOccurred())

		By("building the fake NICo image")
		cmd := exec.Command("docker", "build", "-f", "Dockerfile.fake", "-t", fakeDockerImage, ".")
		_, err = utils.Run(cmd)
		Expect(err).NotTo(HaveOccurred(), "Failed to build the fake NICo image")

		By("loading the fake NICo image on Kind")
		Expect(utils.LoadImageToKindClusterWithName(fakeDockerImage)).To(Succeed())

		// clusterctl init does not wait for its webhook deployments to become available.
		By("waiting for the Cluster API webhooks to be ready")
		for _, deployment := range []struct{ namespace, name string }{
			{"capi-system", "capi-controller-manager"},
			{"capi-kubeadm-bootstrap-system", "capi-kubeadm-bootstrap-controller-manager"},
			{"capi-kubeadm-control-plane-system", "capi-kubeadm-control-plane-controller-manager"},
		} {
			cmd = exec.Command("kubectl", "--context", kindContext(), "wait", "deployment/"+deployment.name,
				"-n", deployment.namespace, "--for", "condition=Available", "--timeout", "2m")
			_, err = utils.Run(cmd)
			Expect(err).NotTo(HaveOccurred(), fmt.Sprintf("%s did not become available", deployment.name))
		}

		By("installing CRDs")
		cmd = exec.Command("make", "install")
		_, err = utils.Run(cmd)
		Expect(err).NotTo(HaveOccurred(), "Failed to install CRDs")

		By("deploying the controller-manager")
		cmd = exec.Command("make", "deploy", "DEPLOY_CONFIG="+managerKustomizationPath)
		_, err = utils.Run(cmd)
		Expect(err).NotTo(HaveOccurred(), "Failed to deploy the controller-manager")

		By("deploying the fake NICo API")
		cmd = exec.Command("kubectl", "--context", kindContext(), "apply", "-f", "hack/tilt/fake-nico-api.yaml")
		_, err = utils.Run(cmd)
		Expect(err).NotTo(HaveOccurred(), "Failed to deploy the fake NICo API")

		By("switching the fake NICo API to the docker backend")
		nodeImage := os.Getenv("KIND_NODE_IMAGE")
		Expect(nodeImage).NotTo(BeEmpty(), "KIND_NODE_IMAGE is required")
		dockerBackendPatch, err := json.Marshal([]map[string]string{
			{"op": "add", "path": "/spec/template/spec/containers/0/args/-", "value": "--backend=docker"},
			{"op": "add", "path": "/spec/template/spec/containers/0/args/-", "value": "--docker-image=" + nodeImage},
		})
		Expect(err).NotTo(HaveOccurred())
		cmd = exec.Command("kubectl", "--context", kindContext(), "patch", "deployment", "fake-nico-api",
			"-n", "fake-nico-api", "--type=json",
			"-p", string(dockerBackendPatch))
		_, err = utils.Run(cmd)
		Expect(err).NotTo(HaveOccurred(), "Failed to switch the fake NICo API to the docker backend")

		By("giving the fake NICo API access to the host's Docker socket")
		cmd = exec.Command("kubectl", "--context", kindContext(), "patch", "deployment", "fake-nico-api",
			"-n", "fake-nico-api", "--type=strategic", "-p", dockerSocketPatch)
		_, err = utils.Run(cmd)
		Expect(err).NotTo(HaveOccurred(), "Failed to mount the Docker socket into the fake NICo API")

		By("waiting for the fake NICo API to be available")
		cmd = exec.Command("kubectl", "--context", kindContext(), "wait", "deployment/fake-nico-api",
			"-n", "fake-nico-api", "--for", "condition=Available", "--timeout", "2m")
		_, err = utils.Run(cmd)
		Expect(err).NotTo(HaveOccurred(), "fake NICo API did not become available")

		By("applying the bootstrap cluster")
		cmd = exec.Command("kubectl", "--context", kindContext(), "apply", "-f", "-")
		cmd.Stdin = bytes.NewReader(bootstrapManifest)
		_, err = utils.Run(cmd)
		Expect(err).NotTo(HaveOccurred(), "Failed to apply the bootstrap cluster")

		By("creating the kindnet ConfigMap")
		cmd = exec.Command("kubectl", "--context", kindContext(), "create", "configmap", "cni-kindnet",
			"-n", "capnico-e2e", "--from-file=kindnet.yaml=test/e2e/testdata/kindnet.yaml")
		_, err = utils.Run(cmd)
		Expect(err).NotTo(HaveOccurred(), "Failed to create the kindnet ConfigMap")
	})

	AfterEach(func() {
		if !CurrentSpecReport().Failed() {
			return
		}

		By("fetching fake NICo API logs")
		cmd := exec.Command("kubectl", "--context", kindContext(), "logs", "deployment/fake-nico-api",
			"-n", "fake-nico-api")
		logs, err := utils.Run(cmd)
		if err != nil {
			_, _ = fmt.Fprintf(GinkgoWriter, "Failed to get fake NICo API logs: %s\n", err)
			return
		}
		_, _ = fmt.Fprintf(GinkgoWriter, "Fake NICo API logs:\n%s\n", logs)
	})

	AfterAll(func() {
		// Delete the Cluster first because NicoMachine cleanup still needs the credentials Secret.
		By("deleting the workload cluster")
		cmd := exec.Command("kubectl", "--context", kindContext(), "delete", "cluster", "capnico-e2e",
			"-n", "capnico-e2e", "--ignore-not-found", "--wait=true", "--timeout=3m")
		_, _ = utils.Run(cmd)

		By("deleting the rest of the bootstrap manifest")
		cmd = exec.Command("kubectl", "--context", kindContext(), "delete", "-f", "-",
			"--ignore-not-found", "--wait=true", "--timeout=1m")
		cmd.Stdin = bytes.NewReader(bootstrapManifest)
		_, _ = utils.Run(cmd)

		By("deleting the fake NICo API")
		cmd = exec.Command("kubectl", "--context", kindContext(),
			"delete", "-f", "hack/tilt/fake-nico-api.yaml", "--ignore-not-found")
		_, _ = utils.Run(cmd)

		By("undeploying the controller-manager")
		cmd = exec.Command("make", "undeploy", "DEPLOY_CONFIG="+managerKustomizationPath,
			"ignore-not-found=true")
		_, _ = utils.Run(cmd)

		By("uninstalling CRDs")
		cmd = exec.Command("make", "uninstall", "ignore-not-found=true")
		_, _ = utils.Run(cmd)
	})

	It("bootstraps a real workload cluster via KubeadmControlPlane", func() {
		By("waiting for the KubeadmControlPlane to report Initialized")
		verifyControlPlaneInitialized := func(g Gomega) {
			cmd := exec.Command("kubectl", "--context", kindContext(), "get", "kubeadmcontrolplane", "capnico-e2e-control-plane",
				"-n", "capnico-e2e", "-o", "jsonpath={.status.conditions[?(@.type=='Initialized')].status}")
			output, err := utils.Run(cmd)
			g.Expect(err).NotTo(HaveOccurred())
			g.Expect(output).To(Equal("True"), "KubeadmControlPlane not Initialized")
		}
		Eventually(verifyControlPlaneInitialized, 10*time.Minute, 5*time.Second).Should(Succeed())

		By("waiting for the Cluster to report Available")
		verifyClusterAvailable := func(g Gomega) {
			cmd := exec.Command("kubectl", "--context", kindContext(), "get", "cluster", "capnico-e2e",
				"-n", "capnico-e2e", "-o", "jsonpath={.status.conditions[?(@.type=='Available')].status}")
			output, err := utils.Run(cmd)
			g.Expect(err).NotTo(HaveOccurred())
			g.Expect(output).To(Equal("True"), "Cluster not Available")
		}
		Eventually(verifyClusterAvailable, 8*time.Minute, 5*time.Second).Should(Succeed())
	})
})
