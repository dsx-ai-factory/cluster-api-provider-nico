// SPDX-FileCopyrightText: Copyright (c) 2026 NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

//go:build e2e

package installation

import (
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const (
	defaultIdentity = "nico-default"
	defaultSecret   = "nico-credentials"
	watchLabel      = "cluster.x-k8s.io/watch-filter"
)

var timeoutArg = "--provider-identity-validation-timeout=" + validationTimeout.String()

var _ = Describe("Helm installation", Ordered, Label("helm"), func() {
	BeforeAll(func() {
		By("installing the chart with the locally built manager")
		Expect(helmInstall("./chart", managerImageRepository, managerImageTag,
			[]string{"--leader-elect", timeoutArg})).To(Succeed())
		waitForManager()
		Expect(apply(healthySecret(managerNamespace))).To(Succeed())
	})

	AfterEach(writeDiagnostics)

	AfterAll(func() {
		_, _ = kubectl("delete", "nicoidentities", "--all", "-n", managerNamespace, "--ignore-not-found")
		removeHelmInstallation()
	})

	It("reports current-generation Ready before any tenant resource exists", func() {
		noTenantResources()
		created := time.Now()
		Expect(apply(identity(managerNamespace, defaultIdentity))).To(Succeed())
		result := waitForHealthy(managerNamespace, defaultIdentity, created)
		Expect(result.Generation).To(BeEquivalentTo(1))
	})

	It("lets an Identity reader see status but not Secrets, and keeps spec writes out of the controller", func() {
		expectReaderAccess(managerNamespace, defaultIdentity, defaultSecret)
	})

	It("rechecks within seconds when its Secret is rotated, deleted and recreated", func() {
		By("rotating to a client secret the issuer rejects")
		changed := time.Now()
		Expect(apply(credentialsSecret(managerNamespace, fakeTokenURL, wrongSecret))).To(Succeed())
		waitForResult(managerNamespace, defaultIdentity, metav1.ConditionFalse, "AuthenticationFailed", changed,
			30*time.Second)

		By("rotating back to the accepted client secret")
		changed = time.Now()
		Expect(apply(healthySecret(managerNamespace))).To(Succeed())
		waitForHealthy(managerNamespace, defaultIdentity, changed)

		By("deleting the Secret")
		changed = time.Now()
		_, err := kubectl("delete", "secret", defaultSecret, "-n", managerNamespace)
		Expect(err).NotTo(HaveOccurred())
		waitForResult(managerNamespace, defaultIdentity, metav1.ConditionFalse, "CredentialsNotFound", changed,
			30*time.Second)

		By("recreating the Secret")
		changed = time.Now()
		Expect(apply(healthySecret(managerNamespace))).To(Succeed())
		waitForHealthy(managerNamespace, defaultIdentity, changed)
	})

	It("reports a check that times out with its completion time", func() {
		held := time.Now()
		Expect(apply(credentialsSecret(managerNamespace, hangTokenURL, fakeClientSecret))).
			To(Succeed())
		result := waitForResult(managerNamespace, defaultIdentity, metav1.ConditionUnknown, "ValidationFailed", held,
			validationTimeout+30*time.Second)
		Expect(result.LastChecked.Time).To(BeTemporally(">=",
			held.Add(validationTimeout).Truncate(time.Second).Add(-time.Second)),
			"lastCheckedTime must record when the check completed, not when it started")

		restored := time.Now()
		Expect(apply(healthySecret(managerNamespace))).To(Succeed())
		waitForHealthy(managerNamespace, defaultIdentity, restored)
	})

	It("never publishes a check superseded by a Secret change", func() {
		recorder := recordReadyReasons(managerNamespace, defaultIdentity)

		By("holding a check open on an endpoint that never answers")
		held := time.Now()
		Expect(apply(credentialsSecret(managerNamespace, hangTokenURL, fakeClientSecret))).
			To(Succeed())
		// The Secret watch starts the held check within a second. Wait for it to
		// reach the endpoint so the next change supersedes a check in flight.
		time.Sleep(3 * time.Second)

		By("fixing the Secret while that check is still held")
		fixed := time.Now()
		Expect(apply(healthySecret(managerNamespace))).To(Succeed())
		result := waitForResult(managerNamespace, defaultIdentity, metav1.ConditionTrue, "ValidationSucceeded",
			fixed, validationTimeout+time.Minute)
		reasons := recorder.stop()

		Expect(result.LastChecked.Time).To(BeTemporally(">=",
			held.Add(validationTimeout).Truncate(time.Second).Add(-time.Second)),
			"the held check should have delayed the recheck until its deadline")
		Expect(reasons).NotTo(BeEmpty())
		Expect(reasons).To(HaveEach("ValidationSucceeded"),
			"the superseded ValidationFailed result must never be published")
	})

	It("checks every Identity again after the controller restarts", func() {
		restarted := time.Now()
		_, err := kubectl("rollout", "restart", managerDeployment(), "-n", managerNamespace)
		Expect(err).NotTo(HaveOccurred())
		waitForManager()
		waitForResult(managerNamespace, defaultIdentity, metav1.ConditionTrue, "ValidationSucceeded", restarted,
			2*time.Minute)
	})

	It("checks an Identity deleted and recreated under the same name, leaving its Secret alone", func() {
		before, err := observe(managerNamespace, defaultIdentity)
		Expect(err).NotTo(HaveOccurred())
		_, err = kubectl("delete", "nicoidentity", defaultIdentity, "-n", managerNamespace, "--wait=true")
		Expect(err).NotTo(HaveOccurred())
		_, err = kubectl("get", "secret", defaultSecret, "-n", managerNamespace)
		Expect(err).NotTo(HaveOccurred(), "deleting an Identity must not delete its Secret")

		recreated := time.Now()
		Expect(apply(identity(managerNamespace, defaultIdentity))).To(Succeed())
		result := waitForHealthy(managerNamespace, defaultIdentity, recreated)
		Expect(result.UID).NotTo(Equal(before.UID))
	})

	It("rechecks on its own within five minutes without a status-write loop", func() {
		settled, err := observe(managerNamespace, defaultIdentity)
		Expect(err).NotTo(HaveOccurred())
		Expect(settled.LastChecked).NotTo(BeNil())
		first := settled.LastChecked.Time
		transition := settled.Ready.LastTransitionTime

		var next observation
		Eventually(func(g Gomega) {
			current, err := observe(managerNamespace, defaultIdentity)
			g.Expect(err).NotTo(HaveOccurred())
			g.Expect(current.LastChecked.Time).To(BeTemporally(">", first))
			next = current
		}, 6*time.Minute, 5*time.Second).Should(Succeed())

		interval := next.LastChecked.Sub(first)
		Expect(interval).To(BeNumerically(">=", 4*time.Minute-time.Second), "the recheck came early")
		Expect(interval).To(BeNumerically("<=", 5*time.Minute+30*time.Second), "the recheck came late")
		Expect(next.Ready.Reason).To(Equal("ValidationSucceeded"))
		Expect(next.Ready.LastTransitionTime).To(Equal(transition),
			"an unchanged result must keep its transition time")
		Expect(tokenRequestsSince(first.Add(time.Second))).To(Equal(1),
			"exactly one check should run between the two observations")
	})

	It("leaves the last result aging while the controller is stopped", func() {
		settled, err := observe(managerNamespace, defaultIdentity)
		Expect(err).NotTo(HaveOccurred())

		By("stopping the controller and rotating the Secret")
		_, err = kubectl("scale", managerDeployment(), "-n", managerNamespace, "--replicas=0")
		Expect(err).NotTo(HaveOccurred())
		_, err = kubectl("wait", "pod", "-n", managerNamespace, "-l", managerSelector, "--for=delete",
			"--timeout=2m")
		Expect(err).NotTo(HaveOccurred())
		Expect(apply(credentialsSecret(managerNamespace, fakeTokenURL, wrongSecret))).To(Succeed())
		Consistently(func(g Gomega) {
			current, err := observe(managerNamespace, defaultIdentity)
			g.Expect(err).NotTo(HaveOccurred())
			g.Expect(current.LastChecked.Time).To(BeTemporally("==", settled.LastChecked.Time))
			g.Expect(current.Ready.Reason).To(Equal("ValidationSucceeded"))
		}, 20*time.Second, 2*time.Second).Should(Succeed())

		By("starting it again")
		started := time.Now()
		_, err = kubectl("scale", managerDeployment(), "-n", managerNamespace, "--replicas=1")
		Expect(err).NotTo(HaveOccurred())
		waitForManager()
		waitForResult(managerNamespace, defaultIdentity, metav1.ConditionFalse, "AuthenticationFailed", started,
			2*time.Minute)
		restored := time.Now()
		Expect(apply(healthySecret(managerNamespace))).To(Succeed())
		waitForHealthy(managerNamespace, defaultIdentity, restored)
	})
})

var _ = Describe("Helm installation with a narrowed scope", Ordered, Label("helm", "scope"), func() {
	const (
		inScope    = "capnico-e2e-scope-a"
		outOfScope = "capnico-e2e-scope-b"
		filtered   = "capnico-e2e-scope-c"
		team       = "e2e-team"
	)

	BeforeAll(func() {
		for _, namespace := range []string{inScope, outOfScope, filtered} {
			createNamespace(namespace)
			Expect(apply(healthySecret(namespace))).To(Succeed())
		}
		By("installing the chart with a manager limited to one namespace")
		Expect(helmInstall("./chart", managerImageRepository, managerImageTag,
			[]string{"--leader-elect", "--namespace=" + inScope, timeoutArg})).To(Succeed())
		waitForManager()
	})

	AfterEach(writeDiagnostics)

	AfterAll(func() {
		for _, namespace := range []string{inScope, outOfScope, filtered} {
			_, _ = kubectl("delete", "nicoidentities", "--all", "-n", namespace, "--ignore-not-found")
		}
		removeHelmInstallation()
		for _, namespace := range []string{inScope, outOfScope, filtered} {
			deleteNamespace(namespace)
		}
	})

	It("checks only the Identities in the manager's namespace", func() {
		quiet := time.Now()
		Expect(apply(identity(outOfScope, "outside"))).To(Succeed())
		expectUnobserved(outOfScope, "outside")
		Expect(tokenRequestsSince(quiet)).To(BeZero(), "an out-of-scope Identity must not be validated")

		created := time.Now()
		Expect(apply(identity(inScope, "inside"))).To(Succeed())
		waitForHealthy(inScope, "inside", created)
		expectUnobserved(outOfScope, "outside")
	})

	It("checks only labeled Identities under a watch filter", func() {
		By("reinstalling the manager across all namespaces with a watch filter")
		Expect(helmInstall("./chart", managerImageRepository, managerImageTag,
			[]string{"--leader-elect", "--watch-filter=" + team, timeoutArg})).To(Succeed())
		waitForManager()

		quiet := time.Now()
		Expect(apply(identity(filtered, "filtered"))).To(Succeed())
		expectUnobserved(filtered, "filtered")
		Expect(tokenRequestsSince(quiet)).To(BeZero(), "unlabeled Identities must not be validated")

		By("adding the watch-filter label")
		labeled := time.Now()
		_, err := kubectl("label", "nicoidentity", "filtered", "-n", filtered, watchLabel+"="+team)
		Expect(err).NotTo(HaveOccurred())
		waitForHealthy(filtered, "filtered", labeled)
	})

	It("leaves the last result aging once an Identity leaves the filter", func() {
		settled, err := observe(filtered, "filtered")
		Expect(err).NotTo(HaveOccurred())
		_, err = kubectl("label", "nicoidentity", "filtered", "-n", filtered, watchLabel+"-")
		Expect(err).NotTo(HaveOccurred())
		Expect(apply(credentialsSecret(filtered, fakeTokenURL, wrongSecret))).To(Succeed())
		Consistently(func(g Gomega) {
			current, err := observe(filtered, "filtered")
			g.Expect(err).NotTo(HaveOccurred())
			g.Expect(current.LastChecked.Time).To(BeTemporally("==", settled.LastChecked.Time))
			g.Expect(current.Ready.Reason).To(Equal("ValidationSucceeded"))
		}, 20*time.Second, 2*time.Second).Should(Succeed())
	})
})

// expectUnobserved asserts that an Identity stays without status for a while.
func expectUnobserved(namespace, name string) {
	Consistently(func(g Gomega) {
		current, err := observe(namespace, name)
		g.Expect(err).NotTo(HaveOccurred())
		g.Expect(current.Ready).To(BeNil(), current.String())
		g.Expect(current.LastChecked).To(BeNil())
	}, 20*time.Second, 2*time.Second).Should(Succeed())
}

// expectReaderAccess checks the installed RBAC with real requests: a user
// bound to the Identity viewer role reads status but cannot read the Secret or
// write the Identity, and the controller may write status but not spec.
func expectReaderAccess(namespace, name, secret string) {
	const readerNamespace = "capnico-e2e-rbac"
	reader := "system:serviceaccount:" + readerNamespace + ":identity-reader"
	createNamespace(readerNamespace)
	DeferCleanup(func() {
		_, _ = kubectl("delete", "clusterrolebinding", "capnico-e2e-identity-reader", "--ignore-not-found")
		deleteNamespace(readerNamespace)
	})
	_, err := kubectl("create", "serviceaccount", "identity-reader", "-n", readerNamespace)
	Expect(err).NotTo(HaveOccurred())
	_, err = kubectl("create", "clusterrolebinding", "capnico-e2e-identity-reader",
		"--clusterrole=nicoidentity-viewer-role", "--serviceaccount="+readerNamespace+":identity-reader")
	Expect(err).NotTo(HaveOccurred())

	By("reading the Identity's status as the reader")
	Eventually(func(g Gomega) {
		out, err := kubectlAs(reader, "get", "nicoidentity", name, "-n", namespace, "-o",
			`jsonpath={.status.conditions[?(@.type=="Ready")].reason}`)
		g.Expect(err).NotTo(HaveOccurred())
		g.Expect(out).To(Equal("ValidationSucceeded"))
	}, 30*time.Second, time.Second).Should(Succeed())

	By("denying the reader the Secret and every Identity write")
	for _, args := range [][]string{
		{"get", "secret", secret, "-n", namespace},
		{"patch", "nicoidentity", name, "-n", namespace, "--type=merge", "-p",
			`{"spec":{"credentialsRef":{"name":"other"}}}`},
		{"patch", "nicoidentity", name, "-n", namespace, "--subresource=status", "--type=merge", "-p",
			`{"status":{"lastCheckedTime":null}}`},
		{"delete", "nicoidentity", name, "-n", namespace, "--dry-run=server"},
	} {
		out, err := kubectlAs(reader, args...)
		Expect(err).To(HaveOccurred(), "the reader must not be allowed to %v", args)
		Expect(out).To(ContainSubstring("forbidden"))
	}

	By("allowing the controller to write status but not spec")
	controller := managerServiceAccount()
	out, err := kubectl("auth", "can-i", "patch", "nicoidentities", "--subresource=status", "--as", controller,
		"-n", namespace)
	Expect(err).NotTo(HaveOccurred())
	Expect(out).To(ContainSubstring("yes"))
	out, err = kubectl("auth", "can-i", "update", "nicoidentities", "--as", controller, "-n", namespace)
	Expect(err).To(HaveOccurred())
	Expect(out).To(ContainSubstring("no"))
}
