package inference

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/ecirlabs/matrix-core/internal/kv"
	"github.com/ecirlabs/matrix-core/internal/market"
	"github.com/ecirlabs/matrix-core/internal/token"
)

// The money path a GPU provider actually cares about, with a wallet at BOTH
// ends: the buyer pays from MetaMask, and the provider is paid to an Ethereum
// address it controls with the same kind of key.
//
// The existing eth tests prove a wallet can BUY. What they do not cover is the
// seller's side, and a provider whose payout account the chain cannot credit
// earns nothing however well the buying half works.

// ledgerSettler applies settlements to a real ledger, so a test can assert that
// money moved rather than that a call was made. The fake settler used elsewhere
// records intent; this one is the difference between "the node tried to pay the
// provider" and "the provider has the money".
type ledgerSettler struct {
	mu     sync.Mutex
	ledger *market.Ledger
	// feeBasisPoints mirrors the protocol fee consensus would take, so the
	// provider's proceeds in this test are the proceeds it would really see.
	feeBasisPoints uint64
	applied        []*token.Transaction
}

func (s *ledgerSettler) SubmitAccountTransfer(from *token.Account, recipient string, amount, nonce uint64) (*token.Transaction, error) {
	tx := &token.Transaction{From: from.PublicKey, To: recipient, Amount: amount, Nonce: nonce}
	if err := tx.Sign(from.PrivateKey); err != nil {
		return nil, err
	}
	return tx, s.Submit(tx)
}

func (s *ledgerSettler) Submit(tx *token.Transaction) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	// The fee comes out of the amount, exactly as the apply path does it: the
	// provider receives the net, not the gross.
	fee := tx.Amount * s.feeBasisPoints / 10_000
	if err := s.ledger.Transfer(tx.SenderID(), tx.To, tx.Amount-fee); err != nil {
		return err
	}
	if fee > 0 {
		if err := s.ledger.Debit(tx.SenderID(), fee); err != nil {
			return err
		}
	}
	s.applied = append(s.applied, tx)
	return nil
}

func (s *ledgerSettler) WaitForSettlement(context.Context, *token.Transaction) (bool, bool, error) {
	return true, true, nil
}

// TestAGPUProviderIsPaidToItsWalletAddress walks the whole path: a provider
// whose order-book id IS the Ethereum address its operator holds in a wallet,
// a buyer paying from another wallet, and no ed25519 key on either side.
func TestAGPUProviderIsPaidToItsWalletAddress(t *testing.T) {
	const (
		pricePerUnit   = 4
		feeBasisPoints = 100 // 1%, the launch policy
		buyerFunds     = 1_000_000
	)

	gpuBox := newMetamask(t)
	buyer := newMetamask(t)
	providerID := gpuBox.accountID()

	store, err := kv.New(kv.Config{Path: t.TempDir()})
	if err != nil {
		t.Fatalf("kv.New: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	mkt, err := market.NewMarket(store)
	if err != nil {
		t.Fatalf("market.NewMarket: %v", err)
	}
	if err := mkt.Ledger().Credit(buyer.accountID(), buyerFunds); err != nil {
		t.Fatalf("Credit: %v", err)
	}
	// The GPU box lists itself under the account it wants to be paid into.
	now := time.Now().UTC()
	if err := mkt.RegisterProvider(market.Provider{
		ID:           providerID,
		Capacity:     200_000,
		PricePerUnit: pricePerUnit,
		Models:       []string{"qwen3-32b"},
		ObservedAt:   now,
		ValidUntil:   now.Add(market.DefaultQuoteTTL),
	}); err != nil {
		t.Fatalf("RegisterProvider: %v", err)
	}

	registry := NewRegistry()
	// The echo backend stands in for the model server, exactly as it does on a
	// sandbox node with no GPU: the marketplace and settlement path exercised
	// here are the ones a vLLM backend would take.
	if err := registry.Register(providerID, NewEchoBackend()); err != nil {
		t.Fatalf("registry.Register: %v", err)
	}

	settler := &ledgerSettler{ledger: mkt.Ledger(), feeBasisPoints: feeBasisPoints}
	svc, err := NewService(Config{
		Market:   mkt,
		Registry: registry,
		Settler:  settler,
		// NOBODY. If this test passes, the node settled while holding no key for
		// either side, which is what self-custody means.
		Accounts: memAccounts{m: map[string]*token.Account{}},
	})
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}

	// The provider must be routable by model, or a buyer naming a model never
	// reaches it however well the payment works.
	if got := mkt.ProvidersForModel("qwen3-32b"); len(got) != 1 || got[0].ID != providerID {
		t.Fatalf("ProvidersForModel = %v, want the gpu box", got)
	}

	req := InferenceRequest{Prompt: "how much is a gpu hour", Model: "qwen3-32b"}
	digest := requestDigest(req)
	auth := &RunAuthorization{PublicKey: buyer.addr[:], Provider: providerID, Model: "qwen3-32b"}
	auth.Timestamp = mustSignedTimestamp(t, buyer, providerID, "qwen3-32b", digest[:], auth)
	if err := svc.VerifyRunAuthorization(buyer.accountID(), req, auth); err != nil {
		t.Fatalf("VerifyRunAuthorization: %v", err)
	}

	providerBefore, err := mkt.Ledger().Balance(providerID)
	if err != nil {
		t.Fatalf("Balance: %v", err)
	}

	pr, err := svc.RunUnsettled(context.Background(), buyer.accountID(), providerID, req, 1000)
	if err != nil {
		t.Fatalf("RunUnsettled: %v", err)
	}
	if pr.To != providerID {
		t.Fatalf("the invoice pays %s, want the gpu box %s", pr.To, providerID)
	}

	tx := pr.Transaction(buyer.addr[:])
	payDigest, err := tx.EthTransferDigest()
	if err != nil {
		t.Fatalf("EthTransferDigest: %v", err)
	}
	tx.Signature = buyer.sign(t, payDigest)

	settled, err := svc.SettleSigned(context.Background(), pr.JobID, tx)
	if err != nil {
		t.Fatalf("SettleSigned: %v", err)
	}
	if settled.Status != InferenceJobCompleted {
		t.Fatalf("status = %s, want completed", settled.Status)
	}
	if settled.Completion == "" {
		t.Fatal("a paid job must hand over its completion")
	}

	// The money actually moved, net of the protocol fee, into an account whose
	// only key lives in a wallet.
	providerAfter, err := mkt.Ledger().Balance(providerID)
	if err != nil {
		t.Fatalf("Balance: %v", err)
	}
	gross := pr.Amount
	wantNet := gross - gross*feeBasisPoints/10_000
	if got := providerAfter - providerBefore; got != wantNet {
		t.Fatalf("the gpu box received %d, want %d net of the %d bp fee", got, wantNet, feeBasisPoints)
	}
	// The bill has to be the token count the backend reported, scaled by the
	// advertised price. InferenceJob.Units is already the scaled charge, so the
	// unscaled quantity comes from Usage - checking one against the other is
	// exactly what a provider auditing its own revenue does.
	billableTokens := UnitsFor(settled.Usage)
	if gross != billableTokens*pricePerUnit {
		t.Fatalf("charged %d for %d billable tokens at %d, want %d",
			gross, billableTokens, pricePerUnit, billableTokens*pricePerUnit)
	}
	if settled.Units != gross {
		t.Fatalf("the job records %d billed, but the invoice was %d", settled.Units, gross)
	}

	buyerAfter, err := mkt.Ledger().Balance(buyer.accountID())
	if err != nil {
		t.Fatalf("Balance: %v", err)
	}
	if buyerAfter != buyerFunds-gross {
		t.Fatalf("the buyer has %d, want %d", buyerAfter, buyerFunds-gross)
	}

	// And the transfer that did it was signed by the buyer's wallet, not by the
	// node on the buyer's behalf.
	if len(settler.applied) != 1 {
		t.Fatalf("%d settlements applied, want 1", len(settler.applied))
	}
	if settler.applied[0].SenderID() != buyer.accountID() {
		t.Fatalf("settled by %s, want the buyer's wallet %s",
			settler.applied[0].SenderID(), buyer.accountID())
	}

	// The reservation came back, so the box can serve the next buyer.
	provider, ok := mkt.GetProvider(providerID)
	if !ok {
		t.Fatal("the provider vanished from the order book")
	}
	if provider.Available != provider.Capacity {
		t.Fatalf("available = %d, want the full capacity %d back", provider.Available, provider.Capacity)
	}
}

// TestAProviderCannotBuyFromItself covers the case an address-shaped provider id
// makes newly reachable: with buyer and provider both being wallet addresses,
// one operator naming its own payout address as the buyer would settle a
// transfer from an account to itself.
func TestAProviderCannotBuyFromItself(t *testing.T) {
	wallet := newMetamask(t)
	store, err := kv.New(kv.Config{Path: t.TempDir()})
	if err != nil {
		t.Fatalf("kv.New: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	mkt, err := market.NewMarket(store)
	if err != nil {
		t.Fatalf("market.NewMarket: %v", err)
	}
	if err := mkt.Ledger().Credit(wallet.accountID(), 1_000_000); err != nil {
		t.Fatalf("Credit: %v", err)
	}
	now := time.Now().UTC()
	if err := mkt.RegisterProvider(market.Provider{
		ID: wallet.accountID(), Capacity: 1000, PricePerUnit: 1,
		ObservedAt: now, ValidUntil: now.Add(market.DefaultQuoteTTL),
	}); err != nil {
		t.Fatalf("RegisterProvider: %v", err)
	}
	registry := NewRegistry()
	if err := registry.Register(wallet.accountID(), NewEchoBackend()); err != nil {
		t.Fatalf("registry.Register: %v", err)
	}
	svc, err := NewService(Config{
		Market:   mkt,
		Registry: registry,
		Settler:  &ledgerSettler{ledger: mkt.Ledger()},
		Accounts: memAccounts{m: map[string]*token.Account{}},
	})
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}

	_, err = svc.SubmitInferenceJob(wallet.accountID(), wallet.accountID(),
		InferenceRequest{Prompt: "self"}, 10)
	if err == nil {
		t.Fatal("a provider buying from its own payout account was accepted")
	}
}
