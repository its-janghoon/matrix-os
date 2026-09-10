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
	initMode := flag.Bool("init", false, "Write a secure baseline config (not a production launch profile)")
	initIdentities := flag.Bool("init-identities", false, "Create and print stable node identities without applying genesis")
	preflightProduction := flag.Bool("preflight-production", false, "Validate consensus-critical production config without applying genesis")
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
		fmt.Println("This is a secure baseline, not a production launch profile; review origins, public access, economics, genesis, peers, and bridge settings before exposure.")
		fmt.Println("An admin API key was generated under security.api_keys in that file.")
		fmt.Println("Pass it to the CLI with --api-key, or export MATRIX_ADMIN_API_KEY.")
		return
	}

	if *initIdentities {
		ids, err := node.InitializeIdentities(*configPath)
		if err != nil {
			log.Fatalf("Failed to initialize identities: %v", err)
		}
		out, err := node.MarshalIdentities(ids)
		if err != nil {
			log.Fatalf("Failed to encode identities: %v", err)
		}
		fmt.Println(string(out))
		return
	}

	if *preflightProduction {
		result, err := node.ValidateProductionConfig(*configPath)
		if err != nil {
			log.Fatalf("Production preflight failed: %v", err)
		}
		out, err := node.MarshalProductionPreflight(result)
		if err != nil {
			log.Fatalf("Failed to encode production preflight: %v", err)
		}
		fmt.Println(string(out))
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
