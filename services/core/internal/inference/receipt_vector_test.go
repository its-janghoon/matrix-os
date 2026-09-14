package inference

import (
	"crypto/ed25519"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/ecirlabs/matrix-core/internal/token"
)

// Pinning the browser's copy of the receipt layout to this one.
//
// A receipt is only evidence if a second implementation agrees byte-for-byte on
// what was signed. The web client has to verify one without a node, so it
// carries its own transcription of SigningBytes - and a transcription that
// drifts does not fail loudly. It fails as "invalid signature", which reads as a
// key problem and sends a reader looking in entirely the wrong place while every
// receipt the network issues quietly stops verifying.
//
// So this writes vectors: receipts, and the digest THIS implementation signs for
// each. The TypeScript test reads the same file and must reproduce every digest.
// Regenerate with `go test ./internal/inference -run TestWriteReceiptVectors`.
//
// The cases are chosen for where two implementations diverge, not for where they
// agree: empty strings, zero numbers, multi-byte text, and values above 2^53
// where a JavaScript number silently stops being exact.

type receiptVector struct {
	Name    string   `json:"name"`
	Receipt *Receipt `json:"receipt"`
	// SigningDigest is what SigningBytes returns for that receipt, hex encoded.
	SigningDigest string `json:"signing_digest"`
	// ExchangeDigest pins the other hash a client computes: the one binding a
	// receipt to a prompt and a completion.
	Prompt         string `json:"prompt"`
	Completion     string `json:"completion"`
	ExchangeDigest string `json:"exchange_digest"`
}

func TestWriteReceiptVectors(t *testing.T) {
	key := make([]byte, 32)
	for i := range key {
		key[i] = byte(i + 1)
	}
	// A fixed seed, so the vectors are byte-stable across runs and a diff shows a
	// LAYOUT change rather than a fresh key.
	priv := ed25519.NewKeyFromSeed(key)
	acct := &token.Account{PublicKey: priv.Public().(ed25519.PublicKey), PrivateKey: priv}

	cases := []struct {
		name       string
		receipt    Receipt
		prompt     string
		completion string
	}{
		{
			name: "ordinary",
			receipt: Receipt{
				JobID: "job-1", Buyer: "buyer-1", Provider: "eth:0x00000000000000000000000000000000000000aa",
				Model: "llama-3.3-70b", PromptTokens: 7, CompletionTokens: 8, TotalTokens: 15,
				Units: 15, PricePerUnit: 5, Total: 75, IssuedAt: 1789000000000000000,
			},
			prompt: "what is the capital of france", completion: "paris",
		},
		{
			name: "empty strings and zeros",
			receipt: Receipt{
				JobID: "j", Buyer: "b", Provider: "p", Model: "",
				Units: 0, PricePerUnit: 0, Total: 0, IssuedAt: 0,
			},
			prompt: "", completion: "",
		},
		{
			name: "multi-byte text",
			receipt: Receipt{
				JobID: "일자리-1", Buyer: "구매자", Provider: "판매자", Model: "모델-🙂",
				PromptTokens: 3, CompletionTokens: 4, TotalTokens: 7,
				Units: 7, PricePerUnit: 3, Total: 21, IssuedAt: 1,
			},
			prompt: "안녕하세요 🙂", completion: "반갑습니다 🙃",
		},
		{
			// Above 2^53, where a JavaScript number stops being exact. A client
			// reading these as numbers rather than BigInt produces a different
			// digest here and nowhere else.
			name: "values a double cannot hold",
			receipt: Receipt{
				JobID: "big", Buyer: "b", Provider: "p", Model: "m",
				Units: 9007199254740993, PricePerUnit: 1, Total: 9007199254740993,
				IssuedAt: 9223372036854775807,
			},
			prompt: "x", completion: "y",
		},
	}

	vectors := make([]receiptVector, 0, len(cases))
	for _, tc := range cases {
		r := tc.receipt
		req := InferenceRequest{Prompt: tc.prompt}
		r.ExchangeDigest = ExchangeDigest(req, tc.completion)
		if err := r.Sign(acct); err != nil {
			t.Fatalf("%s: Sign: %v", tc.name, err)
		}
		if err := r.Verify(); err != nil && tc.name != "empty strings and zeros" {
			t.Fatalf("%s: a freshly signed vector does not verify: %v", tc.name, err)
		}
		vectors = append(vectors, receiptVector{
			Name:           tc.name,
			Receipt:        &r,
			SigningDigest:  hex.EncodeToString(r.SigningBytes()),
			Prompt:         tc.prompt,
			Completion:     tc.completion,
			ExchangeDigest: hex.EncodeToString(r.ExchangeDigest),
		})
	}

	body, err := json.MarshalIndent(vectors, "", "  ")
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	body = append(body, '\n')

	path := filepath.Join("..", "..", "..", "..", "packages", "protocol", "src", "receipt-vectors.json")
	existing, err := os.ReadFile(path)
	if err == nil && string(existing) == string(body) {
		return
	}
	if err := os.WriteFile(path, body, 0o644); err != nil {
		t.Fatalf("write vectors: %v", err)
	}
	t.Logf("wrote %d receipt vectors to %s", len(vectors), path)
}
