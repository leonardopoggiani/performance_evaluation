package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"time"

	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/tools/clientcmd"

	"k8s.io/client-go/kubernetes"

	migrationoperator "github.com/leonardopoggiani/live-migration-operator/controllers"
)

func waitForPodCreation(clientset *kubernetes.Clientset, ctx context.Context) {
	// Set up a watch for pod creation events
	watch, err := clientset.CoreV1().Pods("default").Watch(ctx, metav1.ListOptions{
		FieldSelector: "metadata.name=dummy-pod",
	})
	if err != nil {
		log.Fatal(err)
	}

	// Create a channel to receive watch events
	eventChan := watch.ResultChan()

	for {
		select {
		case event, ok := <-eventChan:
			if !ok {
				// Watch channel closed
				return
			}

			// Check the event type
			if event.Type == "ADDED" {
				// Pod created
				pod, ok := event.Object.(*v1.Pod)
				if !ok {
					log.Println("Unexpected object type received")
					continue
				}

				// Check if it's the desired pod
				if pod.Name == "dummy-pod" {
					fmt.Println("Dummy Pod created!")
					return
				}
			}

		case <-time.After(1 * time.Minute):
			// Timeout after 1 minute
			fmt.Println("Timeout: Dummy Pod creation not detected")
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

	waitForPodCreation(clientset, ctx)

	// once created the dummy pod and correctly offloaded, i can create a pod to migrate
	migration_operator := migrationoperator.LiveMigrationReconciler{}

	repetitions := 1
	numContainers := 1

	for j := 0; j <= repetitions; j++ {
		fmt.Printf("Repetitions %d", j)
		for i := 0; i <= numContainers; i++ {
			fmt.Printf("Containers %d", i)
			migration_operator.CreateContainers(ctx, i, clientset)
		}
	}

	// migration_operator.MigratePod()
}
