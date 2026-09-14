package bridge

import "testing"

// The production shape of this defect: `burn(uint256, string)` takes the native
// recipient as a STRING and the contract cannot validate it, so whatever the
// burner typed is what gets emitted and the tokens are destroyed either way.
// A leading `0x` used to mean the escrow was never released, with no recovery.
func TestNormalizeNativeRecipient(t *testing.T) {
	const account = "3830e93454e8dce17720c09b3c79d60ac931e404d0c8259caad21ac8d0ebea6f"

	t.Run("accepts the spellings that mean the same value", func(t *testing.T) {
		for name, raw := range map[string]string{
			"already canonical": account,
			"0x prefixed":       "0x" + account,
			"0X prefixed":       "0X" + account,
			"uppercase hex":     "3830E93454E8DCE17720C09B3C79D60AC931E404D0C8259CAAD21AC8D0EBEA6F",
			"0x and uppercase":  "0X3830E93454E8DCE17720C09B3C79D60AC931E404D0C8259CAAD21AC8D0EBEA6F",
			"surrounding space": "  " + account + "  ",
		} {
			if got := NormalizeNativeRecipient(raw); got != account {
				t.Errorf("%s: got %q, want %q", name, got, account)
			}
		}
	})

	// It must not guess. Releasing somebody's escrow to an account they did not
	// name is worse than refusing, so anything that is not exactly an account id
	// after stripping a prefix comes back untouched and is refused downstream.
	t.Run("never guesses at anything else", func(t *testing.T) {
		for name, raw := range map[string]string{
			"too short":      "3830e934",
			"too long":       account + "ff",
			"not hex":        "zzz0e93454e8dce17720c09b3c79d60ac931e404d0c8259caad21ac8d0ebea6f",
			"an eth address": "0x70997970C51812dc3A010C7d01b50e0d17dc79C8",
			"empty":          "",
			"eth: prefixed":  "eth:0x70997970c51812dc3a010c7d01b50e0d17dc79c8",
			"only a prefix":  "0x",
			"inner space":    "3830e934 54e8dce17720c09b3c79d60ac931e404d0c8259caad21ac8d0ebea6f",
		} {
			if got := NormalizeNativeRecipient(raw); got != raw {
				t.Errorf("%s: changed %q to %q; it should have been left alone", name, raw, got)
			}
		}
	})

	t.Run("is idempotent", func(t *testing.T) {
		once := NormalizeNativeRecipient("0x" + account)
		if twice := NormalizeNativeRecipient(once); twice != once {
			t.Fatalf("not idempotent: %q then %q", once, twice)
		}
	})
}
