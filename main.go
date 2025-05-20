package main

import (
	"context"
	"flag"
	"os"
	"os/signal"
	"syscall"

	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/klog/v2"

	"rollout-helper/internal/alertmanager"
	"rollout-helper/internal/watcher"
)

var (
	alertManagerURL = flag.String("alertmanager-url", "", "AlertManager URL")
	kubeconfig      = flag.String("kubeconfig", "", "Path to kubeconfig file")
	noAlertManager  = flag.Bool("no-alertmanager", false, "Run without AlertManager, just log state events")
)

func main() {
	klog.InitFlags(nil)
	flag.Parse()

	if !*noAlertManager && *alertManagerURL == "" {
		klog.Fatal("alertmanager-url flag is required when not using --no-alertmanager")
	}

	// Get alert manager token from environment
	alertManagerToken := os.Getenv("ALERTMNGR_TOKEN")
	if !*noAlertManager && alertManagerToken == "" {
		klog.Fatal("ALERTMNGR_TOKEN environment variable is required when not using --no-alertmanager")
	}

	// Create Kubernetes client
	var config *rest.Config
	var err error
	if *kubeconfig != "" {
		config, err = clientcmd.BuildConfigFromFlags("", *kubeconfig)
	} else {
		config, err = rest.InClusterConfig()
	}
	if err != nil {
		klog.Fatalf("Failed to create k8s config: %v", err)
	}

	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		klog.Fatalf("Failed to create k8s client: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Setup signal handling
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	// Initialize components
	var silenceManager *alertmanager.SilenceManager
	if !*noAlertManager {
		alertManagerClient := alertmanager.NewClient(*alertManagerURL, alertManagerToken)
		silenceManager = alertmanager.NewSilenceManager(alertManagerClient, clientset)
	}
	nodeWatcher := watcher.NewWatcher(clientset)

	// Start the watcher
	nodeWatcher.Start(ctx)

	// Process node state changes
	go func() {
		for state := range nodeWatcher.StateChannel() {
			if *noAlertManager {
				klog.Infof("Node state change - Node: %s, IsRolling: %v", state.Name, state.IsRolling)
			} else {
				if err := silenceManager.HandleNodeState(ctx, state.Name, state.IsRolling); err != nil {
					klog.Errorf("Failed to handle node state for %s: %v", state.Name, err)
				}
			}
		}
	}()

	klog.Info("Starting rollout helper...")

	// Wait for termination signal
	<-sigCh
	klog.Info("Shutting down...")
}
