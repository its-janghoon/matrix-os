// Package openaiapi serves the OpenAI-compatible HTTP surface: a developer
// reaches the marketplace by changing `base_url` in the openai SDK and nothing
// else.
//
// The node already SPOKE this protocol - inference.OpenAIBackend POSTs to a
// configurable /v1/chat/completions so a provider can proxy to OpenAI, Together,
// Groq or a local gateway - but nothing SERVED it. Reaching the network meant
// naming a provider id and making two calls (SubmitInferenceJob, then
// FulfillInferenceJob) against a bespoke protocol, which is the difference
// between an API a developer adopts in a minute and one they evaluate and skip.
//
// Three things the marketplace needs that the OpenAI request does not carry:
//
//   - WHO PAYS. OpenAI's API identifies the caller by the API key alone, so the
//     buyer comes from the key (admin.APIKey.Account). The balance lives on the
//     ledger; the key proves which account it may spend from and is not itself a
//     stored credit balance. A key with no account cannot buy inference, which
//     is the safe reading: it can still drive every other surface.
//   - WHICH PROVIDER. The request names a model, so the model is routed to the
//     cheapest provider advertising it that still has capacity
//     (market.ProvidersForModel). Ties break on provider ID, so two identical
//     requests do not scatter at random across equally-priced providers.
//   - HOW MUCH TO RESERVE. The reservation is an upfront estimate; the settled
//     charge is derived from the tokens the backend actually reported. The
//     estimate is deliberately generous, because a reservation that is too small
//     clamps the settlement and would underpay a provider for real work.
//
// Custody: this is the hosted-wallet door, and it should be called that. The
// node settles by signing on the buyer's behalf with a key it already holds
// (node.walletAccounts), exactly as the CLI and the inference RPC do. Nothing
// here can settle for an account whose key the node does not have, and an
// attempt reports an error rather than a silent free run.
package openaiapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/ecirlabs/matrix-core/internal/inference"
	"github.com/ecirlabs/matrix-core/internal/market"
)

// ChatCompletionsPath and ModelsPath are the two routes this package serves.
// They are the paths the openai SDK derives from a base_url, so the protocol
// fixes them rather than config.
const (
	ChatCompletionsPath = "/v1/chat/completions"
	ModelsPath          = "/v1/models"
)

// maxRequestBytes bounds a request body. A chat transcript is larger than an
// account id but not unbounded, and a cap keeps a hostile caller from making the
// node allocate.
const maxRequestBytes = 4 << 20

// unitsEstimateFloor is the smallest reservation the route makes. The settled
// charge is clamped to the reservation, so a floor stops a very short prompt
// from under-reserving and leaving a provider underpaid for a long completion it
// was asked to produce.
const unitsEstimateFloor uint64 = 64

// charsPerEstimatedToken converts a rough character count into the unit
// estimate. It only has to be in the right neighbourhood: it decides the
// reservation, never the price, which comes from the tokens the backend reports.
const charsPerEstimatedToken = 4

// uncappedCompletionReserve is the completion room reserved when a request sets
// no max_tokens, so the length is the backend's choice.
const uncappedCompletionReserve uint64 = 1024

// Inference is the subset of *inference.Service this handler needs. It is an
// interface so a test can drive the route without a consensus engine behind it.
type Inference interface {
	SubmitInferenceJob(buyer, providerID string, req inference.InferenceRequest, unitsEstimate uint64) (*inference.InferenceJob, error)
	FulfillJob(ctx context.Context, jobID string) (*inference.InferenceJob, error)
}

// Streamer is the optional interface an Inference implementation provides when
// it can stream a completion. It is optional so a caller can wire a non-
// streaming inference service and have the route say so, rather than the type
// system forbidding a configuration that is otherwise fine.
type Streamer interface {
	StreamJob(ctx context.Context, jobID string, onChunk inference.ChunkFunc) (*inference.InferenceJob, *inference.StreamResult, error)
}

// Router picks the provider for a model and enumerates what is servable.
//
// ProvidersForModel may return its candidates in any order. The route selects
// among them itself rather than taking the head, so a Router implementation that
// stops sorting cannot silently change who gets paid.
//
// Both methods answer from THIS NODE'S order book. That is the right scope for
// routing, because this node can only fulfil what it serves - but it is the
// wrong scope for answering "what does the marketplace sell", which is what a
// door onto a marketplace is asked. See Directory.
type Router interface {
	ProvidersForModel(model string) []market.Provider
	ListProviders() []market.Provider
}

// RemoteSeller is one seller this node has heard of but does not host.
//
// Spelled out here rather than taken from marketexchange so this package keeps
// depending on nothing but the market vocabulary, and so the fields are exactly
// the ones a caller needs to be sent somewhere useful: who, where, and on what
// terms.
type RemoteSeller struct {
	ProviderID   string
	Endpoint     string
	Models       []string
	PricePerUnit uint64
	Available    uint64
}

// Directory is the optional view of sellers BEYOND this node.
//
// WHY IT EXISTS. The package promise is that a developer reaches the
// marketplace by changing base_url and nothing else. Routing reads the local
// order book, which is correct - this node settles and fulfils, so it can only
// sell what it serves. But the two read-shaped answers were coming from the
// same place, and that made both of them lie on any node that is not itself a
// seller:
//
//   - GET /v1/models returned {"data": []}, which reads as "this network has no
//     models" rather than "this node hosts none". A validator is exactly the
//     address a newcomer is given, and it is exactly the node that hosts none.
//   - A chat request answered "no provider ON THIS NETWORK is serving model X",
//     which was a statement about the network made after looking only at one
//     node's own shelf. It was frequently false.
//
// So the directory is read for the ANSWERS, never for the routing. A model only
// a remote seller has is listed, and named as theirs, with the address to buy it
// from - and a request for it is refused with that address rather than with a
// denial that the model exists.
//
// It is optional because a node with no exchange has no directory to consult,
// and that node's local-only answers are then the whole truth it has.
//
// WHAT THIS IS NOT. It is not brokering. This node does not reserve, fulfil or
// settle on a remote seller's behalf; doing that means becoming an inference
// client of another node, with its own escrow and streaming, and it is the
// larger piece of work this makes the case for rather than does.
type Directory interface {
	RemoteSellers() []RemoteSeller
}

// Authenticator resolves the caller's credential.
type Authenticator interface {
	// AccountFor returns the on-chain account the request's credential spends
	// from. An empty account with a nil error means an authenticated caller whose
	// key names no account, which cannot buy inference.
	AccountFor(r *http.Request) (account string, err error)
}

// Config configures the handler.
type Config struct {
	// Inference runs and settles jobs. Required.
	Inference Inference
	// Router selects a provider by model. Required.
	Router Router
	// Directory, when set, is what this node has heard of the rest of the
	// market. It never routes - see Directory - but without it /v1/models and a
	// routing refusal describe this node's own shelf while sounding like they
	// describe the network. Nil on a node with no exchange, where the local
	// answer is the whole truth available.
	Directory Directory
	// Idempotency, when set, deduplicates requests that carry an
	// Idempotency-Key header so a retried POST is not a second charge. Nil
	// disables it, which is the behaviour this endpoint had.
	Idempotency IdempotencyStore
	// Auth resolves the buyer from the request credential. When nil every
	// request is refused: a route that charges an account cannot fall back to
	// "no auth configured" and guess whose money to spend.
	Auth Authenticator
	// Now is the clock used for the `created` field. Nil means time.Now.
	Now func() time.Time
}

// Handler serves the OpenAI-compatible routes.
type Handler struct {
	cfg Config
}

// NewHandler validates cfg and returns the handler.
func NewHandler(cfg Config) (*Handler, error) {
	if cfg.Inference == nil {
		return nil, errors.New("openaiapi: an inference service is required")
	}
	if cfg.Router == nil {
		return nil, errors.New("openaiapi: a router is required")
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	return &Handler{cfg: cfg}, nil
}

// Routes returns the path -> handler pairs to mount, so the caller can serve
// them on whatever mux already carries its CORS and listener policy.
func (h *Handler) Routes() map[string]http.Handler {
	return map[string]http.Handler{
		ChatCompletionsPath: http.HandlerFunc(h.chatCompletions),
		ModelsPath:          http.HandlerFunc(h.models),
	}
}

// chatRequest is the subset of the OpenAI chat-completions request this route
// reads. Unknown fields are ignored rather than rejected: a client library sends
// parameters we have no provider-independent meaning for (`n`, `stop`,
// `presence_penalty`), and failing the whole request over one of them would
// break a working SDK call for no benefit.
type chatRequest struct {
	Model       string        `json:"model"`
	Messages    []chatMessage `json:"messages"`
	MaxTokens   int           `json:"max_tokens"`
	Temperature float64       `json:"temperature"`
	Stream      bool          `json:"stream"`
}

type chatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type chatResponse struct {
	ID      string       `json:"id"`
	Object  string       `json:"object"`
	Created int64        `json:"created"`
	Model   string       `json:"model"`
	Choices []chatChoice `json:"choices"`
	Usage   chatUsage    `json:"usage"`
	// Provider is not part of OpenAI's schema. It is added because on this
	// network "who served this" is a marketplace fact the caller paid for and
	// can check; an SDK ignores the extra field.
	Provider string `json:"provider,omitempty"`
	// Receipt is the serving node's SIGNED account of what it charged and for
	// what: the model, the token counts, the money, over a digest of this exact
	// prompt and completion.
	//
	// It is here rather than in a header because a buyer who needs it needs to
	// keep it, and the body is what an SDK hands back. An SDK ignores the extra
	// field, so the response stays a valid chat completion for a client that
	// does not care - and a client that does gets evidence it can store, verify
	// offline, and put in front of somebody.
	//
	// Absent when the node holds no signing key. A receipt nobody signed would be
	// a claim with no author, which is what there was before.
	Receipt *inference.Receipt `json:"receipt,omitempty"`
}

type chatChoice struct {
	Index        int         `json:"index"`
	Message      chatMessage `json:"message"`
	FinishReason string      `json:"finish_reason"`
}

type chatUsage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

func (h *Handler) chatCompletions(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "invalid_request_error",
			"this endpoint accepts POST")
		return
	}

	buyer, ok := h.buyer(w, r)
	if !ok {
		return
	}

	var req chatRequest
	if !decodeJSON(w, r, &req) {
		return
	}

	if strings.TrimSpace(req.Model) == "" {
		writeError(w, http.StatusBadRequest, "invalid_request_error",
			"'model' is required: it is what selects a provider on this network")
		return
	}
	if len(req.Messages) == 0 {
		writeError(w, http.StatusBadRequest, "invalid_request_error",
			"'messages' must contain at least one message")
		return
	}

	msgs := make([]inference.Message, 0, len(req.Messages))
	for i, m := range req.Messages {
		role, err := mapRole(m.Role)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid_request_error",
				fmt.Sprintf("messages[%d]: %v", i, err))
			return
		}
		msgs = append(msgs, inference.Message{Role: role, Content: m.Content})
	}

	// Resolved before a provider is chosen, so a duplicate never reserves anyone's
	// capacity.
	guard, ok := h.beginIdempotent(w, r, buyer,
		requestFingerprint(req.Model, msgs, req.MaxTokens, req.Temperature))
	if !ok {
		return
	}

	candidates := h.cfg.Router.ProvidersForModel(req.Model)
	if len(candidates) == 0 {
		guard.release()
		// 404 naming the model, which is what OpenAI answers for an unknown model
		// and what a client library reports usefully. What the message SAYS
		// depends on whether the model exists elsewhere, because the old wording
		// - "no provider on this network is serving" - was a claim about the
		// network made after reading one node's own shelf, and it was wrong
		// exactly when it mattered: a caller pointed at a validator, asking for a
		// model a real seller was serving that minute.
		writeError(w, http.StatusNotFound, "invalid_request_error", h.unroutable(req.Model))
		return
	}
	provider := cheapest(candidates)

	if req.Stream {
		// From here the response is server-sent events, and a failure after the
		// first frame cannot be an HTTP status. See stream.go.
		h.streamChatCompletions(w, r, buyer, req, msgs, provider.ID, guard)
		return
	}

	job, err := h.cfg.Inference.SubmitInferenceJob(buyer, provider.ID, inference.InferenceRequest{
		Model:       req.Model,
		Messages:    msgs,
		MaxTokens:   req.MaxTokens,
		Temperature: req.Temperature,
	}, estimateUnits(msgs, req.MaxTokens))
	if err != nil {
		guard.release()
		status, kind := classify(err)
		writeError(w, status, kind, err.Error())
		return
	}

	done, err := h.cfg.Inference.FulfillJob(r.Context(), job.ID)
	if err != nil {
		// A failed job charged nobody, so the key is released and a retry under it
		// is what the buyer wants. Keeping it would turn one transport error into
		// a permanently unusable key.
		guard.release()
		status, kind := classify(err)
		writeError(w, status, kind, err.Error())
		return
	}
	guard.complete(done.ID)

	model := done.Model
	if model == "" {
		model = req.Model
	}
	writeJSON(w, http.StatusOK, chatResponse{
		ID:      "chatcmpl-" + done.ID,
		Object:  "chat.completion",
		Created: h.cfg.Now().UTC().Unix(),
		Model:   model,
		Choices: []chatChoice{{
			Index:        0,
			Message:      chatMessage{Role: string(inference.RoleAssistant), Content: done.Completion},
			FinishReason: "stop",
		}},
		Usage: chatUsage{
			PromptTokens:     done.Usage.PromptTokens,
			CompletionTokens: done.Usage.CompletionTokens,
			TotalTokens:      done.Usage.TotalTokens,
		},
		Provider: done.Provider,
		Receipt:  done.Receipt,
	})
}

type modelsResponse struct {
	Object string      `json:"object"`
	Data   []modelInfo `json:"data"`
}

type modelInfo struct {
	ID      string `json:"id"`
	Object  string `json:"object"`
	OwnedBy string `json:"owned_by"`
	// Extra, marketplace-specific fields an SDK ignores. They are what a caller
	// choosing a model on a market actually wants to see.
	Providers    int    `json:"providers"`
	PricePerUnit uint64 `json:"price_per_unit"`
	// Servable says whether THIS node can fulfil this model, and Endpoint names
	// where to go when it cannot.
	//
	// A model nobody here hosts is listed rather than hidden, because hiding it
	// answers "the network has no such model" to a question that was about the
	// network. Listing it without saying where to buy it would be worse than
	// hiding it - a name that 404s on use is a dead end - so the two fields
	// always travel together.
	Servable bool   `json:"servable"`
	Endpoint string `json:"endpoint,omitempty"`
}

// unroutable explains why a model cannot be served HERE, and where it can be.
//
// The distinction this draws is the whole point. "Nobody sells this" and "you
// asked the wrong node" are different facts with different remedies, and
// answering the first when the second is true sends a developer away believing
// the marketplace is empty. When a seller has it, the message is their address,
// because that is the entire remaining step.
func (h *Handler) unroutable(model string) string {
	if h.cfg.Directory != nil {
		var (
			endpoint string
			price    uint64
			sellers  int
		)
		for _, seller := range h.cfg.Directory.RemoteSellers() {
			if seller.Endpoint == "" || seller.Available == 0 || !servesModel(seller, model) {
				continue
			}
			sellers++
			if endpoint == "" || seller.PricePerUnit < price {
				endpoint, price = seller.Endpoint, seller.PricePerUnit
			}
		}
		if endpoint != "" {
			return fmt.Sprintf(
				"this node does not serve model %q, but %d seller(s) on the network do. "+
					"Point base_url at %s, which is the cheapest of them at %d base units per unit. "+
					"This node can tell you who is selling; it cannot buy on your behalf.",
				model, sellers, endpoint, price)
		}
	}
	return fmt.Sprintf("no seller this node has heard of is serving model %q with capacity to spare", model)
}

// servesModel matches a seller's advertisement the way the order book does:
// model names are case-insensitive across the vendors we proxy, so "Qwen3-32B"
// and "qwen3-32b" must not be two different routing targets.
func servesModel(seller RemoteSeller, model string) bool {
	want := market.NormalizeModel(model)
	for _, m := range seller.Models {
		if market.NormalizeModel(m) == want {
			return true
		}
	}
	return false
}

// models answers /v1/models with the distinct models THE MARKET advertises, each
// with how many sellers serve it, the cheapest price among them, and whether
// this node is one of them.
//
// A developer calling client.models.list() is asking what this network can do.
// It used to answer from the local order book alone, so on any node that is not
// itself a seller it returned an empty list - and a validator is both the
// address a newcomer is given and the node that hosts nothing. "This network has
// no models" is a very discouraging answer to be given wrongly, and it is
// indistinguishable from the true one.
func (h *Handler) models(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "invalid_request_error",
			"this endpoint accepts GET")
		return
	}
	// Listing what a network serves is not a spend, but it does describe the
	// operator's providers, so it sits behind the same credential as the rest of
	// this surface.
	if _, ok := h.buyerOptional(w, r); !ok {
		return
	}

	type agg struct {
		providers int
		cheapest  uint64
		// Set by a local provider only. It decides whether a request for this
		// model can be served here or has to be sent elsewhere.
		servable bool
		// The cheapest REMOTE seller's address, which is where a caller is sent
		// for a model this node cannot serve. Cheapest so the advice matches the
		// choice the router would make if it could.
		endpoint      string
		endpointPrice uint64
	}
	byModel := map[string]*agg{}
	see := func(model string, price uint64, local bool, endpoint string) {
		a, ok := byModel[model]
		if !ok {
			a = &agg{cheapest: price}
			byModel[model] = a
		}
		a.providers++
		if price < a.cheapest {
			a.cheapest = price
		}
		if local {
			a.servable = true
			return
		}
		if endpoint != "" && (a.endpoint == "" || price < a.endpointPrice) {
			a.endpoint, a.endpointPrice = endpoint, price
		}
	}

	for _, p := range h.cfg.Router.ListProviders() {
		for _, m := range p.Models {
			see(m, p.PricePerUnit, true, "")
		}
	}
	// Then the rest of the market, which this node can describe but not fulfil.
	// A seller with no endpoint to publish is skipped rather than listed as
	// unreachable: naming a model a caller has no way to buy is the dead end
	// this is here to remove.
	if h.cfg.Directory != nil {
		for _, seller := range h.cfg.Directory.RemoteSellers() {
			if seller.Endpoint == "" || seller.Available == 0 {
				continue
			}
			for _, m := range seller.Models {
				see(m, seller.PricePerUnit, false, seller.Endpoint)
			}
		}
	}

	ids := make([]string, 0, len(byModel))
	for m := range byModel {
		ids = append(ids, m)
	}
	sort.Strings(ids)

	data := make([]modelInfo, 0, len(ids))
	for _, id := range ids {
		a := byModel[id]
		info := modelInfo{
			ID:           id,
			Object:       "model",
			OwnedBy:      "matrix-marketplace",
			Providers:    a.providers,
			PricePerUnit: a.cheapest,
			Servable:     a.servable,
		}
		if !a.servable {
			info.Endpoint = a.endpoint
		}
		data = append(data, info)
	}
	writeJSON(w, http.StatusOK, modelsResponse{Object: "list", Data: data})
}

// buyer resolves the account to charge, writing the error response itself and
// returning ok=false when it cannot.
func (h *Handler) buyer(w http.ResponseWriter, r *http.Request) (string, bool) {
	account, ok := h.buyerOptional(w, r)
	if !ok {
		return "", false
	}
	if account == "" {
		// An authenticated key with no account. Refusing is the safe reading:
		// the alternative is guessing whose balance to spend.
		writeError(w, http.StatusForbidden, "invalid_request_error",
			"this api key is not tied to an account, so there is no balance to charge; "+
				"set `account` on the key under security.api_keys")
		return "", false
	}
	return account, true
}

func (h *Handler) buyerOptional(w http.ResponseWriter, r *http.Request) (string, bool) {
	if h.cfg.Auth == nil {
		writeError(w, http.StatusUnauthorized, "invalid_request_error",
			"this endpoint requires an api key and none is configured on this node")
		return "", false
	}
	account, err := h.cfg.Auth.AccountFor(r)
	if err != nil {
		writeError(w, http.StatusUnauthorized, "invalid_request_error", "invalid api key")
		return "", false
	}
	return account, true
}

// estimateUnits turns the request into the upfront reservation. It is
// deliberately generous: the settled charge is derived from the tokens the
// backend reported and clamped to this reservation, so over-reserving costs the
// buyer nothing while under-reserving underpays the provider for real work.
func estimateUnits(msgs []inference.Message, maxTokens int) uint64 {
	chars := 0
	for _, m := range msgs {
		chars += len(m.Content)
	}
	est := uint64(chars / charsPerEstimatedToken)
	if maxTokens > 0 {
		est += uint64(maxTokens)
	} else {
		est += uncappedCompletionReserve
	}
	if est < unitsEstimateFloor {
		est = unitsEstimateFloor
	}
	return est
}

// cheapest picks the provider a request is routed to: lowest price per unit,
// breaking ties on ID so two identical requests do not scatter at random across
// equally-priced providers. Callers get the same answer from the same order
// book, which is what makes a bill explainable.
func cheapest(candidates []market.Provider) market.Provider {
	best := candidates[0]
	for _, p := range candidates[1:] {
		if p.PricePerUnit < best.PricePerUnit ||
			(p.PricePerUnit == best.PricePerUnit && p.ID < best.ID) {
			best = p
		}
	}
	return best
}

func mapRole(role string) (inference.Role, error) {
	switch strings.ToLower(strings.TrimSpace(role)) {
	case string(inference.RoleSystem):
		return inference.RoleSystem, nil
	case string(inference.RoleUser), "":
		// An absent role reads as "user", which is what a minimal hand-written
		// request means and what every SDK sends anyway.
		return inference.RoleUser, nil
	case string(inference.RoleAssistant):
		return inference.RoleAssistant, nil
	case "tool", "function", "developer":
		return "", fmt.Errorf("role %q is not supported on this network yet", role)
	default:
		return "", fmt.Errorf("unknown role %q", role)
	}
}

// classify maps an inference/market error onto the HTTP status and OpenAI error
// type a client library branches on. Anything unrecognised is a 500, because
// reporting an internal failure as a client error sends a caller looking for a
// bug in their own request.
func classify(err error) (int, string) {
	switch {
	case errors.Is(err, market.ErrProviderNotFound), errors.Is(err, inference.ErrJobNotFound):
		return http.StatusNotFound, "invalid_request_error"
	case errors.Is(err, market.ErrInsufficientFunds):
		// OpenAI's own code for "you are out of money", which an SDK surfaces as
		// a billing problem rather than a bad request.
		return http.StatusPaymentRequired, "insufficient_quota"
	case errors.Is(err, market.ErrInsufficientCapacity):
		return http.StatusServiceUnavailable, "server_error"
	case errors.Is(err, inference.ErrEmptyPrompt):
		return http.StatusBadRequest, "invalid_request_error"
	case errors.Is(err, inference.ErrNoBackend), errors.Is(err, inference.ErrBackendNotFound):
		return http.StatusServiceUnavailable, "server_error"
	default:
		return http.StatusInternalServerError, "server_error"
	}
}

func decodeJSON(w http.ResponseWriter, r *http.Request, into any) bool {
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxRequestBytes))
	if err != nil {
		writeError(w, http.StatusRequestEntityTooLarge, "invalid_request_error",
			"request body is too large")
		return false
	}
	if err := json.Unmarshal(body, into); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request_error",
			"request body is not valid JSON: "+err.Error())
		return false
	}
	return true
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

// errorEnvelope is OpenAI's error shape. Client libraries read `error.message`
// and `error.type`, so an error outside this envelope surfaces to a developer as
// an unhelpful "unknown error".
type errorEnvelope struct {
	Error errorBody `json:"error"`
}

type errorBody struct {
	Message string `json:"message"`
	Type    string `json:"type"`
}

func writeError(w http.ResponseWriter, status int, kind, message string) {
	writeJSON(w, status, errorEnvelope{Error: errorBody{Message: message, Type: kind}})
}
