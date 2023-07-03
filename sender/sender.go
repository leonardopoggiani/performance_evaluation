package main

import (
	"context"
	"fmt"
	"math/rand"
	"os"
	"os/exec"
	"strings"
	"time"

	migrationoperator "github.com/leonardopoggiani/live-migration-operator/controllers"
	_ "github.com/mattn/go-sqlite3"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"
)

func deletePodsStartingWithTest(ctx context.Context, clientset *kubernetes.Clientset) error {
	podList, err := clientset.CoreV1().Pods("").List(ctx, metav1.ListOptions{})
	if err != nil {
		return err
	}

	for _, pod := range podList.Items {
		if pod.ObjectMeta.Name[:5] == "test-" {
			err := clientset.CoreV1().Pods(pod.Namespace).Delete(ctx, pod.Name, metav1.DeleteOptions{})
			if err != nil {
				return err
			}
			fmt.Printf("Deleted pod %s\n", pod.Name)
		}
	}

	return nil
}

func CreateContainers(ctx context.Context, numContainers int, clientset *kubernetes.Clientset, reconciler migrationoperator.LiveMigrationReconciler) *v1.Pod {
	// Generate a random string
	rand.Seed(time.Now().UnixNano())
	randStr := fmt.Sprintf("%d", rand.Intn(4000)+1000)

	createContainers := []v1.Container{}
	fmt.Println("numContainers: " + fmt.Sprintf("%d", numContainers))
	// Add the specified number of containers to the Pod manifest
	for i := 0; i < numContainers; i++ {
		fmt.Println("Creating container: " + fmt.Sprintf("container-%d", i))

		container := v1.Container{
			Name:            fmt.Sprintf("container-%d", i),
			Image:           "docker.io/library/tomcat:latest",
			ImagePullPolicy: v1.PullPolicy("IfNotPresent"),
			Ports: []v1.ContainerPort{
				{
					ContainerPort: 8080,
					Protocol:      v1.Protocol("TCP"),
				},
			},
		}

		createContainers = append(createContainers, container)
	}

	// Create the Pod with the random string appended to the name
	pod, err := clientset.CoreV1().Pods("default").Create(ctx, &v1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name: fmt.Sprintf("test-pod-%d-containers-%s", numContainers, randStr),
			Labels: map[string]string{
				"app": "test",
			},
		},
		Spec: v1.PodSpec{
			Containers:            createContainers,
			ShareProcessNamespace: &[]bool{true}[0],
		},
	}, metav1.CreateOptions{})

	if err != nil {
		fmt.Println(err.Error())
		return nil
	} else {
		fmt.Printf("Pod created %s", pod.Name)
		fmt.Println(createContainers[0].Name)
	}

	err = reconciler.WaitForContainerReady(pod.Name, "default", createContainers[0].Name, clientset)
	if err != nil {
		fmt.Println(err.Error())
		return nil
	} else {
		fmt.Println("Container ready")
	}

	return pod
}

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
				service, ok := event.Object.(*v1.Service)
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

	fmt.Println("Kubeconfig file not found")

	deletePodsStartingWithTest(ctx, clientset)
	waitForServiceCreation(clientset, ctx)

	reconciler := migrationoperator.LiveMigrationReconciler{}

	// once created the dummy pod and correctly offloaded, i can create a pod to migrate
	repetitions := 1
	numContainers := 1

	for j := 0; j <= repetitions-1; j++ {
		fmt.Printf("Repetitions %d \n", j)
		pod := CreateContainers(ctx, numContainers, clientset, reconciler)

		// Create a slice of Container structs
		var containers []migrationoperator.Container

		// Append the container ID and name for each container in each pod
		pods, err := clientset.CoreV1().Pods("default").List(ctx, metav1.ListOptions{})
		if err != nil {
			fmt.Println(err.Error())
			return
		}

		for _, pod := range pods.Items {
			for _, containerStatus := range pod.Status.ContainerStatuses {
				idParts := strings.Split(containerStatus.ContainerID, "//")

				fmt.Println("containerStatus.ContainerID: " + containerStatus.ContainerID)
				fmt.Println("containerStatus.Name: " + containerStatus.Name)

				if len(idParts) < 2 {
					fmt.Println("Malformed container ID")
					return
				}
				containerID := idParts[1]

				container := migrationoperator.Container{
					ID:   containerID,
					Name: containerStatus.Name,
				}
				containers = append(containers, container)
			}
		}

		fmt.Println("Checkpointing pod")
		fmt.Println("pod.Name: " + pod.Name)

		err = reconciler.CheckpointPodCrio(containers, "default", pod.Name)
		if err != nil {
			fmt.Println(err.Error())
			return
		} else {
			fmt.Println("Checkpointing completed")
		}

		err = reconciler.TerminateCheckpointedPod(ctx, pod.Name, clientset)
		if err != nil {
			fmt.Println(err.Error())
			return
		} else {
			fmt.Println("Pod terminated")
		}

		directory := "/tmp/checkpoints/checkpoints/"

		files, err := os.ReadDir(directory)
		if err != nil {
			fmt.Printf("Error reading directory: %v\n", err)
			return
		}

		for _, file := range files {
			if file.IsDir() {
				fmt.Printf("Directory: %s\n", file.Name())
			} else {
				fmt.Printf("File: %s\n", file.Name())
			}
		}

		err = reconciler.MigrateCheckpoint(ctx, directory, clientset)
		if err != nil {
			fmt.Println(err.Error())
			return
		} else {
			fmt.Println("Migration completed")
		}

		// delete checkpoints folder
		if _, err := exec.Command("sudo", "rm", "-rf", directory+"/").Output(); err != nil {
			fmt.Println(err.Error())
			return
		} else {
			fmt.Println("Checkpoints folder deleted")
		}

		if _, err = exec.Command("sudo", "mkdir", "/tmp/checkpoints/checkpoints/").Output(); err != nil {
			fmt.Println(err.Error())
			return
		} else {
			fmt.Println("Checkpoints folder created")
		}

		// deletePodsStartingWithTest(ctx, clientset)
	}
}
