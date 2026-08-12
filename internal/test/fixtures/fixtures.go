// SPDX-FileCopyrightText: Copyright (c) 2026 NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

// Package fixtures registers file-backed envtest cases as Ginkgo specs.
package fixtures

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/onsi/ginkgo/v2"
	"github.com/onsi/gomega"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	runtimejson "k8s.io/apimachinery/pkg/runtime/serializer/json"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/rest"

	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/yaml"

	"github.com/NVIDIA/cluster-api-provider-nico/internal/test/matchers"
)

const (
	defaultClientInputSuffix = "_client_objects.yaml"
	updateExpectedEnv        = "TESTUTIL_UPDATE_EXPECTED"
)

// CaseSet configures a group of file-backed Ginkgo testcases.
type CaseSet struct {
	// Description names the outer Ginkgo container.
	Description string
	// DirPrefix selects testcase directories under testdata.
	DirPrefix string
	// MaskExpectedMetadata normalizes volatile Kubernetes metadata before comparison.
	MaskExpectedMetadata bool
	// SchemeFn creates an isolated runtime Scheme for each testcase.
	SchemeFn func() *runtime.Scheme
	// EnvironmentFn creates an isolated envtest environment for each testcase.
	EnvironmentFn func(*Case) *envtest.Environment
	// CompareObjects returns fresh object lists collected for the golden output.
	CompareObjects func() []client.ObjectList
	// Setup initializes the testcase once before its ordered steps run.
	Setup func(ginkgo.SpecContext, *Case, CaseSet)
	// DefineSteps registers the ordered Ginkgo It nodes for each testcase.
	DefineSteps func(*Case, CaseSet)
}

type Case struct {
	Name             string
	Inputs           map[string]string
	ExpectedFilepath string
	Scheme           *runtime.Scheme
	Environment      *envtest.Environment
	Config           *rest.Config
	Client           client.Client

	mu                 sync.Mutex
	managerCancel      context.CancelFunc
	managerDone        chan error
	environmentStopped bool
}

func DescribeCaseSet(set CaseSet) bool {
	validateCaseSet(set)
	cases, err := discoverCases("testdata", set.DirPrefix)
	if err != nil {
		panic(err)
	}

	return ginkgo.Describe(set.Description, func() {
		for _, tc := range cases {
			ginkgo.Context(tc.Name, ginkgo.Ordered, func() {
				ginkgo.BeforeAll(func(ctx ginkgo.SpecContext) {
					tc.Scheme = set.SchemeFn()
					gomega.Expect(tc.Scheme).NotTo(gomega.BeNil())

					tc.Environment = set.EnvironmentFn(tc)
					gomega.Expect(tc.Environment).NotTo(gomega.BeNil())
					gomega.Expect(os.Getenv("KUBEBUILDER_ASSETS")).NotTo(gomega.BeEmpty(),
						"KUBEBUILDER_ASSETS must point to envtest binaries")

					tc.Config, err = tc.Environment.Start()
					gomega.Expect(err).NotTo(gomega.HaveOccurred())
					ginkgo.DeferCleanup(tc.StopEnvironment, ginkgo.NodeTimeout(time.Minute))

					if tc.Config.QPS == 0 {
						tc.Config.QPS = 100
					}
					if tc.Config.Burst == 0 {
						tc.Config.Burst = 200
					}

					tc.Client, err = client.New(tc.Config, client.Options{Scheme: tc.Scheme})
					gomega.Expect(err).NotTo(gomega.HaveOccurred())

					set.Setup(ctx, tc, set)
				})

				set.DefineSteps(tc, set)

				ginkgo.It("matches the expected objects", func(ctx ginkgo.SpecContext) {
					compareOrUpdateObjects(ctx, tc, set.CompareObjects(), set.MaskExpectedMetadata)
				})
			})
		}
	})
}

func (c *Case) HasInput(name string) bool {
	_, ok := c.Inputs[name]
	return ok
}

func (c *Case) StartManager(ctx context.Context, mgr manager.Manager) {
	c.mu.Lock()
	gomega.Expect(c.managerCancel).To(gomega.BeNil(), "manager already started")

	managerCtx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	c.managerCancel = cancel
	c.managerDone = done
	c.mu.Unlock()

	go func() {
		done <- mgr.Start(managerCtx)
		close(done)
	}()

	select {
	case <-mgr.Elected():
		ginkgo.DeferCleanup(c.StopManager, ginkgo.NodeTimeout(time.Minute))
	case err := <-done:
		cancel()
		c.clearManager()
		ginkgo.Fail(fmt.Sprintf("manager stopped before becoming ready: %v", err))
	case <-ctx.Done():
		cancel()
		c.clearManager()
		gomega.Expect(ctx.Err()).NotTo(gomega.HaveOccurred())
	}
}

func (c *Case) StopManager(ctx context.Context) error {
	c.mu.Lock()
	cancel := c.managerCancel
	done := c.managerDone
	c.managerCancel = nil
	c.managerDone = nil
	c.mu.Unlock()

	if cancel == nil || done == nil {
		return nil
	}

	cancel()
	select {
	case err := <-done:
		return err
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (c *Case) StopEnvironment(ctx context.Context) error {
	c.mu.Lock()
	if c.environmentStopped || c.Environment == nil {
		c.mu.Unlock()
		return nil
	}
	c.environmentStopped = true
	environment := c.Environment
	c.mu.Unlock()

	done := make(chan error, 1)
	go func() {
		done <- stopEnvironment(environment)
	}()

	select {
	case err := <-done:
		return err
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (c *Case) CreateObjects(ctx context.Context) error {
	objects, err := c.objectsFromInputs(defaultClientInputSuffix)
	if err != nil {
		return err
	}

	for _, object := range objects {
		if err := c.Client.Create(ctx, object); err != nil {
			return fmt.Errorf("create %T %s: %w", object, client.ObjectKeyFromObject(object), err)
		}
	}
	return nil
}

func (c *Case) PatchObjects(ctx context.Context, filename string) error {
	objects, err := c.objectsFromFile(filename)
	if err != nil {
		return err
	}

	for _, object := range objects {
		objectMap, err := runtime.DefaultUnstructuredConverter.ToUnstructured(object)
		if err != nil {
			return fmt.Errorf("convert %s to unstructured: %w", client.ObjectKeyFromObject(object), err)
		}
		patch, err := json.Marshal(objectMap)
		if err != nil {
			return fmt.Errorf("marshal patch for %s: %w", client.ObjectKeyFromObject(object), err)
		}
		if err := c.Client.Patch(ctx, object, client.RawPatch(types.MergePatchType, patch)); err != nil {
			return fmt.Errorf("patch %T %s: %w", object, client.ObjectKeyFromObject(object), err)
		}
	}
	return nil
}

func (c *Case) DeleteObjects(ctx context.Context, filename string) ([]client.Object, error) {
	objects, err := c.objectsFromFile(filename)
	if err != nil {
		return nil, err
	}

	for _, object := range objects {
		if err := c.Client.Delete(ctx, object); err != nil {
			return nil, fmt.Errorf("delete %T %s: %w", object, client.ObjectKeyFromObject(object), err)
		}
	}
	return objects, nil
}

func validateCaseSet(set CaseSet) {
	if strings.TrimSpace(set.Description) == "" {
		panic("fixtures: CaseSet.Description is required")
	}
	if set.SchemeFn == nil {
		panic(fmt.Sprintf("fixtures: SchemeFn is required for %q", set.Description))
	}
	if set.EnvironmentFn == nil {
		panic(fmt.Sprintf("fixtures: EnvironmentFn is required for %q", set.Description))
	}
	if set.CompareObjects == nil {
		panic(fmt.Sprintf("fixtures: CompareObjects is required for %q", set.Description))
	}
	if set.Setup == nil {
		panic(fmt.Sprintf("fixtures: Setup is required for %q", set.Description))
	}
	if set.DefineSteps == nil {
		panic(fmt.Sprintf("fixtures: DefineSteps is required for %q", set.Description))
	}
}

func discoverCases(root, prefix string) ([]*Case, error) {
	root, err := filepath.Abs(root)
	if err != nil {
		return nil, fmt.Errorf("resolve testdata directory: %w", err)
	}

	var cases []*Case
	names := map[string]struct{}{}
	expectedPaths := map[string]struct{}{}
	err = filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if !entry.IsDir() || prefix != "" && !strings.HasPrefix(entry.Name(), prefix) {
			return nil
		}

		files, err := os.ReadDir(path)
		if err != nil {
			return err
		}
		inputs := map[string]string{}
		for _, file := range files {
			if file.IsDir() || !strings.HasPrefix(file.Name(), "input") {
				continue
			}
			content, err := os.ReadFile(filepath.Join(path, file.Name()))
			if err != nil {
				return err
			}
			inputs[file.Name()] = string(content)
		}
		if len(inputs) == 0 {
			return nil
		}

		name, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		expectedPath := filepath.Join(path, "expected_objects.yaml")
		if _, exists := names[name]; exists {
			return fmt.Errorf("duplicate fixture name %q", name)
		}
		if _, exists := expectedPaths[expectedPath]; exists {
			return fmt.Errorf("duplicate fixture expected path %q", expectedPath)
		}
		names[name] = struct{}{}
		expectedPaths[expectedPath] = struct{}{}
		cases = append(cases, &Case{
			Name:             name,
			Inputs:           inputs,
			ExpectedFilepath: expectedPath,
		})
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("discover fixtures in %s: %w", root, err)
	}
	return cases, nil
}

func (c *Case) objectsFromInputs(suffix string) ([]client.Object, error) {
	filenames := make([]string, 0, len(c.Inputs))
	for filename := range c.Inputs {
		if strings.HasSuffix(filename, suffix) {
			filenames = append(filenames, filename)
		}
	}
	sort.Strings(filenames)

	var objects []client.Object
	for _, filename := range filenames {
		decoded, err := c.decodeObjects(filename, c.Inputs[filename])
		if err != nil {
			return nil, err
		}
		objects = append(objects, decoded...)
	}
	if len(objects) == 0 {
		return nil, errors.New("no input objects found")
	}
	return objects, nil
}

func (c *Case) objectsFromFile(filename string) ([]client.Object, error) {
	data, ok := c.Inputs[filename]
	if !ok {
		return nil, fmt.Errorf("input file %q not found", filename)
	}
	return c.decodeObjects(filename, data)
}

func (c *Case) decodeObjects(filename, data string) ([]client.Object, error) {
	serializer := runtimejson.NewSerializer(runtimejson.DefaultMetaFactory, c.Scheme, c.Scheme, false)
	var objects []client.Object

	for document := range strings.SplitSeq(data, "\n---\n") {
		document = strings.TrimSpace(document)
		if document == "" {
			continue
		}

		content := []byte(document)
		if strings.HasSuffix(filename, ".yaml") {
			value := map[string]any{}
			if err := yaml.Unmarshal(content, &value); err != nil {
				return nil, fmt.Errorf("decode YAML %s: %w", filename, err)
			}
			var err error
			content, err = json.Marshal(value)
			if err != nil {
				return nil, fmt.Errorf("convert YAML %s to JSON: %w", filename, err)
			}
		}

		decoded, gvk, err := serializer.Decode(content, nil, nil)
		if err != nil {
			return nil, fmt.Errorf("decode object from %s: %w", filename, err)
		}
		if unstructuredObject, ok := decoded.(*unstructured.Unstructured); ok && strings.HasSuffix(gvk.Kind, "List") {
			return nil, fmt.Errorf("object lists are not supported in %s: %s", filename, unstructuredObject.GetKind())
		}
		object, ok := decoded.(client.Object)
		if !ok {
			return nil, fmt.Errorf("decoded value from %s is not a Kubernetes object: %T", filename, decoded)
		}
		objects = append(objects, object)
	}

	if len(objects) == 0 {
		return nil, fmt.Errorf("no input objects found in %s", filename)
	}
	return objects, nil
}

func compareOrUpdateObjects(ctx context.Context, tc *Case, compareObjects []client.ObjectList, maskExpectedMetadata bool) {
	actual, err := collectObjects(ctx, tc, compareObjects, maskExpectedMetadata)
	gomega.Expect(err).NotTo(gomega.HaveOccurred())

	newPath := tc.ExpectedFilepath + ".new"
	if os.Getenv(updateExpectedEnv) == "true" {
		gomega.Expect(os.WriteFile(tc.ExpectedFilepath, []byte(actual), 0o600)).To(gomega.Succeed())
		gomega.Expect(removeIfPresent(newPath)).To(gomega.Succeed())
		return
	}

	expectedBytes, err := os.ReadFile(tc.ExpectedFilepath)
	if os.IsNotExist(err) {
		expectedBytes = nil
	} else {
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
	}

	gomega.Expect(os.WriteFile(newPath, []byte(actual), 0o600)).To(gomega.Succeed())
	gomega.Expect(actual).To(matchers.MatchGolden(string(expectedBytes), tc.ExpectedFilepath, newPath))
	gomega.Expect(removeIfPresent(newPath)).To(gomega.Succeed())
}

func collectObjects(ctx context.Context, tc *Case, compareObjects []client.ObjectList, maskExpectedMetadata bool) (string, error) {
	var actual strings.Builder
	for _, list := range compareObjects {
		if list == nil {
			return "", errors.New("CompareObjects returned a nil object list")
		}
		list = list.DeepCopyObject().(client.ObjectList)
		if err := tc.Client.List(ctx, list); err != nil {
			return "", fmt.Errorf("list %T: %w", list, err)
		}

		unstructuredList := &unstructured.UnstructuredList{}
		if err := tc.Scheme.Convert(list, unstructuredList, nil); err != nil {
			return "", fmt.Errorf("convert %T to unstructured list: %w", list, err)
		}
		if maskExpectedMetadata {
			for i := range unstructuredList.Items {
				if err := maskObjectMetadata(&unstructuredList.Items[i]); err != nil {
					return "", err
				}
			}
			unstructured.RemoveNestedField(unstructuredList.Object, "metadata", "resourceVersion")
			unstructured.RemoveNestedField(unstructuredList.Object, "kind")
			unstructured.RemoveNestedField(unstructuredList.Object, "apiVersion")
		}

		sort.Slice(unstructuredList.Items, func(i, j int) bool {
			leftNS, leftName := unstructuredList.Items[i].GetNamespace(), unstructuredList.Items[i].GetName()
			rightNS, rightName := unstructuredList.Items[j].GetNamespace(), unstructuredList.Items[j].GetName()
			if leftNS != rightNS {
				return leftNS < rightNS
			}
			return leftName < rightName
		})

		content, err := yaml.Marshal(unstructuredList)
		if err != nil {
			return "", fmt.Errorf("marshal %T: %w", list, err)
		}
		actual.WriteString("---\n")
		actual.Write(content)
	}
	return actual.String(), nil
}

func maskObjectMetadata(object *unstructured.Unstructured) error {
	object.SetUID("")
	object.SetManagedFields(nil)
	object.SetCreationTimestamp(metav1.Time{})
	object.SetResourceVersion("")
	if err := unstructured.SetNestedField(object.Object, nil, "metadata", "creationTimestamp"); err != nil {
		return fmt.Errorf("normalize creationTimestamp for %s: %w", object.GetName(), err)
	}

	if !object.GetDeletionTimestamp().IsZero() {
		timestamp := metav1.NewTime(time.Unix(0, 1))
		object.SetDeletionTimestamp(&timestamp)
	}

	owners := object.GetOwnerReferences()
	for i := range owners {
		if owners[i].UID != "" {
			owners[i].UID = "00000000-0000-0000-0000-000000000000"
		}
	}
	object.SetOwnerReferences(owners)

	conditions, found, err := unstructured.NestedSlice(object.Object, "status", "conditions")
	if err != nil {
		return fmt.Errorf("read conditions for %s: %w", object.GetName(), err)
	}
	if !found && len(conditions) == 0 {
		return nil
	}
	for i := range conditions {
		condition, ok := conditions[i].(map[string]any)
		if !ok {
			return fmt.Errorf("condition %d for %s is %T", i, object.GetName(), conditions[i])
		}
		if err := unstructured.SetNestedField(condition, "1970-01-01T00:00:00Z", "lastTransitionTime"); err != nil {
			return fmt.Errorf("normalize condition %d for %s: %w", i, object.GetName(), err)
		}
	}
	sort.Slice(conditions, func(i, j int) bool {
		left, _, _ := unstructured.NestedString(conditions[i].(map[string]any), "type")
		right, _, _ := unstructured.NestedString(conditions[j].(map[string]any), "type")
		return left < right
	})
	if err := unstructured.SetNestedSlice(object.Object, conditions, "status", "conditions"); err != nil {
		return fmt.Errorf("write conditions for %s: %w", object.GetName(), err)
	}
	return nil
}

func removeIfPresent(path string) error {
	err := os.Remove(path)
	if os.IsNotExist(err) {
		return nil
	}
	return err
}

func stopEnvironment(environment *envtest.Environment) (err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			err = fmt.Errorf("stop envtest: %v", recovered)
		}
	}()
	return environment.Stop()
}

func (c *Case) clearManager() {
	c.mu.Lock()
	c.managerCancel = nil
	c.managerDone = nil
	c.mu.Unlock()
}
