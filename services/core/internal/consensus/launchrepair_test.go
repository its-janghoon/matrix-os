package consensus

import (
	"encoding/hex"
	"testing"
	"time"

	"github.com/ecirlabs/matrix-core/internal/kv"
	"github.com/ecirlabs/matrix-core/internal/market"
	"github.com/ecirlabs/matrix-core/internal/token"
)

func launchRepairTx(t *testing.T) *token.Transaction {
	t.Helper()
	return repairTx(t, launchRepairFounder, launchRepairRecipient, launchRepairAmount)
}

func repairTx(t *testing.T, account, recipient string, amount uint64) *token.Transaction {
	t.Helper()
	pub, err := hex.DecodeString(account)
	if err != nil {
		t.Fatalf("decode signer: %v", err)
	}
	return &token.Transaction{
		From:   pub,
		To:     recipient,
		Amount: amount,
	}
}

func TestLaunchRepairIsExactAndSingleUse(t *testing.T) {
	if !IsReservedRecipient(launchRepairRecipient) {
		t.Fatal("launch repair is not a reserved consensus operation")
	}
	if err := verifyLaunchRepair(
		launchRepairFounder,
		launchRepairRecipient,
		launchRepairAmount,
		launchRepairBefore,
	); err != nil {
		t.Fatalf("exact repair rejected: %v", err)
	}

	for name, mutate := range map[string]func(*token.Transaction, *uint64){
		"wrong sender": func(tx *token.Transaction, _ *uint64) { tx.From[0] ^= 1 },
		"wrong amount": func(tx *token.Transaction, _ *uint64) { tx.Amount++ },
		"wrong state":  func(_ *token.Transaction, balance *uint64) { (*balance)++ },
	} {
		t.Run(name, func(t *testing.T) {
			tx := launchRepairTx(t)
			balance := uint64(launchRepairBefore)
			mutate(tx, &balance)
			if err := verifyLaunchRepair(tx.SenderID(), tx.To, tx.Amount, balance); err == nil {
				t.Fatal("invalid repair accepted")
			}
		})
	}
}

func TestApplyLaunchRepairMovesExistingSupplyOnce(t *testing.T) {
	store, err := kv.New(kv.Config{Path: t.TempDir()})
	if err != nil {
		t.Fatalf("store: %v", err)
	}
	defer store.Close()
	ledger := market.NewLedger(store)
	if err := ledger.Credit(launchRepairFounder, launchRepairBefore); err != nil {
		t.Fatalf("credit founder: %v", err)
	}
	const poolBefore = uint64(1_000)
	if err := ledger.Credit(token.RewardPoolAccount, poolBefore); err != nil {
		t.Fatalf("credit pool: %v", err)
	}

	tx := launchRepairTx(t)
	var first, second bool
	if err := ledger.Atomically(func(ltx market.LedgerTx) error {
		var err error
		first, err = applyLaunchRepair(ltx, tx)
		return err
	}); err != nil {
		t.Fatalf("first apply: %v", err)
	}
	if err := ledger.Atomically(func(ltx market.LedgerTx) error {
		var err error
		second, err = applyLaunchRepair(ltx, tx)
		return err
	}); err != nil {
		t.Fatalf("second apply: %v", err)
	}
	if !first || second {
		t.Fatalf("applied first=%v second=%v, want true then false", first, second)
	}
	if got, _ := ledger.Balance(launchRepairFounder); got != launchRepairBefore+launchRepairAmount {
		t.Fatalf("founder balance = %d", got)
	}
	if got, _ := ledger.Balance(token.RewardPoolAccount); got != poolBefore-launchRepairAmount {
		t.Fatalf("pool balance = %d", got)
	}
}

func TestApplyValidatorRestartRepairRestoresOnlySlashedBond(t *testing.T) {
	store, err := kv.New(kv.Config{Path: t.TempDir()})
	if err != nil {
		t.Fatalf("store: %v", err)
	}
	defer store.Close()
	ledger := market.NewLedger(store)
	const poolBefore = uint64(2_000_000_000_000_000)
	if err := ledger.Credit(token.RewardPoolAccount, poolBefore); err != nil {
		t.Fatalf("credit pool: %v", err)
	}

	tx := repairTx(t, validatorRepairAccount, validatorRepairRecipient, validatorRepairAmount)
	var applied bool
	if err := ledger.Atomically(func(ltx market.LedgerTx) error {
		var err error
		applied, err = applyLaunchRepair(ltx, tx)
		return err
	}); err != nil {
		t.Fatalf("apply: %v", err)
	}
	if !applied {
		t.Fatal("validator repair did not apply")
	}
	if got, _ := ledger.Balance(validatorRepairAccount); got != validatorRepairAmount {
		t.Fatalf("validator balance = %d", got)
	}
	if got, _ := ledger.Balance(token.RewardPoolAccount); got != poolBefore-validatorRepairAmount {
		t.Fatalf("pool balance = %d", got)
	}
}

func TestFounderRepairPardonsOnlyVirginiaRestartSlash(t *testing.T) {
	other := SetChange{Kind: SetChangeSlash, ValidatorID: launchRepairFounder}
	virginia := SetChange{Kind: SetChangeSlash, ValidatorID: validatorRepairAccount}
	e := &Engine{
		pendingChanges: []SetChange{other, virginia},
		approvedSpecs:  []SetChange{virginia, other},
		approvedChanges: map[string]struct{}{
			virginia.String(): {},
			other.String():    {},
		},
		setChangeOffered: map[string]time.Time{
			virginia.String(): time.Now(),
			other.String():    time.Now(),
		},
	}

	e.pardonVirginiaRestartSlashLocked()

	if len(e.pendingChanges) != 1 || e.pendingChanges[0].String() != other.String() {
		t.Fatalf("pending after pardon = %v", e.pendingChanges)
	}
	if len(e.approvedSpecs) != 1 || e.approvedSpecs[0].String() != other.String() {
		t.Fatalf("approved after pardon = %v", e.approvedSpecs)
	}
	if _, ok := e.approvedChanges[virginia.String()]; ok {
		t.Fatal("Virginia slash approval survived pardon")
	}
	if _, ok := e.setChangeOffered[virginia.String()]; ok {
		t.Fatal("Virginia slash offer survived pardon")
	}
}

// Clearing the approval is not enough on its own: bonded-open approves a slash
// from stored evidence, so the pardon has to be visible there too or the
// restored bond is taken again at the epoch after the validator rejoins.
func TestPardonedEvidenceDoesNotApproveASlash(t *testing.T) {
	store, err := kv.New(kv.Config{Path: t.TempDir()})
	if err != nil {
		t.Fatalf("store: %v", err)
	}
	defer store.Close()

	pardoned := equivocationAt(t, validatorRepairAccount, pardonedEvidenceHeight, pardonedEvidenceRound)
	e := &Engine{evidence: NewEvidenceStore(store)}
	if _, err := e.evidence.Record(&pardoned); err != nil {
		t.Fatalf("record pardoned evidence: %v", err)
	}
	if !isPardonedRestartEvidence(&pardoned) {
		t.Fatal("the recorded restart equivocation is not the pardoned one")
	}
	if e.hasStoredEvidenceLocked(validatorRepairAccount) {
		t.Fatal("pardoned evidence still approves a slash")
	}

	// The pardon is that one position only. A second offence by the same key is
	// an ordinary slashable act.
	later := equivocationAt(t, validatorRepairAccount, pardonedEvidenceHeight+1, pardonedEvidenceRound)
	if isPardonedRestartEvidence(&later) {
		t.Fatal("a different height matched the pardon")
	}
	if _, err := e.evidence.Record(&later); err != nil {
		t.Fatalf("record later evidence: %v", err)
	}
	if !e.hasStoredEvidenceLocked(validatorRepairAccount) {
		t.Fatal("an unpardoned offence by the same validator was ignored")
	}
}

// equivocationAt builds a stored-shaped record for a position. It does not have
// to verify: the pardon and hasStoredEvidenceLocked read records that were
// already verified when they were written.
func equivocationAt(t *testing.T, voter string, height, round uint64) Equivocation {
	t.Helper()
	vote := func(hash string) Vote {
		return Vote{
			Type:      VoteTypePrevote,
			Height:    height,
			Round:     round,
			BlockHash: []byte(hash),
			VoterID:   voter,
		}
	}
	return NewEquivocation(vote("block-a"), vote("block-b"))
}
