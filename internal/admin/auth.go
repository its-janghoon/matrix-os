package admin

import (
	"context"
	"crypto/subtle"
	"errors"
	"fmt"
	"sync"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

var (
	// ErrUnauthorized is returned when authentication fails
	ErrUnauthorized = errors.New("unauthorized")
	// ErrForbidden is returned when authorization fails
	ErrForbidden = errors.New("forbidden")
)

// Role represents a user role
type Role string

const (
	// RoleAdmin has full access to all operations
	RoleAdmin Role = "admin"
	// RoleOperator can deploy and manage but not read sensitive logs
	RoleOperator Role = "operator"
	// RoleViewer can only read logs and list deployments
	RoleViewer Role = "viewer"
)

// Permission represents what actions a role can perform
type Permission string

const (
	PermissionDeployAgent  Permission = "deploy:agent"
	PermissionDeployMatrix Permission = "deploy:matrix"
	PermissionStopDeploy   Permission = "deploy:stop"
	PermissionRemoveDeploy Permission = "deploy:remove"
	PermissionReadLogs     Permission = "logs:read"
	PermissionReadSensitive Permission = "logs:sensitive"
)

// rolePermissions maps roles to their permissions
var rolePermissions = map[Role][]Permission{
	RoleAdmin: {
		PermissionDeployAgent,
		PermissionDeployMatrix,
		PermissionStopDeploy,
		PermissionRemoveDeploy,
		PermissionReadLogs,
		PermissionReadSensitive,
	},
	RoleOperator: {
		PermissionDeployAgent,
		PermissionDeployMatrix,
		PermissionStopDeploy,
		PermissionRemoveDeploy,
		PermissionReadLogs,
	},
	RoleViewer: {
		PermissionReadLogs,
	},
}

// APIKey represents an API key with associated role
type APIKey struct {
	Key  string
	Role Role
	Name string
}

// Authenticator handles authentication and authorization
type Authenticator struct {
	keys map[string]*APIKey
	mu   sync.RWMutex
}

// NewAuthenticator creates a new authenticator
func NewAuthenticator() *Authenticator {
	return &Authenticator{
		keys: make(map[string]*APIKey),
	}
}

// AddKey adds an API key to the authenticator
func (a *Authenticator) AddKey(key *APIKey) error {
	if key.Key == "" {
		return fmt.Errorf("key cannot be empty")
	}
	if key.Role == "" {
		return fmt.Errorf("role cannot be empty")
	}

	a.mu.Lock()
	defer a.mu.Unlock()

	a.keys[key.Key] = key
	return nil
}

// RemoveKey removes an API key
func (a *Authenticator) RemoveKey(key string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	delete(a.keys, key)
}

// Authenticate validates an API key and returns the associated role
func (a *Authenticator) Authenticate(ctx context.Context) (Role, error) {
	md, ok := metadata.FromIncomingContext(ctx)
	if !ok {
		return "", ErrUnauthorized
	}

	// Extract API key from metadata
	apiKeys := md.Get("authorization")
	if len(apiKeys) == 0 {
		return "", ErrUnauthorized
	}

	// Support "Bearer <token>" or just the token
	apiKey := apiKeys[0]
	if len(apiKey) > 7 && apiKey[:7] == "Bearer " {
		apiKey = apiKey[7:]
	}

	a.mu.RLock()
	defer a.mu.RUnlock()

	key, exists := a.keys[apiKey]
	if !exists {
		return "", ErrUnauthorized
	}

	// Use constant-time comparison to prevent timing attacks
	if subtle.ConstantTimeCompare([]byte(apiKey), []byte(key.Key)) != 1 {
		return "", ErrUnauthorized
	}

	return key.Role, nil
}

// Authorize checks if a role has the required permission
func (a *Authenticator) Authorize(role Role, permission Permission) error {
	permissions, exists := rolePermissions[role]
	if !exists {
		return ErrForbidden
	}

	for _, p := range permissions {
		if p == permission {
			return nil
		}
	}

	return ErrForbidden
}

// CheckPermission checks authentication and authorization in one call
func (a *Authenticator) CheckPermission(ctx context.Context, permission Permission) (Role, error) {
	role, err := a.Authenticate(ctx)
	if err != nil {
		return "", err
	}

	if err := a.Authorize(role, permission); err != nil {
		return "", err
	}

	return role, nil
}

// UnaryAuthInterceptor creates a gRPC unary interceptor for authentication
func (a *Authenticator) UnaryAuthInterceptor(permission Permission) grpc.UnaryServerInterceptor {
	return func(
		ctx context.Context,
		req interface{},
		info *grpc.UnaryServerInfo,
		handler grpc.UnaryHandler,
	) (interface{}, error) {
		// Skip auth for health check
		if info.FullMethod == "/grpc.health.v1.Health/Check" {
			return handler(ctx, req)
		}

		_, err := a.CheckPermission(ctx, permission)
		if err != nil {
			if err == ErrUnauthorized {
				return nil, status.Errorf(codes.Unauthenticated, "authentication required")
			}
			return nil, status.Errorf(codes.PermissionDenied, "insufficient permissions")
		}

		return handler(ctx, req)
	}
}

// StreamAuthInterceptor creates a gRPC stream interceptor for authentication
func (a *Authenticator) StreamAuthInterceptor(permission Permission) grpc.StreamServerInterceptor {
	return func(
		srv interface{},
		ss grpc.ServerStream,
		info *grpc.StreamServerInfo,
		handler grpc.StreamHandler,
	) error {
		// Skip auth for health check
		if info.FullMethod == "/grpc.health.v1.Health/Watch" {
			return handler(srv, ss)
		}

		_, err := a.CheckPermission(ss.Context(), permission)
		if err != nil {
			if err == ErrUnauthorized {
				return status.Errorf(codes.Unauthenticated, "authentication required")
			}
			return status.Errorf(codes.PermissionDenied, "insufficient permissions")
		}

		return handler(srv, ss)
	}
}

// requireAuthUnaryInterceptor requires authentication but doesn't check specific permissions
// Individual methods will check their own permissions
func (a *Authenticator) requireAuthUnaryInterceptor(
	ctx context.Context,
	req interface{},
	info *grpc.UnaryServerInfo,
	handler grpc.UnaryHandler,
) (interface{}, error) {
	// Skip auth for health check
	if info.FullMethod == "/grpc.health.v1.Health/Check" {
		return handler(ctx, req)
	}

	_, err := a.Authenticate(ctx)
	if err != nil {
		return nil, status.Errorf(codes.Unauthenticated, "authentication required")
	}

	return handler(ctx, req)
}

// requireAuthStreamInterceptor requires authentication but doesn't check specific permissions
func (a *Authenticator) requireAuthStreamInterceptor(
	srv interface{},
	ss grpc.ServerStream,
	info *grpc.StreamServerInfo,
	handler grpc.StreamHandler,
) error {
	// Skip auth for health check
	if info.FullMethod == "/grpc.health.v1.Health/Watch" {
		return handler(srv, ss)
	}

	_, err := a.Authenticate(ss.Context())
	if err != nil {
		return status.Errorf(codes.Unauthenticated, "authentication required")
	}

	return handler(srv, ss)
}
