package token

import (
	"encoding/hex"
	"fmt"
	"strconv"
	"strings"
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
	spendPrefix = "spend/"
	// SpendEscrowPrefix names the account that HOLDS a budget.
	SpendEscrowPrefix = spendPrefix + "escrow/"
	// SpendDrawPrefix names a payment out of one.
	SpendDrawPrefix = spendPrefix + "draw/"
	// SpendClosePrefix names the return of whatever is left.
	SpendClosePrefix = spendPrefix + "close/"
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
func (s SpendEscrow) Account() string { return SpendEscrowPrefix + s.terms() }

// DrawRecipient is the recipient that pays payee out of this budget. It is
// signed by the delegate, so the recipient carries the escrow the money comes
// from: the signature identifies who is spending and not what is being spent.
func (s SpendEscrow) DrawRecipient(payee string) string {
	return SpendDrawPrefix + payee + "/" + s.terms()
}

// CloseRecipient is the recipient that returns what is left to the buyer.
func (s SpendEscrow) CloseRecipient() string { return SpendClosePrefix + s.terms() }

// IsSpendRecipient reports whether a recipient names any of the three budget
// operations, so the apply path can tell one from an ordinary transfer.
func IsSpendRecipient(to string) bool { return strings.HasPrefix(to, spendPrefix) }

// IsSpendEscrowAccount reports whether a recipient is a budget account itself,
// which is the one of the three that HOLDS a balance.
func IsSpendEscrowAccount(to string) bool { return strings.HasPrefix(to, SpendEscrowPrefix) }

// ParseSpendEscrow decodes the terms of a budget account.
func ParseSpendEscrow(to string) (SpendEscrow, error) {
	if !IsSpendEscrowAccount(to) {
		return SpendEscrow{}, fmt.Errorf("%w: %q is not a budget account", ErrInvalidAuthorization, to)
	}
	return parseTerms(strings.TrimPrefix(to, SpendEscrowPrefix))
}

// ParseSpendDraw decodes a draw into who is paid and which budget pays them.
func ParseSpendDraw(to string) (payee string, escrow SpendEscrow, err error) {
	if !strings.HasPrefix(to, SpendDrawPrefix) {
		return "", SpendEscrow{}, fmt.Errorf("%w: %q is not a draw", ErrInvalidAuthorization, to)
	}
	rest := strings.TrimPrefix(to, SpendDrawPrefix)
	// SplitN with 2, and the payee first, because the terms segment is fixed in
	// shape while a payee is an account id whose spelling this must not assume.
	parts := strings.SplitN(rest, "/", 2)
	if len(parts) != 2 {
		return "", SpendEscrow{}, fmt.Errorf("%w: a draw must be <payee>/<terms>, got %q",
			ErrInvalidAuthorization, rest)
	}
	payee = parts[0]
	if err := validAccountID(payee); err != nil {
		return "", SpendEscrow{}, fmt.Errorf("%w: draw payee %q: %v", ErrInvalidAuthorization, payee, err)
	}
	// Whether the payee is a RESERVED recipient is checked by the caller, not
	// here: the list of namespaces consensus treats specially lives in
	// consensus, and duplicating it is how two lists drift apart.
	if IsSpendRecipient(payee) {
		return "", SpendEscrow{}, fmt.Errorf("%w: a draw pays an account, not another budget: %q",
			ErrInvalidAuthorization, payee)
	}
	escrow, err = parseTerms(parts[1])
	if err != nil {
		return "", SpendEscrow{}, err
	}
	return payee, escrow, nil
}

// ParseSpendClose decodes which budget is being closed.
func ParseSpendClose(to string) (SpendEscrow, error) {
	if !strings.HasPrefix(to, SpendClosePrefix) {
		return SpendEscrow{}, fmt.Errorf("%w: %q is not a close", ErrInvalidAuthorization, to)
	}
	return parseTerms(strings.TrimPrefix(to, SpendClosePrefix))
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
			"<buyer>.<delegate>.<per-job cap>.<max price>.<expiry>.<nonce>, got %q", ErrInvalidAuthorization, raw)
	}
	s := SpendEscrow{Buyer: parts[0], Delegate: parts[1]}

	if err := validAccountID(s.Buyer); err != nil {
		return SpendEscrow{}, fmt.Errorf("%w: budget buyer %q: %v", ErrInvalidAuthorization, s.Buyer, err)
	}
	// A budget owned by another budget is not a budget: nobody holds a key for
	// one, so nothing could ever close it and the deposit is stranded. The
	// wider reserved-namespace check is the caller's, for the reason given in
	// ParseSpendDraw.
	if IsSpendRecipient(s.Buyer) {
		return SpendEscrow{}, fmt.Errorf("%w: budget buyer %q is itself a budget", ErrInvalidAuthorization, s.Buyer)
	}
	// Fixed width and lowercase: a variable-length or mixed-case delegate would
	// give one key two spellings and therefore one budget two accounts.
	raw32, err := hex.DecodeString(s.Delegate)
	if err != nil || len(raw32) != 32 || s.Delegate != strings.ToLower(s.Delegate) {
		return SpendEscrow{}, fmt.Errorf("%w: delegate must be a 64-lowercase-hex ed25519 key, got %q",
			ErrInvalidAuthorization, s.Delegate)
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
		return SpendEscrow{}, fmt.Errorf("%w: expiry %d is not a plausible unix second", ErrInvalidAuthorization, expiry)
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
		return 0, fmt.Errorf("%w: %s must be positive", ErrInvalidAuthorization, field)
	}
	return v, nil
}

// canonicalUintAllowingZero parses a decimal with exactly one spelling: no sign,
// no leading zero, no underscores. Two spellings of one number would be two
// account names for one budget.
func canonicalUintAllowingZero(raw, field string) (uint64, error) {
	if raw == "" || (len(raw) > 1 && raw[0] == '0') {
		return 0, fmt.Errorf("%w: %s %q is not canonical decimal", ErrInvalidAuthorization, field, raw)
	}
	v, err := strconv.ParseUint(raw, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("%w: %s %q: %v", ErrInvalidAuthorization, field, raw, err)
	}
	return v, nil
}

// validAccountID accepts the two shapes an ordinary account id takes.
func validAccountID(id string) error {
	if IsEthAccountID(id) {
		if _, err := ParseEthAccountID(id); err != nil {
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
	if _, err := ParsePublicKeyHex(id); err != nil {
		return err
	}
	return nil
}
