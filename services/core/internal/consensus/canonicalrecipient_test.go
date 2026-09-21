package consensus

import (
	"errors"
	"strings"
	"testing"

	"github.com/ecirlabs/matrix-core/internal/token"
)

// The EIP-55 vectors from the standard, so these are not this package's own idea
// of what a checksummed address looks like. The lowercase one is the id the
// ledger keys the account by; the mixed-case one is what every wallet displays.
const (
	displayedAddress = "0x5aAeb6053F3E94C9b9A09f33669435E7Ef1BeAed"
	keyedAddress     = "0x5aaeb6053f3e94c9b9a09f33669435e7ef1beaed"
)

// The fault in one assertion. Before activation the chain credits the string a
// person copied out of their wallet, which is an account no private key
// controls; after activation it refuses the transaction instead. There is no
// third option, because the recipient is inside the signature.
func TestTheFormAWalletDisplaysIsRefusedFromItsActivationHeight(t *testing.T) {
	const activation uint64 = 100
	nodes, stop := newCluster(t, 1, func(c *Config) {
		c.ProtocolUpgrades = append(c.ProtocolUpgrades,
			ProtocolUpgrade{Height: activation, Version: ProtocolVersionCanonicalEthRecipient})
	})
	defer stop()
	e := nodes[0].engine

	sender, _ := token.GenerateAccount()
	tx := signedTransfer(t, sender, token.EthAccountPrefix+displayedAddress, 1_000, 1)

	e.mu.Lock()
	before := e.verifyCanonicalRecipientLocked(tx, activation-1)
	at := e.verifyCanonicalRecipientLocked(tx, activation)
	e.mu.Unlock()

	if before != nil {
		t.Errorf("a rule that did not exist yet refused a transaction at height %d: %v", activation-1, before)
	}
	if at == nil {
		t.Error("the displayed form was accepted at the activation height, so the money still strands")
	}
	if !errors.Is(at, token.ErrNonCanonicalRecipient) {
		t.Errorf("refused for the wrong reason, so a caller cannot tell which rule spoke: %v", at)
	}
}

// The id the ledger actually keys the account by must keep working. This is the
// form every client sends, so refusing it would break all of them to protect
// against a form nobody types.
func TestTheKeyedFormStillWorksAfterActivation(t *testing.T) {
	const activation uint64 = 1
	nodes, stop := newCluster(t, 1, func(c *Config) {
		c.ProtocolUpgrades = append(c.ProtocolUpgrades,
			ProtocolUpgrade{Height: activation, Version: ProtocolVersionCanonicalEthRecipient})
	})
	defer stop()
	e := nodes[0].engine

	sender, _ := token.GenerateAccount()
	tx := signedTransfer(t, sender, token.EthAccountPrefix+keyedAddress, 1_000, 1)

	e.mu.Lock()
	err := e.verifyCanonicalRecipientLocked(tx, activation+50)
	e.mu.Unlock()
	if err != nil {
		t.Fatalf("the form every client sends was refused: %v", err)
	}
}

// The rule answers about `eth:` ids and nothing else. An ed25519 id has one form
// already, and a reserved recipient carries its terms in the name, where case is
// meaning rather than notation - so widening this would refuse transactions that
// have always been valid and cannot strand anything.
func TestOnlyEthRecipientsAreHeldToACanonicalForm(t *testing.T) {
	const activation uint64 = 1
	nodes, stop := newCluster(t, 1, func(c *Config) {
		c.ProtocolUpgrades = append(c.ProtocolUpgrades,
			ProtocolUpgrade{Height: activation, Version: ProtocolVersionCanonicalEthRecipient})
	})
	defer stop()
	e := nodes[0].engine

	recipient, _ := token.GenerateAccount()
	untouched := []string{
		recipient.AccountID(),
		strings.ToUpper(recipient.AccountID()),
		"bridge/escrow",
		"consensus/stake/bond/" + recipient.AccountID(),
	}

	sender, _ := token.GenerateAccount()
	for i, to := range untouched {
		tx := signedTransfer(t, sender, to, 1_000, uint64(i+1))
		e.mu.Lock()
		err := e.verifyCanonicalRecipientLocked(tx, activation+1)
		e.mu.Unlock()
		if err != nil {
			t.Errorf("a recipient this rule has no business judging was refused: %q: %v", to, err)
		}
	}
}

// The property that keeps the client and the chain from drifting apart: whatever
// CanonicalAccountID hands back is a recipient consensus accepts. If these two
// ever read the rule differently, an honest wallet would build transactions the
// chain rejects.
func TestWhatTheClientCanonicalizesIsAlwaysAcceptedByTheChain(t *testing.T) {
	const activation uint64 = 1
	nodes, stop := newCluster(t, 1, func(c *Config) {
		c.ProtocolUpgrades = append(c.ProtocolUpgrades,
			ProtocolUpgrade{Height: activation, Version: ProtocolVersionCanonicalEthRecipient})
	})
	defer stop()
	e := nodes[0].engine

	ed25519Recipient, _ := token.GenerateAccount()
	inputs := []string{
		token.EthAccountPrefix + displayedAddress,
		token.EthAccountPrefix + keyedAddress,
		token.EthAccountPrefix + strings.ToUpper(keyedAddress[2:]),
		"  " + token.EthAccountPrefix + displayedAddress + "\n",
		ed25519Recipient.AccountID(),
		"bridge/escrow",
	}

	sender, _ := token.GenerateAccount()
	for i, raw := range inputs {
		canonical, err := token.CanonicalAccountID(raw)
		if err != nil {
			// A wrong checksum is refused by both halves, which is agreement.
			continue
		}
		tx := signedTransfer(t, sender, canonical, 1_000, uint64(i+1))
		e.mu.Lock()
		err = e.verifyCanonicalRecipientLocked(tx, activation+1)
		e.mu.Unlock()
		if err != nil {
			t.Errorf("the client canonicalized %q to %q and the chain refused it: %v", raw, canonical, err)
		}
	}
}

// A node must never PROPOSE what it would itself refuse. Otherwise every honest
// validator rejects the block, the round times out, and one stranded transfer
// stalls the chain instead of simply failing.
func TestANonCanonicalRecipientIsNotProposedAfterActivation(t *testing.T) {
	const activation uint64 = 1
	nodes, stop := newCluster(t, 1, func(c *Config) {
		c.ProtocolUpgrades = append(c.ProtocolUpgrades,
			ProtocolUpgrade{Height: activation, Version: ProtocolVersionCanonicalEthRecipient})
	})
	defer stop()
	e := nodes[0].engine

	sender, _ := token.GenerateAccount()
	stranding := signedTransfer(t, sender, token.EthAccountPrefix+displayedAddress, 1_000, 1)

	// Placed straight into the mempool rather than through Submit, which refuses
	// it: the point here is that a transaction already sitting there when the
	// height arrives is not proposed either.
	e.mu.Lock()
	e.mempool = append(e.mempool, *stranding)
	e.height = activation + 5
	block, _ := e.buildProposalLocked()
	e.mu.Unlock()

	if block != nil {
		for i := range block.Txs {
			if block.Txs[i].To == token.EthAccountPrefix+displayedAddress {
				t.Fatal("a node proposed a transaction it would refuse in someone else's block")
			}
		}
	}
}

// Submit is where a sender can still do something about it. A refusal here is
// permanent in the direction that matters - a non-canonical recipient does not
// become valid at a later height, it becomes invalid at one - so holding the
// transaction would only hide the failure.
func TestSubmitTellsTheSenderRatherThanHoldingAStrandingTransfer(t *testing.T) {
	const activation uint64 = 1
	nodes, stop := newCluster(t, 1, func(c *Config) {
		c.ProtocolUpgrades = append(c.ProtocolUpgrades,
			ProtocolUpgrade{Height: activation, Version: ProtocolVersionCanonicalEthRecipient})
	})
	defer stop()
	e := nodes[0].engine

	e.mu.Lock()
	e.height = activation + 1
	e.mu.Unlock()

	sender, _ := token.GenerateAccount()
	stranding := signedTransfer(t, sender, token.EthAccountPrefix+displayedAddress, 1_000, 1)
	if err := e.Submit(stranding); err == nil {
		t.Fatal("submit accepted a transfer that can never be included, so the sender is told nothing")
	}

	keyed := signedTransfer(t, sender, token.EthAccountPrefix+keyedAddress, 1_000, 1)
	if err := e.Submit(keyed); err != nil {
		t.Fatalf("submit refused the form every client sends: %v", err)
	}
}
