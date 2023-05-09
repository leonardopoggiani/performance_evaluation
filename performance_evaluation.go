package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"database/sql"

	migrationoperator "github.com/leonardopoggiani/live-migration-operator/controllers"
	_ "github.com/mattn/go-sqlite3"

	"github.com/docker/docker/client"
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

func saveSizeToDB(db *sql.DB, numContainers int64, size float64, checkpoint_type string) {
	// Prepare SQL statement
	stmt, err := db.Prepare("INSERT INTO checkpoint_sizes (containers, size, checkpoint_type) VALUES (?, ?, ?)")
	if err != nil {
		return
	}
	defer stmt.Close()

	// Execute statement
	_, err = stmt.Exec(numContainers, size, checkpoint_type)
	if err != nil {
		return
	}
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
}

func getCheckpointSizePipelined(ctx context.Context, clientset *kubernetes.Clientset, numContainers int, db *sql.DB) {

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

	err = LiveMigrationReconciler.CheckpointPodPipelined(containers, "default", pod.Name)
	if err != nil {
		cleanUp(ctx, clientset, pod)
		fmt.Println(err.Error())
		return
	}

	directory := "/tmp/checkpoints/checkpoints"
	var size int64 = 0

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

	sizeInMB := float64(size) / (1024 * 1024)
	fmt.Printf("The size of %s is %.2f MB.\n", directory, sizeInMB)
	saveSizeToDB(db, int64(numContainers), sizeInMB, "pipelined")

	// delete checkpoints folder
	if _, err := exec.Command("sudo", "rm", "-f", directory+"/*").Output(); err != nil {
		cleanUp(ctx, clientset, pod)
		fmt.Println(err.Error())
		return
	}

	// check that checkpoints folder is empty
	if output, err := exec.Command("sudo", "ls", directory).Output(); err != nil {
		cleanUp(ctx, clientset, pod)
		fmt.Println(err.Error())
		return
	} else {
		fmt.Printf("Output: %s\n", output)
	}

	cleanUp(ctx, clientset, pod)
}

func getCheckpointSizeSequential(ctx context.Context, clientset *kubernetes.Clientset, numContainers int, db *sql.DB) {

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
	saveSizeToDB(db, int64(numContainers), sizeInMB, "sequential")

	// delete checkpoints folder
	if _, err := exec.Command("sudo", "rm", "-f", directory+"/*").Output(); err != nil {
		cleanUp(ctx, clientset, pod)
		fmt.Println(err.Error())
		return
	}

	// check that checkpoints folder is empty
	if output, err := exec.Command("sudo", "ls", directory).Output(); err != nil {
		cleanUp(ctx, clientset, pod)
		fmt.Println(err.Error())
		return
	} else {
		fmt.Printf("Output: %s\n", output)
	}

	cleanUp(ctx, clientset, pod)
}

func cleanUp(ctx context.Context, clientset *kubernetes.Clientset, pod *v1.Pod) {
	err := clientset.CoreV1().Pods("default").Delete(ctx, pod.Name, metav1.DeleteOptions{})
	if err != nil {
		panic(err.Error())
	}
}

func getCheckpointTimeSequential(ctx context.Context, clientset *kubernetes.Clientset, numContainers int, db *sql.DB) {

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

	cleanUp(ctx, clientset, pod)
}

func getCheckpointTimePipelined(ctx context.Context, clientset *kubernetes.Clientset, numContainers int, db *sql.DB) {

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

	start := time.Now()

	err = LiveMigrationReconciler.CheckpointPodPipelined(containers, "default", pod.Name)
	if err != nil {
		return
	}

	// Calculate the time taken for the checkpoint
	elapsed := time.Since(start)
	fmt.Println("Elapsed pipelined: ", elapsed)

	saveTimeToDB(db, int64(numContainers), elapsed, "pipelined")

	// delete checkpoint folder
	directory := "/tmp/checkpoints/checkpoints"
	if _, err := exec.Command("sudo", "rm", "-rf", directory+"/*").Output(); err != nil {
		cleanUp(ctx, clientset, pod)
		fmt.Println(err.Error())
		return
	}

	cleanUp(ctx, clientset, pod)
}

func getRestoreTime(ctx context.Context, clientset *kubernetes.Clientset, numContainers int, db *sql.DB) {
	// Get the start time of the restore

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

	err = LiveMigrationReconciler.CheckpointPodPipelined(containers, "default", pod.Name)
	if err != nil {
		return
	}

	// create dummy file
	_, err = os.Create("/tmp/checkpoints/checkpoints/dummy")
	if err != nil {
		fmt.Println(err.Error())
		return
	}

	start := time.Now()

	_, err = LiveMigrationReconciler.BuildahRestore(ctx, "/tmp/checkpoints/checkpoints")
	if err != nil {
		return
	}
	// Calculate the time taken for the restore
	elapsed := time.Since(start)
	fmt.Println("Elapsed sequential: ", elapsed)

	saveRestoreTimeToDB(db, int64(numContainers), elapsed, "sequential")

	// eliminate docker image
	for i := 0; i < numContainers; i++ {
		buildahDeleteImage("localhost/leonardopoggiani/checkpoint-images:container-" + strconv.Itoa(i))
	}

	start = time.Now()

	_, err = LiveMigrationReconciler.BuildahRestorePipelined(ctx, "/tmp/checkpoints/checkpoints")
	if err != nil {
		return
	}
	// Calculate the time taken for the restore
	elapsed = time.Since(start)
	fmt.Println("Elapsed pipelined: ", elapsed)

	saveRestoreTimeToDB(db, int64(numContainers), elapsed, "pipelined")

	// eliminate docker image
	for i := 0; i < numContainers; i++ {
		buildahDeleteImage("localhost/leonardopoggiani/checkpoint-images:container-" + strconv.Itoa(i))
	}

	start = time.Now()

	_, err = LiveMigrationReconciler.BuildahRestoreParallelized(ctx, "/tmp/checkpoints/checkpoints")
	if err != nil {
		return
	}
	// Calculate the time taken for the restore
	elapsed = time.Since(start)
	fmt.Println("Elapsed parallel: ", elapsed)

	saveRestoreTimeToDB(db, int64(numContainers), elapsed, "parallelized")

	// eliminate docker image
	for i := 0; i < numContainers; i++ {
		buildahDeleteImage("localhost/leonardopoggiani/checkpoint-images:container-" + strconv.Itoa(i))
	}

	cleanUp(ctx, clientset, pod)
}

func saveRestoreTimeToDB(db *sql.DB, numContainers int64, elapsed time.Duration, checkpoint_type string) {
	// Prepare SQL statement
	stmt, err := db.Prepare("INSERT INTO restore_times (containers, elapsed, checkpoint_type) VALUES (?, ?, ?)")
	if err != nil {
		return
	}
	defer stmt.Close()

	// Execute statement
	_, err = stmt.Exec(numContainers, elapsed.Milliseconds(), checkpoint_type)
	if err != nil {
		return
	}
}

func buildahDeleteImage(imageName string) {
	buildahCmd := exec.Command("sudo", "buildah", "rmi", imageName)
	err := buildahCmd.Run()
	if err != nil {
		fmt.Println("Error removing image:", err)
		return
	}
	fmt.Println("Image", imageName, "removed successfully.")
}

func getCheckpointImageRestoreSize(ctx context.Context, clientset *kubernetes.Clientset, numContainers int, db *sql.DB) {
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

	// Create a new Docker client
	cli, err := client.NewClientWithOpts(client.FromEnv)
	if err != nil {
		log.Fatal(err)
	}

	// eliminate docker image
	for i := 0; i < numContainers; i++ {
		// Get the image details
		imageName := "localhost/leonardopoggiani/checkpoint-images:container-" + strconv.Itoa(i)
		image, _, err := cli.ImageInspectWithRaw(context.Background(), imageName)
		if err != nil {
			log.Fatal(err)
		}

		// Print the size in MB
		sizeInMB := float64(image.Size/1000000) / (1024 * 1024)
		fmt.Printf("The size of %s is %.2f MB.\n", directory, sizeInMB)
		saveDockerSizeToDB(db, int64(numContainers), sizeInMB)
	}

	// delete checkpoints folder
	if _, err := exec.Command("sudo", "rm", "-f", directory+"/*").Output(); err != nil {
		cleanUp(ctx, clientset, pod)
		fmt.Println(err.Error())
		return
	}

	// check that checkpoints folder is empty
	if output, err := exec.Command("sudo", "ls", directory).Output(); err != nil {
		cleanUp(ctx, clientset, pod)
		fmt.Println(err.Error())
		return
	} else {
		fmt.Printf("Output: %s\n", output)
	}

	cleanUp(ctx, clientset, pod)
}

func saveDockerSizeToDB(db *sql.DB, numContainers int64, size float64) {
	// Prepare SQL statement
	stmt, err := db.Prepare("INSERT INTO docker_sizes (containers, size, checkpoint_type) VALUES (?, ?, ?)")
	if err != nil {
		return
	}
	defer stmt.Close()

	// Execute statement
	_, err = stmt.Exec(numContainers, size)
	if err != nil {
		return
	}
}

func getTimeDirectVsTriangularized(ctx context.Context, clientset *kubernetes.Clientset, numContainers int, db *sql.DB, exchange string) {
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

	start := time.Now()

	err = LiveMigrationReconciler.CheckpointPodCrio(containers, "default", pod.Name)
	if err != nil {
		fmt.Println(err.Error())
		cleanUp(ctx, clientset, pod)
		return
	}

	cleanUp(ctx, clientset, pod)

	LiveMigrationReconciler.BuildahRestore(ctx, "/tmp/checkpoints/checkpoints")
	LiveMigrationReconciler.PushDockerImage("localhost/leonardopoggiani/checkpoint-images:container-"+strconv.Itoa(numContainers-1), "container-"+strconv.Itoa(numContainers-1), pod.Name)

	createContainers := []v1.Container{}

	if exchange == "direct" {
		// TODO: send to the other node
		for i := 0; i < numContainers; i++ {
			container := v1.Container{
				Name:            fmt.Sprintf("container-%d", i),
				Image:           "localhost/leonardopoggiani/checkpoint-images:container-" + strconv.Itoa(i),
				ImagePullPolicy: v1.PullPolicy("IfNotPresent"),
			}

			createContainers = append(createContainers, container)
		}
	} else if exchange == "triangularized" {
		for i := 0; i < numContainers; i++ {
			container := v1.Container{
				Name:            fmt.Sprintf("container-%d", i),
				Image:           "docker.io/leonardopoggiaini/checkpoint-images:container-" + strconv.Itoa(i),
				ImagePullPolicy: v1.PullPolicy("IfNotPresent"),
			}

			createContainers = append(createContainers, container)
		}
	}

	// Create the Pod
	pod, err = clientset.CoreV1().Pods("default").Create(ctx, &v1.Pod{
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
		return
	}

	LiveMigrationReconciler.WaitForContainerReady(pod.Name, "default", "container-"+strconv.Itoa(numContainers-1), clientset)

	elapsed := time.Since(start)
	fmt.Printf("Time to checkpoint and restore %d containers: %s\n", numContainers, elapsed)

	saveTotalTimeDB(db, int64(numContainers), elapsed.Seconds(), exchange)
}

func saveTotalTimeDB(db *sql.DB, numContainers int64, size float64, checkpointType string) {
	// Prepare SQL statement
	stmt, err := db.Prepare("INSERT INTO total_times (containers, size, checkpoint_type) VALUES (?, ?, ?)")
	if err != nil {
		return
	}
	defer stmt.Close()

	// Execute statement
	_, err = stmt.Exec(numContainers, size, checkpointType)
	if err != nil {
		return
	}
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

	// Create table if it doesn't exist
	_, err = db.Exec("CREATE TABLE IF NOT EXISTS restore_times (timestamp TIMESTAMP DEFAULT CURRENT_TIMESTAMP, containers INTEGER, elapsed FLOAT, checkpoint_type STRING)")
	if err != nil {
		panic(err)
	}

	// Create table if it doesn't exist
	_, err = db.Exec("CREATE TABLE IF NOT EXISTS docker_sizes (timestamp TIMESTAMP DEFAULT CURRENT_TIMESTAMP, containers INTEGER, elapsed FLOAT)")
	if err != nil {
		panic(err)
	}

	// Create table if it doesn't exist
	_, err = db.Exec("CREATE TABLE IF NOT EXISTS total_times (timestamp TIMESTAMP DEFAULT CURRENT_TIMESTAMP, containers INTEGER, elapsed FLOAT, checkpoint_type STRING)")
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

	containerCounts := []int{1, 2, 5, 10}
	// 	containerCounts := []int{1, 2, 3, 5, 10}
	repetitions := 20
	//  repetitions := 20

	for i := 0; i < repetitions; i++ {
		fmt.Printf("Repetition %d\n", i)

		for _, numContainers := range containerCounts {
			fmt.Printf("Size for %d containers\n", numContainers)
			getCheckpointSizePipelined(ctx, clientset, numContainers, db)
		}

		for _, numContainers := range containerCounts {
			fmt.Printf("Time for %d containers\n", numContainers)
			getCheckpointTimePipelined(ctx, clientset, numContainers, db)
		}
	}

	for i := 0; i < repetitions; i++ {
		fmt.Printf("Repetition %d\n", i)

		for _, numContainers := range containerCounts {
			fmt.Printf("Size for %d containers\n", numContainers)
			getCheckpointSizeSequential(ctx, clientset, numContainers, db)
		}

		for _, numContainers := range containerCounts {
			fmt.Printf("Time for %d containers\n", numContainers)
			getCheckpointTimeSequential(ctx, clientset, numContainers, db)
		}
	}

	for i := 0; i < repetitions; i++ {
		fmt.Printf("Repetition %d\n", i)

		for _, numContainers := range containerCounts {
			fmt.Printf("Size for %d containers\n", numContainers)
			getRestoreTime(ctx, clientset, numContainers, db)
		}
	}

	for i := 0; i < repetitions; i++ {
		fmt.Printf("Repetition %d\n", i)

		for _, numContainers := range containerCounts {
			fmt.Printf("Size for %d containers\n", numContainers)
			getCheckpointImageRestoreSize(ctx, clientset, numContainers, db)
		}
	}

	for i := 0; i < repetitions; i++ {
		fmt.Printf("Repetition %d\n", i)
		for _, numContainers := range containerCounts {
			fmt.Printf("Total times for %d containers\n", numContainers)
			getTimeDirectVsTriangularized(ctx, clientset, numContainers, db, "triangularized")
		}
	}

	for i := 0; i < repetitions; i++ {
		fmt.Printf("Repetition %d\n", i)
		for _, numContainers := range containerCounts {
			fmt.Printf("Total times for %d containers\n", numContainers)
			getTimeDirectVsTriangularized(ctx, clientset, numContainers, db, "direct")
		}
	}

	// Command to call the Python program
	cmd := exec.Command("python", "graphs.py")

	// Run the command and capture the output
	output, err := cmd.Output()
	if err != nil {
		fmt.Println("Error:", err)
		return
	}

	// Print the output of the Python program
	fmt.Println(string(output))

}
