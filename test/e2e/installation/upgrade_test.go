// SPDX-FileCopyrightText: Copyright (c) 2026 NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

//go:build e2e

package installation

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/dsx-ai-factory/cluster-api-provider-nico/test/utils"
)

const (
	// previousRelease is the latest stable release without NicoIdentity.
	previousRelease = "v0.0.45"
	// previousImage is built from previousRelease's tag. The published image
	// needs registry credentials, so the suite builds the same source instead
	// and records that the upgrade starts from a source-built image.
	previousImage      = bundleRegistry + "/controller:" + previousRelease
	releaseDownloadURL = "https://github.com/dsx-ai-factory/cluster-api-provider-nico/releases/download/"
	exampleNamespace   = "default"
)

// ensurePreviousImage builds the previous release's manager image from its
// git tag once and loads it into Kind.
func ensurePreviousImage() {
	if _, err := utils.Run(execCommand("docker", "image", "inspect", previousImage)); err != nil {
		archive := filepath.Join(GinkgoT().TempDir(), "source.tar")
		_, err := utils.Run(execCommand("git", "archive", "--format=tar", "-o", archive, previousRelease))
		Expect(err).NotTo(HaveOccurred())
		source, err := os.Open(archive)
		Expect(err).NotTo(HaveOccurred())
		defer func() { _ = source.Close() }()
		build := execCommand("docker", "build", "-t", previousImage, "-")
		build.Stdin = source
		_, err = utils.Run(build)
		Expect(err).NotTo(HaveOccurred(), "Failed to build the %s manager from its tag", previousRelease)
	}
	id, err := utils.Run(execCommand("docker", "image", "inspect", "--format", "{{.Id}}", previousImage))
	Expect(err).NotTo(HaveOccurred())
	AddReportEntry("previous manager image", fmt.Sprintf("%s built from tag %s, image ID %s", previousImage,
		previousRelease, strings.TrimSpace(id)))
	Expect(utils.LoadImageToKindClusterWithName(previousImage)).To(Succeed())
}

// extractPreviousChart writes the previous release's chart from its git tag.
func extractPreviousChart() string {
	dir := GinkgoT().TempDir()
	archive := filepath.Join(dir, "chart.tar")
	_, err := utils.Run(execCommand("git", "archive", "--format=tar", "-o", archive, previousRelease, "chart"))
	Expect(err).NotTo(HaveOccurred())
	_, err = utils.Run(execCommand("tar", "-xf", archive, "-C", dir))
	Expect(err).NotTo(HaveOccurred())
	return filepath.Join(dir, "chart")
}

// downloadPreviousRelease fetches the previous release's published bundle and
// metadata into dir, verifies both against its checksums.txt, and returns the
// bundle's path.
func downloadPreviousRelease(dir string) string {
	Expect(os.MkdirAll(dir, 0o755)).To(Succeed())
	sums := map[string]string{}
	scanner := bufio.NewScanner(strings.NewReader(string(fetch(previousRelease + "/checksums.txt"))))
	for scanner.Scan() {
		if fields := strings.Fields(scanner.Text()); len(fields) == 2 {
			sums[strings.TrimLeft(path.Base(fields[1]), "*")] = fields[0]
		}
	}
	for _, name := range []string{"infrastructure-components.yaml", "metadata.yaml"} {
		content := fetch(previousRelease + "/" + name)
		digest := sha256.Sum256(content)
		Expect(hex.EncodeToString(digest[:])).To(Equal(sums[name]), "%s does not match checksums.txt", name)
		Expect(os.WriteFile(filepath.Join(dir, name), content, 0o600)).To(Succeed())
		AddReportEntry("previous release asset", fmt.Sprintf("%s %s sha256 %s", previousRelease, name, sums[name]))
	}
	return filepath.Join(dir, "infrastructure-components.yaml")
}

func fetch(asset string) []byte {
	client := &http.Client{Timeout: time.Minute}
	response, err := client.Get(releaseDownloadURL + asset)
	Expect(err).NotTo(HaveOccurred())
	defer func() { _ = response.Body.Close() }()
	Expect(response.StatusCode).To(Equal(http.StatusOK), asset)
	content, err := io.ReadAll(response.Body)
	Expect(err).NotTo(HaveOccurred())
	return content
}

// bundleImageName returns the last path element of the manager image a bundle
// names, without its tag or digest. clusterctl's image override keeps it.
func bundleImageName(bundlePath string) string {
	content, err := os.ReadFile(bundlePath)
	Expect(err).NotTo(HaveOccurred())
	for line := range strings.SplitSeq(string(content), "\n") {
		reference, found := strings.CutPrefix(strings.TrimSpace(line), "image: ")
		if !found {
			continue
		}
		reference, _, _ = strings.Cut(reference, "@")
		name := path.Base(reference)
		name, _, _ = strings.Cut(name, ":")
		AddReportEntry("previous bundle image", reference)
		return name
	}
	Fail("the bundle names no image")
	return ""
}

// waitForExampleCluster waits for examples/cluster-fake.yaml's cluster and
// both machines to be provisioned.
func waitForExampleCluster() {
	Eventually(func(g Gomega) {
		ready, err := kubectl("get", "nicocluster", "demo", "-n", exampleNamespace, "-o",
			"jsonpath={.status.ready}")
		g.Expect(err).NotTo(HaveOccurred())
		g.Expect(ready).To(Equal("true"))
		for _, machine := range []string{"demo-cp-0", "demo-worker-0"} {
			out, err := kubectl("get", "nicomachine", machine, "-n", exampleNamespace, "-o",
				"jsonpath={.spec.providerID} {.status.instanceID}")
			g.Expect(err).NotTo(HaveOccurred())
			g.Expect(strings.Fields(out)).To(HaveLen(2), "%s is not provisioned yet", machine)
		}
	}, 3*time.Minute, 2*time.Second).Should(Succeed())
}

// exampleSnapshot records what an upgrade must leave unchanged: the cluster's
// spec, including its Secret reference, and each machine's spec and instance.
func exampleSnapshot() string {
	var snapshot strings.Builder
	out, err := kubectl("get", "nicocluster", "demo", "-n", exampleNamespace, "-o", "jsonpath={.spec}")
	Expect(err).NotTo(HaveOccurred())
	snapshot.WriteString(out)
	for _, machine := range []string{"demo-cp-0", "demo-worker-0"} {
		out, err := kubectl("get", "nicomachine", machine, "-n", exampleNamespace, "-o",
			"jsonpath={.spec} {.status.instanceID}")
		Expect(err).NotTo(HaveOccurred())
		snapshot.WriteString("\n" + out)
	}
	return snapshot.String()
}

// instanceCreatesSince counts instance creations the fake NICo logged since t.
func instanceCreatesSince(t time.Time) int {
	out, err := kubectl("logs", "deployment/fake-nico-api", "-n", fakeNamespace,
		"--since-time="+t.UTC().Format(time.RFC3339))
	Expect(err).NotTo(HaveOccurred())
	return strings.Count(out, "POST /v2/org/org-1/nico/instance ")
}

// removeExampleCluster deletes the cluster first, while the provider and its
// credentials still exist to release the machines, then the rest.
func removeExampleCluster() {
	_, _ = kubectl("delete", "cluster", "demo", "-n", exampleNamespace, "--ignore-not-found", "--wait=true",
		"--timeout=3m")
	_, _ = kubectl("delete", "-f", exampleManifest, "--ignore-not-found", "--wait=true", "--timeout=2m")
}

var _ = Describe("Helm upgrade from the previous release", Ordered, Label("upgrade", "helm"), func() {
	var before string

	BeforeAll(func() {
		ensurePreviousImage()
		previousChart := extractPreviousChart()

		By("installing the previous release's chart, which has no NicoIdentity")
		Expect(helmInstall(previousChart, bundleRegistry+"/controller", previousRelease,
			[]string{"--leader-elect"})).To(Succeed())
		waitForManager()
		_, err := kubectl("get", "crd", "nicoidentities.infrastructure.cluster.x-k8s.io")
		Expect(err).To(HaveOccurred(), "the previous release must not have the NicoIdentity CRD")

		By("provisioning the example cluster with the previous release")
		_, err = kubectl("apply", "-f", exampleManifest)
		Expect(err).NotTo(HaveOccurred())
		waitForExampleCluster()
		before = exampleSnapshot()
	})

	AfterEach(writeDiagnostics)

	AfterAll(func() {
		removeExampleCluster()
		_, _ = kubectl("delete", "nicoidentities", "--all", "-n", managerNamespace, "--ignore-not-found")
		removeHelmInstallation()
	})

	It("keeps the previous release's clusters unchanged through helm upgrade", func() {
		upgraded := time.Now()
		Expect(helmInstall("./chart", managerImageRepository, managerImageTag,
			[]string{"--leader-elect"})).To(Succeed())
		waitForManager()
		Expect(managerImageInUse()).To(Equal(managerImage))
		_, err := kubectl("get", "crd", "nicoidentities.infrastructure.cluster.x-k8s.io")
		Expect(err).NotTo(HaveOccurred(), "the upgrade must install the NicoIdentity CRD")

		waitForExampleCluster()
		Expect(exampleSnapshot()).To(Equal(before))
		Expect(instanceCreatesSince(upgraded)).To(BeZero(), "the upgrade must not create instances")
	})

	It("observes the default credentials once an Identity is created after the upgrade", func() {
		created := time.Now()
		Expect(apply(identity(managerNamespace, defaultIdentity))).To(Succeed())
		waitForHealthy(managerNamespace, defaultIdentity, created)
	})

	It("leaves clusters unchanged when the Identity is deleted", func() {
		_, err := kubectl("delete", "nicoidentity", defaultIdentity, "-n", managerNamespace, "--wait=true")
		Expect(err).NotTo(HaveOccurred())
		waitForExampleCluster()
		Expect(exampleSnapshot()).To(Equal(before))
	})

	It("rolls back to the previous release with the CRD and Identity kept and observation stopped", func() {
		created := time.Now()
		Expect(apply(identity(managerNamespace, defaultIdentity))).To(Succeed())
		settled := waitForHealthy(managerNamespace, defaultIdentity, created)

		By("rolling the release back to the previous chart and manager")
		_, err := helm("rollback", releaseName, "1", "--namespace", managerNamespace, "--wait", "--timeout", "3m")
		Expect(err).NotTo(HaveOccurred())
		waitForManager()
		Expect(managerImageInUse()).To(Equal(previousImage))
		_, err = kubectl("get", "crd", "nicoidentities.infrastructure.cluster.x-k8s.io")
		Expect(err).NotTo(HaveOccurred(), "the chart keeps the CRD on rollback")

		By("changing the Identity, which only the newer manager would recheck")
		_, err = kubectl("label", "nicoidentity", defaultIdentity, "-n", managerNamespace, "e2e/rolled-back=true")
		Expect(err).NotTo(HaveOccurred())
		Consistently(func(g Gomega) {
			current, err := observe(managerNamespace, defaultIdentity)
			g.Expect(err).NotTo(HaveOccurred())
			g.Expect(current.LastChecked.Time).To(BeTemporally("==", settled.LastChecked.Time))
		}, 20*time.Second, 2*time.Second).Should(Succeed())

		waitForExampleCluster()
		Expect(exampleSnapshot()).To(Equal(before))
	})
})

var _ = Describe("Release bundle upgrade from the previous release", Ordered, Label("upgrade", "bundle"), func() {
	var config, current, before string

	BeforeAll(func() {
		repository := GinkgoT().TempDir()
		previousBundle := downloadPreviousRelease(filepath.Join(repository, "infrastructure-nico", previousRelease))
		name := bundleImageName(previousBundle)

		ensurePreviousImage()
		previous := bundleRegistry + "/" + name + ":" + previousRelease
		if previous != previousImage {
			tagAndLoad(previousImage, previous)
		}
		current = bundleRegistry + "/" + name + ":" + bundleVersion
		tagAndLoad(managerImage, current)
		renderBundle(repository, bundleVersion, current)
		// The override points the published bundle at the source-built image.
		config = clusterctlConfig(repository, bundleVersion, bundleRegistry)

		By("installing the published previous bundle with clusterctl")
		_, err := clusterctl(config, "init", "--infrastructure", "nico:"+previousRelease, "--wait-providers")
		Expect(err).NotTo(HaveOccurred())
		waitForManager()
		Expect(managerImageInUse()).To(Equal(previous))

		By("provisioning the example cluster with the previous release")
		_, err = kubectl("apply", "-f", exampleManifest)
		Expect(err).NotTo(HaveOccurred())
		waitForExampleCluster()
		before = exampleSnapshot()
	})

	AfterEach(writeDiagnostics)

	AfterAll(func() {
		removeExampleCluster()
		_, _ = kubectl("delete", "nicoidentities", "--all", "-n", managerNamespace, "--ignore-not-found")
		removeBundleInstallation(config)
	})

	It("keeps the previous release's clusters unchanged through clusterctl upgrade", func() {
		upgraded := time.Now()
		_, err := clusterctl(config, "upgrade", "apply", "--infrastructure", "nico:"+bundleVersion,
			"--wait-providers")
		Expect(err).NotTo(HaveOccurred())
		waitForManager()
		Expect(managerImageInUse()).To(Equal(current))
		_, err = kubectl("get", "crd", "nicoidentities.infrastructure.cluster.x-k8s.io")
		Expect(err).NotTo(HaveOccurred(), "the upgrade must install the NicoIdentity CRD")

		waitForExampleCluster()
		Expect(exampleSnapshot()).To(Equal(before))
		Expect(instanceCreatesSince(upgraded)).To(BeZero(), "the upgrade must not create instances")
	})

	It("observes the default credentials once an Identity is created after the upgrade", func() {
		created := time.Now()
		Expect(apply(identity(managerNamespace, defaultIdentity))).To(Succeed())
		waitForHealthy(managerNamespace, defaultIdentity, created)
	})
})

var _ = Describe("Helm installation with CRDs managed separately", Ordered, Label("helm", "crds"), func() {
	BeforeAll(func() {
		By("installing every CRD except NicoIdentity, as a separate CRD pipeline might")
		_, err := utils.Run(execCommand("make", "install"))
		Expect(err).NotTo(HaveOccurred())
		_, err = kubectl("delete", "crd", "nicoidentities.infrastructure.cluster.x-k8s.io", "--wait=true")
		Expect(err).NotTo(HaveOccurred())

		By("installing the chart without its CRDs")
		Expect(helmInstall("./chart", managerImageRepository, managerImageTag,
			[]string{"--leader-elect", timeoutArg}, "--set", "crd.enabled=false")).To(Succeed())
		waitForManager()
	})

	AfterEach(writeDiagnostics)

	AfterAll(func() {
		_, _ = kubectl("delete", "nicoidentities", "--all", "-n", managerNamespace, "--ignore-not-found")
		removeHelmInstallation()
	})

	It("keeps the manager running without the NicoIdentity CRD", func() {
		// Longer than the two-minute cache-sync timeout that a missing watched
		// kind would trip, so a crash would show as a restart.
		Consistently(func(g Gomega) {
			g.Expect(managerRestarts()).To(Equal("0"))
		}, 150*time.Second, 10*time.Second).Should(Succeed())
		Expect(managerLogs()).To(ContainSubstring("NicoIdentity CRD is not installed"))
	})

	It("starts observing once the CRD is installed and the manager restarts", func() {
		_, err := utils.Run(execCommand("make", "install"))
		Expect(err).NotTo(HaveOccurred())
		_, err = kubectl("rollout", "restart", managerDeployment(), "-n", managerNamespace)
		Expect(err).NotTo(HaveOccurred())
		waitForManager()

		Expect(apply(healthySecret(managerNamespace))).To(Succeed())
		created := time.Now()
		Expect(apply(identity(managerNamespace, defaultIdentity))).To(Succeed())
		waitForHealthy(managerNamespace, defaultIdentity, created)
	})
})
