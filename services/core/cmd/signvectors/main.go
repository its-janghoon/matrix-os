// Command signvectors writes cross-language test vectors for the signing layouts.
//
// Usage:
//
//	go run ./cmd/signvectors > ../../packages/sdk/src/sign-vectors.json
//
// WHY THIS EXISTS. The run-authorization digest has THREE implementations: Go
// here, packages/protocol as the canonical TypeScript one, and packages/sdk,
// which is published and so keeps its own dependency-free transcription. The two
// TypeScript copies are already pinned to each other by a differential test over
// randomized inputs. Nothing pinned either of them to GO, which is the side that
// verifies.
//
// A drift across that boundary does not look like a layout bug. It arrives in
// production as "invalid signature", which reads as a key problem and sends
// somebody looking in the wrong place - and on this network a run authorization
// is what lets a browser buy inference without an API key, so the failure is
// "nobody can pay" rather than "one client is odd".
//
// THE TRAP THESE VECTORS ARE AIMED AT. Go len() counts BYTES and JavaScript
// .length counts UTF-16 code units, so the length prefixes agree on ASCII and
// disagree on everything else. A model name with an accent, a prompt in Korean,
// an emoji in a chat turn: each is a different digest on the two sides while
// every ASCII test passes. So the cases below are deliberately not tidy - they
// carry multi-byte characters, characters outside the BMP (where UTF-16 uses a
// surrogate pair and the two counts differ by more than the byte length), an
// empty transcript, and an empty field.
//
// Regenerate it when a signing layout changes ON PURPOSE. A test failing here
// means the two sides disagree; a test failing after regeneration means the
// TypeScript side has not been changed to match.
package main

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"strconv"

	"github.com/ecirlabs/matrix-core/internal/inference"
)

type vector struct {
	Name      string `json:"name"`
	Why       string `json:"why"`
	PublicKey string `json:"publicKeyHex"`
	Provider  string `json:"provider"`
	Model     string `json:"model"`
	// Timestamp is a STRING, not a JSON number.
	//
	// It is int64 nanoseconds, which passes 2^53 in 1970 + 104 days, so a JSON
	// number cannot carry it: JavaScript parses 1790059598000000000 to a
	// different value and the digest computed from it does not match. Caught by
	// the TypeScript side of this guard on its first run, which is what the guard
	// is for. Proto JSON already spells int64 as a string for the same reason -
	// the market API returns amounts and totals that way.
	Timestamp string          `json:"timestamp"`
	Messages  []vectorMessage `json:"messages"`
	// MaxTokens and Temperature joined the digest in v0.5.8. Carried explicitly so
	// the vectors exercise them rather than only their zero values - a field hashed
	// as zero everywhere is indistinguishable from a field nobody hashes.
	MaxTokens   int     `json:"maxTokens"`
	Temperature float64 `json:"temperature"`
	// Tools joined the digest in v0.5.9, as a block emitted ONLY when the request
	// has a tool surface. That conditionality is itself a thing to pin: a vector
	// with no tools must hash exactly as it did before tools existed, so the cases
	// below deliberately include both kinds and a TypeScript copy that emits the
	// block unconditionally fails on the tool-free ones.
	Tools []vectorTool `json:"tools,omitempty"`
	// SigningBytesHex is the whole authorization payload, so a difference in the
	// digest and a difference in the framing around it are told apart.
	SigningBytesHex string `json:"signingBytesHex"`
}

// vectorMessage is a turn as the JSON file carries it. Tool fields are omitted
// when empty so the tool-free vectors stay readable and a reader can see at a
// glance which cases exercise the block.
type vectorMessage struct {
	Role       string           `json:"role"`
	Content    string           `json:"content"`
	ToolCallID string           `json:"toolCallId,omitempty"`
	ToolCalls  []vectorToolCall `json:"toolCalls,omitempty"`
}

type vectorTool struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	// Parameters is a STRING holding raw JSON, not a nested object.
	//
	// It has to be, for the same reason the timestamp is a string: the bytes are
	// what is hashed, and a nested object would be re-serialized by each language's
	// JSON writer with its own key order and spacing. Two implementations would
	// then hash two different byte strings from one file and the vector would be
	// testing the JSON writers rather than the digest.
	Parameters string `json:"parameters,omitempty"`
}

type vectorToolCall struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

func main() {
	// A fixed key: these vectors are about the LAYOUT, and a random key would
	// make the file churn on every regeneration without testing anything more.
	key := make([]byte, 32)
	for i := range key {
		key[i] = byte(i)
	}

	cases := []struct {
		name, why, provider, model string
		timestamp                  int64
		maxTokens                  int
		temperature                float64
		messages                   []inference.Message
		tools                      []inference.ToolDefinition
	}{
		{
			name:      "ascii",
			why:       "the case every implementation gets right, so a failure here is a framing bug rather than an encoding one",
			provider:  "eth:0x856e3fff84a5e833420b43cec0b4e13c16779817",
			model:     "qwen3.6-27b",
			timestamp: 1790059598000000000,
			messages: []inference.Message{
				{Role: inference.RoleUser, Content: "hello"},
			},
		},
		{
			name:      "korean",
			why:       "three bytes per character and one UTF-16 unit each: Go's length prefix is three times JavaScript's",
			provider:  "eth:0x856e3fff84a5e833420b43cec0b4e13c16779817",
			model:     "qwen3.6-27b",
			timestamp: 1790059598000000000,
			messages: []inference.Message{
				{Role: inference.RoleUser, Content: "안녕하세요, 오늘 날씨 어때요?"},
				{Role: inference.RoleAssistant, Content: "좋습니다."},
			},
		},
		{
			name:      "astral",
			why:       "outside the BMP, so UTF-16 uses a surrogate PAIR: two units for four bytes, which is the case a naive byte-count fix still gets wrong",
			provider:  "eth:0x856e3fff84a5e833420b43cec0b4e13c16779817",
			model:     "모델-🜛-v2",
			timestamp: 1,
			messages: []inference.Message{
				{Role: inference.RoleUser, Content: "𝄞 🧑‍🚀 done"},
			},
		},
		{
			name:      "empty-transcript",
			why:       "no messages at all still has a digest of its own; the run is refused later, not here",
			provider:  "p",
			model:     "m",
			timestamp: 0,
			messages:  nil,
		},
		{
			name:      "empty-fields",
			why:       "an empty provider, model and content are length-prefixed as zero rather than skipped, which is what keeps them from being confused with an absent field",
			provider:  "",
			model:     "",
			timestamp: -1,
			messages: []inference.Message{
				{Role: inference.RoleUser, Content: ""},
			},
		},
		{
			name:      "adjacent-turns",
			why:       "the prefixes are why \"ab\" and \"a\"+\"b\" do not hash the same; without them one signature would authorize either conversation",
			provider:  "p",
			model:     "m",
			timestamp: 2,
			messages: []inference.Message{
				{Role: inference.RoleUser, Content: "a"},
				{Role: inference.RoleUser, Content: "b"},
			},
		},
		{
			name:      "bounds-set",
			why:       "max_tokens and temperature are in the digest from v0.5.8; a node could otherwise raise the token budget on a signed request and the meter would charge the buyer for it",
			provider:  "eth:0x856e3fff84a5e833420b43cec0b4e13c16779817",
			model:     "qwen3.6-27b",
			timestamp: 1790059598000000000,
			maxTokens: 4096,
			// A value with no exact short decimal, hashed as its IEEE-754 bits
			// rather than formatted: a decimal rendering would need two languages
			// to agree about digits and trailing zeros, and the bits have one
			// spelling.
			temperature: 0.7,
			messages: []inference.Message{
				{Role: inference.RoleUser, Content: "write a haiku"},
			},
		},
		{
			name:        "bounds-edge",
			why:         "a whole-number temperature and a token budget of one, so the two fields are exercised at their small end as well as their usual one",
			provider:    "p",
			model:       "m",
			timestamp:   3,
			maxTokens:   1,
			temperature: 2,
			messages: []inference.Message{
				{Role: inference.RoleUser, Content: "x"},
			},
		},
		{
			name:      "tool-offered",
			why:       "one tool offered and none called yet: this is the request /chat sends on the first turn of a search, and it is where a node adding a tool of its own has to fail",
			provider:  "eth:0x856e3fff84a5e833420b43cec0b4e13c16779817",
			model:     "qwen3.6-27b",
			timestamp: 1790059598000000000,
			maxTokens: 512,
			messages: []inference.Message{
				{Role: inference.RoleUser, Content: "서울 날씨 어때?"},
			},
			tools: []inference.ToolDefinition{{
				Name:        "web_search",
				Description: "Search the web and return result titles and snippets.",
				Parameters:  json.RawMessage(`{"type":"object","properties":{"query":{"type":"string"}},"required":["query"]}`),
			}},
		},
		{
			name:      "tool-name-astral",
			why:       "a non-ASCII tool name and a schema with a non-ASCII description: the tool block has its own length prefixes and they meet the byte-versus-UTF-16 trap independently of the transcript's",
			provider:  "p",
			model:     "m",
			timestamp: 4,
			messages: []inference.Message{
				{Role: inference.RoleUser, Content: "go"},
			},
			tools: []inference.ToolDefinition{
				{
					Name:        "날씨_조회🜛",
					Description: "도시 이름으로 날씨를 찾는다",
					Parameters:  json.RawMessage(`{"type":"object","properties":{"도시":{"type":"string"}}}`),
				},
				{
					// No description and no parameters: both are length-prefixed as
					// zero rather than skipped, so a tool with neither is still
					// distinguishable from one that is absent.
					Name: "b",
				},
			},
		},
		{
			name:      "tool-loop-turn",
			why:       "iteration two: the assistant's own call and the client's result are replayed, and both are signed - a node that rewrote the arguments would be telling the model it had asked something it did not ask",
			provider:  "eth:0x856e3fff84a5e833420b43cec0b4e13c16779817",
			model:     "qwen3.6-27b",
			timestamp: 1790059598000000005,
			maxTokens: 512,
			messages: []inference.Message{
				{Role: inference.RoleUser, Content: "서울 날씨 어때?"},
				{Role: inference.RoleAssistant, ToolCalls: []inference.ToolCall{{
					ID:        "call_abc123",
					Name:      "web_search",
					Arguments: `{"query":"서울 날씨"}`,
				}}},
				{Role: inference.RoleTool, ToolCallID: "call_abc123", Content: "맑음, 최고 26도"},
			},
			tools: []inference.ToolDefinition{{
				Name:        "web_search",
				Description: "Search the web and return result titles and snippets.",
				Parameters:  json.RawMessage(`{"type":"object","properties":{"query":{"type":"string"}},"required":["query"]}`),
			}},
		},
		{
			name:      "tool-metadata-without-tools",
			why:       "a transcript carrying a tool result while offering no tools still emits the block, because the result and the call it answers are what a node would otherwise be free to rewrite",
			provider:  "p",
			model:     "m",
			timestamp: 5,
			messages: []inference.Message{
				{Role: inference.RoleTool, ToolCallID: "c1", Content: "42"},
			},
		},
		{
			name:      "two-calls-one-turn",
			why:       "two calls in one assistant turn, so their order is inside the signature: swapping them would pair each result with the other's call",
			provider:  "p",
			model:     "m",
			timestamp: 6,
			messages: []inference.Message{
				{Role: inference.RoleAssistant, ToolCalls: []inference.ToolCall{
					{ID: "c1", Name: "a", Arguments: `{"x":1}`},
					{ID: "c2", Name: "a", Arguments: `{"x":2}`},
				}},
			},
		},
	}

	out := make([]vector, 0, len(cases))
	for _, c := range cases {
		auth := &inference.RunAuthorization{
			PublicKey: key,
			Provider:  c.provider,
			Model:     c.model,
			Timestamp: c.timestamp,
		}
		req := inference.InferenceRequest{
			Model:       c.model,
			Messages:    c.messages,
			MaxTokens:   c.maxTokens,
			Temperature: c.temperature,
			Tools:       c.tools,
		}
		wire := make([]vectorMessage, 0, len(c.messages))
		for _, m := range c.messages {
			vm := vectorMessage{
				Role:       string(m.Role),
				Content:    m.Content,
				ToolCallID: m.ToolCallID,
			}
			for _, call := range m.ToolCalls {
				vm.ToolCalls = append(vm.ToolCalls, vectorToolCall{
					ID:        call.ID,
					Name:      call.Name,
					Arguments: call.Arguments,
				})
			}
			wire = append(wire, vm)
		}
		var tools []vectorTool
		for _, t := range c.tools {
			tools = append(tools, vectorTool{
				Name:        t.Name,
				Description: t.Description,
				Parameters:  string(t.Parameters),
			})
		}
		out = append(out, vector{
			Name:            c.name,
			Why:             c.why,
			PublicKey:       hex.EncodeToString(key),
			Provider:        c.provider,
			Model:           c.model,
			Timestamp:       strconv.FormatInt(c.timestamp, 10),
			Messages:        wire,
			MaxTokens:       c.maxTokens,
			Temperature:     c.temperature,
			Tools:           tools,
			SigningBytesHex: hex.EncodeToString(auth.SigningBytes(req)),
		})
	}

	body, err := json.MarshalIndent(out, "", "  ")
	if err != nil {
		fmt.Fprintf(os.Stderr, "signvectors: %v\n", err)
		os.Exit(1)
	}
	if _, err := os.Stdout.Write(append(body, '\n')); err != nil {
		fmt.Fprintf(os.Stderr, "signvectors: %v\n", err)
		os.Exit(1)
	}
}
