// SPDX-FileCopyrightText: Copyright (c) 2026 NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"errors"
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
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"
	"sigs.k8s.io/controller-runtime/pkg/webhook"

	infrav1 "github.com/dsx-ai-factory/cluster-api-provider-nico/api/v1alpha1"
	"github.com/dsx-ai-factory/cluster-api-provider-nico/controllers"
	"github.com/dsx-ai-factory/cluster-api-provider-nico/internal/nico"
	"github.com/dsx-ai-factory/cluster-api-provider-nico/internal/webhooks"
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
	var webhookPort int
	var webhookCertDir string
	var leaderElect bool
	var watchNamespace string
	var watchFilterValue string

	defaultProviderNamespace := os.Getenv(podNamespaceEnvVar)
	if defaultProviderNamespace == "" {
		defaultProviderNamespace = defaultProviderCredentialsNamespace
	}

	flag.StringVar(&metricsAddr, "metrics-bind-address", ":8080", "The address the metric endpoint binds to.")
	flag.StringVar(&probeAddr, "health-probe-bind-address", ":8081", "The address the probe endpoint binds to.")
	flag.IntVar(&webhookPort, "webhook-port", 9443, "Webhook server port.")
	flag.StringVar(
		&webhookCertDir,
		"webhook-cert-dir",
		"/tmp/k8s-webhook-server/serving-certs/",
		"Webhook certificate directory.",
	)
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
	if providerConfig.RebootAnnotation == nico.DefaultSoftRebootAnnotation ||
		providerConfig.RebootAnnotation == nico.DefaultHardRebootAnnotation {
		setupLog.Error(
			errors.New("reboot annotation key conflicts with an explicit reboot key"),
			"invalid provider configuration",
			"annotation", providerConfig.RebootAnnotation,
		)
		os.Exit(1)
	}
	if err := providerConfig.Validate(); err != nil {
		setupLog.Error(err, "invalid provider configuration")
		os.Exit(1)
	}

	setupLog.Info("provider configuration", "config", providerConfig)

	mgr, err := ctrl.NewManager(ctrl.GetConfigOrDie(), ctrl.Options{
		Scheme:                 scheme,
		Metrics:                metricsserver.Options{BindAddress: metricsAddr},
		HealthProbeBindAddress: probeAddr,
		LeaderElection:         leaderElect,
		LeaderElectionID:       "nico.infrastructure.cluster.x-k8s.io",
		Cache:                  cache.Options{DefaultNamespaces: watchNamespaces},
		WebhookServer: webhook.NewServer(webhook.Options{
			Port:    webhookPort,
			CertDir: webhookCertDir,
		}),
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

	// The NicoIdentity controller starts only when the API server serves its CRD,
	// so installations that manage CRDs separately still start without it.
	err = (&controllers.NicoIdentityReconciler{
		Client:           mgr.GetClient(),
		ProviderConfig:   providerConfig,
		WatchFilterValue: watchFilterValue,
	}).SetupWithManager(ctx, mgr)
	switch {
	case errors.Is(err, controllers.ErrNicoIdentityCRDMissing):
		setupLog.Info("NicoIdentity CRD is not installed; credential observation stays off " +
			"until it is installed and the manager restarts")
	case err != nil:
		setupLog.Error(err, "unable to create controller", "controller", "NicoIdentity")
		os.Exit(1)
	}

	// +kubebuilder:scaffold:builder
	if err := (&webhooks.NicoMachineTemplate{}).SetupWebhookWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create webhook", "webhook", "NicoMachineTemplate")
		os.Exit(1)
	}

	if err := mgr.AddHealthzCheck("webhook", mgr.GetWebhookServer().StartedChecker()); err != nil {
		setupLog.Error(err, "unable to set up health check")
		os.Exit(1)
	}
	if err := mgr.AddReadyzCheck("webhook", mgr.GetWebhookServer().StartedChecker()); err != nil {
		setupLog.Error(err, "unable to set up ready check")
		os.Exit(1)
	}

	setupLog.Info("starting manager")
	if err := mgr.Start(ctx); err != nil {
		setupLog.Error(err, "problem running manager")
		os.Exit(1)
	}
}
