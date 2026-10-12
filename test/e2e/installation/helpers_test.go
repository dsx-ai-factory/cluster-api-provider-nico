// SPDX-FileCopyrightText: Copyright (c) 2026 NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

//go:build e2e

package installation

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	infrav1 "github.com/dsx-ai-factory/cluster-api-provider-nico/api/v1alpha1"
	"github.com/dsx-ai-factory/cluster-api-provider-nico/test/utils"
)

const (
	managerImage           = "example.com/cluster-api-provider-nico:v0.0.1"
	managerImageRepository = "example.com/cluster-api-provider-nico"
	managerImageTag        = "v0.0.1"
	fakeImage              = "fake-nico-api-server"

	fakeManifest    = "hack/tilt/fake-nico-api.yaml"
	fakeNamespace   = "fake-nico-api"
	hangManifest    = "test/e2e/installation/testdata/hang-endpoint.yaml"
	hangNamespace   = "capnico-e2e-hang"
	exampleManifest = "examples/cluster-fake.yaml"

	releaseName      = "capi-provider-nico"
	managerNamespace = "capnico-system"
	managerSelector  = "control-plane=controller-manager"

	// The synthetic client the fake accepts, from hack/tilt/fake-nico-api.yaml.
	fakeEndpoint     = "http://fake-nico-api.fake-nico-api.svc.cluster.local:8090"
	fakeTokenURL     = fakeEndpoint + "/token"
	fakeClientID     = "fake-client"
	fakeClientSecret = "fake-secret"
	wrongSecret      = "not-the-fake-secret"
	hangTokenURL     = "http://hang.capnico-e2e-hang.svc.cluster.local:8080/cgi-bin/token"

	// The suite shortens the validation deadline so held checks finish quickly.
	validationTimeout = 15 * time.Second
)

var capiDeployments = []struct{ namespace, name string }{
	{"capi-system", "capi-controller-manager"},
	{"capi-kubeadm-bootstrap-system", "capi-kubeadm-bootstrap-controller-manager"},
	{"capi-kubeadm-control-plane-system", "capi-kubeadm-control-plane-controller-manager"},
}

var nicoCRDs = []string{
	"nicoclusters.infrastructure.cluster.x-k8s.io",
	"nicoclustertemplates.infrastructure.cluster.x-k8s.io",
	"nicoidentities.infrastructure.cluster.x-k8s.io",
	"nicomachines.infrastructure.cluster.x-k8s.io",
	"nicomachinetemplates.infrastructure.cluster.x-k8s.io",
}

func kindCluster() string {
	if name := os.Getenv("KIND_CLUSTER"); name != "" {
		return name
	}
	return "cluster-api-provider-nico-test-e2e"
}

func kubeContext() string { return "kind-" + kindCluster() }

func clusterctlBinary() string {
	if path := os.Getenv("CLUSTERCTL"); path != "" {
		return path
	}
	return "bin/clusterctl"
}

// execCommand builds a command that utils.Run executes from the repository root.
func execCommand(name string, args ...string) *exec.Cmd { return exec.Command(name, args...) }

func kubectl(args ...string) (string, error) {
	return utils.Run(exec.Command("kubectl", append([]string{"--context", kubeContext()}, args...)...))
}

// kubectlAs sends the request as the given user, so the API server evaluates
// that user's permissions rather than the administrator's.
func kubectlAs(user string, args ...string) (string, error) {
	return kubectl(append([]string{"--as", user}, args...)...)
}

// apply sends a manifest on stdin, so Secret values never appear in the
// command line that utils.Run logs.
func apply(manifest string) error {
	cmd := exec.Command("kubectl", "--context", kubeContext(), "apply", "-f", "-")
	cmd.Stdin = strings.NewReader(manifest)
	_, err := utils.Run(cmd)
	return err
}

func helm(args ...string) (string, error) {
	return utils.Run(exec.Command("helm", append(args, "--kube-context", kubeContext())...))
}

// helmInstall installs or upgrades the release from chart with the given image
// and manager arguments, and waits for the manager to become ready.
func helmInstall(chart, repository, tag string, managerArgs []string, extra ...string) error {
	encodedArgs, err := json.Marshal(managerArgs)
	if err != nil {
		return err
	}
	args := append([]string{
		"upgrade", "--install", releaseName, chart,
		"--namespace", managerNamespace, "--create-namespace", "--wait", "--timeout", "3m",
		"--set", "manager.image.repository=" + repository,
		"--set", "manager.image.tag=" + tag,
		"--set", "manager.image.pullPolicy=IfNotPresent",
		"--set-json", "manager.args=" + string(encodedArgs),
	}, extra...)
	_, err = helm(args...)
	return err
}

// removeHelmInstallation uninstalls the release and deletes what the chart
// keeps on purpose (CRDs and the namespace), so the next installation starts clean.
func removeHelmInstallation() {
	_, _ = helm("uninstall", releaseName, "--namespace", managerNamespace, "--ignore-not-found", "--wait")
	deleteNicoCRDsAndNamespace()
}

func deleteNicoCRDsAndNamespace() {
	_, _ = kubectl(append([]string{"delete", "crd", "--ignore-not-found", "--wait=true", "--timeout=2m"},
		nicoCRDs...)...)
	_, _ = kubectl("delete", "namespace", managerNamespace, "--ignore-not-found", "--wait=true",
		"--timeout=2m")
}

func waitForManager() {
	_, err := kubectl("rollout", "status", "deployment", "-n", managerNamespace, "-l", managerSelector,
		"--timeout=3m")
	Expect(err).NotTo(HaveOccurred(), "the manager did not become ready")
	Eventually(func(g Gomega) {
		out, err := kubectl("get", "pods", "-n", managerNamespace, "-l", managerSelector, "-o",
			`jsonpath={range .items[*]}{.status.conditions[?(@.type=="Ready")].status}{"\n"}{end}`)
		g.Expect(err).NotTo(HaveOccurred())
		g.Expect(utils.GetNonEmptyLines(out)).To(ConsistOf("True"))
	}, 2*time.Minute, time.Second).Should(Succeed())
}

func managerDeployment() string {
	out, err := kubectl("get", "deployment", "-n", managerNamespace, "-l", managerSelector, "-o", "name")
	Expect(err).NotTo(HaveOccurred())
	lines := utils.GetNonEmptyLines(out)
	Expect(lines).To(HaveLen(1), "expected one manager Deployment")
	return lines[0]
}

// managerServiceAccount returns the user the running manager authenticates as.
func managerServiceAccount() string {
	out, err := kubectl("get", "pods", "-n", managerNamespace, "-l", managerSelector, "-o",
		"jsonpath={.items[0].spec.serviceAccountName}")
	Expect(err).NotTo(HaveOccurred())
	Expect(out).NotTo(BeEmpty())
	return "system:serviceaccount:" + managerNamespace + ":" + out
}

func managerRestarts() string {
	out, err := kubectl("get", "pods", "-n", managerNamespace, "-l", managerSelector, "-o",
		"jsonpath={.items[*].status.containerStatuses[*].restartCount}")
	Expect(err).NotTo(HaveOccurred())
	return strings.TrimSpace(out)
}

func managerLogs() string {
	out, err := kubectl("logs", "-n", managerNamespace, "-l", managerSelector, "--tail=-1")
	Expect(err).NotTo(HaveOccurred())
	return out
}

func createNamespace(name string) {
	Expect(apply(fmt.Sprintf("apiVersion: v1\nkind: Namespace\nmetadata:\n  name: %s\n", name))).To(Succeed())
}

func deleteNamespace(name string) {
	_, _ = kubectl("delete", "namespace", name, "--ignore-not-found", "--wait=true", "--timeout=2m")
}

// credentialsSecret renders the Secret defaultSecret for the fake NICo in the
// layout of examples/cluster-fake.yaml. Only the token URL and client secret vary.
func credentialsSecret(namespace, tokenURL, clientSecret string) string {
	return fmt.Sprintf(`apiVersion: v1
kind: Secret
metadata:
  name: %s
  namespace: %s
type: Opaque
stringData:
  endpoint: %s
  orgID: org-1
  tokenURL: %s
  clientID: %s
  clientSecret: %s
`, defaultSecret, namespace, fakeEndpoint, tokenURL, fakeClientID, clientSecret)
}

func healthySecret(namespace string) string {
	return credentialsSecret(namespace, fakeTokenURL, fakeClientSecret)
}

// identity renders an Identity naming the Secret defaultSecret in its namespace.
func identity(namespace, name string) string {
	return fmt.Sprintf(`apiVersion: infrastructure.cluster.x-k8s.io/v1alpha1
kind: NicoIdentity
metadata:
  name: %s
  namespace: %s
spec:
  credentialsRef:
    name: %s
`, name, namespace, defaultSecret)
}

// observation is the part of an Identity the tests assert on.
type observation struct {
	UID         string
	Generation  int64
	Ready       *metav1.Condition
	LastChecked *metav1.Time
}

func (o observation) String() string {
	if o.Ready == nil {
		return fmt.Sprintf("generation %d, no Ready condition", o.Generation)
	}
	return fmt.Sprintf("generation %d, Ready=%s %s (observed %d), checked %v", o.Generation, o.Ready.Status,
		o.Ready.Reason, o.Ready.ObservedGeneration, o.LastChecked)
}

func observe(namespace, name string) (observation, error) {
	out, err := kubectl("get", "nicoidentity", name, "-n", namespace, "-o", "json")
	if err != nil {
		return observation{}, err
	}
	var object infrav1.NicoIdentity
	if err := json.Unmarshal([]byte(out), &object); err != nil {
		return observation{}, err
	}
	result := observation{UID: string(object.UID), Generation: object.Generation,
		LastChecked: object.Status.LastCheckedTime}
	for i := range object.Status.Conditions {
		if object.Status.Conditions[i].Type == "Ready" {
			result.Ready = &object.Status.Conditions[i]
		}
	}
	return result, nil
}

// waitForResult waits for a current-generation Ready condition with the given
// status and reason whose check completed no earlier than notBefore. Status
// times have one-second precision, so notBefore is truncated to the second.
func waitForResult(namespace, name string, status metav1.ConditionStatus, reason string,
	notBefore time.Time, timeout time.Duration) observation {
	var result observation
	notBefore = notBefore.Truncate(time.Second)
	Eventually(func(g Gomega) {
		current, err := observe(namespace, name)
		g.Expect(err).NotTo(HaveOccurred())
		g.Expect(current.Ready).NotTo(BeNil(), "no Ready condition yet")
		g.Expect(current.Ready.Status).To(Equal(status), current.String())
		g.Expect(current.Ready.Reason).To(Equal(reason), current.String())
		g.Expect(current.Ready.ObservedGeneration).To(Equal(current.Generation), current.String())
		g.Expect(current.LastChecked).NotTo(BeNil())
		g.Expect(current.LastChecked.Time).NotTo(BeTemporally("<", notBefore), current.String())
		result = current
	}, timeout, time.Second).Should(Succeed())
	return result
}

func waitForHealthy(namespace, name string, notBefore time.Time) observation {
	return waitForResult(namespace, name, metav1.ConditionTrue, "ValidationSucceeded", notBefore, time.Minute)
}

// tokenRequestsSince counts token requests the fake NICo logged since t.
// Every Identity check requests a fresh token, so this counts checks.
func tokenRequestsSince(t time.Time) int {
	out, err := kubectl("logs", "deployment/fake-nico-api", "-n", fakeNamespace,
		"--since-time="+t.UTC().Format(time.RFC3339))
	Expect(err).NotTo(HaveOccurred())
	return strings.Count(out, "POST /token ")
}

// readyReasonRecorder records every Ready reason a watch delivers, so a
// result that is published and overwritten within a second is still seen.
type readyReasonRecorder struct {
	cmd     *exec.Cmd
	mu      sync.Mutex
	reasons []string
	done    chan struct{}
}

func recordReadyReasons(namespace, name string) *readyReasonRecorder {
	root, err := utils.GetProjectDir()
	Expect(err).NotTo(HaveOccurred())
	cmd := exec.Command("kubectl", "--context", kubeContext(), "get", "nicoidentity", name, "-n", namespace,
		"--watch", "-o", `jsonpath={.status.conditions[?(@.type=="Ready")].reason}{"\n"}`)
	cmd.Dir = root
	stdout, err := cmd.StdoutPipe()
	Expect(err).NotTo(HaveOccurred())
	Expect(cmd.Start()).To(Succeed())
	recorder := &readyReasonRecorder{cmd: cmd, done: make(chan struct{})}
	go func() {
		defer close(recorder.done)
		scanner := bufio.NewScanner(stdout)
		for scanner.Scan() {
			if line := strings.TrimSpace(scanner.Text()); line != "" {
				recorder.mu.Lock()
				recorder.reasons = append(recorder.reasons, line)
				recorder.mu.Unlock()
			}
		}
	}()
	return recorder
}

func (r *readyReasonRecorder) stop() []string {
	_ = r.cmd.Process.Kill()
	<-r.done
	_ = r.cmd.Wait()
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.reasons...)
}

// noTenantResources asserts that the installation has nothing to provision.
func noTenantResources() {
	for _, kind := range []string{"nicoclusters", "nicomachines", "clusters.cluster.x-k8s.io"} {
		out, err := kubectl("get", kind, "-A", "-o", "name")
		Expect(err).NotTo(HaveOccurred())
		Expect(utils.GetNonEmptyLines(out)).To(BeEmpty(), "expected no %s", kind)
	}
}

// writeDiagnostics prints the manager's logs and the Identities when a spec fails.
func writeDiagnostics() {
	if !CurrentSpecReport().Failed() {
		return
	}
	if out, err := kubectl("logs", "-n", managerNamespace, "-l", managerSelector, "--tail=200"); err == nil {
		_, _ = fmt.Fprintf(GinkgoWriter, "Manager logs:\n%s\n", out)
	}
	if out, err := kubectl("get", "nicoidentities", "-A", "-o", "yaml"); err == nil {
		_, _ = fmt.Fprintf(GinkgoWriter, "NicoIdentities:\n%s\n", out)
	}
	if out, err := kubectl("get", "events", "-n", managerNamespace, "--sort-by=.lastTimestamp"); err == nil {
		_, _ = fmt.Fprintf(GinkgoWriter, "Events in %s:\n%s\n", managerNamespace, out)
	}
}
