package admin

import (
	"context"
	"testing"

	"google.golang.org/grpc/metadata"
)

func TestAuthenticator_AddKey(t *testing.T) {
	auth := NewAuthenticator()

	tests := []struct {
		name    string
		key     *APIKey
		wantErr bool
	}{
		{
			name: "valid key",
			key: &APIKey{
				Key:  "test-key-123",
				Role: RoleAdmin,
				Name: "test",
			},
			wantErr: false,
		},
		{
			name: "empty key",
			key: &APIKey{
				Key:  "",
				Role: RoleAdmin,
			},
			wantErr: true,
		},
		{
			name: "empty role",
			key: &APIKey{
				Key:  "test-key",
				Role: "",
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := auth.AddKey(tt.key)
			if (err != nil) != tt.wantErr {
				t.Errorf("AddKey() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestAuthenticator_Authenticate(t *testing.T) {
	auth := NewAuthenticator()
	adminKey := &APIKey{
		Key:  "admin-key-123",
		Role: RoleAdmin,
		Name: "admin",
	}
	operatorKey := &APIKey{
		Key:  "operator-key-456",
		Role: RoleOperator,
		Name: "operator",
	}

	if err := auth.AddKey(adminKey); err != nil {
		t.Fatalf("Failed to add admin key: %v", err)
	}
	if err := auth.AddKey(operatorKey); err != nil {
		t.Fatalf("Failed to add operator key: %v", err)
	}

	tests := []struct {
		name    string
		ctx     context.Context
		wantRole Role
		wantErr error
	}{
		{
			name:    "valid admin key",
			ctx:     metadata.NewIncomingContext(context.Background(), metadata.New(map[string]string{"authorization": "admin-key-123"})),
			wantRole: RoleAdmin,
			wantErr:  nil,
		},
		{
			name:    "valid operator key",
			ctx:     metadata.NewIncomingContext(context.Background(), metadata.New(map[string]string{"authorization": "operator-key-456"})),
			wantRole: RoleOperator,
			wantErr:  nil,
		},
		{
			name:    "bearer token format",
			ctx:     metadata.NewIncomingContext(context.Background(), metadata.New(map[string]string{"authorization": "Bearer admin-key-123"})),
			wantRole: RoleAdmin,
			wantErr:  nil,
		},
		{
			name:    "invalid key",
			ctx:     metadata.NewIncomingContext(context.Background(), metadata.New(map[string]string{"authorization": "invalid-key"})),
			wantRole: "",
			wantErr:  ErrUnauthorized,
		},
		{
			name:    "no metadata",
			ctx:     context.Background(),
			wantRole: "",
			wantErr:  ErrUnauthorized,
		},
		{
			name:    "no authorization header",
			ctx:     metadata.NewIncomingContext(context.Background(), metadata.New(map[string]string{})),
			wantRole: "",
			wantErr:  ErrUnauthorized,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			role, err := auth.Authenticate(tt.ctx)
			if err != tt.wantErr {
				t.Errorf("Authenticate() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if role != tt.wantRole {
				t.Errorf("Authenticate() role = %v, want %v", role, tt.wantRole)
			}
		})
	}
}

func TestAuthenticator_Authorize(t *testing.T) {
	auth := NewAuthenticator()

	tests := []struct {
		name       string
		role       Role
		permission Permission
		wantErr    error
	}{
		{
			name:       "admin can deploy agent",
			role:       RoleAdmin,
			permission: PermissionDeployAgent,
			wantErr:    nil,
		},
		{
			name:       "admin can read sensitive logs",
			role:       RoleAdmin,
			permission: PermissionReadSensitive,
			wantErr:    nil,
		},
		{
			name:       "operator can deploy agent",
			role:       RoleOperator,
			permission: PermissionDeployAgent,
			wantErr:    nil,
		},
		{
			name:       "operator cannot read sensitive logs",
			role:       RoleOperator,
			permission: PermissionReadSensitive,
			wantErr:    ErrForbidden,
		},
		{
			name:       "viewer cannot deploy",
			role:       RoleViewer,
			permission: PermissionDeployAgent,
			wantErr:    ErrForbidden,
		},
		{
			name:       "viewer can read logs",
			role:       RoleViewer,
			permission: PermissionReadLogs,
			wantErr:    nil,
		},
		{
			name:       "invalid role",
			role:       Role("invalid"),
			permission: PermissionReadLogs,
			wantErr:    ErrForbidden,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := auth.Authorize(tt.role, tt.permission)
			if err != tt.wantErr {
				t.Errorf("Authorize() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestAuthenticator_CheckPermission(t *testing.T) {
	auth := NewAuthenticator()
	adminKey := &APIKey{
		Key:  "admin-key",
		Role: RoleAdmin,
	}
	operatorKey := &APIKey{
		Key:  "operator-key",
		Role: RoleOperator,
	}

	if err := auth.AddKey(adminKey); err != nil {
		t.Fatalf("Failed to add admin key: %v", err)
	}
	if err := auth.AddKey(operatorKey); err != nil {
		t.Fatalf("Failed to add operator key: %v", err)
	}

	tests := []struct {
		name       string
		ctx        context.Context
		permission Permission
		wantRole   Role
		wantErr    error
	}{
		{
			name:       "admin can deploy",
			ctx:        metadata.NewIncomingContext(context.Background(), metadata.New(map[string]string{"authorization": "admin-key"})),
			permission: PermissionDeployAgent,
			wantRole:   RoleAdmin,
			wantErr:    nil,
		},
		{
			name:       "operator can deploy",
			ctx:        metadata.NewIncomingContext(context.Background(), metadata.New(map[string]string{"authorization": "operator-key"})),
			permission: PermissionDeployAgent,
			wantRole:   RoleOperator,
			wantErr:    nil,
		},
		{
			name:       "operator cannot read sensitive",
			ctx:        metadata.NewIncomingContext(context.Background(), metadata.New(map[string]string{"authorization": "operator-key"})),
			permission: PermissionReadSensitive,
			wantRole:   "",
			wantErr:    ErrForbidden,
		},
		{
			name:       "no auth header",
			ctx:        context.Background(),
			permission: PermissionDeployAgent,
			wantRole:   "",
			wantErr:    ErrUnauthorized,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			role, err := auth.CheckPermission(tt.ctx, tt.permission)
			if err != tt.wantErr {
				t.Errorf("CheckPermission() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if role != tt.wantRole {
				t.Errorf("CheckPermission() role = %v, want %v", role, tt.wantRole)
			}
		})
	}
}

func TestDeployService_Authorization(t *testing.T) {
	auth := NewAuthenticator()
	adminKey := &APIKey{
		Key:  "admin-key",
		Role: RoleAdmin,
	}
	viewerKey := &APIKey{
		Key:  "viewer-key",
		Role: RoleViewer,
	}

	if err := auth.AddKey(adminKey); err != nil {
		t.Fatalf("Failed to add admin key: %v", err)
	}
	if err := auth.AddKey(viewerKey); err != nil {
		t.Fatalf("Failed to add viewer key: %v", err)
	}

	service := NewDeployService(auth)

	tests := []struct {
		name    string
		ctx     context.Context
		fn      func(context.Context) error
		wantErr error
	}{
		{
			name: "admin can deploy agent",
			ctx:  metadata.NewIncomingContext(context.Background(), metadata.New(map[string]string{"authorization": "admin-key"})),
			fn: func(ctx context.Context) error {
				return service.DeployAgent(ctx, "test-agent", map[string]interface{}{})
			},
			wantErr: nil,
		},
		{
			name: "viewer cannot deploy agent",
			ctx:  metadata.NewIncomingContext(context.Background(), metadata.New(map[string]string{"authorization": "viewer-key"})),
			fn: func(ctx context.Context) error {
				return service.DeployAgent(ctx, "test-agent", map[string]interface{}{})
			},
			wantErr: ErrForbidden,
		},
		{
			name: "no auth cannot deploy",
			ctx:  context.Background(),
			fn: func(ctx context.Context) error {
				return service.DeployAgent(ctx, "test-agent", map[string]interface{}{})
			},
			wantErr: ErrUnauthorized,
		},
		{
			name: "admin can stop deployment",
			ctx:  metadata.NewIncomingContext(context.Background(), metadata.New(map[string]string{"authorization": "admin-key"})),
			fn: func(ctx context.Context) error {
				// First deploy
				if err := service.DeployAgent(ctx, "test-agent", map[string]interface{}{}); err != nil {
					return err
				}
				return service.StopDeployment(ctx, "test-agent")
			},
			wantErr: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.fn(tt.ctx)
			if err != tt.wantErr {
				t.Errorf("error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestLogsService_Authorization(t *testing.T) {
	auth := NewAuthenticator()
	adminKey := &APIKey{
		Key:  "admin-key",
		Role: RoleAdmin,
	}
	operatorKey := &APIKey{
		Key:  "operator-key",
		Role: RoleOperator,
	}

	if err := auth.AddKey(adminKey); err != nil {
		t.Fatalf("Failed to add admin key: %v", err)
	}
	if err := auth.AddKey(operatorKey); err != nil {
		t.Fatalf("Failed to add operator key: %v", err)
	}

	service := NewLogsService(auth)

	// Add some test logs
	service.AddLog("info", "agent", "agent started", nil)
	service.AddLog("info", "admin", "admin action", nil)

	tests := []struct {
		name    string
		ctx     context.Context
		filters LogFilters
		wantLen int
		wantErr error
	}{
		{
			name:    "admin can read all logs",
			ctx:     metadata.NewIncomingContext(context.Background(), metadata.New(map[string]string{"authorization": "admin-key"})),
			filters: LogFilters{},
			wantLen: 2,
			wantErr: nil,
		},
		{
			name:    "admin can read sensitive logs",
			ctx:     metadata.NewIncomingContext(context.Background(), metadata.New(map[string]string{"authorization": "admin-key"})),
			filters: LogFilters{Component: "admin"},
			wantLen: 1,
			wantErr: nil,
		},
		{
			name:    "operator can read non-sensitive logs",
			ctx:     metadata.NewIncomingContext(context.Background(), metadata.New(map[string]string{"authorization": "operator-key"})),
			filters: LogFilters{Component: "agent"},
			wantLen: 1,
			wantErr: nil,
		},
		{
			name:    "operator cannot read sensitive logs",
			ctx:     metadata.NewIncomingContext(context.Background(), metadata.New(map[string]string{"authorization": "operator-key"})),
			filters: LogFilters{Component: "admin"},
			wantLen: 0,
			wantErr: ErrForbidden,
		},
		{
			name:    "no auth cannot read logs",
			ctx:     context.Background(),
			filters: LogFilters{},
			wantLen: 0,
			wantErr: ErrUnauthorized,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			logs, err := service.GetLogs(tt.ctx, tt.filters)
			if err != tt.wantErr {
				t.Errorf("GetLogs() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if len(logs) != tt.wantLen {
				t.Errorf("GetLogs() len = %v, want %v", len(logs), tt.wantLen)
			}
		})
	}
}
