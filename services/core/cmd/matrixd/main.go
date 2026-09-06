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
)

func main() {
	// Parse command line flags
	initMode := flag.Bool("init", false, "Initialize a new node")
	configPath := flag.String("config", "config.yaml", "Path to config file")
	flag.Parse()

	if *initMode {
		if err := node.Initialize(*configPath); err != nil {
			log.Fatalf("Failed to initialize node: %v", err)
		}
		fmt.Println("Node initialized successfully")
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
