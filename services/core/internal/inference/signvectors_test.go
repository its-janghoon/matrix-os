package inference

import (
	"encoding/hex"
	"encoding/json"
	"os"
	"strconv"
	"testing"
)

// The Go half of the cross-language guard. The TypeScript half reads the same
// file from packages/protocol and packages/sdk.
//
// This test does not check that the layout is GOOD - the other tests do that. It
// checks that the committed vectors still describe what this code produces, so a
// layout change shows up as a failing test with an instruction rather than as a
// client whose signatures stop verifying in production.

const signVectorsPath = "../../../../packages/sdk/src/sign-vectors.json"

type signVector struct {
	Name      string `json:"name"`
	Why       string `json:"why"`
	PublicKey string `json:"publicKeyHex"`
	Provider  string `json:"provider"`
	Model     string `json:"model"`
	// A STRING because int64 nanoseconds do not survive a JSON number in
	// JavaScript. See cmd/signvectors.
	Timestamp       string              `json:"timestamp"`
	Messages        []map[string]string `json:"messages"`
	MaxTokens       int                 `json:"maxTokens"`
	Temperature     float64             `json:"temperature"`
	SigningBytesHex string              `json:"signingBytesHex"`
}

func TestTheCommittedSignVectorsMatchThisCode(t *testing.T) {
	raw, err := os.ReadFile(signVectorsPath)
	if err != nil {
		t.Fatalf("read %s: %v", signVectorsPath, err)
	}
	var vectors []signVector
	if err := json.Unmarshal(raw, &vectors); err != nil {
		t.Fatalf("parse %s: %v", signVectorsPath, err)
	}
	if len(vectors) == 0 {
		t.Fatal("the vector file is empty, so it guards nothing")
	}

	for _, v := range vectors {
		key, err := hex.DecodeString(v.PublicKey)
		if err != nil {
			t.Fatalf("%s: bad key hex: %v", v.Name, err)
		}
		msgs := make([]Message, 0, len(v.Messages))
		for _, m := range v.Messages {
			msgs = append(msgs, Message{Role: Role(m["role"]), Content: m["content"]})
		}
		ts, err := strconv.ParseInt(v.Timestamp, 10, 64)
		if err != nil {
			t.Fatalf("%s: bad timestamp %q: %v", v.Name, v.Timestamp, err)
		}
		auth := &RunAuthorization{
			PublicKey: key,
			Provider:  v.Provider,
			Model:     v.Model,
			Timestamp: ts,
		}
		got := hex.EncodeToString(auth.SigningBytes(InferenceRequest{
			Model:       v.Model,
			Messages:    msgs,
			MaxTokens:   v.MaxTokens,
			Temperature: v.Temperature,
		}))
		if got != v.SigningBytesHex {
			t.Errorf("%s (%s): the committed vector no longer matches this code.\n"+
				"  want %s\n   got %s\n"+
				"If the layout changed on purpose, regenerate and change BOTH TypeScript copies:\n"+
				"  cd services/core && go run ./cmd/signvectors > ../../packages/sdk/src/sign-vectors.json",
				v.Name, v.Why, v.SigningBytesHex, got)
		}
	}
}

// A vector file that only covered ASCII would pass while the two sides disagreed
// on every non-ASCII prompt, because that is exactly where a byte count and a
// UTF-16 count diverge. This asserts the file keeps carrying the hard cases.
func TestTheSignVectorsStillCoverTheEncodingTrap(t *testing.T) {
	raw, err := os.ReadFile(signVectorsPath)
	if err != nil {
		t.Fatalf("read %s: %v", signVectorsPath, err)
	}
	var vectors []signVector
	if err := json.Unmarshal(raw, &vectors); err != nil {
		t.Fatalf("parse %s: %v", signVectorsPath, err)
	}

	var multiByte, astral, empty bool
	for _, v := range vectors {
		fields := []string{v.Provider, v.Model}
		for _, m := range v.Messages {
			fields = append(fields, m["content"])
		}
		if len(v.Messages) == 0 {
			empty = true
		}
		for _, f := range fields {
			for _, r := range f {
				switch {
				case r > 0xFFFF:
					// Needs a surrogate PAIR in UTF-16, so the unit count and the
					// byte count differ by more than a constant factor.
					astral = true
				case r > 0x7F:
					multiByte = true
				}
			}
		}
	}
	if !multiByte {
		t.Error("no vector carries a multi-byte character, so a byte-versus-UTF-16 drift would pass")
	}
	if !astral {
		t.Error("no vector carries a character outside the BMP, which is the case a naive byte-count fix still gets wrong")
	}
	if !empty {
		t.Error("no vector has an empty transcript, which has a digest of its own")
	}
}
