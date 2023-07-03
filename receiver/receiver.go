package main

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"time"

	"github.com/fsnotify/fsnotify"
	migrationoperator "github.com/leonardopoggiani/live-migration-operator/controllers"
	_ "github.com/mattn/go-sqlite3"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
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

func waitForFile(timeout time.Duration) bool {
	filePath := "/tmp/checkpoints/checkpoints/dummy"

	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		log.Fatalf("Failed to create watcher: %v", err)
	}
	defer watcher.Close()

	done := make(chan bool)

	go func() {
		for {
			select {
			case event, ok := <-watcher.Events:
				if !ok {
					return
				}
				if event.Op&fsnotify.Create == fsnotify.Create {
					if filepath.Clean(event.Name) == filePath {
						fmt.Println("File 'dummy' detected.")
						done <- true
					}
				}
			case err, ok := <-watcher.Errors:
				if !ok {
					return
				}
				log.Printf("Error occurred in watcher: %v", err)
			default:
				// Continue executing other tasks or operations
			}
		}
	}()

	err = watcher.Add(filepath.Dir(filePath))
	if err != nil {
		log.Fatalf("Failed to add watcher: %v", err)
	}

	select {
	case <-done:
		return true
	case <-time.After(timeout):
		return false
	}
}

func deleteDummyPodAndService(ctx context.Context, clientset *kubernetes.Clientset) error {

	// Delete Pod
	err := clientset.CoreV1().Pods("liqo-demo").Delete(ctx, "dummy-pod", metav1.DeleteOptions{})
	if err != nil {
		return fmt.Errorf("error deleting pod: %v", err)
	}

	// Delete Service
	err = clientset.CoreV1().Services("liqo-demo").Delete(ctx, "dummy-service", metav1.DeleteOptions{})
	if err != nil {
		return fmt.Errorf("error deleting service: %v", err)
	}

	return nil
}

func main() {
	fmt.Println("Receiver program, waiting for migration request")
	runtime.GOMAXPROCS(runtime.NumCPU())

	// Connect to the SQLite database
	db, err := sql.Open("sqlite3", "performance.db")
	if err != nil {
		fmt.Println(err.Error())
	}
	defer db.Close()

	// Create the time_measurements table if it doesn't exist
	_, err = db.Exec(`
		CREATE TABLE IF NOT EXISTS time_measurements (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			mode TEXT,
			start_time TIMESTAMP,
			end_time TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
			elapsed_time INTEGER
		)
	`)
	if err != nil {
		fmt.Println(err.Error())
	}

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

	reconciler := migrationoperator.LiveMigrationReconciler{}

	// Check if the pod exists
	_, err = clientset.CoreV1().Pods("liqo-demo").Get(ctx, "dummy=pod", metav1.GetOptions{})
	if err == nil {
		_ = deleteDummyPodAndService(ctx, clientset)
		_ = reconciler.WaitForPodDeletion(ctx, "dummy-pod", "liqo-demo", clientset)
	}

	err = reconciler.CreateDummyPod(clientset, ctx)
	if err != nil {
		fmt.Println(err.Error())
		return
	}

	err = reconciler.CreateDummyService(clientset, ctx)
	if err != nil {
		fmt.Println(err.Error())
		return
	}

	directory := "/tmp/checkpoints/checkpoints/"

	for {
		if waitForFile(21000 * time.Second) {
			fmt.Println("File detected, restoring pod")

			start := time.Now()

			pod, err := reconciler.BuildahRestore(ctx, directory, clientset)
			if err != nil {
				fmt.Println(err.Error())
				os.Exit(1) // Terminate the process with a non-zero exit code
			} else {
				fmt.Println("Pod restored")

				reconciler.WaitForContainerReady(pod.Name, "default", pod.Spec.Containers[0].Name, clientset)

				elapsed := time.Since(start)
				fmt.Printf("[MEASURE] Checkpointing took %d\n", elapsed.Milliseconds())

				// Insert the time measurement into the database
				_, err = db.Exec("INSERT INTO time_measurements (mode, start_time, end_time, elapsed_time) VALUES (?, ?, ?, ?)", "sequential_restore", start, time.Now(), elapsed.Milliseconds())
				if err != nil {
					fmt.Println(err.Error())
				}

				deletePodsStartingWithTest(ctx, clientset)
			}

			/// delete checkpoints folder
			if _, err := exec.Command("sudo", "rm", "-rf", directory).Output(); err != nil {
				fmt.Println("Delete checkpoints failed")
				fmt.Println(err.Error())
				return
			}

			if _, err = exec.Command("sudo", "mkdir", "/tmp/checkpoints/checkpoints/").Output(); err != nil {
				fmt.Println(err.Error())
				return
			}

		} else {
			fmt.Println("Timeout: File not detected.")
			os.Exit(1)
		}
	}
}
