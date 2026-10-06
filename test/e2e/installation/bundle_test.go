// SPDX-FileCopyrightText: Copyright (c) 2026 NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

//go:build e2e

package installation

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"k8s.io/apimachinery/pkg/util/yaml"

	"github.com/dsx-ai-factory/cluster-api-provider-nico/test/utils"
)

const (
	// bundleRegistry holds the images the clusterctl installations pull. Kind
	// already has them loaded, so nothing is pulled from a registry.
	bundleRegistry = "example.local/capnico"
	// bundleVersion labels the locally rendered bundle. It sits in
	// metadata.yaml's 0.1 series, which clusterctl requires.
	bundleVersion = "v0.1.99"
)

// renderBundle renders the release artifacts for version, naming image, into
// a clusterctl local repository rooted at repository.
func renderBundle(repository, version, image string) string {
	dir := filepath.Join(repository, "infrastructure-nico", version)
	_, err := utils.Run(execCommand("make", "release-manifests", "RELEASE_DIR="+dir, "CONTROLLER_IMG="+image))
	Expect(err).NotTo(HaveOccurred(), "Failed to render the release bundle")
	return filepath.Join(dir, "infrastructure-components.yaml")
}

// clusterctlConfig writes a clusterctl configuration that installs the nico
// provider from the local repository. A non-empty imageRepository overrides
// where clusterctl points the provider's images, as an operator mirror would.
func clusterctlConfig(repository, version, imageRepository string) string {
	var config strings.Builder
	fmt.Fprintf(&config, "providers:\n  - name: nico\n    type: InfrastructureProvider\n    url: file://%s\n",
		filepath.Join(repository, "infrastructure-nico", version, "infrastructure-components.yaml"))
	if imageRepository != "" {
		fmt.Fprintf(&config, "images:\n  infrastructure-nico:\n    repository: %s\n", imageRepository)
	}
	path := filepath.Join(repository, "clusterctl.yaml")
	Expect(os.WriteFile(path, []byte(config.String()), 0o600)).To(Succeed())
	return path
}

func clusterctl(config string, args ...string) (string, error) {
	return utils.Run(execCommand(clusterctlBinary(),
		append(args, "--config", config, "--kubeconfig-context", kubeContext())...))
}

func tagAndLoad(source, target string) {
	_, err := utils.Run(execCommand("docker", "tag", source, target))
	Expect(err).NotTo(HaveOccurred())
	Expect(utils.LoadImageToKindClusterWithName(target)).To(Succeed())
}

func managerImageInUse() string {
	out, err := kubectl("get", "deployment", "-n", managerNamespace, "-l", managerSelector, "-o",
		"jsonpath={.items[0].spec.template.spec.containers[0].image}")
	Expect(err).NotTo(HaveOccurred())
	return out
}

// removeBundleInstallation deletes the provider with clusterctl, then what
// clusterctl leaves behind. clusterctl deletes a cluster-scoped object only
// when its name starts with the provider namespace, and the bundle's
// ClusterRoles and bindings do not, so they are removed by provider label.
func removeBundleInstallation(config string) {
	if config != "" {
		_, _ = clusterctl(config, "delete", "--infrastructure", "nico", "--include-crd", "--include-namespace")
	}
	_, _ = kubectl("delete", "clusterrole,clusterrolebinding", "-l", "cluster.x-k8s.io/provider=infrastructure-nico",
		"--ignore-not-found")
	deleteNicoCRDsAndNamespace()
}

var _ = Describe("Release bundle installation", Ordered, Label("bundle"), func() {
	var config string

	BeforeAll(func() {
		repository := GinkgoT().TempDir()
		image := bundleRegistry + "/controller:" + bundleVersion
		tagAndLoad(managerImage, image)
		renderBundle(repository, bundleVersion, image)
		config = clusterctlConfig(repository, bundleVersion, "")

		By("installing the provider with clusterctl from the rendered bundle")
		_, err := clusterctl(config, "init", "--infrastructure", "nico:"+bundleVersion, "--wait-providers")
		Expect(err).NotTo(HaveOccurred())
		waitForManager()
		Expect(managerImageInUse()).To(Equal(image))
		Expect(apply(healthySecret(managerNamespace))).To(Succeed())
	})

	AfterEach(writeDiagnostics)

	AfterAll(func() {
		_, _ = kubectl("delete", "nicoidentities", "--all", "-n", managerNamespace, "--ignore-not-found")
		removeBundleInstallation(config)
	})

	It("reports current-generation Ready with no manager arguments added", func() {
		noTenantResources()
		created := time.Now()
		Expect(apply(identity(managerNamespace, defaultIdentity))).To(Succeed())
		waitForHealthy(managerNamespace, defaultIdentity, created)
	})

	It("installs the same reader and controller permissions as the chart", func() {
		expectReaderAccess(managerNamespace, defaultIdentity, defaultSecret)
	})
})

var _ = Describe("Installation artifacts", Label("parity"), func() {
	It("give the chart and the release bundle the same CRD schemas and roles", func() {
		bundlePath := renderBundle(GinkgoT().TempDir(), bundleVersion, managerImage)
		content, err := os.ReadFile(bundlePath)
		Expect(err).NotTo(HaveOccurred())
		bundle := decodeManifests(string(content))
		rendered, err := utils.Run(execCommand("helm", "template", releaseName, "./chart",
			"--namespace", managerNamespace))
		Expect(err).NotTo(HaveOccurred())
		chart := decodeManifests(rendered)

		for _, crd := range nicoCRDs {
			Expect(chart.find("CustomResourceDefinition", crd)["spec"]).
				To(Equal(bundle.find("CustomResourceDefinition", crd)["spec"]), crd)
		}
		for _, role := range []string{
			"manager-role", "nicoidentity-admin-role", "nicoidentity-editor-role", "nicoidentity-viewer-role",
		} {
			Expect(sortedRules(chart.find("ClusterRole", role))).
				To(Equal(sortedRules(bundle.find("ClusterRole", role))), role)
		}
	})

	It("regenerate the chart and bundle sources without changes on a second pass", func() {
		before := workingTreeState()
		_, err := utils.Run(execCommand("make", "manifests"))
		Expect(err).NotTo(HaveOccurred())
		Expect(workingTreeState()).To(Equal(before), "a second generation pass changed the working tree")
	})
})

type manifests []map[string]any

func decodeManifests(content string) manifests {
	decoder := yaml.NewYAMLOrJSONDecoder(strings.NewReader(content), 4096)
	var result manifests
	for {
		var object map[string]any
		err := decoder.Decode(&object)
		if errors.Is(err, io.EOF) {
			return result
		}
		Expect(err).NotTo(HaveOccurred())
		if object != nil {
			result = append(result, object)
		}
	}
}

func (m manifests) find(kind, name string) map[string]any {
	for _, object := range m {
		metadata, _ := object["metadata"].(map[string]any)
		if object["kind"] == kind && metadata["name"] == name {
			return object
		}
	}
	Fail(fmt.Sprintf("%s %s not found", kind, name))
	return nil
}

// sortedRules returns a role's rules in a stable order for comparison.
func sortedRules(role map[string]any) []string {
	rules, _ := role["rules"].([]any)
	result := make([]string, 0, len(rules))
	for _, rule := range rules {
		encoded, err := json.Marshal(rule)
		Expect(err).NotTo(HaveOccurred())
		result = append(result, string(encoded))
	}
	slices.Sort(result)
	return result
}

// workingTreeState captures tracked and untracked changes, so a generation
// pass that rewrites a file is detected even when the file was already dirty.
func workingTreeState() string {
	status, err := utils.Run(execCommand("git", "status", "--porcelain", "--untracked-files=all"))
	Expect(err).NotTo(HaveOccurred())
	diff, err := utils.Run(execCommand("git", "diff"))
	Expect(err).NotTo(HaveOccurred())
	return status + "\n" + diff
}
