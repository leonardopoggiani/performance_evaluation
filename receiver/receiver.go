package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/fsnotify/fsnotify"
	migrationoperator "github.com/leonardopoggiani/live-migration-operator/controllers"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"
)

func waitForFileCreation() error {
	directory := "/tmp/checkpoints/checkpoints"
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return err
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
					filename := filepath.Base(event.Name)
					if filename == "dummy" {
						fmt.Println("File 'dummy' created")
						done <- true
						return
					}
				}
			case err, ok := <-watcher.Errors:
				if !ok {
					return
				}
				fmt.Println("Error:", err)
			}
		}
	}()

	err = filepath.Walk(directory, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.Mode().IsRegular() {
			err = watcher.Add(path)
			if err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return err
	}

	// Sort the files by modification time
	files, err := readDirectory(directory)
	if err != nil {
		return err
	}
	sort.Slice(files, func(i, j int) bool {
		return files[i].ModTime().Before(files[j].ModTime())
	})

	// Check if the last file is "dummy"
	if len(files) > 0 && files[len(files)-1].Name() == "dummy" {
		fmt.Println("File 'dummy' already exists")
		return nil
	}

	fmt.Println("Waiting for file 'dummy' to be created...")
	select {
	case <-done:
		return nil
	case <-time.After(60 * time.Second): // Timeout after 60 seconds
		return fmt.Errorf("Timeout: File 'dummy' not created within the specified time")
	}
}

func readDirectory(directory string) ([]os.FileInfo, error) {
	files := []os.FileInfo{}

	err := filepath.WalkDir(directory, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}

		if !d.IsDir() {
			fileInfo, err := d.Info()
			if err != nil {
				return err
			}
			files = append(files, fileInfo)
		}

		return nil
	})

	if err != nil {
		return nil, err
	}

	return files, nil
}

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

	reconciler := migrationoperator.LiveMigrationReconciler{}

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

	err = waitForFileCreation()
	if err != nil {
		fmt.Println(err.Error())
		return
	}

	directory := "/tmp/checkpoints/checkpoints/"

	_, err = reconciler.BuildahRestore(ctx, directory, clientset)
	if err != nil {
		fmt.Println(err.Error())
		return
	} else {
		fmt.Println("Pod restored")
	}
}
