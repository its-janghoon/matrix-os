package admin

import "testing"

// authCtx is shared with credential_test.go in this package.

func TestReplaceKeys_SwapsTheAcceptedSet(t *testing.T) {
	a := NewAuthenticator()
	if err := a.AddKey(&APIKey{Key: "old-key", Role: RoleAdmin, Name: "old"}); err != nil {
		t.Fatalf("add: %v", err)
	}

	if err := a.ReplaceKeys([]*APIKey{{Key: "new-key", Role: RoleViewer, Name: "new", Account: "eth:0xabc"}}); err != nil {
		t.Fatalf("replace: %v", err)
	}

	if _, err := a.AuthenticateKey(authCtx("old-key")); err == nil {
		t.Fatal("a revoked key still authenticates")
	}
	got, err := a.AuthenticateKey(authCtx("new-key"))
	if err != nil {
		t.Fatalf("the new key does not authenticate: %v", err)
	}
	if got.Role != RoleViewer || got.Account != "eth:0xabc" || got.Name != "new" {
		t.Fatalf("replaced key lost its metadata: %+v", got)
	}
	// Same rule as AddKey: a record handed back to a caller carries no usable
	// credential, because these travel toward logs.
	if got.Key != "" {
		t.Fatalf("the returned record carries a usable credential: %q", got.Key)
	}
}

// TestReplaceKeys_RejectedSetLeavesTheNodeReachable is the one that matters.
//
// The obvious implementation clears the map and then adds the new keys. With a
// config that half-parses, that locks the operator out of the node they were in
// the middle of administering - at the moment they are already holding a broken
// file.
func TestReplaceKeys_RejectedSetLeavesTheNodeReachable(t *testing.T) {
	a := NewAuthenticator()
	if err := a.AddKey(&APIKey{Key: "working-key", Role: RoleAdmin, Name: "working"}); err != nil {
		t.Fatalf("add: %v", err)
	}

	bad := [][]*APIKey{
		nil,
		{},
		{{Key: "", Role: RoleAdmin, Name: "no credential"}},
		{{Key: "fine", Role: RoleAdmin}, {Key: "", Role: RoleAdmin}},
		{{Key: "fine", Role: "", Name: "no role"}},
		{nil},
	}
	for i, set := range bad {
		if err := a.ReplaceKeys(set); err == nil {
			t.Fatalf("set %d was accepted and should not have been", i)
		}
		if _, err := a.AuthenticateKey(authCtx("working-key")); err != nil {
			t.Fatalf("set %d disarmed the node: the working key stopped authenticating (%v)", i, err)
		}
	}
}

// A partially-valid set must not apply its valid half either: a key set is one
// decision, and half of it is a set the operator never wrote.
func TestReplaceKeys_IsAllOrNothing(t *testing.T) {
	a := NewAuthenticator()
	if err := a.AddKey(&APIKey{Key: "working-key", Role: RoleAdmin, Name: "working"}); err != nil {
		t.Fatalf("add: %v", err)
	}
	err := a.ReplaceKeys([]*APIKey{
		{Key: "would-have-worked", Role: RoleAdmin, Name: "first"},
		{Key: "", Role: RoleAdmin, Name: "broken"},
	})
	if err == nil {
		t.Fatal("a set with a broken entry was accepted")
	}
	if _, err := a.AuthenticateKey(authCtx("would-have-worked")); err == nil {
		t.Fatal("half of a rejected set was applied")
	}
	if _, err := a.AuthenticateKey(authCtx("working-key")); err != nil {
		t.Fatalf("the running key was lost: %v", err)
	}
}
