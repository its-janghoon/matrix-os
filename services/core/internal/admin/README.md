# Admin Package - Authentication & Authorization

This package implements OWASP A01 (Broken Access Control) fixes for the admin gRPC server.

## Features

- **API Key Authentication**: Simple API key-based authentication via gRPC metadata
- **Role-Based Access Control (RBAC)**: Three roles with different permission levels
- **Permission Checks**: Fine-grained permissions for different operations
- **gRPC Interceptors**: Automatic authentication checking at the gRPC level

## Roles

### Admin
Full access to all operations:
- Deploy agents and matrices
- Stop and remove deployments
- Read all logs (including sensitive)

### Operator
Can deploy and manage but cannot read sensitive logs:
- Deploy agents and matrices
- Stop and remove deployments
- Read non-sensitive logs

### Viewer
Read-only access:
- Read non-sensitive logs only

## Usage

### Creating a Server with Authentication

```go
adminKey := &admin.APIKey{
    Key:  "your-secret-api-key",
    Role: admin.RoleAdmin,
    Name: "admin-user",
}

server, err := admin.NewServer(admin.Config{
    Addr:        "0.0.0.0:9090",
    RequireAuth: true,
    APIKeys:     []*admin.APIKey{adminKey},
})
```

### Making Authenticated Requests

When making gRPC calls, include the API key in the metadata:

```go
ctx := metadata.NewIncomingContext(context.Background(), metadata.New(map[string]string{
    "authorization": "your-secret-api-key",
}))

// Or with Bearer token format:
ctx := metadata.NewIncomingContext(context.Background(), metadata.New(map[string]string{
    "authorization": "Bearer your-secret-api-key",
}))
```

### Service Methods

All service methods automatically check permissions:

```go
// DeployAgent requires PermissionDeployAgent
err := deploySvc.DeployAgent(ctx, "agent-id", config)

// GetLogs requires PermissionReadLogs
logs, err := logsSvc.GetLogs(ctx, filters)

// Reading sensitive logs requires PermissionReadSensitive
logs, err := logsSvc.GetLogs(ctx, LogFilters{Component: "admin"})
```

## Security Features

1. **Constant-Time Comparison**: API key comparison uses `subtle.ConstantTimeCompare` to prevent timing attacks
2. **Health Check Bypass**: Health check endpoints are excluded from authentication
3. **Granular Permissions**: Each operation checks specific permissions
4. **Sensitive Log Filtering**: Non-admin users cannot access sensitive logs even if they pass the initial auth check

## Testing

Run tests with:
```bash
go test ./internal/admin/... -v
```

Tests cover:
- Authentication with valid/invalid keys
- Authorization for different roles
- Permission checks for all operations
- Integration tests for server setup

## Configuration

The `RequireAuth` flag in the config controls whether authentication is enforced:
- `RequireAuth: true` - All requests must be authenticated
- `RequireAuth: false` - Authentication is optional (for development)

## Future Improvements

- JWT token support
- Token expiration and refresh
- Rate limiting per API key
- Audit logging of authentication events
- API key rotation
- mTLS support
