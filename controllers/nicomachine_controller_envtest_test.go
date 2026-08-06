package controllers

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/onsi/ginkgo/v2"
	"github.com/onsi/gomega"

	nicosdk "github.com/NVIDIA/ncx-infra-controller-rest/sdk/standard"
	corev1 "k8s.io/api/core/v1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/serializer"
	"k8s.io/utils/ptr"
	clusterv1 "sigs.k8s.io/cluster-api/api/core/v1beta2"
	"sigs.k8s.io/cluster-api/util/conditions"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/config"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"

	infrav1 "gitlab-master.nvidia.com/nke/cluster-api-provider-nico/api/v1alpha1"
	"gitlab-master.nvidia.com/nke/cluster-api-provider-nico/internal/nico"
	nicofake "gitlab-master.nvidia.com/nke/cluster-api-provider-nico/internal/nico/fake"
	"gitlab-master.nvidia.com/nke/cluster-api-provider-nico/internal/test/fixtures"
)

var caseFakes sync.Map // case name -> *nicofake.Client

var _ = fixtures.DescribeCaseSet(nicoMachineCaseSet(
	"NicoMachine create reconciliation",
	"nicomachine-create-ready",
	nil,
	func(tc *fixtures.Case, _ fixtures.CaseSet) {
		ginkgo.It("reconciles the NicoMachine to Ready", func(ctx ginkgo.SpecContext) {
			gomega.Eventually(func(g gomega.Gomega) {
				assertNicoMachineReady(g, ctx, tc.Client, "nicomachine-1")
			}).WithTimeout(30 * time.Second).WithPolling(time.Second).Should(gomega.Succeed())

			fakeAPI := loadCaseFake(tc.Name)
			req := fakeAPI.LastCreateRequest()
			gomega.Expect(req).NotTo(gomega.BeNil())
			gomega.Expect(req.GetName()).To(gomega.Equal("machine-1"))
			gomega.Expect(req.GetTenantId()).To(gomega.Equal("tenant-1"))
			gomega.Expect(req.GetVpcId()).To(gomega.Equal("vpc-1"))
			gomega.Expect(req.GetMachineId()).To(gomega.Equal("machine-id-1"))
			gomega.Expect(req.GetUserData()).To(gomega.ContainSubstring("#cloud-config"))
			gomega.Expect(req.GetInterfaces()).To(gomega.HaveLen(1))
			gomega.Expect(req.GetInterfaces()[0].GetSubnetId()).To(gomega.Equal("subnet-1"))

			labels := fakeAPI.LastAppliedLabels()
			gomega.Expect(labels).To(gomega.HaveKeyWithValue(labelKeyMachineID, "machine-id-1"))
			gomega.Expect(labels).To(gomega.HaveKeyWithValue(labelKeySiteID, "site-1"))
			gomega.Expect(labels).To(gomega.HaveKeyWithValue(labelKeySiteName, "fake-site"))
			gomega.Expect(labels).To(gomega.HaveKeyWithValue(labelKeyVPCID, "vpc-1"))
			gomega.Expect(labels).To(gomega.HaveKeyWithValue(labelKeyVPCName, "fake-vpc"))
			gomega.Expect(labels).To(gomega.HaveKeyWithValue(clusterv1.ClusterNameLabel, "cluster-1"))
			gomega.Expect(labels).To(gomega.HaveKeyWithValue("cluster.x-k8s.io/machine-name", "machine-1"))
		})
	},
))

var _ = fixtures.DescribeCaseSet(nicoMachineCaseSet(
	"NicoMachine create already exists",
	"nicomachine-create-already-exists",
	func(fakeAPI *nicofake.Client) {
		fakeAPI.SeedInstance(seededReadyInstance("seeded-1", "machine-1"))
	},
	func(tc *fixtures.Case, _ fixtures.CaseSet) {
		ginkgo.It("recovers via FindInstanceByName and reaches Ready", func(ctx ginkgo.SpecContext) {
			gomega.Eventually(func(g gomega.Gomega) {
				assertNicoMachineReady(g, ctx, tc.Client, "nicomachine-1")
			}).WithTimeout(30 * time.Second).WithPolling(time.Second).Should(gomega.Succeed())

			fakeAPI := loadCaseFake(tc.Name)
			gomega.Expect(fakeAPI.LastCreateRequest()).NotTo(gomega.BeNil())
			gomega.Expect(fakeAPI.InstanceCount()).To(gomega.Equal(1))

			var current infrav1.NicoMachine
			gomega.Expect(tc.Client.Get(ctx, client.ObjectKey{Namespace: "test-ns", Name: "nicomachine-1"}, &current)).To(gomega.Succeed())
			gomega.Expect(current.Status.InstanceID).To(gomega.Equal("seeded-1"))
		})
	},
))

var _ = fixtures.DescribeCaseSet(nicoMachineCaseSet(
	"NicoMachine bootstrap secret late",
	"nicomachine-bootstrap-secret-late",
	nil,
	func(tc *fixtures.Case, _ fixtures.CaseSet) {
		ginkgo.It("waits for bootstrap data", func(ctx ginkgo.SpecContext) {
			gomega.Eventually(func(g gomega.Gomega) {
				assertNicoMachineReadyReason(g, ctx, tc.Client, "nicomachine-1", metav1.ConditionFalse, infrav1.WaitingForBootstrapDataReason)
			}).WithTimeout(30 * time.Second).WithPolling(time.Second).Should(gomega.Succeed())
			gomega.Expect(loadCaseFake(tc.Name).LastCreateRequest()).To(gomega.BeNil())
		})

		ginkgo.It("creates the bootstrap Secret", func(ctx ginkgo.SpecContext) {
			secret := &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "bootstrap-1",
					Namespace: "test-ns",
				},
				Type: corev1.SecretTypeOpaque,
				StringData: map[string]string{
					"value": "#cloud-config\n",
				},
			}
			gomega.Expect(tc.Client.Create(ctx, secret)).To(gomega.Succeed())
		})

		// Recovery is via RequeueAfter (machineRequeueFast), not a Secret watch.
		ginkgo.It("reaches Ready after bootstrap data arrives", func(ctx ginkgo.SpecContext) {
			gomega.Eventually(func(g gomega.Gomega) {
				assertNicoMachineReady(g, ctx, tc.Client, "nicomachine-1")
			}).WithTimeout(45 * time.Second).WithPolling(time.Second).Should(gomega.Succeed())
			gomega.Expect(loadCaseFake(tc.Name).LastCreateRequest()).NotTo(gomega.BeNil())
		})
	},
))

var _ = fixtures.DescribeCaseSet(nicoMachineCaseSet(
	"NicoMachine import existing",
	"nicomachine-import-existing",
	func(fakeAPI *nicofake.Client) {
		fakeAPI.SeedInstance(seededReadyInstance("imported-1", "imported-machine"))
	},
	func(tc *fixtures.Case, _ fixtures.CaseSet) {
		ginkgo.It("imports without CreateInstance", func(ctx ginkgo.SpecContext) {
			gomega.Eventually(func(g gomega.Gomega) {
				assertNicoMachineReady(g, ctx, tc.Client, "nicomachine-1")
			}).WithTimeout(30 * time.Second).WithPolling(time.Second).Should(gomega.Succeed())

			fakeAPI := loadCaseFake(tc.Name)
			gomega.Expect(fakeAPI.LastCreateRequest()).To(gomega.BeNil())
			gomega.Expect(fakeAPI.InstanceCount()).To(gomega.Equal(1))

			var current infrav1.NicoMachine
			gomega.Expect(tc.Client.Get(ctx, client.ObjectKey{Namespace: "test-ns", Name: "nicomachine-1"}, &current)).To(gomega.Succeed())
			gomega.Expect(current.Status.InstanceID).To(gomega.Equal("imported-1"))
			gomega.Expect(current.Spec.ProviderID).To(gomega.Equal(nico.ProviderID("imported-1")))
		})
	},
))

var _ = fixtures.DescribeCaseSet(nicoMachineCaseSet(
	"NicoMachine import already claimed",
	"nicomachine-import-already-claimed",
	func(fakeAPI *nicofake.Client) {
		fakeAPI.SeedInstance(seededReadyInstance("imported-1", "imported-machine"))
	},
	func(tc *fixtures.Case, _ fixtures.CaseSet) {
		ginkgo.It("keeps the claimer Ready", func(ctx ginkgo.SpecContext) {
			gomega.Eventually(func(g gomega.Gomega) {
				assertNicoMachineReady(g, ctx, tc.Client, "nicomachine-claimer")
			}).WithTimeout(30 * time.Second).WithPolling(time.Second).Should(gomega.Succeed())
		})

		ginkgo.It("marks the second import as InstanceAlreadyClaimed", func(ctx ginkgo.SpecContext) {
			gomega.Eventually(func(g gomega.Gomega) {
				assertNicoMachineReadyReason(g, ctx, tc.Client, "nicomachine-1", metav1.ConditionFalse, infrav1.InstanceAlreadyClaimedReason)
			}).WithTimeout(30 * time.Second).WithPolling(time.Second).Should(gomega.Succeed())
			gomega.Expect(loadCaseFake(tc.Name).LastCreateRequest()).To(gomega.BeNil())
		})
	},
))

var _ = fixtures.DescribeCaseSet(nicoMachineCaseSet(
	"NicoMachine delete reconciliation",
	"nicomachine-delete-",
	nil,
	func(tc *fixtures.Case, _ fixtures.CaseSet) {
		var instanceID string

		ginkgo.It("reconciles the NicoMachine to Ready", func(ctx ginkgo.SpecContext) {
			gomega.Eventually(func(g gomega.Gomega) {
				var current infrav1.NicoMachine
				g.Expect(tc.Client.Get(ctx, client.ObjectKey{Namespace: "test-ns", Name: "nicomachine-1"}, &current)).To(gomega.Succeed())
				g.Expect(current.Status.Ready).To(gomega.BeTrue())
				g.Expect(current.Status.InstanceID).NotTo(gomega.BeEmpty())
				instanceID = current.Status.InstanceID
			}).WithTimeout(30 * time.Second).WithPolling(time.Second).Should(gomega.Succeed())
		})

		ginkgo.It("deletes the NicoMachine", func(ctx ginkgo.SpecContext) {
			var current infrav1.NicoMachine
			gomega.Expect(tc.Client.Get(ctx, client.ObjectKey{Namespace: "test-ns", Name: "nicomachine-1"}, &current)).To(gomega.Succeed())
			gomega.Expect(tc.Client.Delete(ctx, &current)).To(gomega.Succeed())
		})

		ginkgo.It("removes the NicoMachine and NICo instance", func(ctx ginkgo.SpecContext) {
			gomega.Eventually(func(g gomega.Gomega) {
				err := tc.Client.Get(ctx, client.ObjectKey{Namespace: "test-ns", Name: "nicomachine-1"}, &infrav1.NicoMachine{})
				g.Expect(apierrors.IsNotFound(err)).To(gomega.BeTrue())
			}).WithTimeout(30 * time.Second).WithPolling(time.Second).Should(gomega.Succeed())

			_, err := loadCaseFake(tc.Name).GetInstance(ctx, instanceID)
			gomega.Expect(errors.Is(err, nico.ErrNotFound)).To(gomega.BeTrue())
		})
	},
))

func nicoMachineCaseSet(
	description, dirPrefix string,
	configureFake func(*nicofake.Client),
	defineSteps func(*fixtures.Case, fixtures.CaseSet),
) fixtures.CaseSet {
	return fixtures.CaseSet{
		Description:          description,
		DirPrefix:            dirPrefix,
		MaskExpectedMetadata: true,
		SchemeFn: func() *runtime.Scheme {
			scheme := runtime.NewScheme()
			gomega.Expect(corev1.AddToScheme(scheme)).To(gomega.Succeed())
			gomega.Expect(clusterv1.AddToScheme(scheme)).To(gomega.Succeed())
			gomega.Expect(infrav1.AddToScheme(scheme)).To(gomega.Succeed())
			return scheme
		},
		EnvironmentFn: func(*fixtures.Case) *envtest.Environment {
			return &envtest.Environment{
				CRDs:                  loadControllerCRDs(ginkgo.GinkgoTB()),
				ErrorIfCRDPathMissing: true,
			}
		},
		CompareObjects: func() []client.ObjectList {
			return []client.ObjectList{
				&infrav1.NicoMachineList{},
				&infrav1.NicoClusterList{},
			}
		},
		Setup: func(ctx ginkgo.SpecContext, tc *fixtures.Case, _ fixtures.CaseSet) {
			tc.Client = client.WithFieldOwner(tc.Client, "testutil")
			gomega.Expect(tc.CreateObjects(ctx)).To(gomega.Succeed())
			gomega.Expect(wireOwnerReferences(ctx, tc.Client, tc.Scheme)).To(gomega.Succeed())
			if tc.Name == "nicomachine-import-already-claimed" {
				gomega.Expect(markNicoMachineImported(ctx, tc.Client, "nicomachine-claimer", "imported-1")).To(gomega.Succeed())
			}

			fakeAPI := nicofake.New()
			fakeAPI.SeedVPC("vpc-1", "fake-vpc")
			if configureFake != nil {
				configureFake(fakeAPI)
			}
			caseFakes.Store(tc.Name, fakeAPI)

			mgr, err := manager.New(tc.Config, manager.Options{
				Scheme: tc.Scheme,
				Controller: config.Controller{
					SkipNameValidation: ptr.To(true),
				},
				Metrics: metricsserver.Options{BindAddress: "0"},
			})
			gomega.Expect(err).NotTo(gomega.HaveOccurred())

			factory := nicoClientFactory(func(_ context.Context, _ *corev1.Secret, _ nico.SecretConfig) (nico.API, error) {
				return fakeAPI, nil
			})
			gomega.Expect((&NicoClusterReconciler{
				Client:            mgr.GetClient(),
				Scheme:            mgr.GetScheme(),
				nicoClientFactory: factory,
			}).SetupWithManager(ctx, mgr)).To(gomega.Succeed())
			gomega.Expect((&NicoMachineReconciler{
				Client:            mgr.GetClient(),
				Scheme:            mgr.GetScheme(),
				nicoClientFactory: factory,
			}).SetupWithManager(ctx, mgr)).To(gomega.Succeed())
			tc.StartManager(ctx, mgr)
		},
		DefineSteps: defineSteps,
	}
}

func loadCaseFake(name string) *nicofake.Client {
	fakeAPI, ok := caseFakes.Load(name)
	gomega.Expect(ok).To(gomega.BeTrue())
	return fakeAPI.(*nicofake.Client)
}

func seededReadyInstance(id, name string) *nicosdk.Instance {
	inst := nicosdk.NewInstance()
	inst.SetId(id)
	inst.SetName(name)
	inst.SetTenantId("tenant-1")
	inst.SetVpcId("vpc-1")
	inst.SetSiteId("site-1")
	inst.SetMachineId("machine-id-1")
	inst.SetStatus(nicosdk.INSTANCESTATUS_READY)
	iface := nicosdk.NewInterface()
	iface.SetSubnetId("subnet-1")
	iface.SetIpAddresses([]string{"10.0.0.10"})
	inst.SetInterfaces([]nicosdk.Interface{*iface})
	return inst
}

func assertNicoMachineReady(g gomega.Gomega, ctx context.Context, c client.Client, name string) {
	ginkgo.GinkgoHelper()
	var current infrav1.NicoMachine
	g.Expect(c.Get(ctx, client.ObjectKey{Namespace: "test-ns", Name: name}, &current)).To(gomega.Succeed())
	cond := conditions.Get(&current, clusterv1.ReadyCondition)
	g.Expect(cond).NotTo(gomega.BeNil())
	g.Expect(cond.Status).To(gomega.Equal(metav1.ConditionTrue))
	g.Expect(cond.Reason).To(gomega.Equal(infrav1.InstanceReadyReason))
	g.Expect(current.Status.Ready).To(gomega.BeTrue())
	g.Expect(current.Status.InstanceID).NotTo(gomega.BeEmpty())
	g.Expect(current.Spec.ProviderID).To(gomega.Equal(nico.ProviderID(current.Status.InstanceID)))
	g.Expect(current.Status.Addresses).NotTo(gomega.BeEmpty())
}

func assertNicoMachineReadyReason(g gomega.Gomega, ctx context.Context, c client.Client, name string, status metav1.ConditionStatus, reason string) {
	ginkgo.GinkgoHelper()
	var current infrav1.NicoMachine
	g.Expect(c.Get(ctx, client.ObjectKey{Namespace: "test-ns", Name: name}, &current)).To(gomega.Succeed())
	cond := conditions.Get(&current, clusterv1.ReadyCondition)
	g.Expect(cond).NotTo(gomega.BeNil())
	g.Expect(cond.Status).To(gomega.Equal(status))
	g.Expect(cond.Reason).To(gomega.Equal(reason))
}

// wireOwnerReferences sets controller ownerRefs from Cluster/Machine infrastructureRefs.
func wireOwnerReferences(ctx context.Context, c client.Client, scheme *runtime.Scheme) error {
	var clusters clusterv1.ClusterList
	if err := c.List(ctx, &clusters, client.InNamespace("test-ns")); err != nil {
		return err
	}
	for i := range clusters.Items {
		cluster := &clusters.Items[i]
		ref := cluster.Spec.InfrastructureRef
		if ref.Name == "" {
			continue
		}
		var nicoCluster infrav1.NicoCluster
		if err := c.Get(ctx, client.ObjectKey{Namespace: cluster.Namespace, Name: ref.Name}, &nicoCluster); err != nil {
			return err
		}
		if err := controllerutil.SetControllerReference(cluster, &nicoCluster, scheme); err != nil {
			return err
		}
		if err := c.Update(ctx, &nicoCluster); err != nil {
			return err
		}
	}

	var machines clusterv1.MachineList
	if err := c.List(ctx, &machines, client.InNamespace("test-ns")); err != nil {
		return err
	}
	for i := range machines.Items {
		machine := &machines.Items[i]
		ref := machine.Spec.InfrastructureRef
		if ref.Name == "" {
			continue
		}
		var nicoMachine infrav1.NicoMachine
		if err := c.Get(ctx, client.ObjectKey{Namespace: machine.Namespace, Name: ref.Name}, &nicoMachine); err != nil {
			return err
		}
		if err := controllerutil.SetControllerReference(machine, &nicoMachine, scheme); err != nil {
			return err
		}
		if err := c.Update(ctx, &nicoMachine); err != nil {
			return err
		}
	}
	return nil
}

func markNicoMachineImported(ctx context.Context, c client.Client, name, instanceID string) error {
	var nicoMachine infrav1.NicoMachine
	if err := c.Get(ctx, client.ObjectKey{Namespace: "test-ns", Name: name}, &nicoMachine); err != nil {
		return err
	}
	nicoMachine.Status.InstanceID = instanceID
	return c.Status().Update(ctx, &nicoMachine)
}

func loadControllerCRDs(t testing.TB) []*apiextensionsv1.CustomResourceDefinition {
	t.Helper()

	capiDir := moduleDir(t, "sigs.k8s.io/cluster-api")
	paths := []string{
		filepath.Join("..", "config", "crd", "bases", "infrastructure.cluster.x-k8s.io_nicoclusters.yaml"),
		filepath.Join("..", "config", "crd", "bases", "infrastructure.cluster.x-k8s.io_nicomachines.yaml"),
		filepath.Join(capiDir, "config", "crd", "bases", "cluster.x-k8s.io_clusters.yaml"),
		filepath.Join(capiDir, "config", "crd", "bases", "cluster.x-k8s.io_machines.yaml"),
	}

	crdScheme := runtime.NewScheme()
	gomega.Expect(apiextensionsv1.AddToScheme(crdScheme)).To(gomega.Succeed())
	decoder := serializer.NewCodecFactory(crdScheme).UniversalDeserializer()

	crds := make([]*apiextensionsv1.CustomResourceDefinition, 0, len(paths))
	for _, path := range paths {
		content, err := os.ReadFile(path)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), "read CRD %s", path)
		obj, _, err := decoder.Decode(content, nil, nil)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), "decode CRD %s", path)
		crd, ok := obj.(*apiextensionsv1.CustomResourceDefinition)
		gomega.Expect(ok).To(gomega.BeTrue(), "CRD type for %s", path)
		crds = append(crds, crd)
	}
	return crds
}

func moduleDir(t testing.TB, module string) string {
	t.Helper()
	output, err := exec.Command("go", "list", "-m", "-f", "{{.Dir}}", module).Output()
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), "go list -m %s", module)
	return strings.TrimSpace(string(output))
}
