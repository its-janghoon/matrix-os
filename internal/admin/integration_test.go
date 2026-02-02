package admin

import (
	"context"
	"testing"

	"google.golang.org/grpc/metadata"
)

// Example integration test showing how authentication works end-to-end
func TestServer_WithAuthentication(t *testing.T) {
	// Create server with authentication enabled
	adminKey := &APIKey{
		Key:  "admin-secret-key",
		Role: RoleAdmin,
		Name: "admin",
	}

	server, err := NewServer(Config{
		Addr:        "127.0.0.1:0", // Use 0 to get random port
		RequireAuth: true,
		APIKeys:     []*APIKey{adminKey},
	})
	if err != nil {
		t.Fatalf("Failed to create server: %v", err)
	}

	// Verify authenticator is set up
	auth := server.GetAuthenticator()
	if auth == nil {
		t.Fatal("Authenticator should not be nil")
	}

	// Test authenticated context
	ctx := metadata.NewIncomingContext(context.Background(), metadata.New(map[string]string{
		"authorization": "admin-secret-key",
	}))

	// Test deployment with auth
	deploySvc := server.GetDeployService()
	err = deploySvc.DeployAgent(ctx, "test-agent", map[string]interface{}{
		"image": "test:latest",
	})
	if err != nil {
		t.Errorf("DeployAgent() with valid auth should succeed, got: %v", err)
	}

	// Test logs with auth
	logsSvc := server.GetLogsService()
	logsSvc.AddLog("info", "agent", "test message", nil)
	logs, err := logsSvc.GetLogs(ctx, LogFilters{})
	if err != nil {
		t.Errorf("GetLogs() with valid auth should succeed, got: %v", err)
	}
	if len(logs) == 0 {
		t.Error("Expected at least one log entry")
	}
}

func TestServer_WithoutAuthentication(t *testing.T) {
	// Create server without authentication
	server, err := NewServer(Config{
		Addr:        "127.0.0.1:0",
		RequireAuth: false,
		APIKeys:     nil,
	})
	if err != nil {
		t.Fatalf("Failed to create server: %v", err)
	}

	// Operations should work without auth when RequireAuth is false
	ctx := context.Background()
	deploySvc := server.GetDeployService()
	err = deploySvc.DeployAgent(ctx, "test-agent", map[string]interface{}{})
	if err != nil {
		t.Errorf("DeployAgent() without auth requirement should succeed, got: %v", err)
	}
}

func TestServer_UnauthorizedAccess(t *testing.T) {
	server, err := NewServer(Config{
		Addr:        "127.0.0.1:0",
		RequireAuth: true,
		APIKeys: []*APIKey{
			{
				Key:  "valid-key",
				Role: RoleAdmin,
			},
		},
	})
	if err != nil {
		t.Fatalf("Failed to create server: %v", err)
	}

	// Test without auth header
	ctx := context.Background()
	deploySvc := server.GetDeployService()
	err = deploySvc.DeployAgent(ctx, "test-agent", map[string]interface{}{})
	if err != ErrUnauthorized {
		t.Errorf("DeployAgent() without auth should fail with ErrUnauthorized, got: %v", err)
	}

	// Test with invalid key
	ctx = metadata.NewIncomingContext(context.Background(), metadata.New(map[string]string{
		"authorization": "invalid-key",
	}))
	err = deploySvc.DeployAgent(ctx, "test-agent", map[string]interface{}{})
	if err != ErrUnauthorized {
		t.Errorf("DeployAgent() with invalid key should fail with ErrUnauthorized, got: %v", err)
	}
}

func TestServer_RoleBasedAccess(t *testing.T) {
	server, err := NewServer(Config{
		Addr:        "127.0.0.1:0",
		RequireAuth: true,
		APIKeys: []*APIKey{
			{
				Key:  "admin-key",
				Role: RoleAdmin,
			},
			{
				Key:  "viewer-key",
				Role: RoleViewer,
			},
		},
	})
	if err != nil {
		t.Fatalf("Failed to create server: %v", err)
	}

	deploySvc := server.GetDeployService()
	logsSvc := server.GetLogsService()

	// Admin can deploy
	adminCtx := metadata.NewIncomingContext(context.Background(), metadata.New(map[string]string{
		"authorization": "admin-key",
	}))
	err = deploySvc.DeployAgent(adminCtx, "test-agent", map[string]interface{}{})
	if err != nil {
		t.Errorf("Admin should be able to deploy, got: %v", err)
	}

	// Viewer cannot deploy
	viewerCtx := metadata.NewIncomingContext(context.Background(), metadata.New(map[string]string{
		"authorization": "viewer-key",
	}))
	err = deploySvc.DeployAgent(viewerCtx, "test-agent-2", map[string]interface{}{})
	if err != ErrForbidden {
		t.Errorf("Viewer should not be able to deploy, got: %v", err)
	}

	// Viewer can read logs
	logsSvc.AddLog("info", "agent", "test", nil)
	logs, err := logsSvc.GetLogs(viewerCtx, LogFilters{})
	if err != nil {
		t.Errorf("Viewer should be able to read logs, got: %v", err)
	}
	if len(logs) == 0 {
		t.Error("Expected at least one log entry")
	}
}
