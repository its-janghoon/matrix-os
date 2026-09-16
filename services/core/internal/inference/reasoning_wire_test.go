package inference

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// There are two names on the wire for one thing, and this node read one of them.
//
// FOUND LIVE, not here. A qwen3 job on the production GPU box reported 5284
// completion tokens of which 4191 were reasoning. vLLM sent the working under
// "reasoning"; this node read "reasoning_content", found nothing, and the
// charge ceiling - which is deliberately derived from bytes the node actually
// holds rather than from a count the seller reports - capped the bill at the
// answer alone. The provider served the work and was paid for a fifth of it,
// and the buyer received none of the working they were partly billed for.
//
// The tests below are per-spelling because that is the axis the bug lived on. A
// single test against whichever name the author had in mind passes while the
// other spelling silently loses everything, which is exactly what shipped.
//
// What is NOT done here, deliberately: usage.completion_tokens_details.
// reasoning_tokens tells us the count directly, and using it to raise the
// ceiling would reintroduce the thing the ceiling exists to stop - a seller
// answering "hello" to "hi" and reporting a thousand tokens. The ceiling rises
// only for text the node received and can hand to the buyer.

func reasoningServer(t *testing.T, field, working string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{
			"model": "qwen3",
			"choices": [{"message": {
				"role": "assistant",
				"content": "Yes, 8191 is prime.",
				%q: %q
			}}],
			"usage": {"prompt_tokens": 25, "completion_tokens": 5284, "total_tokens": 5309}
		}`, field, working)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestTheWorkingIsReadUnderEitherName(t *testing.T) {
	const working = "8191 = 2^13 - 1. Trial division to 90 finds no factor."

	for _, field := range []string{"reasoning", "reasoning_content"} {
		t.Run(field, func(t *testing.T) {
			t.Setenv("MATRIX_TEST_OPENAI_KEY", "k")
			srv := reasoningServer(t, field, working)

			backend, err := NewOpenAIBackend(OpenAIConfig{
				BaseURL:    srv.URL,
				APIKeyEnv:  "MATRIX_TEST_OPENAI_KEY",
				HTTPClient: srv.Client(),
			})
			if err != nil {
				t.Fatalf("NewOpenAIBackend: %v", err)
			}

			resp, err := backend.Infer(context.Background(), InferenceRequest{
				Model: "qwen3", Prompt: "Is 8191 prime?",
			})
			if err != nil {
				t.Fatalf("Infer: %v", err)
			}
			if resp.Reasoning != working {
				t.Fatalf("the working under %q was not read: got %q", field, resp.Reasoning)
			}
			if resp.Completion != "Yes, 8191 is prime." {
				t.Fatalf("completion = %q", resp.Completion)
			}
		})
	}
}

// The consequence the live sale actually suffered, stated as money rather than
// as a missing field: the ceiling has to cover the working, or the provider is
// paid for the answer alone.
func TestTheCeilingCoversTheWorkingUnderEitherName(t *testing.T) {
	working := strings.Repeat("trial division by 7, 11, 13, 17. ", 200)

	for _, field := range []string{"reasoning", "reasoning_content"} {
		t.Run(field, func(t *testing.T) {
			t.Setenv("MATRIX_TEST_OPENAI_KEY", "k")
			srv := reasoningServer(t, field, working)

			backend, err := NewOpenAIBackend(OpenAIConfig{
				BaseURL:    srv.URL,
				APIKeyEnv:  "MATRIX_TEST_OPENAI_KEY",
				HTTPClient: srv.Client(),
			})
			if err != nil {
				t.Fatalf("NewOpenAIBackend: %v", err)
			}
			req := InferenceRequest{Model: "qwen3", Prompt: "Is 8191 prime?"}
			resp, err := backend.Infer(context.Background(), req)
			if err != nil {
				t.Fatalf("Infer: %v", err)
			}

			withWorking := MaxUnitsFor(req, resp.Completion, resp.Reasoning)
			answerOnly := MaxUnitsFor(req, resp.Completion, "")
			if withWorking <= answerOnly {
				t.Fatalf("the ceiling did not rise for the working: %d vs %d",
					withWorking, answerOnly)
			}
			// The model reported 5284 completion tokens. A ceiling that covers
			// the working must let most of that through; one that does not caps
			// the provider at the answer.
			if withWorking < 5000 {
				t.Fatalf("ceiling %d still clips a 5284-token job", withWorking)
			}
		})
	}
}

// The streaming path read NEITHER name, so a streamed reasoning job delivered no
// working at all and was billed as if it had produced only its answer.
func TestTheStreamingPathCarriesTheWorking(t *testing.T) {
	for _, field := range []string{"reasoning", "reasoning_content"} {
		t.Run(field, func(t *testing.T) {
			t.Setenv("MATRIX_TEST_OPENAI_KEY", "k")
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "text/event-stream")
				flusher, _ := w.(http.Flusher)
				frames := []string{
					fmt.Sprintf(`{"model":"qwen3","choices":[{"delta":{%q:"first I check "}}]}`, field),
					fmt.Sprintf(`{"model":"qwen3","choices":[{"delta":{%q:"small primes."}}]}`, field),
					`{"model":"qwen3","choices":[{"delta":{"content":"Yes, "}}]}`,
					`{"model":"qwen3","choices":[{"delta":{"content":"prime."}}]}`,
					`{"model":"qwen3","choices":[{"delta":{},"finish_reason":"stop"}],` +
						`"usage":{"prompt_tokens":25,"completion_tokens":5284,"total_tokens":5309}}`,
				}
				for _, f := range frames {
					fmt.Fprintf(w, "data: %s\n\n", f)
					if flusher != nil {
						flusher.Flush()
					}
				}
				fmt.Fprint(w, "data: [DONE]\n\n")
			}))
			defer srv.Close()

			backend, err := NewOpenAIBackend(OpenAIConfig{
				BaseURL:    srv.URL,
				APIKeyEnv:  "MATRIX_TEST_OPENAI_KEY",
				HTTPClient: srv.Client(),
			})
			if err != nil {
				t.Fatalf("NewOpenAIBackend: %v", err)
			}

			var streamed strings.Builder
			workingTokens := 0
			resp, err := backend.InferStream(context.Background(),
				InferenceRequest{Model: "qwen3", Prompt: "Is 8191 prime?"},
				func(chunk string) error {
					streamed.WriteString(chunk)
					return nil
				},
				func(tokens int) error {
					workingTokens += tokens
					return nil
				})
			if err != nil {
				t.Fatalf("InferStream: %v", err)
			}

			if resp.Reasoning != "first I check small primes." {
				t.Fatalf("the working did not survive the stream: %q", resp.Reasoning)
			}
			if resp.Completion != "Yes, prime." {
				t.Fatalf("completion = %q", resp.Completion)
			}
			// The working must NOT be forwarded as completion deltas. The
			// assembled stream is what a buyer displays and what the receipt's
			// digest commits to; interleaving the working would make it neither
			// the answer nor the working.
			if streamed.String() != "Yes, prime." {
				t.Fatalf("the working leaked into the completion stream: %q", streamed.String())
			}
			// Its SIZE does travel. Without this a reasoning model reports no
			// progress for the part of the run that IS the wait: four fifths of
			// the answer can be working, and a progress line that counts only
			// completion deltas sits at zero until the thinking is already over.
			if workingTokens == 0 {
				t.Fatal("the working produced no progress at all, so the wait it exists to show stays silent")
			}
		})
	}
}
