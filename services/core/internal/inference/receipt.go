package inference

import (
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/ecirlabs/matrix-core/internal/token"
)

// A signed statement of what a seller charged, and for what.
//
// WHAT THIS IS FOR, and what it is not. Two frauds are left after the billing
// ceiling and the bond: a seller can answer with a cheaper model than it
// advertised, and it can answer with rubbish. Neither is provable to a chain -
// no consensus can judge whether a completion was really llama-70b - so neither
// can be slashed, and pretending otherwise would be worse than leaving them
// open.
//
// What can be done is make the CLAIM non-repudiable. A receipt is the serving
// node's signature over exactly what it says it did: this model, these tokens,
// this many units, this total, for this prompt and this completion. It does not
// prevent the lie. It removes the seller's ability to deny having told it.
//
// That is a smaller thing than enforcement and a real one. Today a buyer who
// suspects a substitution has a bill and a memory; with this they have a signed
// document, and so does anyone they show it to - a third party can verify it
// holding nothing but the receipt and the text of the exchange, with no node, no
// account and no access to the chain.
//
// WHY THE NODE SIGNS, and not the payout account. The payout account may be a
// wallet address whose key the node does not hold - that is what the GPU runbook
// tells an operator to use. The node is the thing that ran the model and made
// the claim, and its key is the one it has. The receipt names both, so a reader
// sees who is answerable and where the money went.
//
// WHY THE TEXT IS HASHED IN. Without it a receipt is a floating claim about some
// exchange, which could be shown for any other. Bound to a digest of the actual
// prompt and completion, a receipt is evidence about ONE request, and a buyer
// holding the text can prove which.

// ErrInvalidReceipt is returned when a receipt is malformed, unsigned, signed by
// somebody other than the node it names, or describes a different exchange.
var ErrInvalidReceipt = errors.New("inference: invalid receipt")

// receiptDomain separates a receipt signature from every other signed structure
// in this repo, so bytes signed as one can never be presented as the other.
const receiptDomain = "matrix/inference/receipt/v1"

// Receipt is the serving node's signed account of one billed inference.
type Receipt struct {
	// JobID, Buyer and Provider identify the sale. Provider is the payout
	// account the money went to, which may be a wallet address.
	JobID    string `json:"job_id"`
	Buyer    string `json:"buyer"`
	Provider string `json:"provider"`
	// NodeID is the account id of the signing node: the party answerable for
	// every claim below. It is derived from PublicKey and checked against it.
	NodeID string `json:"node_id"`
	// Model is what the node says produced the completion. It is the field that
	// makes a substitution attributable rather than deniable: a node that serves
	// a cheaper model and writes the advertised one here has signed the
	// difference.
	Model string `json:"model"`
	// PromptTokens, CompletionTokens and TotalTokens are what the backend
	// REPORTED - the seller's own claim about the work.
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
	// Units is what was actually billed, which is not the same number: the
	// report is clamped to the reservation and to what the text can honestly
	// have cost. Both are recorded so the difference is visible rather than
	// reconstructed.
	//
	// THE `,string` TAGS ARE LOAD-BEARING, on this field and the three below.
	// These are 64-bit values, and JSON numbers are IEEE doubles: anything past
	// 2^53 is rounded by every JavaScript parser there is. IssuedAt is unix
	// NANOSECONDS - about 1.8e18 - so every receipt carries one, and a browser
	// reading it as a number would compute a different signing digest and
	// conclude the signature was invalid.
	//
	// That failure is the dangerous shape: it depends on whether a particular
	// timestamp happens to round to itself, so it passes in testing on a round
	// number and fails in production on an arbitrary one, reading as a key
	// problem the whole time. Carried as strings, the bytes a browser verifies
	// are the bytes the node signed.
	Units uint64 `json:"units,string"`
	// PricePerUnit and Total are the money. Total must equal Units *
	// PricePerUnit, and a verifier checks it rather than trusting it.
	PricePerUnit uint64 `json:"price_per_unit,string"`
	Total        uint64 `json:"total,string"`
	// ExchangeDigest binds this receipt to one prompt and one completion, so it
	// cannot be detached and shown for a different request.
	ExchangeDigest []byte `json:"exchange_digest"`
	// IssuedAt is unix nanoseconds at signing.
	IssuedAt int64 `json:"issued_at,string"`

	PublicKey ed25519.PublicKey `json:"public_key"`
	Signature []byte            `json:"signature"`
}

// ExchangeDigest hashes the request and the completion into the value a receipt
// commits to.
//
// Length-prefixed throughout, so no two distinct exchanges serialise the same
// way - without it a prompt ending in text the completion begins with could be
// re-split into a different pair with an identical digest.
func ExchangeDigest(req InferenceRequest, completion string) []byte {
	h := sha256.New()
	writeLenPrefixed(h, []byte(receiptDomain))

	msgs, err := req.EffectiveMessages()
	if err != nil {
		msgs = nil
	}
	var count [8]byte
	binary.BigEndian.PutUint64(count[:], uint64(len(msgs)))
	_, _ = h.Write(count[:])
	for _, m := range msgs {
		writeLenPrefixed(h, []byte(m.Role))
		writeLenPrefixed(h, []byte(m.Content))
	}
	writeLenPrefixed(h, []byte(completion))
	return h.Sum(nil)
}

// SigningBytes is the canonical payload a receipt's signature covers: every
// field except the signature itself.
func (r *Receipt) SigningBytes() []byte {
	h := sha256.New()
	writeLenPrefixed(h, []byte(receiptDomain))
	writeLenPrefixed(h, []byte(r.JobID))
	writeLenPrefixed(h, []byte(r.Buyer))
	writeLenPrefixed(h, []byte(r.Provider))
	writeLenPrefixed(h, []byte(r.NodeID))
	writeLenPrefixed(h, []byte(r.Model))

	var num [8]byte
	for _, v := range []uint64{
		uint64(r.PromptTokens), uint64(r.CompletionTokens), uint64(r.TotalTokens),
		r.Units, r.PricePerUnit, r.Total, uint64(r.IssuedAt),
	} {
		binary.BigEndian.PutUint64(num[:], v)
		_, _ = h.Write(num[:])
	}
	writeLenPrefixed(h, r.ExchangeDigest)
	writeLenPrefixed(h, r.PublicKey)
	return h.Sum(nil)
}

// Sign signs the receipt with the serving node's key and fills in NodeID and
// PublicKey from it, so the two can never disagree with what was signed.
func (r *Receipt) Sign(node *token.Account) error {
	if node == nil || len(node.PrivateKey) != ed25519.PrivateKeySize {
		return fmt.Errorf("%w: a node signing key is required", ErrInvalidReceipt)
	}
	r.PublicKey = node.PublicKey
	r.NodeID = node.AccountID()
	r.Signature = ed25519.Sign(node.PrivateKey, r.SigningBytes())
	return nil
}

// Verify checks a receipt on its own terms: that it is signed by the node it
// names, and that its arithmetic holds.
//
// It deliberately does NOT check the receipt against an exchange - see
// VerifyFor, which is the check a buyer actually wants. Splitting them keeps
// this usable by a third party holding only the document.
func (r *Receipt) Verify() error {
	if r == nil {
		return fmt.Errorf("%w: no receipt", ErrInvalidReceipt)
	}
	if len(r.PublicKey) != ed25519.PublicKeySize {
		return fmt.Errorf("%w: public key must be %d bytes", ErrInvalidReceipt, ed25519.PublicKeySize)
	}
	if r.NodeID == "" || r.NodeID != token.AccountIDFromPublicKey(r.PublicKey) {
		return fmt.Errorf("%w: node id does not match the signing key", ErrInvalidReceipt)
	}
	if len(r.Signature) == 0 {
		return fmt.Errorf("%w: unsigned", ErrInvalidReceipt)
	}
	if !ed25519.Verify(r.PublicKey, r.SigningBytes(), r.Signature) {
		return fmt.Errorf("%w: signature does not verify", ErrInvalidReceipt)
	}
	if r.JobID == "" || r.Buyer == "" || r.Provider == "" {
		return fmt.Errorf("%w: a receipt must name the job, the buyer and the payout account", ErrInvalidReceipt)
	}
	if len(r.ExchangeDigest) != sha256.Size {
		return fmt.Errorf("%w: exchange digest must be %d bytes", ErrInvalidReceipt, sha256.Size)
	}
	// The arithmetic is checked rather than trusted. A total that does not follow
	// from the units and the price is the simplest possible overcharge, and it
	// would otherwise be a signed document nobody read closely.
	want, err := checkedMul(r.Units, r.PricePerUnit)
	if err != nil {
		return fmt.Errorf("%w: units * price overflows", ErrInvalidReceipt)
	}
	if want != r.Total {
		return fmt.Errorf("%w: total is %d but %d units at %d each is %d",
			ErrInvalidReceipt, r.Total, r.Units, r.PricePerUnit, want)
	}
	return nil
}

// VerifyFor is the buyer's check: a valid receipt, for THIS exchange, naming
// this buyer.
//
// The digest comparison is what makes a receipt evidence rather than a note. A
// signed statement about some unnamed request proves nothing; one that can only
// belong to the prompt the buyer sent and the completion they got is a document
// they can put in front of anyone.
func (r *Receipt) VerifyFor(buyer string, req InferenceRequest, completion string) error {
	if err := r.Verify(); err != nil {
		return err
	}
	if buyer != "" && r.Buyer != buyer {
		return fmt.Errorf("%w: receipt is for buyer %s, not %s", ErrInvalidReceipt, r.Buyer, buyer)
	}
	want := ExchangeDigest(req, completion)
	if len(want) != len(r.ExchangeDigest) {
		return fmt.Errorf("%w: exchange digest length differs", ErrInvalidReceipt)
	}
	for i := range want {
		if want[i] != r.ExchangeDigest[i] {
			return fmt.Errorf("%w: this receipt is for a different prompt or completion", ErrInvalidReceipt)
		}
	}
	return nil
}

// checkedMul is market.CheckedMul without the import, which would be a cycle.
func checkedMul(a, b uint64) (uint64, error) {
	if a == 0 || b == 0 {
		return 0, nil
	}
	if a > ^uint64(0)/b {
		return 0, fmt.Errorf("overflow")
	}
	return a * b, nil
}

// issueReceipt builds and signs the node's account of a settled job. A node with
// no signing key issues none rather than an unsigned one: a receipt nobody
// signed is a claim with no author, which is what there was before.
// units is the RAW billable count, not job.Units - those are different numbers
// and the difference is a factor of the price. job.Units is the settled CHARGE
// (billable units already multiplied by the provider's price per unit), so
// passing it here would make the receipt claim a total that does not follow from
// its own arithmetic, and Verify would reject every receipt the node issued.
func (s *Service) issueReceipt(job *InferenceJob, usage Usage, units, pricePerUnit, total uint64) *Receipt {
	if s.node == nil {
		return nil
	}
	r := &Receipt{
		JobID:            job.ID,
		Buyer:            job.Buyer,
		Provider:         job.Provider,
		Model:            job.Model,
		PromptTokens:     usage.PromptTokens,
		CompletionTokens: usage.CompletionTokens,
		TotalTokens:      usage.TotalTokens,
		Units:            units,
		PricePerUnit:     pricePerUnit,
		Total:            total,
		ExchangeDigest:   ExchangeDigest(job.Request, job.Completion),
		IssuedAt:         time.Now().UTC().UnixNano(),
	}
	if err := r.Sign(s.node); err != nil {
		return nil
	}
	return r
}

// MarshalReceipt renders a receipt as the JSON a buyer stores or forwards.
func MarshalReceipt(r *Receipt) ([]byte, error) { return json.Marshal(r) }

// ParseReceipt reads one back.
func ParseReceipt(body []byte) (*Receipt, error) {
	var r Receipt
	if err := json.Unmarshal(body, &r); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidReceipt, err)
	}
	return &r, nil
}

// ed25519Sign is a test seam: signing bytes with an account's key without
// touching the identity fields Sign fills in, so a test can construct the
// mismatch a forger would.
func ed25519Sign(acct *token.Account, payload []byte) []byte {
	return ed25519.Sign(acct.PrivateKey, payload)
}
