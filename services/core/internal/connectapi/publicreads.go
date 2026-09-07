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
