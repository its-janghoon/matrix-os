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
	Timestamp       string           `json:"timestamp"`
	Messages        []signVectorMsg  `json:"messages"`
	MaxTokens       int              `json:"maxTokens"`
	Temperature     float64          `json:"temperature"`
	Tools           []signVectorTool `json:"tools"`
	SigningBytesHex string           `json:"signingBytesHex"`
}

type signVectorMsg struct {
	Role       string               `json:"role"`
	Content    string               `json:"content"`
	ToolCallID string               `json:"toolCallId"`
	ToolCalls  []signVectorToolCall `json:"toolCalls"`
}

type signVectorTool struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	// Raw JSON as a STRING: the bytes are what is hashed. See cmd/signvectors.
	Parameters string `json:"parameters"`
}

type signVectorToolCall struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

// request rebuilds the request a vector describes. Shared by both tests so the
// coverage assertion below cannot drift from what is actually hashed.
func (v signVector) request() InferenceRequest {
	msgs := make([]Message, 0, len(v.Messages))
	for _, m := range v.Messages {
		msg := Message{Role: Role(m.Role), Content: m.Content, ToolCallID: m.ToolCallID}
		for _, c := range m.ToolCalls {
			msg.ToolCalls = append(msg.ToolCalls, ToolCall{ID: c.ID, Name: c.Name, Arguments: c.Arguments})
		}
		msgs = append(msgs, msg)
	}
	var tools []ToolDefinition
	for _, t := range v.Tools {
		tools = append(tools, ToolDefinition{
			Name:        t.Name,
			Description: t.Description,
			Parameters:  json.RawMessage(t.Parameters),
		})
	}
	return InferenceRequest{
		Model:       v.Model,
		Messages:    msgs,
		MaxTokens:   v.MaxTokens,
		Temperature: v.Temperature,
		Tools:       tools,
	}
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
		got := hex.EncodeToString(auth.SigningBytes(v.request()))
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
	var withTools, withoutTools, withCalls, astralToolName bool
	for _, v := range vectors {
		fields := []string{v.Provider, v.Model}
		for _, m := range v.Messages {
			fields = append(fields, m.Content)
			if len(m.ToolCalls) > 0 {
				withCalls = true
			}
		}
		if len(v.Messages) == 0 {
			empty = true
		}
		if len(v.Tools) > 0 {
			withTools = true
			for _, t := range v.Tools {
				fields = append(fields, t.Name, t.Description, t.Parameters)
				for _, r := range t.Name {
					if r > 0xFFFF {
						astralToolName = true
					}
				}
			}
		} else {
			withoutTools = true
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
	if !withTools {
		t.Error("no vector offers a tool, so the tool block is untested")
	}
	if !withoutTools {
		// This is the one that catches an implementation emitting the block
		// unconditionally: it would hash a zero count where this side hashes
		// nothing, and only a tool-FREE vector notices.
		t.Error("every vector offers a tool, so nothing pins the block to being absent " +
			"when there is no tool surface - which is what keeps every pre-tool digest unchanged")
	}
	if !withCalls {
		t.Error("no vector replays an assistant tool call, so the per-message metadata is untested")
	}
	if !astralToolName {
		t.Error("no tool NAME carries a character outside the BMP; the tool block has its own " +
			"length prefixes and meets the encoding trap independently of the transcript's")
	}
}
