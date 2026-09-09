// SPDX-FileCopyrightText: Copyright (c) 2026 NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"flag"
	"os"

	corev1 "k8s.io/api/core/v1"
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

	infrav1 "github.com/NVIDIA/cluster-api-provider-nico/api/v1alpha1"
	"github.com/NVIDIA/cluster-api-provider-nico/controllers"
	"github.com/NVIDIA/cluster-api-provider-nico/internal/nico"
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

func main() {
	var metricsAddr string
	var probeAddr string
	var leaderElect bool
	var watchNamespace string
	var watchFilterValue string

	defaultProviderNamespace := os.Getenv(podNamespaceEnvVar)
	if defaultProviderNamespace == "" {
		defaultProviderNamespace = defaultProviderCredentialsNamespace
	}

	flag.StringVar(&metricsAddr, "metrics-bind-address", ":8080", "The address the metric endpoint binds to.")
	flag.StringVar(&probeAddr, "health-probe-bind-address", ":8081", "The address the probe endpoint binds to.")
	flag.BoolVar(&leaderElect, "leader-elect", false, "Enable leader election for the controller manager.")
	flag.StringVar(
		&watchNamespace,
		"namespace",
		"",
		"Namespace that the controller watches to reconcile Cluster API objects. "+
			"If unspecified, the controller watches all namespaces.",
	)
	flag.StringVar(
		&watchFilterValue,
		"watch-filter",
		"",
		"Label value that the controller watches to reconcile Cluster API objects. "+
			"The label key is cluster.x-k8s.io/watch-filter. "+
			"If unspecified, the controller watches all objects.",
	)

	providerConfig := nico.ProviderConfig{
		Credentials: types.NamespacedName{
			Namespace: defaultProviderNamespace,
			Name:      defaultProviderCredentialsSecretName,
		},
	}
	providerConfig.BindFlags(flag.CommandLine)

	zapOpts := zap.Options{}
	zapOpts.BindFlags(flag.CommandLine)

	flag.Parse()

	var watchNamespaces map[string]cache.Config
	if watchNamespace != "" {
		watchNamespaces = map[string]cache.Config{watchNamespace: {}}
	}

	ctrl.SetLogger(zap.New(zap.UseFlagOptions(&zapOpts)))

	setupLog.Info("provider configuration", "config", providerConfig)

	mgr, err := ctrl.NewManager(ctrl.GetConfigOrDie(), ctrl.Options{
		Scheme:                 scheme,
		Metrics:                metricsserver.Options{BindAddress: metricsAddr},
		HealthProbeBindAddress: probeAddr,
		LeaderElection:         leaderElect,
		LeaderElectionID:       "nico.infrastructure.cluster.x-k8s.io",
		Cache:                  cache.Options{DefaultNamespaces: watchNamespaces},
		Client: client.Options{
			Cache: &client.CacheOptions{DisableFor: []client.Object{&corev1.Secret{}}},
		},
	})
	if err != nil {
		setupLog.Error(err, "unable to start manager")
		os.Exit(1)
	}
	ctx := ctrl.SetupSignalHandler()

	if err := (&controllers.NicoClusterReconciler{
		Client:           mgr.GetClient(),
		Scheme:           mgr.GetScheme(),
		ProviderConfig:   providerConfig,
		WatchFilterValue: watchFilterValue,
	}).SetupWithManager(ctx, mgr); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "NicoCluster")
		os.Exit(1)
	}

	if err := (&controllers.NicoMachineReconciler{
		Client:           mgr.GetClient(),
		Scheme:           mgr.GetScheme(),
		ProviderConfig:   providerConfig,
		WatchFilterValue: watchFilterValue,
	}).SetupWithManager(ctx, mgr); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "NicoMachine")
		os.Exit(1)
	}

	// +kubebuilder:scaffold:builder

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
