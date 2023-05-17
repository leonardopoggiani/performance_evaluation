package main

import (
	"context"
	"fmt"
	"os"

	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"

	migration_operator "github.com/leonardopoggiani/live-migration-operator/controllers"
)

func main() {
	fmt.Println("Receiver program, waiting for migration request")

	// Load Kubernetes config
	kubeconfigPath := os.Getenv("KUBECONFIG")
	if kubeconfigPath == "" {
		kubeconfigPath = "~/.kube/config"
	}

	kubeconfigPath = os.ExpandEnv(kubeconfigPath)
	if _, err := os.Stat(kubeconfigPath); os.IsNotExist(err) {
		fmt.Println("Kubeconfig file not found")
		return
	}

	kubeconfig, err := clientcmd.BuildConfigFromFlags("", kubeconfigPath)
	if err != nil {
		fmt.Println("Error loading kubeconfig")
		return
	}

	// Create Kubernetes API client
	clientset, err := kubernetes.NewForConfig(kubeconfig)
	if err != nil {
		fmt.Println("Error creating kubernetes client")
		return
	}

	ctx := context.Background()

	migrationoperator := migration_operator.LiveMigrationReconciler{}

	migrationoperator.CreateDummyPod(clientset, ctx)
	migrationoperator.CreateDummyService(clientset, ctx)

}
