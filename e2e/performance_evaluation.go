package e2e

import (
	"context"
	"fmt"
	"log"
	"math/rand"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
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
	// Generate a random string
	rand.Seed(time.Now().UnixNano())
	randStr := fmt.Sprintf("%d", rand.Intn(4000)+1000)

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

	// Create the Pod with the random string appended to the name
	pod, err := clientset.CoreV1().Pods("default").Create(ctx, &v1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name: fmt.Sprintf("test-pod-%d-containers-%s", numContainers, randStr),
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

func getCheckpointSizePipelined(ctx context.Context, clientset *kubernetes.Clientset, numContainers int, db *sql.DB) {

	pod := createContainers(ctx, numContainers, clientset)

	LiveMigrationReconciler := migrationoperator.LiveMigrationReconciler{}

	err := LiveMigrationReconciler.WaitForContainerReady(pod.Name, "default", fmt.Sprintf("container-%d", numContainers-1), clientset)
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
	saveToDB(db, int64(numContainers), sizeInMB, "pipelined", "checkpoint_sizes")

	// delete checkpoints folder
	if _, err := exec.Command("sudo", "rm", "-f", directory+"/").Output(); err != nil {
		cleanUp(ctx, clientset, pod)
		fmt.Println(err.Error())
		return
	}

	if _, err = exec.Command("sudo", "mkdir", "/tmp/checkpoints/checkpoints/").Output(); err != nil {
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

	err := LiveMigrationReconciler.WaitForContainerReady(pod.Name, "default", fmt.Sprintf("container-%d", numContainers-1), clientset)
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
	saveToDB(db, int64(numContainers), sizeInMB, "sequential", "checkpoint_sizes")

	// delete checkpoints folder
	if _, err := exec.Command("sudo", "rm", "-f", directory+"/").Output(); err != nil {
		cleanUp(ctx, clientset, pod)
		fmt.Println(err.Error())
		return
	}

	if _, err = exec.Command("sudo", "mkdir", "/tmp/checkpoints/checkpoints/").Output(); err != nil {
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
	fmt.Println("Garbage collecting => " + pod.Name)
	err := clientset.CoreV1().Pods("default").Delete(ctx, pod.Name, metav1.DeleteOptions{})
	if err != nil {
		fmt.Println(err.Error())
		return
	}
}

func getCheckpointTimeSequential(ctx context.Context, clientset *kubernetes.Clientset, numContainers int, db *sql.DB) {

	pod := createContainers(ctx, numContainers, clientset)

	LiveMigrationReconciler := migrationoperator.LiveMigrationReconciler{}

	err := LiveMigrationReconciler.WaitForContainerReady(pod.Name, "default", fmt.Sprintf("container-%d", numContainers-1), clientset)
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

	saveToDB(db, int64(numContainers), float64(elapsed.Milliseconds()), "sequential", "checkpoint_times")

	// delete checkpoint folder
	directory := "/tmp/checkpoints/checkpoints"
	if _, err := exec.Command("sudo", "rm", "-rf", directory+"/").Output(); err != nil {
		cleanUp(ctx, clientset, pod)
		fmt.Println(err.Error())
		return
	}

	if _, err = exec.Command("sudo", "mkdir", "/tmp/checkpoints/checkpoints/").Output(); err != nil {
		cleanUp(ctx, clientset, pod)
		fmt.Println(err.Error())
		return
	}

	cleanUp(ctx, clientset, pod)
}

func countFilesInFolder(folderPath string) (int, error) {
	fileCount := 0

	err := filepath.Walk(folderPath, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			fmt.Println(err.Error())
			return err
		}
		if !info.Mode().IsRegular() {
			fmt.Println("Not a regular file")
			return nil
		}
		fileCount += 1
		return nil
	})
	if err != nil {
		fmt.Println(err.Error())
		return 0, err
	}

	return fileCount, nil
}

func getCheckpointTimePipelined(ctx context.Context, clientset *kubernetes.Clientset, numContainers int, db *sql.DB) {

	pod := createContainers(ctx, numContainers, clientset)

	LiveMigrationReconciler := migrationoperator.LiveMigrationReconciler{}

	err := LiveMigrationReconciler.WaitForContainerReady(pod.Name, "default", fmt.Sprintf("container-%d", numContainers-1), clientset)
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

	saveToDB(db, int64(numContainers), float64(elapsed.Milliseconds()), "pipelined", "checkpoint_times")

	// delete checkpoint folder
	directory := "/tmp/checkpoints/checkpoints"
	if _, err := exec.Command("sudo", "rm", "-rf", directory+"/").Output(); err != nil {
		cleanUp(ctx, clientset, pod)
		fmt.Println(err.Error())
		return
	}

	if _, err = exec.Command("sudo", "mkdir", "/tmp/checkpoints/checkpoints/").Output(); err != nil {
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

	err := LiveMigrationReconciler.WaitForContainerReady(pod.Name, "default", fmt.Sprintf("container-%d", numContainers-1), clientset)
	if err != nil {
		cleanUp(ctx, clientset, pod)
		fmt.Println(err.Error())
		return
	}

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

	path := "/tmp/checkpoints/checkpoints/"
	// create dummy file
	_, err = os.Create(path + "dummy")
	if err != nil {
		fmt.Println(err.Error())
		return
	}

	files, err := countFilesInFolder(path)
	if err != nil {
		fmt.Println(err.Error())
		return
	}

	fmt.Printf("Files count => %d", files)

	start := time.Now()

	pod, err = LiveMigrationReconciler.BuildahRestore(ctx, "/tmp/checkpoints/checkpoints", clientset)
	if err != nil {
		fmt.Println(err.Error())
		return
	}
	// Calculate the time taken for the restore
	elapsed := time.Since(start)
	fmt.Println("Elapsed sequential: ", elapsed)

	saveToDB(db, int64(numContainers), float64(elapsed.Milliseconds()), "sequential", "restore_times")

	// eliminate docker image
	for i := 0; i < numContainers; i++ {
		buildahDeleteImage("localhost/leonardopoggiani/checkpoint-images:container-" + strconv.Itoa(i))
	}

	cleanUp(ctx, clientset, pod)
	start = time.Now()

	pod, err = LiveMigrationReconciler.BuildahRestorePipelined(ctx, "/tmp/checkpoints/checkpoints", clientset)
	if err != nil {
		fmt.Println(err.Error())
		return
	}
	// Calculate the time taken for the restore
	elapsed = time.Since(start)
	fmt.Println("Elapsed pipelined: ", elapsed)

	saveToDB(db, int64(numContainers), float64(elapsed.Milliseconds()), "pipelined", "restore_times")

	// eliminate docker image
	for i := 0; i < numContainers; i++ {
		buildahDeleteImage("localhost/leonardopoggiani/checkpoint-images:container-" + strconv.Itoa(i))
	}

	cleanUp(ctx, clientset, pod)

	start = time.Now()

	pod, err = LiveMigrationReconciler.BuildahRestoreParallelized(ctx, "/tmp/checkpoints/checkpoints", clientset)
	if err != nil {
		fmt.Println(err.Error())
		return
	}

	// Calculate the time taken for the restore
	elapsed = time.Since(start)
	fmt.Println("Elapsed parallel: ", elapsed)

	saveToDB(db, int64(numContainers), float64(elapsed.Milliseconds()), "parallelized", "restore_times")

	// eliminate docker image
	for i := 0; i < numContainers; i++ {
		buildahDeleteImage("localhost/leonardopoggiani/checkpoint-images:container-" + strconv.Itoa(i))
	}

	cleanUp(ctx, clientset, pod)
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

func getImageSize(imageName string) (float64, error) {
	// Build the command to get the image size
	cmd := exec.Command("sudo", "buildah", "inspect", "-f", "{{.Size}}", imageName)

	// Run the command and capture the output
	output, err := cmd.Output()
	if err != nil {
		return 0, err
	}

	// Parse the output as a float64
	sizeInBytes, err := strconv.ParseFloat(strings.TrimSpace(string(output)), 64)
	if err != nil {
		return 0, err
	}

	// Convert the size to MB
	sizeInMB := sizeInBytes / (1024 * 1024)

	return sizeInMB, nil
}

func getCheckpointImageRestoreSize(ctx context.Context, clientset *kubernetes.Clientset, numContainers int, db *sql.DB) {
	pod := createContainers(ctx, numContainers, clientset)

	LiveMigrationReconciler := migrationoperator.LiveMigrationReconciler{}

	err := LiveMigrationReconciler.WaitForContainerReady(pod.Name, "default", fmt.Sprintf("container-%d", numContainers-1), clientset)
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

	for i := 0; i < numContainers; i++ {
		// Get the image name
		imageName := "localhost/leonardopoggiani/checkpoint-images:container-" + strconv.Itoa(i)

		// Get the image size
		sizeInMB, err := getImageSize(imageName)
		if err != nil {
			log.Fatal(err)
		}

		fmt.Printf("The size of %s is %.2f MB.\n", imageName, sizeInMB)
	}

	// delete checkpoints folder
	if _, err := exec.Command("sudo", "rm", "-f", directory+"/").Output(); err != nil {
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

func getTimeDirectVsTriangularized(ctx context.Context, clientset *kubernetes.Clientset, numContainers int, db *sql.DB, exchange string) {
	pod := createContainers(ctx, numContainers, clientset)

	LiveMigrationReconciler := migrationoperator.LiveMigrationReconciler{}

	err := LiveMigrationReconciler.WaitForContainerReady(pod.Name, "default", fmt.Sprintf("container-%d", numContainers-1), clientset)
	if err != nil {
		cleanUp(ctx, clientset, pod)
		fmt.Println(err.Error())
		return
	}

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

	pod, err = LiveMigrationReconciler.BuildahRestore(ctx, "/tmp/checkpoints/checkpoints", clientset)
	if err != nil {
		fmt.Println(err.Error())
		return
	}

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

	saveToDB(db, int64(numContainers), elapsed.Seconds(), exchange, "total_times")
}

func saveToDB(db *sql.DB, numContainers int64, size float64, checkpointType string, db_name string) {
	// Prepare SQL statement
	stmt, err := db.Prepare("INSERT INTO " + db_name + " (containers, size, checkpoint_type) VALUES (?, ?, ?)")
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

func performance_evaluation() {
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

	containerCounts := []int{1}
	// 	containerCounts := []int{1, 2, 3, 5, 10}
	repetitions := 2
	//  repetitions := 20

	fmt.Printf("############### SIZE ###############\n")
	for i := 0; i < repetitions; i++ {
		fmt.Printf("Repetition %d\n", i)

		for _, numContainers := range containerCounts {
			fmt.Printf("Size for %d containers\n", numContainers)
			getCheckpointSizePipelined(ctx, clientset, numContainers, db)
		}

		deletePodsStartingWithTest(ctx, clientset)

		fmt.Printf("############### CHECKPOINT TIME ###############\n")

		for _, numContainers := range containerCounts {
			fmt.Printf("Time for %d containers\n", numContainers)
			getCheckpointTimePipelined(ctx, clientset, numContainers, db)
		}

		deletePodsStartingWithTest(ctx, clientset)

	}

	for i := 0; i < repetitions; i++ {
		fmt.Printf("############### SIZE ###############\n")
		fmt.Printf("Repetition %d\n", i)

		for _, numContainers := range containerCounts {
			fmt.Printf("Size for %d containers\n", numContainers)
			getCheckpointSizeSequential(ctx, clientset, numContainers, db)
		}

		deletePodsStartingWithTest(ctx, clientset)

		fmt.Printf("############### CHECKPOINT TIME ###############\n")

		for _, numContainers := range containerCounts {
			fmt.Printf("Time for %d containers\n", numContainers)
			getCheckpointTimeSequential(ctx, clientset, numContainers, db)
		}

		deletePodsStartingWithTest(ctx, clientset)

	}

	fmt.Printf("############### RESTORE TIME ###############\n")

	for i := 0; i < repetitions; i++ {
		fmt.Printf("Repetition %d\n", i)

		for _, numContainers := range containerCounts {
			fmt.Printf("Restore time for %d containers\n", numContainers)
			getRestoreTime(ctx, clientset, numContainers, db)
		}

		deletePodsStartingWithTest(ctx, clientset)
	}

	fmt.Printf("############### DOCKER IMAGE SIZE ###############\n")

	for i := 0; i < repetitions; i++ {
		fmt.Printf("Repetition %d\n", i)

		for _, numContainers := range containerCounts {
			fmt.Printf("Docker image size for %d containers\n", numContainers)
			getCheckpointImageRestoreSize(ctx, clientset, numContainers, db)
		}

		deletePodsStartingWithTest(ctx, clientset)

	}

	fmt.Printf("############### TOTAL TIME ###############\n")

	for i := 0; i < repetitions; i++ {
		fmt.Printf("Repetition %d\n", i)
		for _, numContainers := range containerCounts {
			fmt.Printf("Total times for %d containers\n", numContainers)
			getTimeDirectVsTriangularized(ctx, clientset, numContainers, db, "triangularized")
		}

		deletePodsStartingWithTest(ctx, clientset)

	}

	fmt.Printf("############### DIFFERENCE TIME ###############\n")

	for i := 0; i < repetitions; i++ {
		fmt.Printf("Repetition %d\n", i)
		for _, numContainers := range containerCounts {
			fmt.Printf("Difference times for %d containers\n", numContainers)
			getTimeDirectVsTriangularized(ctx, clientset, numContainers, db, "direct")
		}

		deletePodsStartingWithTest(ctx, clientset)

	}

	deletePodsStartingWithTest(ctx, clientset)

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
