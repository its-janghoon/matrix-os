package openaiapi

import (
	"encoding/json"
	"testing"

	"github.com/ecirlabs/matrix-core/internal/inference"
)

// The /v1 door used to answer `role "tool" is not supported on this network yet`,
// honestly, because a tool result had no call it could be bound to. It is supported
// now, and these are the boundaries of that.

func TestTheToolRoleIsAcceptedAndItsNeighboursAreNot(t *testing.T) {
	got, err := mapRole("tool")
	if err != nil {
		t.Fatalf("the tool role was refused: %v", err)
	}
	if got != inference.RoleTool {
		t.Errorf("tool mapped to %q", got)
	}

	// Deliberately NOT aliased to something close.
	//
	// "function" is OpenAI's retired spelling of a tool result and carries a NAME where
	// a tool message carries a call id, so treating it as a tool message would bind a
	// result to whichever call the model guessed. "developer" is a system message with a
	// precedence this network does not implement, so mapping it to system would claim an
	// ordering guarantee nothing enforces.
	for _, role := range []string{"function", "developer"} {
		if _, err := mapRole(role); err == nil {
			t.Errorf("role %q was accepted, though nothing here implements it", role)
		}
	}
}

func TestAToolDefinitionMustBeUsable(t *testing.T) {
	cases := []struct {
		name  string
		tools string
		want  string
	}{
		{
			name:  "no name",
			tools: `[{"type":"function","function":{"description":"x"}}]`,
			want:  "function.name",
		},
		{
			name:  "the same name twice",
			tools: `[{"type":"function","function":{"name":"t"}},{"type":"function","function":{"name":"t"}}]`,
			want:  "twice",
		},
		{
			name:  "a schema that is not an object",
			tools: `[{"type":"function","function":{"name":"t","parameters":"a string"}}]`,
			want:  "JSON Schema object",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var parsed []chatTool
			if err := json.Unmarshal([]byte(c.tools), &parsed); err != nil {
				t.Fatalf("fixture: %v", err)
			}
			_, err := toInferenceTools(parsed)
			if err == nil {
				t.Fatalf("accepted a tool that cannot work")
			}
			if !contains(err.Error(), c.want) {
				t.Errorf("error %q does not mention %q, so a caller cannot fix it", err, c.want)
			}
		})
	}
}

func TestAUsableToolSurvivesWithItsSchemaBytesIntact(t *testing.T) {
	// The bytes are inside the buyer's signature, so re-encoding them here would make
	// the node compute a different digest from the one the client signed and refuse a
	// correct request.
	raw := `{"type":"object","properties":{"query":{"type":"string"}},"required":["query"]}`
	var parsed []chatTool
	if err := json.Unmarshal([]byte(`[{"type":"function","function":`+
		`{"name":"web_search","description":"Search.","parameters":`+raw+`}}]`), &parsed); err != nil {
		t.Fatalf("fixture: %v", err)
	}
	tools, err := toInferenceTools(parsed)
	if err != nil {
		t.Fatalf("toInferenceTools: %v", err)
	}
	if len(tools) != 1 {
		t.Fatalf("got %d tools", len(tools))
	}
	if string(tools[0].Parameters) != raw {
		t.Errorf("the schema bytes changed:\n want %s\n  got %s", raw, tools[0].Parameters)
	}
}

func TestToolsChangeTheIdempotencyFingerprint(t *testing.T) {
	// Two requests identical but for their tools ask the model to do different things.
	// Treating the second as a replay would answer it with the first one's completion.
	msgs := []inference.Message{{Role: inference.RoleUser, Content: "weather?"}}
	search := []inference.ToolDefinition{{Name: "web_search", Parameters: json.RawMessage(`{}`)}}
	other := []inference.ToolDefinition{{Name: "shell", Parameters: json.RawMessage(`{}`)}}

	bare := requestFingerprint("m", msgs, 0, 0, nil)
	withSearch := requestFingerprint("m", msgs, 0, 0, search)
	withOther := requestFingerprint("m", msgs, 0, 0, other)

	if bare == withSearch {
		t.Error("adding a tool did not change the fingerprint, so a retry with one removed would " +
			"be served the other request's answer")
	}
	if withSearch == withOther {
		t.Error("two different tools produced one fingerprint")
	}
	// And an empty slice is the same request as none, or a client that sends `tools: []`
	// would never match its own retry.
	if requestFingerprint("m", msgs, 0, 0, []inference.ToolDefinition{}) != bare {
		t.Error("an empty tool list is not the same request as no tool list")
	}
}

func TestATranscriptsToolMetadataChangesTheFingerprint(t *testing.T) {
	// The loop's iterations differ in their transcript, so they were never at risk of
	// colliding - but a result re-pointed at another call is a different request and
	// must not be served from cache.
	base := []inference.Message{
		{Role: inference.RoleUser, Content: "weather?"},
		{Role: inference.RoleTool, ToolCallID: "c1", Content: "sunny"},
	}
	moved := []inference.Message{
		{Role: inference.RoleUser, Content: "weather?"},
		{Role: inference.RoleTool, ToolCallID: "c2", Content: "sunny"},
	}
	if requestFingerprint("m", base, 0, 0, nil) == requestFingerprint("m", moved, 0, 0, nil) {
		t.Error("re-pointing a tool result did not change the fingerprint")
	}
}

func contains(haystack, needle string) bool {
	return len(needle) > 0 && len(haystack) >= len(needle) && indexOf(haystack, needle) >= 0
}

func indexOf(haystack, needle string) int {
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			return i
		}
	}
	return -1
}
