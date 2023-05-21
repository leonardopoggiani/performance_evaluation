package main

import (
	"context"
	"fmt"
	"os"

	v1 "k8s.io/api/core/v1"
	_ "github.com/mattn/go-sqlite3"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"

	migrationoperator "github.com/leonardopoggiani/live-migration-operator/controllers"

	performance "github.com/leonardopoggiani/performance-evaluation/e2e"
)

func waitForServiceCreation(clientset *kubernetes.Clientset, ctx context.Context) {
	watcher, err := clientset.CoreV1().Services("liqo-demo").Watch(ctx, metav1.ListOptions{})
	if err != nil {
		fmt.Println("Error watching services")
		return
	}
	defer watcher.Stop()

	for {
		select {
		case event, ok := <-watcher.ResultChan():
			if !ok {
				fmt.Println("Watcher channel closed")
				return
			}
			if event.Type == watch.Added {
				service, ok := event.Object.(*corev1.Service)
				if !ok {
					fmt.Println("Error casting service object")
					return
				}
				if service.Name == "dummy-service" {
					fmt.Println("Service dummy-service created")
					return
				}
			}
		case <-ctx.Done():
			fmt.Println("Context done")
			return
		}
	}
}

func main() {
	fmt.Println("Sender program, sending migration request")

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

	waitForServiceCreation(clientset, ctx)

	// once created the dummy pod and correctly offloaded, i can create a pod to migrate

	repetitions := 1
	numContainers := 1

	for j := 0; j <= repetitions; j++ {
		fmt.Printf("Repetitions %d", j)
		for i := 0; i <= numContainers; i++ {
			fmt.Printf("Containers %d", i)
			performance.CreateContainers(ctx, i, clientset)
		}
	}

	// migration_operator.MigratePod()
}
