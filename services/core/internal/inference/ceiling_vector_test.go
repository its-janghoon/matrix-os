package inference

import "testing"

// The same vectors as apps/web/src/lib/wallet/ceiling.test.ts.
//
// Two implementations of one bound only stay equal if something fails when they
// stop being. A client computing a TIGHTER ceiling than this would refuse bills
// the node considers honest, which reads to a buyer as the seller cheating; a
// looser one waves through the overcharge the ceiling exists to catch.
func TestCeilingVectorsMatchTheClient(t *testing.T) {
	repeat := func(s string, n int) string {
		out := ""
		for i := 0; i < n; i++ {
			out += s
		}
		return out
	}
	for _, tc := range []struct {
		name       string
		messages   []Message
		completion string
		reasoning  string
		want       uint64
	}{
		{"a short ascii exchange falls to the floor",
			[]Message{{Role: "user", Content: "hi"}}, "hello", "", 64},
		{"plain ascii above the floor",
			[]Message{{Role: "user", Content: repeat("a", 200)}}, repeat("b", 100), "", 336},
		{"reasoning is counted",
			[]Message{{Role: "user", Content: repeat("a", 100)}}, repeat("b", 50), repeat("c", 400), 586},
		{"korean text is counted in bytes",
			[]Message{{Role: "user", Content: repeat("안녕하세요", 40)}}, repeat("반갑습니다", 40), "", 1236},
		{"an emoji is four bytes",
			[]Message{{Role: "user", Content: repeat("🙂", 100)}}, "", "", 436},
		{"several messages each carry the template overhead",
			[]Message{
				{Role: "system", Content: "be brief"},
				{Role: "user", Content: repeat("x", 300)},
				{Role: "assistant", Content: repeat("y", 300)},
			}, repeat("z", 300), "", 991},
	} {
		if got := MaxUnitsFor(InferenceRequest{Messages: tc.messages}, tc.completion, tc.reasoning); got != tc.want {
			t.Errorf("%s: got %d want %d", tc.name, got, tc.want)
		}
	}
}
