// SPDX-FileCopyrightText: Copyright (c) 2026 NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	clusterv1 "sigs.k8s.io/cluster-api/api/core/v1beta2"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"

	infrav1 "github.com/dsx-ai-factory/cluster-api-provider-nico/api/v1alpha1"
	"github.com/dsx-ai-factory/cluster-api-provider-nico/controllers"
	"github.com/dsx-ai-factory/cluster-api-provider-nico/internal/nico"
	// +kubebuilder:scaffold:imports
)

const (
	defaultProviderCredentialsNamespace  = "capnico-system"
	defaultProviderCredentialsSecretName = "nico-credentials"
	podNamespaceEnvVar                   = "POD_NAMESPACE"
)

var (
	scheme   = runtime.NewScheme()
	setupLog = ctrl.Log.WithName("setup")
)

func init() {
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))
	utilruntime.Must(clusterv1.AddToScheme(scheme))
	utilruntime.Must(infrav1.AddToScheme(scheme))
	// +kubebuilder:scaffold:scheme
}

// managerConfig holds the manager settings parsed from flags.
type managerConfig struct {
	metricsAddr      string
	probeAddr        string
	leaderElect      bool
	watchNamespace   string
	watchFilterValue string
	provider         nico.ProviderConfig
	zapOptions       zap.Options
	// identityCheckInterval shortens the Identity recheck for runtime tests. It
	// has no flag; zero keeps the controller's five-minute default.
	identityCheckInterval time.Duration
}

// bindFlags registers the manager flags on fs. The provider credentials
// namespace defaults to podNamespace, or capnico-system when that is empty.
func bindFlags(fs *flag.FlagSet, podNamespace string) *managerConfig {
	if podNamespace == "" {
		podNamespace = defaultProviderCredentialsNamespace
	}
	cfg := &managerConfig{
		provider: nico.ProviderConfig{
			Credentials: types.NamespacedName{
				Namespace: podNamespace,
				Name:      defaultProviderCredentialsSecretName,
			},
		},
	}

	fs.StringVar(&cfg.metricsAddr, "metrics-bind-address", ":8080", "The address the metric endpoint binds to.")
	fs.StringVar(&cfg.probeAddr, "health-probe-bind-address", ":8081", "The address the probe endpoint binds to.")
	fs.BoolVar(&cfg.leaderElect, "leader-elect", false, "Enable leader election for the controller manager.")
	fs.StringVar(
		&cfg.watchNamespace,
		"namespace",
		"",
		"Namespace that the controller watches to reconcile Cluster API objects. "+
			"If unspecified, the controller watches all namespaces.",
	)
	fs.StringVar(
		&cfg.watchFilterValue,
		"watch-filter",
		"",
		"Label value that the controller watches to reconcile Cluster API objects. "+
			"The label key is cluster.x-k8s.io/watch-filter. "+
			"If unspecified, the controller watches all objects.",
	)
	cfg.provider.BindFlags(fs)
	cfg.zapOptions.BindFlags(fs)
	return cfg
}

// managerOptions builds the manager options. The selected NicoIdentity gets its
// own cache scope so --namespace cannot hide it and no other Identity is cached.
func (cfg *managerConfig) managerOptions() ctrl.Options {
	var watchNamespaces map[string]cache.Config
	if cfg.watchNamespace != "" {
		watchNamespaces = map[string]cache.Config{cfg.watchNamespace: {}}
	}
	cacheOptions := cache.Options{DefaultNamespaces: watchNamespaces}
	if cfg.provider.IdentityName != "" {
		cacheOptions.ByObject = map[client.Object]cache.ByObject{
			&infrav1.NicoIdentity{}: {
				Namespaces: map[string]cache.Config{cfg.provider.Credentials.Namespace: {}},
				Field:      fields.OneTermEqualSelector("metadata.name", cfg.provider.IdentityName),
			},
		}
	}

	return ctrl.Options{
		Scheme:                 scheme,
		Metrics:                metricsserver.Options{BindAddress: cfg.metricsAddr},
		HealthProbeBindAddress: cfg.probeAddr,
		LeaderElection:         cfg.leaderElect,
		LeaderElectionID:       "nico.infrastructure.cluster.x-k8s.io",
		Cache:                  cacheOptions,
		Client: client.Options{
			Cache: &client.CacheOptions{DisableFor: []client.Object{&corev1.Secret{}}},
		},
	}
}

// explainManagerError names a missing NicoIdentity CRD, which the Identity
// cache scope needs when --provider-identity-name selects an Identity.
func explainManagerError(cfg *managerConfig, err error) error {
	if cfg.provider.IdentityName != "" && meta.IsNoMatchError(err) {
		return fmt.Errorf("%w: %w", controllers.ErrNicoIdentityCRDMissing, err)
	}
	return err
}

// setupReconcilers registers the provider controllers. The NicoIdentity
// controller starts only when --provider-identity-name selects an Identity.
func setupReconcilers(ctx context.Context, mgr ctrl.Manager, cfg *managerConfig) error {
	if err := (&controllers.NicoClusterReconciler{
		Client:           mgr.GetClient(),
		Scheme:           mgr.GetScheme(),
		ProviderConfig:   cfg.provider,
		WatchFilterValue: cfg.watchFilterValue,
	}).SetupWithManager(ctx, mgr); err != nil {
		return fmt.Errorf("NicoCluster controller: %w", err)
	}

	if err := (&controllers.NicoMachineReconciler{
		Client:           mgr.GetClient(),
		Scheme:           mgr.GetScheme(),
		ProviderConfig:   cfg.provider,
		WatchFilterValue: cfg.watchFilterValue,
	}).SetupWithManager(ctx, mgr); err != nil {
		return fmt.Errorf("NicoMachine controller: %w", err)
	}

	if cfg.provider.IdentityName != "" {
		if err := (&controllers.NicoIdentityReconciler{
			Client:         mgr.GetClient(),
			ProviderConfig: cfg.provider,
			CheckInterval:  cfg.identityCheckInterval,
		}).SetupWithManager(mgr); err != nil {
			return fmt.Errorf("NicoIdentity controller: %w", err)
		}
	}
	// +kubebuilder:scaffold:builder
	return nil
}

func main() {
	cfg := bindFlags(flag.CommandLine, os.Getenv(podNamespaceEnvVar))
	flag.Parse()

	ctrl.SetLogger(zap.New(zap.UseFlagOptions(&cfg.zapOptions)))
	if cfg.provider.RebootAnnotation == nico.DefaultSoftRebootAnnotation ||
		cfg.provider.RebootAnnotation == nico.DefaultHardRebootAnnotation {
		setupLog.Error(
			errors.New("reboot annotation key conflicts with an explicit reboot key"),
			"invalid provider configuration",
			"annotation", cfg.provider.RebootAnnotation,
		)
		os.Exit(1)
	}
	if err := cfg.provider.Validate(); err != nil {
		setupLog.Error(err, "invalid provider configuration")
		os.Exit(1)
	}

	setupLog.Info("provider configuration", "config", cfg.provider)

	mgr, err := ctrl.NewManager(ctrl.GetConfigOrDie(), cfg.managerOptions())
	if err != nil {
		setupLog.Error(explainManagerError(cfg, err), "unable to start manager")
		os.Exit(1)
	}
	ctx := ctrl.SetupSignalHandler()

	if err := setupReconcilers(ctx, mgr, cfg); err != nil {
		setupLog.Error(err, "unable to create controller")
		os.Exit(1)
	}

	if err := mgr.AddHealthzCheck("healthz", healthz.Ping); err != nil {
		setupLog.Error(err, "unable to set up health check")
		os.Exit(1)
	}
	if err := mgr.AddReadyzCheck("readyz", healthz.Ping); err != nil {
		setupLog.Error(err, "unable to set up ready check")
		os.Exit(1)
	}

	setupLog.Info("starting manager")
	if err := mgr.Start(ctx); err != nil {
		setupLog.Error(err, "problem running manager")
		os.Exit(1)
	}
}
