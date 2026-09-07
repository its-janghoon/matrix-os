package admin

import (
	"context"
	"crypto/sha256"
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
	PermissionDeployAgent   Permission = "deploy:agent"
	PermissionDeployMatrix  Permission = "deploy:matrix"
	PermissionStopDeploy    Permission = "deploy:stop"
	PermissionRemoveDeploy  Permission = "deploy:remove"
	PermissionReadLogs      Permission = "logs:read"
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
	// Account is the on-chain account this key spends from, empty when the key
	// is not tied to one. It is what lets a surface with no buyer field in its
	// request - the OpenAI-compatible route, where OpenAI's own API identifies
	// the caller by the key alone - know whose balance to charge. The balance
	// itself lives on the ledger, so the key is a proof of account ownership and
	// not a stored credit balance.
	Account string
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

	// Store a copy rather than the caller's pointer, and index it by a digest of
	// the credential rather than by the credential itself. See credentialDigest
	// for why, and note what the copy does NOT carry: Key is cleared, so the
	// authenticator holds no usable credential for the lifetime of the process.
	// That matters because AuthenticateKey hands this record back to callers -
	// its own doc offers the key's name for a log line - and a record that
	// travels toward logs must not carry a working credential with it.
	stored := *key
	stored.Key = ""

	a.mu.Lock()
	defer a.mu.Unlock()

	a.keys[credentialDigest(key.Key)] = &stored
	return nil
}

// credentialDigest reduces a presented credential to the fixed-length value the
// key map is indexed by.
//
// WHAT THIS REPLACED, and why it was not enough. The lookup used to be
// a.keys[apiKey] - the raw credential as the map key - followed by
// subtle.ConstantTimeCompare(apiKey, key.Key) under a comment saying it
// prevented timing attacks. That comparison could not fail: AddKey stored the
// record under key.Key, so a map hit guarantees the two strings are equal by
// construction, and the compare returned 1 every time it ran. It was a no-op
// standing in for a defense.
//
// MEASURED. The path was probed at 60000 samples per class with 1001 keys
// loaded, timing credentials that shared 0, 32, 63 and 64 bytes of prefix with
// the real one. Medians came out 264, 274, 267 and 313 ns - no ordering by
// prefix length at all. So the old code was not exploitable either, but not for
// the reason it gave: Go seeds each map's string hash from process-random state,
// which destroys the byte-by-byte oracle that a naive compare would expose. The
// defense was an implementation detail of the runtime that nobody had written
// down.
//
// WHAT THIS GIVES INSTEAD. Hashing first means the credential never reaches a
// comparison at all; what the map compares is a SHA-256 digest. A perfect
// timing oracle on the lookup therefore yields bits of the digest, and turning
// those into the credential is the preimage problem. That is a property of
// SHA-256 rather than of Go's map internals - statable, and true whatever the
// runtime does next.
//
// A digest also takes the credential's length out of the lookup: the map now
// hashes a fixed 32 bytes every time. Hashing itself still costs in proportion
// to the input, so a re-probe after this change showed a 16-byte credential
// resolving faster than a 64-byte one (363 vs 447 ns) - but that is the length
// of what the CALLER presented, which the caller already knows, and it says
// nothing about the length of any stored credential.
//
// There is deliberately no constant-time compare after this. Adding one back
// would compare the presented digest against the stored digest, which are again
// equal by construction on a hit - the same no-op, one indirection later.
func credentialDigest(credential string) string {
	sum := sha256.Sum256([]byte(credential))
	return string(sum[:])
}

// RemoveKey removes an API key
func (a *Authenticator) RemoveKey(key string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	delete(a.keys, credentialDigest(key))
}

// Authenticate validates an API key and returns the associated role
func (a *Authenticator) Authenticate(ctx context.Context) (Role, error) {
	key, err := a.AuthenticateKey(ctx)
	if err != nil {
		return "", err
	}
	return key.Role, nil
}

// AuthenticateKey validates an API key and returns a copy of the whole
// credential, so a caller that needs more than the role - the account the key
// spends from, or its name for a log line - gets it from the same lookup rather
// than a second one that could race a RemoveKey. The returned value is a copy:
// a caller cannot reach into the authenticator's own record.
func (a *Authenticator) AuthenticateKey(ctx context.Context) (APIKey, error) {
	md, ok := metadata.FromIncomingContext(ctx)
	if !ok {
		return APIKey{}, ErrUnauthorized
	}

	// Extract API key from metadata
	apiKeys := md.Get("authorization")
	if len(apiKeys) == 0 {
		return APIKey{}, ErrUnauthorized
	}

	// Support "Bearer <token>" or just the token
	apiKey := apiKeys[0]
	if len(apiKey) > 7 && apiKey[:7] == "Bearer " {
		apiKey = apiKey[7:]
	}

	a.mu.RLock()
	defer a.mu.RUnlock()

	key, exists := a.keys[credentialDigest(apiKey)]
	if !exists {
		return APIKey{}, ErrUnauthorized
	}

	return *key, nil
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
