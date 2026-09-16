package consensus

import (
	"encoding/hex"
	"fmt"
	"strconv"
	"strings"

	"github.com/ecirlabs/matrix-core/internal/market"
	"github.com/ecirlabs/matrix-core/internal/token"
)

// A budget a buyer signs once, spent by a key that cannot do anything else.
//
// The problem it solves is that a signature here authorises exactly one payment
// of an exact amount, so a buyer holding their own key approves every message -
// twice, because the price does not exist until the work is done. The only
// escape the protocol had was custody, a node holding the buyer's private key,
// which buys a quiet interface with the worst risk there is.
//
// HOW THE TERMS REACH CONSENSUS, and why this needs no new message format. The
// first sketch had every draw carry a signed authorization so consensus could
// read its bounds, which meant a new field on the transaction. It does not need
// one: this chain already addresses consensus operations by naming them in the
// recipient - a bond is a transfer to "consensus/stake/bond/<id>" - so the terms
// go in the ESCROW ACCOUNT'S NAME. Three things follow, and together they are
// why this is the shape:
//
//   - The buyer's ordinary transfer signature already covers the terms, because
//     `to` is part of what a transfer signs. Opening a budget is a transfer.
//   - The remaining budget is that account's BALANCE, so there is no second
//     record to keep in step with it.
//   - That balance is already inside the state root, which is a fold over
//     balances. A separate spend record would have been new state the root does
//     not cover - the shape a silent divergence takes.
//
// WHAT BOUNDS A STOLEN DELEGATE KEY is the balance and the expiry, and those are
// exactly the two consensus can check from the block alone. MaxPricePerUnit is
// carried here because it is signed with the rest, but it is enforced by the
// buyer's own client when it picks a seller: a draw is an amount moving to a
// provider, with no unit count in it to divide by, and a unit count carried
// alongside would be asserted by whoever holds the key - which in the case that
// would need defending against is the thief. See token/spendauth.go.
const (
	spendPrefix       = "spend/"
	spendEscrowPrefix = spendPrefix + "escrow/"
	spendDrawPrefix   = spendPrefix + "draw/"
	spendClosePrefix  = spendPrefix + "close/"
)

// termSeparator joins the terms inside one path segment.
//
// A dot rather than a slash, because the whole token has to survive being
// embedded in a draw recipient that is itself slash-separated. No account id,
// hex key or canonical decimal contains one: an ed25519 id is hex, an eth id is
// "eth:0x" and hex, and a decimal is digits.
const termSeparator = "."

// SpendEscrow is a budget: who it belongs to, who may spend it, and the limits
// they spend it under. It is the identity of an escrow account, so the same
// terms produce the same account name on every node.
type SpendEscrow struct {
	// Buyer is the account the budget came from and the account a close
	// returns the remainder to.
	Buyer string
	// Delegate is the lowercase hex ed25519 public key allowed to draw.
	Delegate string
	// PerJobCap is the most one draw may take.
	PerJobCap uint64
	// MaxPricePerUnit is the buyer's price ceiling, enforced by their client.
	MaxPricePerUnit uint64
	// Expiry is the unix second at and after which nothing may be drawn.
	Expiry int64
	// Nonce distinguishes two budgets with otherwise identical terms, so a
	// buyer can open a second one without it being the same account as a
	// finished first.
	Nonce uint64
}

// terms renders the fields as the single path segment every recipient embeds.
func (s SpendEscrow) terms() string {
	return strings.Join([]string{
		s.Buyer,
		s.Delegate,
		strconv.FormatUint(s.PerJobCap, 10),
		strconv.FormatUint(s.MaxPricePerUnit, 10),
		strconv.FormatInt(s.Expiry, 10),
		strconv.FormatUint(s.Nonce, 10),
	}, termSeparator)
}

// Account is the ledger account holding this budget. Opening one is a transfer
// to this string, and its balance is what is left to spend.
func (s SpendEscrow) Account() string { return spendEscrowPrefix + s.terms() }

// DrawRecipient is the recipient that pays payee out of this budget. It is
// signed by the delegate, so the recipient carries the escrow the money comes
// from: the signature identifies who is spending and not what is being spent.
func (s SpendEscrow) DrawRecipient(payee string) string {
	return spendDrawPrefix + payee + "/" + s.terms()
}

// CloseRecipient is the recipient that returns what is left to the buyer.
func (s SpendEscrow) CloseRecipient() string { return spendClosePrefix + s.terms() }

// IsSpendRecipient reports whether a recipient names any of the three budget
// operations, so the apply path can tell one from an ordinary transfer.
func IsSpendRecipient(to string) bool { return strings.HasPrefix(to, spendPrefix) }

// IsSpendEscrowAccount reports whether a recipient is a budget account itself,
// which is the one of the three that HOLDS a balance.
func IsSpendEscrowAccount(to string) bool { return strings.HasPrefix(to, spendEscrowPrefix) }

// ParseSpendEscrow decodes the terms of a budget account.
func ParseSpendEscrow(to string) (SpendEscrow, error) {
	if !IsSpendEscrowAccount(to) {
		return SpendEscrow{}, fmt.Errorf("%w: %q is not a budget account", ErrInvalidMessage, to)
	}
	return parseTerms(strings.TrimPrefix(to, spendEscrowPrefix))
}

// ParseSpendDraw decodes a draw into who is paid and which budget pays them.
func ParseSpendDraw(to string) (payee string, escrow SpendEscrow, err error) {
	if !strings.HasPrefix(to, spendDrawPrefix) {
		return "", SpendEscrow{}, fmt.Errorf("%w: %q is not a draw", ErrInvalidMessage, to)
	}
	rest := strings.TrimPrefix(to, spendDrawPrefix)
	// SplitN with 2, and the payee first, because the terms segment is fixed in
	// shape while a payee is an account id whose spelling this must not assume.
	parts := strings.SplitN(rest, "/", 2)
	if len(parts) != 2 {
		return "", SpendEscrow{}, fmt.Errorf("%w: a draw must be <payee>/<terms>, got %q",
			ErrInvalidMessage, rest)
	}
	payee = parts[0]
	if err := validAccountID(payee); err != nil {
		return "", SpendEscrow{}, fmt.Errorf("%w: draw payee %q: %v", ErrInvalidMessage, payee, err)
	}
	// A draw into another budget account, or into any other reserved operation,
	// would let a delegate move the money somewhere consensus treats specially
	// rather than pay a seller with it. The whole value of the delegate being
	// bounded is that its output is a payment.
	if IsReservedRecipient(payee) || IsSpendRecipient(payee) {
		return "", SpendEscrow{}, fmt.Errorf("%w: a draw pays an account, not the reserved "+
			"recipient %q", ErrInvalidMessage, payee)
	}
	escrow, err = parseTerms(parts[1])
	if err != nil {
		return "", SpendEscrow{}, err
	}
	return payee, escrow, nil
}

// ParseSpendClose decodes which budget is being closed.
func ParseSpendClose(to string) (SpendEscrow, error) {
	if !strings.HasPrefix(to, spendClosePrefix) {
		return SpendEscrow{}, fmt.Errorf("%w: %q is not a close", ErrInvalidMessage, to)
	}
	return parseTerms(strings.TrimPrefix(to, spendClosePrefix))
}

// parseTerms decodes the six-field segment every budget recipient carries.
//
// Anything malformed is an error and never a best guess. The terms ARE the
// account's identity: a decoder that accepted two spellings of one budget would
// let a buyer's money sit in an account their own close cannot name.
func parseTerms(raw string) (SpendEscrow, error) {
	parts := strings.Split(raw, termSeparator)
	if len(parts) != 6 {
		return SpendEscrow{}, fmt.Errorf("%w: budget terms must be "+
			"<buyer>.<delegate>.<per-job cap>.<max price>.<expiry>.<nonce>, got %q", ErrInvalidMessage, raw)
	}
	s := SpendEscrow{Buyer: parts[0], Delegate: parts[1]}

	if err := validAccountID(s.Buyer); err != nil {
		return SpendEscrow{}, fmt.Errorf("%w: budget buyer %q: %v", ErrInvalidMessage, s.Buyer, err)
	}
	// A budget owned by a reserved account is not a budget: nobody holds a key
	// for one, so nothing could ever close it and the deposit is stranded.
	if IsReservedRecipient(s.Buyer) || IsSpendRecipient(s.Buyer) {
		return SpendEscrow{}, fmt.Errorf("%w: budget buyer %q is a reserved recipient", ErrInvalidMessage, s.Buyer)
	}
	// Fixed width and lowercase: a variable-length or mixed-case delegate would
	// give one key two spellings and therefore one budget two accounts.
	raw32, err := hex.DecodeString(s.Delegate)
	if err != nil || len(raw32) != 32 || s.Delegate != strings.ToLower(s.Delegate) {
		return SpendEscrow{}, fmt.Errorf("%w: delegate must be a 64-lowercase-hex ed25519 key, got %q",
			ErrInvalidMessage, s.Delegate)
	}

	if s.PerJobCap, err = canonicalUint(parts[2], "per-job cap"); err != nil {
		return SpendEscrow{}, err
	}
	if s.MaxPricePerUnit, err = canonicalUint(parts[3], "max price per unit"); err != nil {
		return SpendEscrow{}, err
	}
	expiry, err := canonicalUint(parts[4], "expiry")
	if err != nil {
		return SpendEscrow{}, err
	}
	// Parsed as unsigned and narrowed, so a negative expiry is a parse failure
	// rather than a value that compares as "long ago" against a block time.
	if expiry > 1<<62 {
		return SpendEscrow{}, fmt.Errorf("%w: expiry %d is not a plausible unix second", ErrInvalidMessage, expiry)
	}
	s.Expiry = int64(expiry)
	// The nonce is the one field allowed to be zero: a buyer's first budget is
	// not a special case, and it distinguishes budgets rather than bounding
	// anything.
	if s.Nonce, err = canonicalUintAllowingZero(parts[5], "nonce"); err != nil {
		return SpendEscrow{}, err
	}
	return s, nil
}

// canonicalUint parses a positive decimal with exactly one spelling.
func canonicalUint(raw, field string) (uint64, error) {
	v, err := canonicalUintAllowingZero(raw, field)
	if err != nil {
		return 0, err
	}
	if v == 0 {
		// Every bound here must be set. A zero read as "unlimited" is a blank
		// cheque written by accident, and the failure would surface as money
		// gone rather than as a refused transfer.
		return 0, fmt.Errorf("%w: %s must be positive", ErrInvalidMessage, field)
	}
	return v, nil
}

// canonicalUintAllowingZero parses a decimal with exactly one spelling: no sign,
// no leading zero, no underscores. Two spellings of one number would be two
// account names for one budget.
func canonicalUintAllowingZero(raw, field string) (uint64, error) {
	if raw == "" || (len(raw) > 1 && raw[0] == '0') {
		return 0, fmt.Errorf("%w: %s %q is not canonical decimal", ErrInvalidMessage, field, raw)
	}
	v, err := strconv.ParseUint(raw, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("%w: %s %q: %v", ErrInvalidMessage, field, raw, err)
	}
	return v, nil
}

// validAccountID accepts the two shapes an ordinary account id takes.
func validAccountID(id string) error {
	if token.IsEthAccountID(id) {
		if _, err := token.ParseEthAccountID(id); err != nil {
			return err
		}
		// The ledger keys on the string verbatim, so a checksummed spelling
		// would be a different account from the one an eth signature resolves
		// to - which is how money reaches a key nobody holds.
		if id != strings.ToLower(id) {
			return fmt.Errorf("an eth account id must be lowercase")
		}
		return nil
	}
	if _, err := token.ParsePublicKeyHex(id); err != nil {
		return err
	}
	return nil
}

// spendEffect is what applying a budget operation did, in the terms the apply
// loop's accounting needs. The ledger moves happen inside applySpendOperation;
// this is only what the loop cannot see from outside.
type spendEffect struct {
	// applied is false for a deterministic skip - an underfunded budget - which
	// every node reaches from the same committed prefix.
	applied bool
	// payer and payee name an ordinary payment for the settled history. Both
	// are empty when the operation moved no money between accounts, which is
	// the case for opening a budget and for closing one: those are a buyer's
	// own coins going into and out of their own escrow, and counting them as
	// revenue would pay the provider emission to somebody funding themselves.
	payer string
	payee string
	// net is what the payee was credited, after the fee.
	net uint64
	// fee is what went to the fee accrual account.
	fee uint64
}

// verifySpendTx refuses a budget operation that can never be valid, before it
// enters a block. It reads nothing but the transaction, which is what lets the
// mempool gate and the block check share it - the two drifting apart is how a
// transaction that no validator will accept gets proposed forever.
//
// Time is NOT checked here and balance is not either. Both are apply-time
// questions with apply-time answers: the clock is the block's timestamp, which
// does not exist yet, and the balance is whatever the committed prefix left.
// Checking them here would mean checking them against this node's own wall
// clock and its own current ledger, which is how two nodes reach different
// verdicts about the same transaction.
func verifySpendTx(tx *token.Transaction) error {
	switch {
	case IsSpendEscrowAccount(tx.To):
		terms, err := ParseSpendEscrow(tx.To)
		if err != nil {
			return err
		}
		// A budget is the buyer's own money. Letting anyone fund anyone's
		// account would make a deposit a gift that only the named buyer can
		// ever close, which is a way to lose money by typing.
		if tx.SenderID() != terms.Buyer {
			return fmt.Errorf("%w: a budget is opened by its buyer %s, not by %s",
				ErrInvalidMessage, terms.Buyer, tx.SenderID())
		}
		if tx.Amount == 0 {
			return fmt.Errorf("%w: opening a budget with nothing in it", ErrInvalidMessage)
		}
		return nil

	case strings.HasPrefix(tx.To, spendDrawPrefix):
		payee, terms, err := ParseSpendDraw(tx.To)
		if err != nil {
			return err
		}
		// The delegate is the only key that may draw, and the check is against
		// the SENDER rather than against anything the recipient claims: a
		// signature says who is spending, and the recipient says what is being
		// spent, which is why the two are separate here.
		if tx.SenderID() != terms.Delegate {
			return fmt.Errorf("%w: only the delegate %s may draw on this budget, not %s",
				ErrInvalidMessage, terms.Delegate, tx.SenderID())
		}
		if tx.Amount == 0 {
			return fmt.Errorf("%w: a draw of nothing", ErrInvalidMessage)
		}
		if tx.Amount > terms.PerJobCap {
			return fmt.Errorf("%w: a draw of %d exceeds the per-job cap of %d",
				ErrInvalidMessage, tx.Amount, terms.PerJobCap)
		}
		if payee == terms.Buyer {
			// Paying the budget's own owner is a refund wearing a draw's
			// clothes, and it would skip the expiry rule that a close obeys.
			return fmt.Errorf("%w: a draw pays a seller; returning money to the buyer is a close",
				ErrInvalidMessage)
		}
		return nil

	case strings.HasPrefix(tx.To, spendClosePrefix):
		if _, err := ParseSpendClose(tx.To); err != nil {
			return err
		}
		// The amount is the whole remaining balance and is not the caller's to
		// choose, exactly as a bond withdrawal's is not.
		if tx.Amount != 0 {
			return fmt.Errorf("%w: a close carries no amount; it returns whatever is left",
				ErrInvalidMessage)
		}
		return nil
	}
	return fmt.Errorf("%w: %q is not a budget operation", ErrInvalidMessage, tx.To)
}

// applySpendOperation performs one budget operation against the ledger.
//
// blockTime is the BLOCK's timestamp and never this node's clock. Four nodes
// reading a wall clock is four answers, and a budget that has expired on one of
// them and not the others is a fork rather than a refusal.
func (e *Engine) applySpendOperation(ltx market.LedgerTx, tx *token.Transaction, blockTime int64) (spendEffect, error) {
	switch {
	case IsSpendEscrowAccount(tx.To):
		terms, err := ParseSpendEscrow(tx.To)
		if err != nil {
			return spendEffect{}, err
		}
		if blockTime >= terms.Expiry {
			// Money into a budget nothing can draw from would sit there until
			// somebody closed it, which is a refund taking two transactions and
			// a block to do nothing.
			return spendEffect{}, nil
		}
		bal, err := ltx.Balance(tx.SenderID())
		if err != nil {
			return spendEffect{}, err
		}
		if bal < tx.Amount {
			return spendEffect{}, nil
		}
		// No fee. A buyer moving their own coins into their own budget has not
		// been paid by anyone, which is the same reason a bond is exempt.
		if err := ltx.Transfer(tx.SenderID(), tx.To, tx.Amount); err != nil {
			return spendEffect{}, err
		}
		return spendEffect{applied: true}, nil

	case strings.HasPrefix(tx.To, spendDrawPrefix):
		payee, terms, err := ParseSpendDraw(tx.To)
		if err != nil {
			return spendEffect{}, err
		}
		if blockTime >= terms.Expiry {
			return spendEffect{}, nil
		}
		escrow := terms.Account()
		bal, err := ltx.Balance(escrow)
		if err != nil {
			return spendEffect{}, err
		}
		if bal < tx.Amount {
			// A deterministic skip and not an error: the budget ran out, which
			// every node sees identically from the same committed prefix.
			return spendEffect{}, nil
		}
		// The fee comes out of the amount, exactly as it does for an ordinary
		// transfer, so what the buyer authorised is what leaves the budget.
		fee := FeeFor(tx.Amount, e.feeBasisPoints)
		net := tx.Amount - fee
		if err := ltx.Transfer(escrow, payee, net); err != nil {
			return spendEffect{}, err
		}
		if fee > 0 {
			if err := ltx.Transfer(escrow, feeAccrualAccount, fee); err != nil {
				return spendEffect{}, err
			}
		}
		// The payer of record is the BUYER and not the delegate. The delegate
		// holds no money and the history should read as the person who did.
		return spendEffect{applied: true, payer: terms.Buyer, payee: payee, net: net, fee: fee}, nil

	case strings.HasPrefix(tx.To, spendClosePrefix):
		terms, err := ParseSpendClose(tx.To)
		if err != nil {
			return spendEffect{}, err
		}
		// Before expiry a close is the buyer's decision and revocation is what
		// it is for. At and after expiry it is not a decision any more - the
		// money is owed back - so anyone may trigger it, which is what stops a
		// balance being stranded by a buyer who has lost interest or their key.
		if blockTime < terms.Expiry && tx.SenderID() != terms.Buyer {
			return spendEffect{}, nil
		}
		escrow := terms.Account()
		bal, err := ltx.Balance(escrow)
		if err != nil {
			return spendEffect{}, err
		}
		if bal == 0 {
			// Closing an empty budget is a no-op rather than a failure, so
			// closing twice is safe and nobody has to check first.
			return spendEffect{applied: true}, nil
		}
		// No fee: this is collateral going back to its owner, not value anyone
		// was paid. The bridge escrow release is exempt for the same reason.
		if err := ltx.Transfer(escrow, terms.Buyer, bal); err != nil {
			return spendEffect{}, err
		}
		return spendEffect{applied: true}, nil
	}
	return spendEffect{}, fmt.Errorf("%w: %q is not a budget operation", ErrInvalidMessage, tx.To)
}
