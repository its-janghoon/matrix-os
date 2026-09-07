// cmd/matrixd/main.go
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/ecirlabs/matrix-core/internal/node"
	"github.com/ecirlabs/matrix-core/internal/version"
)

func main() {
	// Parse command line flags
	initMode := flag.Bool("init", false, "Initialize a new node")
	configPath := flag.String("config", "config.yaml", "Path to config file")
	showVersion := flag.Bool("version", false, "Print the version and exit")
	flag.Parse()

	if *showVersion {
		fmt.Printf("matrixd %s\n", version.String())
		return
	}

	if *initMode {
		if err := node.Initialize(*configPath); err != nil {
			log.Fatalf("Failed to initialize node: %v", err)
		}
		fmt.Printf("Node initialized: %s\n", *configPath)
		// Say where the key is rather than printing it: the file is 0600, and a
		// key echoed here lands in shell history and CI logs. Without this line
		// the key is invisible, and a reader concludes ACLs are simply broken.
		fmt.Println("An admin API key was generated under security.api_keys in that file.")
		fmt.Println("Pass it to the CLI with --api-key, or export MATRIX_ADMIN_API_KEY.")
		return
	}

	// Create node context
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Initialize the node
	n, err := node.New(ctx, *configPath)
	if err != nil {
		log.Fatalf("Failed to create node: %v", err)
	}

	// Start the node
	if err := n.Start(); err != nil {
		log.Fatalf("Failed to start node: %v", err)
	}

	// Handle shutdown signals
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	// Wait for shutdown signal
	<-sigChan
	fmt.Println("\nShutting down gracefully...")

	// Initiate graceful shutdown
	if err := n.Stop(); err != nil {
		log.Printf("Error during shutdown: %v", err)
	}
}
