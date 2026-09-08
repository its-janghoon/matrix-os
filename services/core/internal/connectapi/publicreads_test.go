package connectapi

import (
	"net/http"
	"sort"
	"testing"

	agentv1 "github.com/ecirlabs/matrix-proto/gen/go/matrix/agent/v1"
	inferencev1 "github.com/ecirlabs/matrix-proto/gen/go/matrix/inference/v1"
	marketv1 "github.com/ecirlabs/matrix-proto/gen/go/matrix/market/v1"
	"google.golang.org/grpc"
)

// TestReadClassificationIsPinned is what makes the Get*/List* rule safe to rely
// on. It enumerates every method the node actually serves and pins which side of
// the line each falls on. Add a mutating `GetSomething` and this test fails
// rather than the method quietly becoming world-callable.
func TestReadClassificationIsPinned(t *testing.T) {
	descs := []*grpc.ServiceDesc{
		&marketv1.MarketService_ServiceDesc,
		&inferencev1.InferenceService_ServiceDesc,
		&agentv1.AgentService_ServiceDesc,
	}

	wantReads := []string{
		"GetAgent",
		"GetBalance",
		"GetBridgeReconciliation",
		"GetInferenceJob",
		"GetJob",
		// GetLockAttestation is a read and is deliberately on the public side.
		// It signs nothing new: it returns this node's signature over a lock the
		// chain has ALREADY committed, and the mint that signature authorizes
		// goes to the Ethereum address the locker chose, not to whoever asked.
		// So an open endpoint lets anyone GATHER an authorization and nobody
		// redirect one - and a browser doing the bridge flow needs exactly that,
		// since it must collect a threshold from several validators.
		//
		// What it does cost is an ECDSA signature per call, which is why it sits
		// behind the same rate limiter as every other public read and why
		// public_reads is off by default.
		"GetLockAttestation",
		"GetTransaction",
		"ListAgents",
		"ListJobs",
		"ListProviders",
		"ListTransactions",
	}
	wantWrites := []string{
		"CancelJob",
		"CompleteJob",
		"DeployAgent",
		"FulfillInferenceJob",
		"FundAccount",
		"RegisterProvider",
		"RunInferenceJob",
		"SettleInferenceJob",
		"SubmitInferenceJob",
		"SubmitJob",
		"SubmitSignedTransfer",
	}

	var reads, writes []string
	for _, d := range descs {
		for _, m := range d.Methods {
			if isReadMethod(m.MethodName) {
				reads = append(reads, m.MethodName)
			} else {
				writes = append(writes, m.MethodName)
			}
		}
	}
	sort.Strings(reads)
	sort.Strings(writes)

	if !equal(reads, wantReads) {
		t.Errorf("reads = %v,\n  want %v", reads, wantReads)
	}
	if !equal(writes, wantWrites) {
		t.Errorf("writes = %v,\n  want %v", writes, wantWrites)
	}
}

func equal(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func publicReadHandler(t *testing.T, publicReads bool) http.Handler {
	t.Helper()
	h, err := NewHandler(Config{
		Bindings:       []Binding{{Desc: &marketv1.MarketService_ServiceDesc, Impl: &fakeMarket{}}},
		Auth:           denyAll{},
		PublicReads:    publicReads,
		AllowedOrigins: []string{"*"},
	})
	if err != nil {
		t.Fatalf("NewHandler: %v", err)
	}
	return h
}

func TestPublicReadsIsOffByDefault(t *testing.T) {
	h := publicReadHandler(t, false)

	// Off by default is not a preference, it is the only safe default: turning it
	// on by default would widen what an unauthenticated caller can see on every
	// node that already exists.
	resp := post(t, h, "/matrix.market.v1.MarketService/GetBalance", `{"account":"alice"}`, nil)
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401 with public reads off", resp.StatusCode)
	}
}

func TestPublicReadsOpensReadsAndOnlyReads(t *testing.T) {
	h := publicReadHandler(t, true)

	read := post(t, h, "/matrix.market.v1.MarketService/GetBalance", `{"account":"alice"}`, nil)
	if read.StatusCode == http.StatusUnauthorized {
		t.Fatal("a read should be reachable with public reads on")
	}

	// The whole point: opening reads must not open a write, or a keyless caller
	// could move money.
	for _, write := range []struct{ path, body string }{
		{"/matrix.market.v1.MarketService/SubmitSignedTransfer", `{"to":"bob","amount":"1"}`},
		{"/matrix.market.v1.MarketService/FundAccount", `{"account":"alice","amount":"1"}`},
		{"/matrix.market.v1.MarketService/RegisterProvider", `{"id":"p","capacity":"1","pricePerUnit":"1"}`},
		{"/matrix.market.v1.MarketService/SubmitJob", `{"buyer":"a","provider":"p","units":"1"}`},
		{"/matrix.market.v1.MarketService/CompleteJob", `{"id":"j"}`},
	} {
		resp := post(t, h, write.path, write.body, nil)
		if resp.StatusCode != http.StatusUnauthorized {
			t.Errorf("%s = %d, want 401: public reads must never open a write", write.path, resp.StatusCode)
		}
	}
}

// TestPublicReadsStillAcceptsACredential: a page may be keyless while the CLI
// against the same endpoint is not, so a key on a read must not be rejected.
func TestPublicReadsStillAcceptsACredential(t *testing.T) {
	h, err := NewHandler(Config{
		Bindings:       []Binding{{Desc: &marketv1.MarketService_ServiceDesc, Impl: &fakeMarket{}}},
		Auth:           requireKey{key: "k"},
		PublicReads:    true,
		AllowedOrigins: []string{"*"},
	})
	if err != nil {
		t.Fatalf("NewHandler: %v", err)
	}

	resp := post(t, h, "/matrix.market.v1.MarketService/GetBalance", `{"account":"alice"}`,
		map[string]string{"Authorization": "Bearer k"})
	if resp.StatusCode == http.StatusUnauthorized {
		t.Fatal("a read with a valid key must still be served")
	}
}

// TestPublicReadsWithNoAuthenticatorChangesNothing: with ACLs off the endpoint
// is already open, so the flag must not become a second, confusing switch.
func TestPublicReadsWithNoAuthenticatorChangesNothing(t *testing.T) {
	h, err := NewHandler(Config{
		Bindings:       []Binding{{Desc: &marketv1.MarketService_ServiceDesc, Impl: &fakeMarket{}}},
		PublicReads:    true,
		AllowedOrigins: []string{"*"},
	})
	if err != nil {
		t.Fatalf("NewHandler: %v", err)
	}
	resp := post(t, h, "/matrix.market.v1.MarketService/SubmitJob", `{"buyer":"a","provider":"p","units":"1"}`, nil)
	if resp.StatusCode == http.StatusUnauthorized {
		t.Fatal("with no authenticator configured nothing is gated, reads or writes")
	}
}
