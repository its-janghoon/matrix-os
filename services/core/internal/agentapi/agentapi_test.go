package agentapi

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	agentv1 "github.com/ecirlabs/matrix-proto/gen/go/matrix/agent/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/ecirlabs/matrix-core/internal/agent"
	"github.com/ecirlabs/matrix-core/internal/kv"
	"github.com/ecirlabs/matrix-core/internal/token"
)

// guestWasm loads the trivial guest module from the agent package's testdata.
// guest.wasm runs to completion (spin.wasm loops forever and is the fuel/timeout
// fixture), so it is the right module for a deploy that must run and return.
func guestWasm(t *testing.T) []byte {
	t.Helper()
	code, err := os.ReadFile("../agent/testdata/guest.wasm")
	if err != nil {
		t.Fatalf("read guest.wasm fixture: %v", err)
	}
	return code
}

func newTestStore(t *testing.T) *kv.Store {
	t.Helper()
	store, err := kv.New(kv.Config{Path: filepath.Join(t.TempDir(), "kv")})
	if err != nil {
		t.Fatalf("open kv store: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store
}

// fakeSettler is an in-memory consensus stand-in for metering tests. It records
// the transfer it is asked to settle and reports a configurable apply outcome so
// a test can drive both a successful charge and an unaffordable one.
type fakeSettler struct {
	applied  bool
	lastFrom string
	lastTo   string
	lastAmt  uint64
	calls    int
}

func (f *fakeSettler) SubmitAccountTransfer(from *token.Account, recipient string, amount, nonce uint64) (*token.Transaction, error) {
	f.calls++
	f.lastFrom = from.AccountID()
	f.lastTo = recipient
	f.lastAmt = amount
	return &token.Transaction{To: recipient, Amount: amount, Nonce: nonce}, nil
}

func (f *fakeSettler) WaitForSettlement(ctx context.Context, tx *token.Transaction) (bool, bool, error) {
	return true, f.applied, nil
}

// fakeAccounts is an in-memory Accounts resolver.
type fakeAccounts map[string]*token.Account

func (a fakeAccounts) Account(id string) (*token.Account, bool) {
	acct, ok := a[id]
	return acct, ok
}

func TestDeployRunsAndPersistsAcrossRestart(t *testing.T) {
	store := newTestStore(t)
	mgr, err := NewManager(ManagerConfig{Store: store})
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}

	res, err := mgr.Deploy(context.Background(), "demo", guestWasm(t), guestLimits(), "")
	if err != nil {
		t.Fatalf("Deploy: %v", err)
	}
	if !res.Ran {
		t.Fatalf("Deploy reported the module did not run: %+v", res.Deployment)
	}
	if res.Deployment.Status != StatusRunning {
		t.Fatalf("status = %q, want running", res.Deployment.Status)
	}
	if res.Charged != 0 {
		t.Fatalf("charged = %d, want 0 (metering disabled)", res.Charged)
	}
	if res.Deployment.ModuleHash == "" || res.Deployment.ModuleSize == 0 {
		t.Fatalf("deployment missing module hash/size: %+v", res.Deployment)
	}

	// A brand-new manager over the SAME store must see the deployment: this is
	// the restart-persistence property.
	restarted, err := NewManager(ManagerConfig{Store: store})
	if err != nil {
		t.Fatalf("NewManager (restart): %v", err)
	}
	got, err := restarted.Get("demo")
	if err != nil {
		t.Fatalf("Get after restart: %v", err)
	}
	if got.ID != "demo" || got.Status != StatusRunning {
		t.Fatalf("reloaded deployment = %+v, want id demo status running", got)
	}
	if list := restarted.List(); len(list) != 1 || list[0].ID != "demo" {
		t.Fatalf("List after restart = %+v, want the one deployment", list)
	}
}

func TestDeployRejectsEmptyAndOversized(t *testing.T) {
	store := newTestStore(t)
	mgr, err := NewManager(ManagerConfig{Store: store, MaxModuleBytes: 8})
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	if _, err := mgr.Deploy(context.Background(), "", guestWasm(t), guestLimits(), ""); !errors.Is(err, ErrEmptyID) {
		t.Fatalf("empty id: err = %v, want ErrEmptyID", err)
	}
	if _, err := mgr.Deploy(context.Background(), "x", nil, guestLimits(), ""); !errors.Is(err, ErrEmptyModule) {
		t.Fatalf("empty module: err = %v, want ErrEmptyModule", err)
	}
	if _, err := mgr.Deploy(context.Background(), "x", guestWasm(t), guestLimits(), ""); !errors.Is(err, ErrModuleTooLarge) {
		t.Fatalf("oversized module: err = %v, want ErrModuleTooLarge", err)
	}
}

func TestMeteringChargesThroughSettler(t *testing.T) {
	store := newTestStore(t)
	payer, err := token.GenerateAccount()
	if err != nil {
		t.Fatalf("GenerateAccount: %v", err)
	}
	settler := &fakeSettler{applied: true}
	mgr, err := NewManager(ManagerConfig{
		Store:    store,
		Meter:    MeterConfig{Price: 7, Recipient: "operator"},
		Settler:  settler,
		Accounts: fakeAccounts{payer.AccountID(): payer},
	})
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	res, err := mgr.Deploy(context.Background(), "metered", guestWasm(t), guestLimits(), payer.AccountID())
	if err != nil {
		t.Fatalf("Deploy: %v", err)
	}
	if res.Charged != 7 {
		t.Fatalf("charged = %d, want 7", res.Charged)
	}
	if settler.lastFrom != payer.AccountID() || settler.lastTo != "operator" || settler.lastAmt != 7 {
		t.Fatalf("settler saw from=%q to=%q amt=%d, want payer->operator 7", settler.lastFrom, settler.lastTo, settler.lastAmt)
	}
	if res.Deployment.LastCharge != 7 {
		t.Fatalf("recorded LastCharge = %d, want 7", res.Deployment.LastCharge)
	}
}

func TestMeteringRefusesWhenUnaffordable(t *testing.T) {
	store := newTestStore(t)
	payer, _ := token.GenerateAccount()
	settler := &fakeSettler{applied: false} // committed but skipped as unaffordable
	mgr, err := NewManager(ManagerConfig{
		Store:    store,
		Meter:    MeterConfig{Price: 100, Recipient: "operator"},
		Settler:  settler,
		Accounts: fakeAccounts{payer.AccountID(): payer},
	})
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	if _, err := mgr.Deploy(context.Background(), "poor", guestWasm(t), guestLimits(), payer.AccountID()); !errors.Is(err, ErrMeterNotApplied) {
		t.Fatalf("Deploy: err = %v, want ErrMeterNotApplied", err)
	}
	// The module must NOT have been persisted or run: a metered deploy that
	// cannot be paid for never runs for free.
	if _, err := mgr.Get("poor"); !errors.Is(err, ErrAgentNotFound) {
		t.Fatalf("Get after refused deploy: err = %v, want ErrAgentNotFound", err)
	}
}

func TestMeteringRefusesWithoutSigningKey(t *testing.T) {
	store := newTestStore(t)
	settler := &fakeSettler{applied: true}
	mgr, err := NewManager(ManagerConfig{
		Store:    store,
		Meter:    MeterConfig{Price: 5, Recipient: "operator"},
		Settler:  settler,
		Accounts: fakeAccounts{}, // no keys
	})
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	if _, err := mgr.Deploy(context.Background(), "keyless", guestWasm(t), guestLimits(), "unknown-account"); !errors.Is(err, ErrNoSigningAccount) {
		t.Fatalf("Deploy: err = %v, want ErrNoSigningAccount", err)
	}
	if settler.calls != 0 {
		t.Fatalf("settler was called %d times for a keyless deploy, want 0", settler.calls)
	}
}

func TestNewManagerRejectsMeteringWithoutRecipientOrDeps(t *testing.T) {
	store := newTestStore(t)
	if _, err := NewManager(ManagerConfig{Store: store, Meter: MeterConfig{Price: 1}}); err == nil {
		t.Fatal("NewManager accepted metering with no recipient")
	}
	if _, err := NewManager(ManagerConfig{Store: store, Meter: MeterConfig{Price: 1, Recipient: "r"}}); err == nil {
		t.Fatal("NewManager accepted metering with no settler")
	}
}

func TestServiceDeployAndList(t *testing.T) {
	store := newTestStore(t)
	mgr, err := NewManager(ManagerConfig{Store: store})
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	svc, err := NewService(mgr)
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	ctx := context.Background()

	dep, err := svc.DeployAgent(ctx, &agentv1.DeployAgentRequest{Id: "svc", WasmModule: guestWasm(t)})
	if err != nil {
		t.Fatalf("DeployAgent: %v", err)
	}
	if !dep.GetRan() || dep.GetAgent().GetStatus() != agentv1.AgentStatus_AGENT_STATUS_RUNNING {
		t.Fatalf("DeployAgent response = %+v, want ran with running status", dep)
	}

	listed, err := svc.ListAgents(ctx, &agentv1.ListAgentsRequest{})
	if err != nil {
		t.Fatalf("ListAgents: %v", err)
	}
	if len(listed.GetAgents()) != 1 || listed.GetAgents()[0].GetId() != "svc" {
		t.Fatalf("ListAgents = %+v, want the one agent", listed.GetAgents())
	}

	if _, err := svc.GetAgent(ctx, &agentv1.GetAgentRequest{Id: "missing"}); status.Code(err) != codes.NotFound {
		t.Fatalf("GetAgent(missing) code = %v, want NotFound", status.Code(err))
	}
	if _, err := svc.DeployAgent(ctx, &agentv1.DeployAgentRequest{Id: "", WasmModule: guestWasm(t)}); status.Code(err) != codes.InvalidArgument {
		t.Fatalf("DeployAgent(empty id) code = %v, want InvalidArgument", status.Code(err))
	}
}

// guestLimits returns valid limits for running the trivial guest fixture.
func guestLimits() agent.ResourceLimits { return agent.DefaultMemoryLimits }
