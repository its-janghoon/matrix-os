// Package ethrpc serves the subset of Ethereum's JSON-RPC that a wallet needs,
// so this chain can be added to MetaMask as a network.
//
// The chain already let an Ethereum key CONTROL an account and sign for it. What
// it had no answer for was the other half: a wallet cannot be told to sign
// something, it has to discover the chain by asking it questions, in a protocol
// it already speaks, before it will show a balance or offer to send.
//
// WHAT IS SERVED AND WHAT IS NOT. Everything here reports on state this chain
// actually has. Methods that would require an EVM - eth_call, and any contract
// interaction - return an error saying so rather than an empty result, because
// an empty result reads to a wallet as "the call succeeded and returned
// nothing", and it will act on that.
//
// GAS IS REPORTED AS FREE BECAUSE IT IS. There is no EVM to meter and no
// gas-denominated fee, so eth_gasPrice answers zero. That is honest about gas
// and it is NOT the whole cost of a transfer: this chain's protocol fee is a
// percentage of the VALUE moved, taken at settlement, and Ethereum's fee model
// has nowhere to express that. A wallet will therefore show a transfer as
// costing nothing when it costs the fee. Closing that gap means either moving to
// a gas-denominated fee or surfacing the value fee somewhere the wallet is not;
// it is a live design decision and not something this package can paper over.
package ethrpc

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"strconv"
	"strings"

	"github.com/ecirlabs/matrix-core/internal/ethsig"
	"github.com/ecirlabs/matrix-core/internal/token"
)

// BlockView is one committed block, in the shape this package reports it.
//
// It is declared here rather than reusing the consensus type so the package
// depends on nothing but the ledger's vocabulary, and so the node's adapter is
// the one place that knows how the two are related.
type BlockView struct {
	Height     uint64
	Hash       []byte
	ParentHash []byte
	Timestamp  int64
	ProposerID string
	Txs        []token.Transaction
}

// TxLookup is the answer to "where is this transaction".
type TxLookup struct {
	// Tx is the transaction itself.
	Tx *token.Transaction
	// Committed is false for one still in the mempool.
	Committed bool
	// Height and Index locate a committed transaction.
	Height uint64
	Index  int
	// Applied reports whether a committed transfer moved credits. Committed is
	// not paid: an unaffordable transfer is ordered by the quorum and then
	// deterministically skipped, and a receipt must not call that a success.
	Applied bool
	// AppliedKnown is false when this node cannot say either way.
	AppliedKnown bool
}

// Backend is the chain, as this package needs it.
type Backend interface {
	// ChainHeight returns the number of committed blocks.
	ChainHeight() uint64
	// Balance returns an account's balance in native base units.
	Balance(accountID string) (uint64, error)
	// NextNonce returns the nonce a sender should use next.
	NextNonce(accountID string, pending bool) uint64
	// SubmitRaw admits a wallet-signed envelope and returns its transaction id.
	SubmitRaw(raw []byte) ([]byte, error)
	// BlockByHeight returns a committed block.
	BlockByHeight(height uint64) (*BlockView, bool)
	// BlockByHash returns a committed block by its hash.
	BlockByHash(hash []byte) (*BlockView, bool)
	// TransactionByHash finds a transaction by its Ethereum id, given as
	// lowercase hex without a 0x prefix.
	TransactionByHash(hashHex string) (TxLookup, bool)
}

// Config configures the handler.
type Config struct {
	// ChainID is this chain's EIP-155 id. Required: a wallet compares what it
	// was configured with against eth_chainId and refuses to send on a mismatch,
	// which is the check that stops a user broadcasting to the wrong network.
	ChainID uint64
	// Backend is the chain. Required.
	Backend Backend
	// ClientVersion is reported by web3_clientVersion.
	ClientVersion string
}

// Handler serves JSON-RPC over HTTP.
type Handler struct {
	cfg Config
}

// NewHandler builds the handler.
func NewHandler(cfg Config) (*Handler, error) {
	if cfg.ChainID == 0 {
		return nil, fmt.Errorf("ethrpc: a chain id is required; zero would let a wallet " +
			"broadcast here a transaction it signed for another network")
	}
	if cfg.Backend == nil {
		return nil, fmt.Errorf("ethrpc: a backend is required")
	}
	if cfg.ClientVersion == "" {
		cfg.ClientVersion = "matrix-os"
	}
	return &Handler{cfg: cfg}, nil
}

// JSON-RPC 2.0 envelopes.
type rpcRequest struct {
	JSONRPC string            `json:"jsonrpc"`
	ID      json.RawMessage   `json:"id"`
	Method  string            `json:"method"`
	Params  []json.RawMessage `json:"params"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type rpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  any             `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

// JSON-RPC error codes. The values are the ones the specification assigns, so a
// wallet's own error handling recognises them.
const (
	codeParse          = -32700
	codeInvalidRequest = -32600
	codeMethodNotFound = -32601
	codeInvalidParams  = -32602
	codeInternal       = -32603
)

// maxRequestBytes bounds a request body. A batch of wallet polls is kilobytes.
const maxRequestBytes = 1 << 20

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "this endpoint serves JSON-RPC over POST", http.StatusMethodNotAllowed)
		return
	}
	body := http.MaxBytesReader(w, r.Body, maxRequestBytes)
	raw, err := io.ReadAll(body)
	if err != nil {
		writeJSON(w, rpcResponse{JSONRPC: "2.0", Error: &rpcError{Code: codeParse, Message: err.Error()}})
		return
	}

	// A wallet batches its polls, so both shapes have to work. The reply shape
	// must match the request shape: answering a batch with a single object makes
	// the client discard every response in it.
	trimmed := strings.TrimSpace(string(raw))
	if strings.HasPrefix(trimmed, "[") {
		var batch []rpcRequest
		if err := json.Unmarshal(raw, &batch); err != nil {
			writeJSON(w, rpcResponse{JSONRPC: "2.0", Error: &rpcError{Code: codeParse, Message: "malformed batch"}})
			return
		}
		out := make([]rpcResponse, 0, len(batch))
		for i := range batch {
			out = append(out, h.dispatch(&batch[i]))
		}
		writeJSON(w, out)
		return
	}

	var req rpcRequest
	if err := json.Unmarshal(raw, &req); err != nil {
		writeJSON(w, rpcResponse{JSONRPC: "2.0", Error: &rpcError{Code: codeParse, Message: "malformed request"}})
		return
	}
	writeJSON(w, h.dispatch(&req))
}

// dispatch routes one request.
func (h *Handler) dispatch(req *rpcRequest) rpcResponse {
	resp := rpcResponse{JSONRPC: "2.0", ID: req.ID}
	if req.Method == "" {
		resp.Error = &rpcError{Code: codeInvalidRequest, Message: "no method"}
		return resp
	}

	result, rpcErr := h.call(req.Method, req.Params)
	if rpcErr != nil {
		resp.Error = rpcErr
		return resp
	}
	resp.Result = result
	return resp
}

// call executes one method.
func (h *Handler) call(method string, params []json.RawMessage) (any, *rpcError) {
	switch method {
	case "web3_clientVersion":
		return h.cfg.ClientVersion, nil
	case "net_version":
		// Decimal, not hex. net_version predates eth_chainId and a wallet that
		// falls back to it parses the answer as a decimal string.
		return strconv.FormatUint(h.cfg.ChainID, 10), nil
	case "net_listening":
		return true, nil
	case "eth_chainId":
		return hexUint(h.cfg.ChainID), nil
	case "eth_syncing":
		return false, nil
	case "eth_accounts":
		// This node holds no wallet keys for RPC callers, and saying otherwise
		// would have a wallet offer accounts it cannot sign for.
		return []string{}, nil
	case "eth_blockNumber":
		return h.blockNumber(), nil
	case "eth_gasPrice", "eth_maxPriorityFeePerGas":
		// Zero, honestly: see the package comment. There is no gas to meter.
		return "0x0", nil
	case "eth_estimateGas":
		// The conventional figure for a plain transfer. It is carried in the
		// envelope because a wallet puts it there, and nothing meters it.
		return "0x5208", nil
	case "eth_getCode":
		// No EVM, so no account has code. "0x" is the correct answer for an
		// ordinary account on Ethereum too, and it is what stops a wallet from
		// treating an address as a contract.
		return "0x", nil
	case "eth_getBalance":
		return h.getBalance(params)
	case "eth_getTransactionCount":
		return h.getTransactionCount(params)
	case "eth_sendRawTransaction":
		return h.sendRawTransaction(params)
	case "eth_getTransactionByHash":
		return h.getTransactionByHash(params)
	case "eth_getTransactionReceipt":
		return h.getTransactionReceipt(params)
	case "eth_getBlockByNumber":
		return h.getBlockByNumber(params)
	case "eth_getBlockByHash":
		return h.getBlockByHash(params)
	case "eth_call":
		return nil, &rpcError{Code: codeMethodNotFound,
			Message: "eth_call is not available: this chain runs no EVM, so there is no contract to call"}
	default:
		if strings.HasPrefix(method, "matrix_") {
			return h.callMatrix(method, params)
		}
		return nil, &rpcError{Code: codeMethodNotFound, Message: "unsupported method " + method}
	}
}

// blockNumber reports the latest committed block number. ChainHeight is the
// NUMBER of blocks, so the latest number is one less; a chain with no blocks has
// no latest block and reports 0, which is what a wallet polling an empty chain
// can act on.
func (h *Handler) blockNumber() string {
	height := h.cfg.Backend.ChainHeight()
	if height == 0 {
		return "0x0"
	}
	return hexUint(height - 1)
}

func (h *Handler) getBalance(params []json.RawMessage) (any, *rpcError) {
	addr, rpcErr := addressParam(params, 0)
	if rpcErr != nil {
		return nil, rpcErr
	}
	balance, err := h.cfg.Backend.Balance(token.EthAccountID(addr))
	if err != nil {
		return nil, &rpcError{Code: codeInternal, Message: err.Error()}
	}
	// Reported in the wallet's 18-decimal scale, which is the scale it will
	// divide by to show a figure. Returning native base units would show a
	// balance a billion times too small.
	return hexBig(token.NativeToERC20(balance)), nil
}

func (h *Handler) getTransactionCount(params []json.RawMessage) (any, *rpcError) {
	addr, rpcErr := addressParam(params, 0)
	if rpcErr != nil {
		return nil, rpcErr
	}
	// "pending" is what a wallet asks for before it signs, and it has to include
	// this node's mempool or two transfers sent seconds apart collide on one
	// nonce. Any other tag is answered from committed state.
	pending := blockTagParam(params, 1) == "pending"
	return hexUint(h.cfg.Backend.NextNonce(token.EthAccountID(addr), pending)), nil
}

func (h *Handler) sendRawTransaction(params []json.RawMessage) (any, *rpcError) {
	if len(params) < 1 {
		return nil, &rpcError{Code: codeInvalidParams, Message: "eth_sendRawTransaction needs the signed transaction"}
	}
	var encoded string
	if err := json.Unmarshal(params[0], &encoded); err != nil {
		return nil, &rpcError{Code: codeInvalidParams, Message: "the signed transaction must be a hex string"}
	}
	raw, err := hex.DecodeString(strings.TrimPrefix(strings.TrimPrefix(encoded, "0x"), "0X"))
	if err != nil {
		return nil, &rpcError{Code: codeInvalidParams, Message: "the signed transaction is not valid hex"}
	}

	hash, err := h.cfg.Backend.SubmitRaw(raw)
	if err != nil {
		// Returned as an error rather than a null id. A wallet handed an id for a
		// transaction that was never admitted shows it pending forever.
		return nil, &rpcError{Code: codeInvalidParams, Message: err.Error()}
	}
	return "0x" + hex.EncodeToString(hash), nil
}

func (h *Handler) getTransactionByHash(params []json.RawMessage) (any, *rpcError) {
	found, lookup, rpcErr := h.lookup(params)
	if rpcErr != nil {
		return nil, rpcErr
	}
	if !found {
		// JSON null, which is how Ethereum says "not found" here. An error would
		// have a wallet stop polling.
		return nil, nil
	}
	return h.transactionView(lookup), nil
}

func (h *Handler) getTransactionReceipt(params []json.RawMessage) (any, *rpcError) {
	found, lookup, rpcErr := h.lookup(params)
	if rpcErr != nil {
		return nil, rpcErr
	}
	// A receipt exists only once the transaction is in a block. Reporting one for
	// a mempool transaction would tell a wallet, and an exchange, that a transfer
	// had settled while it was still only proposed.
	if !found || !lookup.Committed {
		return nil, nil
	}

	block, ok := h.cfg.Backend.BlockByHeight(lookup.Height)
	if !ok {
		return nil, nil
	}
	hash, err := lookup.Tx.EVMHash()
	if err != nil {
		return nil, &rpcError{Code: codeInternal, Message: err.Error()}
	}

	// status: committed is NOT paid. An unaffordable transfer is ordered by the
	// quorum and then deterministically skipped by every node, and calling that a
	// success is how an exchange credits a deposit that never arrived. A node
	// that cannot say either way - one that restarted since - reports failure
	// rather than guessing success.
	status := "0x0"
	if lookup.Applied && lookup.AppliedKnown {
		status = "0x1"
	}

	return map[string]any{
		"transactionHash":   "0x" + hex.EncodeToString(hash),
		"transactionIndex":  hexUint(uint64(lookup.Index)),
		"blockHash":         "0x" + hex.EncodeToString(block.Hash),
		"blockNumber":       hexUint(block.Height),
		"from":              senderAddressHex(lookup.Tx),
		"to":                recipientAddressHex(lookup.Tx),
		"cumulativeGasUsed": "0x5208",
		"gasUsed":           "0x5208",
		"effectiveGasPrice": "0x0",
		"contractAddress":   nil,
		"logs":              []any{},
		"logsBloom":         "0x" + strings.Repeat("00", 256),
		"type":              "0x0",
		"status":            status,
	}, nil
}

// lookup resolves the hash parameter shared by the two transaction reads.
func (h *Handler) lookup(params []json.RawMessage) (bool, TxLookup, *rpcError) {
	if len(params) < 1 {
		return false, TxLookup{}, &rpcError{Code: codeInvalidParams, Message: "a transaction hash is required"}
	}
	var encoded string
	if err := json.Unmarshal(params[0], &encoded); err != nil {
		return false, TxLookup{}, &rpcError{Code: codeInvalidParams, Message: "the transaction hash must be a hex string"}
	}
	normalized := strings.ToLower(strings.TrimPrefix(strings.TrimPrefix(encoded, "0x"), "0X"))
	if len(normalized) != 64 {
		return false, TxLookup{}, &rpcError{Code: codeInvalidParams, Message: "a transaction hash is 32 bytes"}
	}
	lookup, ok := h.cfg.Backend.TransactionByHash(normalized)
	return ok, lookup, nil
}

// transactionView renders a transaction the way a wallet expects to read one.
func (h *Handler) transactionView(lookup TxLookup) map[string]any {
	hash, _ := lookup.Tx.EVMHash()
	view := map[string]any{
		"hash":     "0x" + hex.EncodeToString(hash),
		"nonce":    hexUint(lookup.Tx.Nonce),
		"from":     senderAddressHex(lookup.Tx),
		"to":       recipientAddressHex(lookup.Tx),
		"value":    hexBig(token.NativeToERC20(lookup.Tx.Amount)),
		"gas":      "0x5208",
		"gasPrice": "0x0",
		"input":    "0x",
		"chainId":  hexUint(h.cfg.ChainID),
	}
	// Null block fields are how Ethereum marks a pending transaction, and a
	// wallet reads them to decide whether to keep polling.
	if !lookup.Committed {
		view["blockHash"] = nil
		view["blockNumber"] = nil
		view["transactionIndex"] = nil
		return view
	}
	view["transactionIndex"] = hexUint(uint64(lookup.Index))
	view["blockNumber"] = hexUint(lookup.Height)
	if block, ok := h.cfg.Backend.BlockByHeight(lookup.Height); ok {
		view["blockHash"] = "0x" + hex.EncodeToString(block.Hash)
	} else {
		view["blockHash"] = nil
	}
	return view
}

func (h *Handler) getBlockByNumber(params []json.RawMessage) (any, *rpcError) {
	if len(params) < 1 {
		return nil, &rpcError{Code: codeInvalidParams, Message: "a block number or tag is required"}
	}
	var tag string
	if err := json.Unmarshal(params[0], &tag); err != nil {
		return nil, &rpcError{Code: codeInvalidParams, Message: "the block number must be a quantity or a tag"}
	}

	height := h.cfg.Backend.ChainHeight()
	var want uint64
	switch strings.ToLower(tag) {
	case "latest", "pending", "safe", "finalized":
		// Every tag but "pending" means the same block here: a committed block is
		// final the moment a quorum commits it, so there is no reorg window for
		// "safe" and "finalized" to be behind. "pending" answers with the latest
		// committed block rather than a speculative one, because this chain does
		// not build a pending block a caller could read.
		if height == 0 {
			return nil, nil
		}
		want = height - 1
	case "earliest":
		want = 0
	default:
		parsed, err := parseHexUint(tag)
		if err != nil {
			return nil, &rpcError{Code: codeInvalidParams, Message: err.Error()}
		}
		want = parsed
	}

	block, ok := h.cfg.Backend.BlockByHeight(want)
	if !ok {
		return nil, nil
	}
	return h.blockView(block, fullTxParam(params, 1)), nil
}

func (h *Handler) getBlockByHash(params []json.RawMessage) (any, *rpcError) {
	if len(params) < 1 {
		return nil, &rpcError{Code: codeInvalidParams, Message: "a block hash is required"}
	}
	var encoded string
	if err := json.Unmarshal(params[0], &encoded); err != nil {
		return nil, &rpcError{Code: codeInvalidParams, Message: "the block hash must be a hex string"}
	}
	raw, err := hex.DecodeString(strings.ToLower(strings.TrimPrefix(strings.TrimPrefix(encoded, "0x"), "0X")))
	if err != nil {
		return nil, &rpcError{Code: codeInvalidParams, Message: "the block hash is not valid hex"}
	}
	block, ok := h.cfg.Backend.BlockByHash(raw)
	if !ok {
		return nil, nil
	}
	return h.blockView(block, fullTxParam(params, 1)), nil
}

// blockView renders a block. Fields Ethereum defines but this chain has no
// equivalent for are reported as their zero value rather than omitted, because a
// wallet indexes into them without checking.
func (h *Handler) blockView(block *BlockView, full bool) map[string]any {
	txs := make([]any, 0, len(block.Txs))
	for i := range block.Txs {
		tx := block.Txs[i]
		if !tx.IsEVM() {
			// A transaction this chain has but Ethereum has no shape for - an
			// ed25519 transfer, a bond, a set change - is left out rather than
			// rendered with a fabricated hash. It is still in the block; it is
			// simply not something this protocol can describe.
			continue
		}
		hash, err := tx.EVMHash()
		if err != nil {
			continue
		}
		if !full {
			txs = append(txs, "0x"+hex.EncodeToString(hash))
			continue
		}
		txs = append(txs, h.transactionView(TxLookup{
			Tx: &tx, Committed: true, Height: block.Height, Index: i,
		}))
	}

	return map[string]any{
		"number":           hexUint(block.Height),
		"hash":             "0x" + hex.EncodeToString(block.Hash),
		"parentHash":       "0x" + hex.EncodeToString(block.ParentHash),
		"timestamp":        hexUint(uint64(block.Timestamp)),
		"transactions":     txs,
		"gasLimit":         "0x0",
		"gasUsed":          "0x0",
		"difficulty":       "0x0",
		"totalDifficulty":  "0x0",
		"size":             "0x0",
		"extraData":        "0x",
		"miner":            "0x0000000000000000000000000000000000000000",
		"nonce":            "0x0000000000000000",
		"sha3Uncles":       "0x" + strings.Repeat("00", 32),
		"logsBloom":        "0x" + strings.Repeat("00", 256),
		"transactionsRoot": "0x" + strings.Repeat("00", 32),
		"stateRoot":        "0x" + strings.Repeat("00", 32),
		"receiptsRoot":     "0x" + strings.Repeat("00", 32),
		"uncles":           []any{},
	}
}

// senderAddressHex renders a transaction's sender as an address, or null when it
// is not an Ethereum-controlled account.
func senderAddressHex(tx *token.Transaction) any {
	if !tx.SenderIsEth() {
		return nil
	}
	addr, err := ethsig.AddressFromBytes(tx.From)
	if err != nil {
		return nil
	}
	return addr.Hex()
}

// recipientAddressHex renders a transaction's recipient as an address.
func recipientAddressHex(tx *token.Transaction) any {
	if !token.IsEthAccountID(tx.To) {
		return nil
	}
	addr, err := token.ParseEthAccountID(tx.To)
	if err != nil {
		return nil
	}
	return addr.Hex()
}

// addressParam reads an address at the given parameter position.
func addressParam(params []json.RawMessage, i int) (ethsig.Address, *rpcError) {
	var zero ethsig.Address
	if len(params) <= i {
		return zero, &rpcError{Code: codeInvalidParams, Message: "an address is required"}
	}
	var s string
	if err := json.Unmarshal(params[i], &s); err != nil {
		return zero, &rpcError{Code: codeInvalidParams, Message: "the address must be a hex string"}
	}
	// ParseAddress, not the checksummed parser: every client sends lowercase
	// here, and requiring a checksum machine-to-machine would reject them all.
	// The checksum belongs where a PERSON types an address.
	addr, err := ethsig.ParseAddress(s)
	if err != nil {
		return zero, &rpcError{Code: codeInvalidParams, Message: err.Error()}
	}
	return addr, nil
}

// blockTagParam reads an optional block tag.
func blockTagParam(params []json.RawMessage, i int) string {
	if len(params) <= i {
		return ""
	}
	var s string
	if err := json.Unmarshal(params[i], &s); err != nil {
		return ""
	}
	return strings.ToLower(s)
}

// fullTxParam reads the "include full transactions" flag.
func fullTxParam(params []json.RawMessage, i int) bool {
	if len(params) <= i {
		return false
	}
	var b bool
	if err := json.Unmarshal(params[i], &b); err != nil {
		return false
	}
	return b
}

// hexUint renders a quantity the way Ethereum's JSON-RPC requires: 0x-prefixed,
// minimal digits, and "0x0" for zero rather than "0x" or "0x00".
func hexUint(v uint64) string { return "0x" + strconv.FormatUint(v, 16) }

// hexBig renders a big quantity in the same minimal form.
func hexBig(v *big.Int) string {
	if v == nil || v.Sign() == 0 {
		return "0x0"
	}
	return "0x" + v.Text(16)
}

// parseHexUint reads a 0x-prefixed quantity.
func parseHexUint(s string) (uint64, error) {
	trimmed := strings.TrimPrefix(strings.TrimPrefix(strings.TrimSpace(s), "0x"), "0X")
	if trimmed == "" {
		return 0, fmt.Errorf("%q is not a quantity", s)
	}
	v, err := strconv.ParseUint(trimmed, 16, 64)
	if err != nil {
		return 0, fmt.Errorf("%q is not a quantity", s)
	}
	return v, nil
}

func writeJSON(w http.ResponseWriter, payload any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(payload)
}
