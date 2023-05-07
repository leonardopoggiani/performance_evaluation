package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"database/sql"

	migrationoperator "github.com/leonardopoggiani/live-migration-operator/controllers"
	_ "github.com/mattn/go-sqlite3"

	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"
)

func createContainers(ctx context.Context, numContainers int, clientset *kubernetes.Clientset) *v1.Pod {

	createContainers := []v1.Container{}
	// Add the specified number of containers to the Pod manifest
	for i := 0; i < numContainers; i++ {
		container := v1.Container{
			Name:            fmt.Sprintf("container-%d", i),
			Image:           "docker.io/library/nginx:latest",
			ImagePullPolicy: v1.PullPolicy("IfNotPresent"),
		}

		createContainers = append(createContainers, container)
	}

	// Create the Pod
	pod, err := clientset.CoreV1().Pods("default").Create(ctx, &v1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name: fmt.Sprintf("test-pod-%d-containers", numContainers),
			Labels: map[string]string{
				"app": "test",
			},
		},
		Spec: v1.PodSpec{
			Containers: createContainers,
		},
	}, metav1.CreateOptions{})
	if err != nil {
		fmt.Println(err.Error())
		return nil
	}

	return pod
}

func saveSizeToDB(db *sql.DB, numContainers int64, size float64, checkpoint_type string) error {
	// Prepare SQL statement
	stmt, err := db.Prepare("INSERT INTO checkpoint_sizes (containers, size, checkpoint_type) VALUES (?, ?, ?)")
	if err != nil {
		return err
	}
	defer stmt.Close()

	// Execute statement
	_, err = stmt.Exec(numContainers, size, checkpoint_type)
	if err != nil {
		return err
	}

	return nil
}

func saveTimeToDB(db *sql.DB, numContainers int64, elapsed time.Duration, checkpoint_type string) {
	// Prepare SQL statement
	stmt, err := db.Prepare("INSERT INTO checkpoint_times (containers, elapsed, checkpoint_type) VALUES (?, ?, ?)")
	if err != nil {
		return
	}
	defer stmt.Close()

	// Execute statement
	_, err = stmt.Exec(numContainers, elapsed.Milliseconds(), checkpoint_type)
	if err != nil {
		return
	}

	return
}

func getCheckpointSize(ctx context.Context, clientset *kubernetes.Clientset, numContainers int, db *sql.DB) {

	pod := createContainers(ctx, numContainers, clientset)

	LiveMigrationReconciler := migrationoperator.LiveMigrationReconciler{}

	err := LiveMigrationReconciler.WaitForContainerReady(fmt.Sprintf("test-pod-%d-containers", numContainers), "default", fmt.Sprintf("container-%d", numContainers-1), clientset)
	if err != nil {
		cleanUp(ctx, clientset, pod)
		fmt.Println(err.Error())
		return
	}

	fmt.Printf("Pod %s is ready\n", pod.Name)

	// Create a slice of Container structs
	var containers []migrationoperator.Container

	// Append the container ID and name for each container in each pod
	pods, err := clientset.CoreV1().Pods("default").List(ctx, metav1.ListOptions{})
	if err != nil {
		fmt.Println(err.Error())
		cleanUp(ctx, clientset, pod)
		return
	}

	for _, pod := range pods.Items {
		for _, containerStatus := range pod.Status.ContainerStatuses {
			idParts := strings.Split(containerStatus.ContainerID, "//")
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

	err = LiveMigrationReconciler.CheckpointPodCrio(containers, "default", pod.Name)
	if err != nil {
		fmt.Println(err.Error())
		cleanUp(ctx, clientset, pod)
		return
	}

	directory := "/tmp/checkpoints/checkpoints"
	var size int64 = 0
	err = filepath.Walk(directory, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			fmt.Println(err.Error())
			cleanUp(ctx, clientset, pod)
			return err
		}
		if !info.Mode().IsRegular() {
			fmt.Println("Not a regular file")
			return nil
		}
		size += info.Size()
		return nil
	})
	if err != nil {
		cleanUp(ctx, clientset, pod)
		fmt.Println(err.Error())
		return
	}

	sizeInMB := float64(size) / (1024 * 1024)
	fmt.Printf("The size of %s is %.2f MB.\n", directory, sizeInMB)
	err = saveSizeToDB(db, int64(numContainers), sizeInMB, "sequential")
	if err != nil {
		fmt.Println(err.Error())
		return
	}

	// delete checkpoints folder
	if _, err := exec.Command("sudo", "rm", "-rf", directory+"/*").Output(); err != nil {
		cleanUp(ctx, clientset, pod)
		fmt.Println(err.Error())
		return
	}

	// check that checkpoints folder is empty
	if _, err := exec.Command("sudo", "ls", directory).Output(); err != nil {
		cleanUp(ctx, clientset, pod)
		fmt.Println(err.Error())
		return
	}

	err = LiveMigrationReconciler.CheckpointPodPipelined(containers, "default", pod.Name)
	if err != nil {
		cleanUp(ctx, clientset, pod)
		fmt.Println(err.Error())
		return
	}

	err = filepath.Walk(directory, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			cleanUp(ctx, clientset, pod)
			fmt.Println(err.Error())
			return err
		}
		if !info.Mode().IsRegular() {
			fmt.Println("Not a regular file")
			return nil
		}
		size += info.Size()
		return nil
	})
	if err != nil {
		cleanUp(ctx, clientset, pod)
		fmt.Println(err.Error())
		return
	}

	sizeInMB = float64(size) / (1024 * 1024)
	fmt.Printf("The size of %s is %.2f MB.\n", directory, sizeInMB)
	err = saveSizeToDB(db, int64(numContainers), sizeInMB, "pipelined")
	if err != nil {
		fmt.Println(err.Error())
		return
	}

	// delete checkpoints folder
	if _, err := exec.Command("sudo", "rm", "-rf", directory+"/*").Output(); err != nil {
		cleanUp(ctx, clientset, pod)
		fmt.Println(err.Error())
		return
	}

	// check that checkpoints folder is empty
	if _, err := exec.Command("sudo", "ls", directory).Output(); err != nil {
		cleanUp(ctx, clientset, pod)
		fmt.Println(err.Error())
		return
	}

	cleanUp(ctx, clientset, pod)
}

func cleanUp(ctx context.Context, clientset *kubernetes.Clientset, pod *v1.Pod) {
	err := clientset.CoreV1().Pods("default").Delete(ctx, pod.Name, metav1.DeleteOptions{})
	if err != nil {
		panic(err.Error())
	}
}

func getTotalTime(clientset *kubernetes.Clientset, numContainers int) (time.Duration, error) {
	// Define the Pod manifest
	podManifest := []byte(fmt.Sprintf(
		`apiVersion: v1
		kind: Pod
		metadata:
		  name: test-pod-%d-containers
		spec:
		  containers:`,
		numContainers))

	// Add the specified number of containers to the Pod manifest
	for i := 0; i < numContainers; i++ {
		containerManifest := fmt.Sprintf(
			`- name: container-%d
			image: nginx`,
			i)
		podManifest = append(podManifest, []byte(containerManifest)...)
	}

	// Finish the Pod manifest
	podManifest = append(podManifest, []byte(`
  		restartPolicy: Never
	`)...)

	// Create the Pod
	pod, err := clientset.CoreV1().Pods("default").Create(context.TODO(), &v1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name: fmt.Sprintf("test-pod-%d-containers", numContainers),
			Labels: map[string]string{
				"app": "test",
			},
		},
		Spec: v1.PodSpec{
			Containers: []v1.Container{
				{
					Name:  "nginx",
					Image: "nginx",
				},
			},
		},
	}, metav1.CreateOptions{})
	if err != nil {
		panic(err.Error())
	}

	fmt.Printf("Pod %s is ready\n", pod.Name)

	LiveMigrationReconciler := migrationoperator.LiveMigrationReconciler{}
	var containers []migrationoperator.Container

	// Append the container ID and name for each container in each pod
	pods, err := clientset.CoreV1().Pods("default").List(context.Background(), metav1.ListOptions{})

	for _, pod := range pods.Items {
		for _, containerStatus := range pod.Status.ContainerStatuses {
			idParts := strings.Split(containerStatus.ContainerID, "//")
			if len(idParts) < 2 {
				return 0, nil
			}
			containerID := idParts[1]

			container := migrationoperator.Container{
				ID:   containerID,
				Name: containerStatus.Name,
			}
			containers = append(containers, container)
		}
	}

	// Get the start time of the checkpoint
	start := time.Now()

	err = LiveMigrationReconciler.CheckpointPodPipelined(containers, "default", pod.Name)
	if err != nil {
		return 0, err
	}
	_, err = LiveMigrationReconciler.BuildahRestorePipelined(context.TODO(), pod.Name)
	if err != nil {
		return 0, err
	}

	// Calculate the time taken for the checkpoint
	elapsed := time.Since(start)

	fmt.Printf("Checkpoint took %s\n", elapsed)

	return 0, nil
}

func getCheckpointTime(ctx context.Context, clientset *kubernetes.Clientset, numContainers int, db *sql.DB) {

	pod := createContainers(ctx, numContainers, clientset)

	LiveMigrationReconciler := migrationoperator.LiveMigrationReconciler{}

	err := LiveMigrationReconciler.WaitForContainerReady(fmt.Sprintf("test-pod-%d-containers", numContainers), "default", fmt.Sprintf("container-%d", numContainers-1), clientset)
	if err != nil {
		cleanUp(ctx, clientset, pod)
		fmt.Println(err.Error())
		return
	}

	fmt.Printf("Pod %s is ready\n", pod.Name)

	// Create a slice of Container structs
	var containers []migrationoperator.Container

	// Append the container ID and name for each container in each pod
	pods, err := clientset.CoreV1().Pods("default").List(ctx, metav1.ListOptions{})
	if err != nil {
		fmt.Println(err.Error())
		cleanUp(ctx, clientset, pod)
		return
	}

	for _, pod := range pods.Items {
		for _, containerStatus := range pod.Status.ContainerStatuses {
			idParts := strings.Split(containerStatus.ContainerID, "//")
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

	// Get the start time of the checkpoint
	start := time.Now()

	err = LiveMigrationReconciler.CheckpointPodCrio(containers, "default", pod.Name)
	if err != nil {
		return
	}

	// Calculate the time taken for the checkpoint
	elapsed := time.Since(start)
	fmt.Println("Elapsed sequential: ", elapsed)

	saveTimeToDB(db, int64(numContainers), elapsed, "sequential")

	// delete checkpoint folder
	directory := "/tmp/checkpoints/checkpoints"
	if _, err := exec.Command("sudo", "rm", "-rf", directory+"/*").Output(); err != nil {
		cleanUp(ctx, clientset, pod)
		fmt.Println(err.Error())
		return
	}

	start = time.Now()

	err = LiveMigrationReconciler.CheckpointPodPipelined(containers, "default", pod.Name)
	if err != nil {
		return
	}

	// Calculate the time taken for the checkpoint
	elapsed = time.Since(start)
	fmt.Println("Elapsed pipelined: ", elapsed)

	saveTimeToDB(db, int64(numContainers), elapsed, "pipelined")

	// delete checkpoint folder
	if _, err := exec.Command("sudo", "rm", "-rf", directory+"/*").Output(); err != nil {
		cleanUp(ctx, clientset, pod)
		fmt.Println(err.Error())
		return
	}

	cleanUp(ctx, clientset, pod)

}

func getRestoreTime(ctx context.Context) (time.Duration, error) {
	// Get the start time of the restore
	LiveMigrationReconciler := migrationoperator.LiveMigrationReconciler{}

	start := time.Now()

	_, err := LiveMigrationReconciler.BuildahRestore(ctx, "")
	if err != nil {
		return 0, err
	}
	// Calculate the time taken for the restore
	elapsed := time.Since(start)
	fmt.Println("Elapsed sequential: ", elapsed)

	start = time.Now()

	_, err = LiveMigrationReconciler.BuildahRestorePipelined(ctx, "")
	if err != nil {
		return 0, err
	}
	// Calculate the time taken for the restore
	elapsed = time.Since(start)
	fmt.Println("Elapsed pipelined: ", elapsed)

	start = time.Now()

	_, err = LiveMigrationReconciler.BuildahRestoreParallelized(ctx, "")
	if err != nil {
		return 0, err
	}
	// Calculate the time taken for the restore
	elapsed = time.Since(start)
	fmt.Println("Elapsed parallel: ", elapsed)

	return 0, nil
}

func getCheckpointImageRestoreSize(ctx context.Context, numContainers int) (int64, error) {
	/*
		config, err := rest.InClusterConfig()
		if err != nil {
			return 0, fmt.Errorf("failed to get in-cluster config: %v", err)
		}

		clientset, err := kubernetes.NewForConfig(config)
		if err != nil {
			return 0, fmt.Errorf("failed to create kubernetes client: %v", err)
		}

		podName := fmt.Sprintf("test-pod-%d-containers", numContainers)

		pod, err := clientset.CoreV1().Pods("default").Get(ctx, podName, metav1.GetOptions{})
		if err != nil {
			return 0, fmt.Errorf("failed to get pod: %v", err)
		}

		containerID := pod.Status.ContainerStatuses[0].ContainerID

		containerLogOptions := &v1.PodLogOptions{
			Container: pod.Spec.Containers[0].Name,
		}

		logStream, err := clientset.CoreV1().Pods("default").GetLogs(podName, containerLogOptions).Stream(ctx)
		if err != nil {
			return 0, fmt.Errorf("failed to get log stream: %v", err)
		}
		defer func(logStream io.ReadCloser) {
			err := logStream.Close()
			if err != nil {
				klog.Errorf("failed to close log stream: %v", err)
			}
		}(logStream)

		logBytes, err := io.ReadAll(logStream)
		if err != nil {
			return 0, fmt.Errorf("failed to read log stream: %v", err)
		}

		return int64(len(logBytes)), nil

	*/
	return 0, nil
}

func getTimeDirectVsTriangularized(clientset *kubernetes.Clientset, numContainers int) {
	// Define the Pod manifest
	podManifest := []byte(fmt.Sprintf(
		`apiVersion: v1
		kind: Pod
		metadata:
		  name: test-pod-%d-containers
		spec:
		  containers:`,
		numContainers))

	// Add the specified number of containers to the Pod manifest
	for i := 0; i < numContainers; i++ {
		containerManifest := fmt.Sprintf(
			`- name: container-%d
			image: nginx`,
			i)
		podManifest = append(podManifest, []byte(containerManifest)...)
	}

	// Finish the Pod manifest
	podManifest = append(podManifest, []byte(`
  		restartPolicy: Never
	`)...)

	// Create the Pod
	pod, err := clientset.CoreV1().Pods("default").Create(context.TODO(), &v1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name: fmt.Sprintf("test-pod-%d-containers", numContainers),
			Labels: map[string]string{
				"app": "test",
			},
		},
		Spec: v1.PodSpec{
			Containers: []v1.Container{
				{
					Name:  "nginx",
					Image: "nginx",
				},
			},
		},
	}, metav1.CreateOptions{})
	if err != nil {
		panic(err.Error())
	}

	fmt.Printf("Pod %s is ready\n", pod.Name)

	// triangularized
	LiveMigrationReconciler := migrationoperator.LiveMigrationReconciler{}

	var containers []migrationoperator.Container

	// Append the container ID and name for each container in each pod
	pods, err := clientset.CoreV1().Pods("default").List(context.Background(), metav1.ListOptions{})

	for _, pod := range pods.Items {
		for _, containerStatus := range pod.Status.ContainerStatuses {
			idParts := strings.Split(containerStatus.ContainerID, "//")
			if len(idParts) < 2 {
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

	err = LiveMigrationReconciler.CheckpointPodCrio(containers, "default", pod.Name)
	if err != nil {
		return
	}

	// ...
}

func main() {
	// Use a context to cancel the loop that checks for sourcePod
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Connect to database
	db, err := sql.Open("sqlite3", "./db/checkpoint_data.db")
	if err != nil {
		panic(err)
	}
	defer db.Close()

	// Create table if it doesn't exist
	_, err = db.Exec("CREATE TABLE IF NOT EXISTS checkpoint_sizes (timestamp TIMESTAMP DEFAULT CURRENT_TIMESTAMP, containers INTEGER, size FLOAT, checkpoint_type STRING)")
	if err != nil {
		panic(err)
	}

	// Create table if it doesn't exist
	_, err = db.Exec("CREATE TABLE IF NOT EXISTS checkpoint_times (timestamp TIMESTAMP DEFAULT CURRENT_TIMESTAMP, containers INTEGER, elapsed FLOAT, checkpoint_type STRING)")
	if err != nil {
		panic(err)
	}

	_, err = db.Exec("DELETE FROM checkpoint_sizes")
	if err != nil {
		panic(err)
	}

	_, err = db.Exec("DELETE FROM checkpoint_times")
	if err != nil {
		panic(err)
	}

	// Load Kubernetes config
	kubeconfigPath := os.Getenv("KUBECONFIG")
	if kubeconfigPath == "" {
		kubeconfigPath = "~/.kube/config"
	}

	kubeconfigPath = os.ExpandEnv(kubeconfigPath)
	if _, err := os.Stat(kubeconfigPath); os.IsNotExist(err) {
		fmt.Println("kubeconfig file not existing")
	}

	kubeconfig, err := clientcmd.BuildConfigFromFlags("", kubeconfigPath)
	if err != nil {
		fmt.Println("Failed to retrieve kubeconfig")
		return
	}

	// Create Kubernetes API client
	clientset, err := kubernetes.NewForConfig(kubeconfig)
	if err != nil {
		fmt.Println("failed to create Kubernetes client")
		return
	}

	containerCounts := []int{1, 2}
	// 	containerCounts := []int{1, 2, 3, 5, 10}
	repetitions := 5
	//  repetitions := 20

	for i := 0; i < repetitions; i++ {
		fmt.Printf("Repetition %d\n", i)

		for _, numContainers := range containerCounts {
			fmt.Printf("Size for %d containers\n", numContainers)
			getCheckpointSize(ctx, clientset, numContainers, db)
		}

		for _, numContainers := range containerCounts {
			fmt.Printf("Time for %d containers\n", numContainers)
			getCheckpointTime(ctx, clientset, numContainers, db)
		}
	}
}
