package admin

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"strings"
	"testing"

	"google.golang.org/grpc/metadata"
)

// The credential lookup, and what it must never do.
//
// These tests exist because the previous version of this code carried a comment
// claiming a timing defense that could not work - it compared the presented
// credential against a value the map lookup had already proved equal to it. A
// no-op with a reassuring comment is worse than no defense, because it stops
// anyone looking. So the replacement is pinned by tests that fail if the raw
// credential ever becomes a map key again, or ever gets stored at all.

func authCtx(credential string) context.Context {
	return metadata.NewIncomingContext(context.Background(),
		metadata.Pairs("authorization", "Bearer "+credential))
}

// TestTheAuthenticatorStoresNoUsableCredential. The authenticator's map is
// long-lived process memory, and AuthenticateKey hands records out of it to
// callers that log them. Neither the keys nor the values may carry a working
// credential.
func TestTheAuthenticatorStoresNoUsableCredential(t *testing.T) {
	const secret = "sk-live-do-not-store-me"
	a := NewAuthenticator()
	if err := a.AddKey(&APIKey{Key: secret, Role: RoleAdmin, Name: "n", Account: "acct-1"}); err != nil {
		t.Fatalf("AddKey: %v", err)
	}

	a.mu.RLock()
	defer a.mu.RUnlock()
	for mapKey, record := range a.keys {
		if strings.Contains(mapKey, secret) {
			t.Fatal("the credential is a map key again; the lookup is back to comparing " +
				"the secret itself rather than a digest of it")
		}
		if len(mapKey) != 32 {
			t.Fatalf("the map key is %d bytes; a digest-indexed map has fixed-length "+
				"keys, and a variable one means the credential's length still reaches "+
				"the hasher", len(mapKey))
		}
		if record.Key != "" {
			t.Fatalf("the stored record still carries the credential (%q); a record that "+
				"reaches a log line must not be usable to authenticate", record.Key)
		}
	}
}

// TestTheReturnedRecordCarriesNoCredential. Same property, from the caller's
// side: a caller that dumps what AuthenticateKey returned has not dumped a
// working key.
func TestTheReturnedRecordCarriesNoCredential(t *testing.T) {
	const secret = "sk-live-2"
	a := NewAuthenticator()
	if err := a.AddKey(&APIKey{Key: secret, Role: RoleAdmin, Name: "n", Account: "acct-1"}); err != nil {
		t.Fatalf("AddKey: %v", err)
	}

	got, err := a.AuthenticateKey(authCtx(secret))
	if err != nil {
		t.Fatalf("AuthenticateKey: %v", err)
	}
	if got.Key != "" {
		t.Fatalf("the returned record carries the credential %q", got.Key)
	}
	// The fields callers actually use must survive the redaction.
	if got.Account != "acct-1" || got.Role != RoleAdmin || got.Name != "n" {
		t.Fatalf("redaction took the useful fields with it: %+v", got)
	}
}

// TestAddKeyDoesNotMutateItsArgument. Clearing the credential on a copy rather
// than in place: the node builds these structs from config and must not find
// them emptied underneath it.
func TestAddKeyDoesNotMutateItsArgument(t *testing.T) {
	in := &APIKey{Key: "sk-caller-owned", Role: RoleViewer}
	a := NewAuthenticator()
	if err := a.AddKey(in); err != nil {
		t.Fatalf("AddKey: %v", err)
	}
	if in.Key != "sk-caller-owned" {
		t.Fatalf("AddKey emptied the caller's struct: %q", in.Key)
	}
}

// TestDigestKeyingStillAcceptsAndRefuses. The whole point of hashing is to
// change nothing observable about who gets in.
func TestDigestKeyingStillAcceptsAndRefuses(t *testing.T) {
	a := NewAuthenticator()
	if err := a.AddKey(&APIKey{Key: "right", Role: RoleAdmin}); err != nil {
		t.Fatalf("AddKey: %v", err)
	}

	if _, err := a.AuthenticateKey(authCtx("right")); err != nil {
		t.Fatalf("the real credential was refused: %v", err)
	}
	for _, wrong := range []string{"wrong", "", "righ", "rightt", "RIGHT"} {
		if _, err := a.AuthenticateKey(authCtx(wrong)); !errors.Is(err, ErrUnauthorized) {
			t.Fatalf("credential %q got err = %v, want ErrUnauthorized", wrong, err)
		}
	}
}

// TestRemoveKeyFindsTheDigestedEntry. RemoveKey takes the plaintext, so it has
// to hash the same way AddKey did. A mismatch here would leave revoked keys
// working - a silent, total failure of revocation.
func TestRemoveKeyFindsTheDigestedEntry(t *testing.T) {
	a := NewAuthenticator()
	if err := a.AddKey(&APIKey{Key: "revoke-me", Role: RoleAdmin}); err != nil {
		t.Fatalf("AddKey: %v", err)
	}
	if _, err := a.AuthenticateKey(authCtx("revoke-me")); err != nil {
		t.Fatalf("AuthenticateKey before RemoveKey: %v", err)
	}

	a.RemoveKey("revoke-me")

	if _, err := a.AuthenticateKey(authCtx("revoke-me")); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("a revoked credential still authenticates: err = %v", err)
	}
	a.mu.RLock()
	n := len(a.keys)
	a.mu.RUnlock()
	if n != 0 {
		t.Fatalf("%d entries left after RemoveKey; it deleted under a key that does "+
			"not match what AddKey wrote", n)
	}
}

// TestDistinctCredentialsDoNotShareARecord guards the one way digest keying
// could go wrong that plaintext keying could not: two credentials colliding into
// one entry would let either key spend the other's account.
func TestDistinctCredentialsDoNotShareARecord(t *testing.T) {
	a := NewAuthenticator()
	creds := make([]string, 0, 500)
	for i := 0; i < 500; i++ {
		raw := make([]byte, 24)
		if _, err := rand.Read(raw); err != nil {
			t.Fatalf("rand: %v", err)
		}
		c := hex.EncodeToString(raw)
		creds = append(creds, c)
		if err := a.AddKey(&APIKey{Key: c, Role: RoleViewer, Account: "acct-" + c}); err != nil {
			t.Fatalf("AddKey: %v", err)
		}
	}
	a.mu.RLock()
	n := len(a.keys)
	a.mu.RUnlock()
	if n != len(creds) {
		t.Fatalf("%d credentials produced %d entries; two of them collided", len(creds), n)
	}
	for _, c := range creds {
		got, err := a.AuthenticateKey(authCtx(c))
		if err != nil {
			t.Fatalf("AuthenticateKey(%s): %v", c, err)
		}
		if got.Account != "acct-"+c {
			t.Fatalf("credential %s resolved to account %q; it is spending someone "+
				"else's balance", c, got.Account)
		}
	}
}
