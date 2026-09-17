package token

import (
	"fmt"
	"strings"
)

// An inference escrow: the reservation, paid before the model runs, so the
// answer has nothing left to withhold and can stream.
//
// WHY THIS EXISTS. `token.Transaction` signs over an EXACT amount, and an
// inference's amount is not knowable until the work is done. That is why the
// client-signed path runs first and invoices second, and why it withholds the
// completion until the invoice is signed - the withholding is the only
// enforcement there is once the GPU time is already spent. The cost is that the
// answer cannot stream: handing it over early hands over the leverage.
//
// The amount is unknowable. The RESERVATION is not: `units_reserved x price` is
// computed before any provider does any work, and a buyer can sign for that. So
// pay the maximum up front, run in the open, and have consensus return what was
// not used.
//
// WHY CONSENSUS APPLIES THE SPLIT rather than the provider refunding. A refund
// the provider chooses to make is the seller's goodwill standing in for a rule,
// on the difference between a generous reservation and a real bill - which is
// most of the money. The bridge already releases escrow this way for the same
// reason: every node reaches the same balances from the committed block alone.
//
// WHO SIGNS THE SETTLEMENT, AND WHY IT IS NOT THE PROVIDER. The settlement names
// the actual, so whoever signs it picks the price within the cap. There is no
// check on the buyer's side today - the browser signs whatever invoice the node
// returns, and `MaxUnitsFor` runs on the SELLER's node, bounding that seller's
// own model server rather than protecting a buyer from its operator. So the only
// thing that has ever stood between a buyer and an inflated bill is their power
// to withhold the signature, and moving settlement to the provider deletes it,
// leaving the bill bounded by a reservation that is generous on purpose. The
// settlement is the buyer's to sign - which under a spend budget is the
// delegate's, and opens no dialog.
//
// WHAT STOPS A BUYER SIMPLY NEVER SETTLING. The claim: at and after the expiry
// the provider may take the whole reservation. By then the work is done and
// delivered, so a refund would make "take the answer and walk" free - worse than
// the design this replaces, where the seller at least still holds the text. The
// buyer is therefore strictly better off settling, because the actual is by
// construction at most the reservation.
type InferEscrow struct {
	// JobID is the market job this escrow pays for. A UUID, so it is unique
	// without a nonce and an operator reading a ledger can find the job.
	JobID string
	// Reserved is `units_reserved x price_per_unit`, in base units: the most this
	// job can cost and the amount deposited.
	Reserved uint64
	// Expiry is the unix SECOND at and after which the provider may claim the
	// whole balance. Not the last moment a settlement lands - the moment the
	// claim opens - because "at" and "after" must not depend on which node asks.
	Expiry uint64
	// Provider is the payout account the settlement pays.
	Provider string
	// Payer receives the refund, and is where the deposit came from. It is an
	// ordinary account id, or a SPEND ESCROW account when a budget funded the
	// job - which is the composition that keeps a reservation from parking a
	// wallet's money for the length of a run.
	Payer string
}

const (
	inferPrefix = "infer/"
	// InferEscrowPrefix names the account that HOLDS a reservation.
	InferEscrowPrefix = inferPrefix + "escrow/"
	// InferSettlePrefix names a settlement: pay the actual, refund the rest.
	InferSettlePrefix = inferPrefix + "settle/"
	// InferClaimPrefix names a provider's claim after expiry.
	InferClaimPrefix = inferPrefix + "claim/"
)

// terms renders the segment every inference-escrow recipient carries.
//
// THE PAYER IS LAST AND THE SEPARATOR IS A SLASH, both on purpose. A budget
// account's own name carries dot-separated terms, so a dot-joined field list
// with a budget in it cannot be split back apart. Putting the one field of
// unbounded shape at the end lets a decoder take the remainder verbatim, which
// is the only way a nested account name survives a round trip.
func (e InferEscrow) terms() string {
	return strings.Join([]string{
		e.JobID,
		fmt.Sprintf("%d.%d", e.Reserved, e.Expiry),
		e.Provider,
		e.Payer,
	}, "/")
}

// Account is the ledger account holding this reservation.
func (e InferEscrow) Account() string { return InferEscrowPrefix + e.terms() }

// SettleRecipient is the recipient a settlement pays to. The transfer's AMOUNT
// is the actual; the rest of the balance goes back to the payer.
func (e InferEscrow) SettleRecipient() string { return InferSettlePrefix + e.terms() }

// ClaimRecipient is the recipient a provider names to take an unsettled
// reservation once its expiry has passed.
func (e InferEscrow) ClaimRecipient() string { return InferClaimPrefix + e.terms() }

// IsInferRecipient reports whether a recipient names any inference-escrow
// operation, so the apply path can tell one from an ordinary transfer.
func IsInferRecipient(to string) bool { return strings.HasPrefix(to, inferPrefix) }

// IsInferEscrowAccount reports whether a recipient is the account that holds a
// reservation, which is the one of the three carrying a balance.
func IsInferEscrowAccount(to string) bool { return strings.HasPrefix(to, InferEscrowPrefix) }

// ParseInferEscrow decodes the account holding a reservation.
func ParseInferEscrow(to string) (InferEscrow, error) {
	if !IsInferEscrowAccount(to) {
		return InferEscrow{}, fmt.Errorf("%w: %q is not an inference escrow", ErrInvalidAuthorization, to)
	}
	return parseInferTerms(strings.TrimPrefix(to, InferEscrowPrefix))
}

// ParseInferSettle decodes which reservation a settlement settles.
func ParseInferSettle(to string) (InferEscrow, error) {
	if !strings.HasPrefix(to, InferSettlePrefix) {
		return InferEscrow{}, fmt.Errorf("%w: %q is not a settlement", ErrInvalidAuthorization, to)
	}
	return parseInferTerms(strings.TrimPrefix(to, InferSettlePrefix))
}

// ParseInferClaim decodes which reservation a claim claims.
func ParseInferClaim(to string) (InferEscrow, error) {
	if !strings.HasPrefix(to, InferClaimPrefix) {
		return InferEscrow{}, fmt.Errorf("%w: %q is not a claim", ErrInvalidAuthorization, to)
	}
	return parseInferTerms(strings.TrimPrefix(to, InferClaimPrefix))
}

// Validate refuses terms that could not describe a real job.
func (e InferEscrow) Validate() error {
	_, err := parseInferTerms(e.terms())
	return err
}

// parseInferTerms decodes the four-segment tail every inference recipient
// carries. Anything malformed is an error and never a best guess: the terms ARE
// the account's identity, and a decoder that accepted two spellings of one
// reservation would let money sit where its own settlement cannot name it.
func parseInferTerms(raw string) (InferEscrow, error) {
	// Four, with the payer taking the remainder: a budget account's name
	// contains slashes of its own and must arrive whole.
	parts := strings.SplitN(raw, "/", 4)
	if len(parts) != 4 {
		return InferEscrow{}, fmt.Errorf("%w: inference escrow terms must be "+
			"<job>/<reserved>.<expiry>/<provider>/<payer>, got %q", ErrInvalidAuthorization, raw)
	}

	e := InferEscrow{JobID: parts[0], Provider: parts[2], Payer: parts[3]}

	if err := validJobID(e.JobID); err != nil {
		return InferEscrow{}, fmt.Errorf("%w: inference escrow job %q: %v", ErrInvalidAuthorization, e.JobID, err)
	}

	amounts := strings.Split(parts[1], termSeparator)
	if len(amounts) != 2 {
		return InferEscrow{}, fmt.Errorf("%w: inference escrow amounts must be <reserved>.<expiry>, got %q",
			ErrInvalidAuthorization, parts[1])
	}
	reserved, err := canonicalUint(amounts[0], "reserved")
	if err != nil {
		return InferEscrow{}, err
	}
	expiry, err := canonicalUint(amounts[1], "expiry")
	if err != nil {
		return InferEscrow{}, err
	}
	e.Reserved, e.Expiry = reserved, expiry

	if err := validAccountID(e.Provider); err != nil {
		return InferEscrow{}, fmt.Errorf("%w: inference escrow provider %q: %v",
			ErrInvalidAuthorization, e.Provider, err)
	}
	// A provider that is itself a reserved recipient would be a settlement paying
	// into a namespace with its own rules, which is a way to reach an apply path
	// sideways.
	if IsInferRecipient(e.Provider) || IsSpendRecipient(e.Provider) {
		return InferEscrow{}, fmt.Errorf("%w: inference escrow provider %q is a reserved recipient",
			ErrInvalidAuthorization, e.Provider)
	}

	if err := validPayer(e.Payer); err != nil {
		return InferEscrow{}, fmt.Errorf("%w: inference escrow payer %q: %v", ErrInvalidAuthorization, e.Payer, err)
	}

	// A payer that is also the provider makes a settlement a transfer to
	// oneself, which the market refuses at submission for the same reason: it is
	// not a sale, and it is how a seller would mint a receipt for work nobody
	// bought.
	if e.Payer == e.Provider {
		return InferEscrow{}, fmt.Errorf("%w: inference escrow payer and provider must differ (%q)",
			ErrInvalidAuthorization, e.Payer)
	}
	return e, nil
}

// validPayer accepts an ordinary account, or a budget account when a spend
// budget is funding the job.
func validPayer(id string) error {
	if IsSpendEscrowAccount(id) {
		if _, err := ParseSpendEscrow(id); err != nil {
			return err
		}
		return nil
	}
	if IsInferRecipient(id) || IsSpendRecipient(id) {
		return fmt.Errorf("a reserved recipient cannot be a payer")
	}
	return validAccountID(id)
}

// validJobID accepts the shape uuid.NewString produces, and only that.
//
// Checked rather than assumed because the job id is a SEGMENT of an account
// name: a value carrying a slash would re-split the terms into a different
// reservation, and one carrying an uppercase letter would be a second spelling
// of an account that already exists.
func validJobID(id string) error {
	if len(id) != 36 {
		return fmt.Errorf("a job id is 36 characters, got %d", len(id))
	}
	for i, r := range id {
		switch i {
		case 8, 13, 18, 23:
			if r != '-' {
				return fmt.Errorf("expected a dash at position %d", i)
			}
		default:
			if !(r >= '0' && r <= '9') && !(r >= 'a' && r <= 'f') {
				return fmt.Errorf("expected lowercase hex at position %d", i)
			}
		}
	}
	return nil
}
