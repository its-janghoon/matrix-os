package connectapi

import "strings"

// This file decides which methods an unauthenticated caller may reach when an
// operator opts into public reads.
//
// The endpoint required a valid API key on EVERY method, reads included. That is
// right for a node its own operator drives and wrong for a public one: a browser
// cannot hold a secret, so a page that wants to show a balance or a block either
// ships a key to everyone who loads it or cannot read the chain at all. Both are
// worse than an open read.
//
// The rule is the method name: Get* and List* are reads, everything else is a
// write. That is not a guess about what a method does - it is the naming
// convention every service in this repo already follows, and
// TestReadClassificationIsPinned enumerates the served surface and fails if a
// method is added that the convention would misclassify. A mutating `GetFoo`
// would break the build rather than quietly become world-callable.
//
// What this deliberately does NOT do is weaken writes. With public reads on, a
// write still needs a key, so opening reads cannot move money or reserve
// capacity. The client-signed inference path is what a keyless caller uses to
// spend, and there the buyer's own signature is the authority.

// isReadMethod reports whether a method name is a read under the convention
// above.
func isReadMethod(method string) bool {
	return strings.HasPrefix(method, "Get") || strings.HasPrefix(method, "List")
}

// signatureAuthorisedWrites are the write methods whose authority is a
// CLIENT SIGNATURE rather than an API key, so opening them adds nothing an
// attacker did not already have.
//
// A browser cannot hold an API key, so a self-custody page needs these three
// reachable without one. Each is safe to open for a specific reason, and the
// list is short and explicit rather than derived from a name, because getting it
// wrong opens a write:
//
//	SubmitSignedTransfer  carries the sender's signature over the exact transfer.
//	                      An unsigned or forged one is refused, so a key would
//	                      only decide WHO MAY ASK, and the signature already
//	                      decides whose money moves.
//	SettleInferenceJob    carries the buyer's signature over the invoice, checked
//	                      field by field against what the node asked for.
//	RunInferenceJob       carries the buyer's RunAuthorization, a signature over
//	                      the provider, model, prompt digest and timestamp. This
//	                      one is only safe BECAUSE of that: without it `buyer` is
//	                      just a string and anyone could make a provider work for
//	                      free against someone else's account. The node ties the
//	                      two settings together so it cannot be opened without
//	                      the authorization being required.
//
// Nothing else belongs here. FundAccount moves money from the reward pool on the
// operator's authority alone, RegisterProvider and SubmitJob commit a provider's
// capacity, and CompleteJob and CancelJob decide a job's outcome - none of them
// carries a signature that could stand in for a credential.
var signatureAuthorisedWrites = map[string]struct{}{
	"SubmitSignedTransfer": {},
	"SettleInferenceJob":   {},
	"RunInferenceJob":      {},
}

// isSignatureAuthorisedWrite reports whether a method's authority is a client
// signature.
func isSignatureAuthorisedWrite(method string) bool {
	_, ok := signatureAuthorisedWrites[method]
	return ok
}
