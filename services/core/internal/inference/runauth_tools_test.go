package inference

import (
	"crypto/ed25519"
	"encoding/json"
	"testing"
)

// What the tool block in the run-authorization digest is for.
//
// A tool definition tells the model what it may do. If it rides in the request but
// outside the signature, a node can add, remove or rewrite one on a request the buyer
// signed and the signature still verifies: the buyer authorised one question, the
// model was asked another, and the buyer paid. These tests are that claim, stated as
// code.

func toolReq(tools ...ToolDefinition) InferenceRequest {
	return InferenceRequest{
		Model:    "m",
		Messages: []Message{{Role: RoleUser, Content: "hello"}},
		Tools:    tools,
	}
}

var searchTool = ToolDefinition{
	Name:        "web_search",
	Description: "Search the web.",
	Parameters:  json.RawMessage(`{"type":"object","properties":{"query":{"type":"string"}}}`),
}

// The property that keeps every signature made before tools existed valid: a request
// with no tool surface hashes exactly as it did, so no second compatibility window is
// owed. An implementation that emitted a zero count unconditionally would fail here.
func TestARequestWithNoToolsHashesAsItDidBeforeToolsExisted(t *testing.T) {
	plain := InferenceRequest{Model: "m", Messages: []Message{{Role: RoleUser, Content: "hello"}}}
	nilTools := plain
	nilTools.Tools = nil
	emptyTools := plain
	emptyTools.Tools = []ToolDefinition{}

	want := requestDigest(plain)
	if got := requestDigest(nilTools); got != want {
		t.Error("a nil tool slice changed the digest, so every pre-tool signature just broke")
	}
	if got := requestDigest(emptyTools); got != want {
		t.Error("an empty tool slice changed the digest; empty and absent must be one thing")
	}
}

func TestAddingRemovingOrRewritingAToolChangesTheDigest(t *testing.T) {
	none := requestDigest(toolReq())
	one := requestDigest(toolReq(searchTool))
	if none == one {
		t.Fatal("attaching a tool did not change the digest, so a node could attach one to a " +
			"signed request and charge the buyer for answering it")
	}

	renamed := searchTool
	renamed.Name = "web_search2"
	if requestDigest(toolReq(renamed)) == one {
		t.Error("renaming a tool did not change the digest")
	}

	redescribed := searchTool
	redescribed.Description = "Search the web, but do it wrong."
	if requestDigest(toolReq(redescribed)) == one {
		t.Error("rewriting a tool's description did not change the digest, and the description is " +
			"what tells the model when to use it")
	}

	reschemad := searchTool
	reschemad.Parameters = json.RawMessage(`{"type":"object","properties":{"cmd":{"type":"string"}}}`)
	if requestDigest(toolReq(reschemad)) == one {
		t.Error("rewriting a tool's schema did not change the digest")
	}
}

func TestToolOrderIsInsideTheSignature(t *testing.T) {
	// Reordering is not semantically neutral to a model, and more to the point the
	// sequence is part of what the buyer signed. Sorting would be a second rule three
	// implementations must agree on for no gain.
	a := ToolDefinition{Name: "a"}
	b := ToolDefinition{Name: "b"}
	if requestDigest(toolReq(a, b)) == requestDigest(toolReq(b, a)) {
		t.Error("swapping two tools produced the same digest")
	}
}

func TestAToolSchemaIsHashedAsTheBytesTheCallerSent(t *testing.T) {
	// Two spellings of the same JSON object are DIFFERENT digests, deliberately: the
	// bytes are what is hashed, because a re-serialized object would need Go and
	// JavaScript to agree about key order and spacing before either could reproduce
	// the other's digest.
	compact := ToolDefinition{Name: "t", Parameters: json.RawMessage(`{"a":1,"b":2}`)}
	spaced := ToolDefinition{Name: "t", Parameters: json.RawMessage(`{"a": 1, "b": 2}`)}
	if requestDigest(toolReq(compact)) == requestDigest(toolReq(spaced)) {
		t.Error("two spellings of one schema hashed the same, which means something is " +
			"re-serializing the bytes instead of hashing them")
	}
}

func TestATranscriptsToolMetadataIsInsideTheSignature(t *testing.T) {
	// Iteration two of a loop replays the model's own call and the client's result. A
	// node free to rewrite the call would be telling the model it had asked something
	// it did not ask.
	base := InferenceRequest{
		Model: "m",
		Messages: []Message{
			{Role: RoleUser, Content: "weather?"},
			{Role: RoleAssistant, ToolCalls: []ToolCall{{ID: "c1", Name: "web_search", Arguments: `{"query":"seoul"}`}}},
			{Role: RoleTool, ToolCallID: "c1", Content: "sunny"},
		},
	}
	want := requestDigest(base)

	rewrittenArgs := base
	rewrittenArgs.Messages = append([]Message(nil), base.Messages...)
	rewrittenArgs.Messages[1] = Message{Role: RoleAssistant, ToolCalls: []ToolCall{
		{ID: "c1", Name: "web_search", Arguments: `{"query":"something else"}`},
	}}
	if requestDigest(rewrittenArgs) == want {
		t.Error("rewriting the arguments of a call already made did not change the digest")
	}

	repointed := base
	repointed.Messages = append([]Message(nil), base.Messages...)
	repointed.Messages[2] = Message{Role: RoleTool, ToolCallID: "c2", Content: "sunny"}
	if requestDigest(repointed) == want {
		t.Error("re-pointing a tool result at a different call did not change the digest, so a " +
			"node could pair a result with the wrong call and the model would answer from it")
	}

	renamedCall := base
	renamedCall.Messages = append([]Message(nil), base.Messages...)
	renamedCall.Messages[1] = Message{Role: RoleAssistant, ToolCalls: []ToolCall{
		{ID: "c1", Name: "other_tool", Arguments: `{"query":"seoul"}`},
	}}
	if requestDigest(renamedCall) == want {
		t.Error("renaming a call already made did not change the digest")
	}
}

func TestTwoCallsInOneTurnKeepTheirOrder(t *testing.T) {
	turn := func(first, second ToolCall) InferenceRequest {
		return InferenceRequest{Model: "m", Messages: []Message{
			{Role: RoleAssistant, ToolCalls: []ToolCall{first, second}},
		}}
	}
	a := ToolCall{ID: "c1", Name: "t", Arguments: `{"n":1}`}
	b := ToolCall{ID: "c2", Name: "t", Arguments: `{"n":2}`}
	if requestDigest(turn(a, b)) == requestDigest(turn(b, a)) {
		t.Error("swapping two calls in one turn produced the same digest, so each result could " +
			"be paired with the other's call")
	}
}

// The compatibility arm accepts a signature made under the pre-v0.5.8 digest, which
// covers the transcript and nothing else. Accepting it for a request carrying tools
// would hand straight back the hole the tool block closes.
func TestTheLegacyDigestArmIsClosedToToolRequests(t *testing.T) {
	pub, priv, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatalf("key: %v", err)
	}
	req := toolReq(searchTool)
	auth := &RunAuthorization{PublicKey: pub, Provider: "p", Model: "m", Timestamp: 1}

	// A client signing the OLD bytes for a request that carries tools.
	auth.Signature = ed25519.Sign(priv, auth.legacySigningBytes(req))
	if acceptsEitherDigest(auth, req, func(b []byte) bool { return ed25519.Verify(pub, b, auth.Signature) }) {
		t.Fatal("a legacy-signed request with tools was accepted: a node could attach any tool " +
			"it liked to it and the signature would still verify")
	}

	// The same client, same rule, on a request with no tools: still accepted, because
	// that is what the compatibility window is for.
	plain := InferenceRequest{Model: "m", Messages: []Message{{Role: RoleUser, Content: "hello"}}}
	auth.Signature = ed25519.Sign(priv, auth.legacySigningBytes(plain))
	if !acceptsEitherDigest(auth, plain, func(b []byte) bool { return ed25519.Verify(pub, b, auth.Signature) }) {
		t.Error("a legacy-signed request with no tools was refused, which breaks every client " +
			"the compatibility window exists for")
	}

	// And the current rule still works for tools, or nothing could use them at all.
	auth.Signature = ed25519.Sign(priv, auth.SigningBytes(req))
	if !acceptsEitherDigest(auth, req, func(b []byte) bool { return ed25519.Verify(pub, b, auth.Signature) }) {
		t.Error("a correctly signed tool request was refused")
	}
}

func TestToolsPresentReadsThePromptAsWellAsTheMessages(t *testing.T) {
	// A request that carries only a bare Prompt still has its effective transcript
	// built, so the surface check must look at the effective messages rather than the
	// literal slice - otherwise a prompt-shaped tool request would take the legacy arm.
	if toolsPresent(InferenceRequest{Prompt: "hi"}) {
		t.Error("a plain prompt reported a tool surface")
	}
	if !toolsPresent(InferenceRequest{Prompt: "hi", Tools: []ToolDefinition{searchTool}}) {
		t.Error("a prompt request offering a tool reported no tool surface")
	}
	if !toolsPresent(InferenceRequest{Messages: []Message{{Role: RoleTool, ToolCallID: "c1", Content: "x"}}}) {
		t.Error("a transcript carrying a tool result reported no tool surface")
	}
}

// The ceiling bounds a charge by the text that crossed the wire. Tool text crossed it
// too, and a ceiling blind to it clamps an honest bill DOWN - underpaying the seller
// for work the buyer received.
func TestTheCeilingCountsToolTextButOnlyWhenThereIsAny(t *testing.T) {
	plain := InferenceRequest{Model: "m", Messages: []Message{{Role: RoleUser, Content: "weather?"}}}
	resp := InferenceResponse{Completion: "sunny"}

	// Unchanged without tools, which is what keeps a buyer on an older client agreeing
	// with its node about every ordinary exchange.
	if MaxUnitsForResponse(plain, resp) != MaxUnitsFor(plain, resp.Completion, resp.Reasoning) {
		t.Error("the response form disagreed with the text form on a request with no tools")
	}

	withTool := plain
	withTool.Tools = []ToolDefinition{searchTool}
	if MaxUnitsForResponse(withTool, resp) <= MaxUnitsForResponse(plain, resp) {
		t.Error("offering a tool did not raise the ceiling, though its schema is prompt tokens " +
			"the seller really spent")
	}

	called := InferenceResponse{ToolCalls: []ToolCall{
		{ID: "call_abc123", Name: "web_search", Arguments: `{"query":"seoul weather today"}`},
	}}
	if MaxUnitsForResponse(withTool, called) <= MaxUnitsForResponse(withTool, InferenceResponse{}) {
		t.Error("a tool call did not raise the ceiling, though the model generated it")
	}
}
