package bridge

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math/big"
	"net/http"
	"strings"
	"sync/atomic"
	"time"
)

// This file adds a tiny, dependency-free Ethereum JSON-RPC client used by the
// burn->unlock Watcher to pull WrappedMatrix `Burned` logs off an Ethereum
// endpoint (a local hardhat node in tests, any JSON-RPC provider in production).
// It intentionally uses only net/http + encoding/json so the bridge keeps its
// stdlib-only footprint (see doc.go); it is NOT a general Ethereum client, only
// the two calls the watcher needs: eth_blockNumber and eth_getLogs.

// EthClient is the minimal Ethereum JSON-RPC surface the Watcher depends on. It
// is an interface so tests can drive the Watcher with a fake and production can
// use the HTTP client, without either importing a heavy Ethereum SDK.
type EthClient interface {
	// BlockNumber returns the latest block height.
	BlockNumber(ctx context.Context) (uint64, error)
	// FilterBurnedLogs returns the WrappedMatrix Burned logs emitted by `contract`
	// in the inclusive block range [from,to]. The returned logs are already
	// filtered to topics[0] == Burned event signature.
	FilterBurnedLogs(ctx context.Context, contract Address, from, to uint64) ([]EthLog, error)
}

// HTTPEthClient is an EthClient backed by an Ethereum JSON-RPC HTTP endpoint. It
// is safe for concurrent use.
type HTTPEthClient struct {
	url    string
	client *http.Client
	id     atomic.Uint64
}

// NewHTTPEthClient builds an HTTP JSON-RPC client for the given endpoint URL
// (e.g. http://127.0.0.1:8545 for a local hardhat node). A nil httpClient
// defaults to a client with a 30s timeout.
func NewHTTPEthClient(url string, httpClient *http.Client) *HTTPEthClient {
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 30 * time.Second}
	}
	return &HTTPEthClient{url: url, client: httpClient}
}

// rpcRequest is a JSON-RPC 2.0 request envelope.
type rpcRequest struct {
	JSONRPC string        `json:"jsonrpc"`
	ID      uint64        `json:"id"`
	Method  string        `json:"method"`
	Params  []interface{} `json:"params"`
}

// rpcResponse is a JSON-RPC 2.0 response envelope with a raw result.
type rpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      uint64          `json:"id"`
	Result  json.RawMessage `json:"result"`
	Error   *rpcError       `json:"error"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func (e *rpcError) Error() string { return fmt.Sprintf("eth rpc error %d: %s", e.Code, e.Message) }

// call performs a single JSON-RPC call and unmarshals the result into out.
func (c *HTTPEthClient) call(ctx context.Context, method string, params []interface{}, out interface{}) error {
	reqBody := rpcRequest{JSONRPC: "2.0", ID: c.id.Add(1), Method: method, Params: params}
	buf, err := json.Marshal(reqBody)
	if err != nil {
		return fmt.Errorf("bridge: marshal %s request: %w", method, err)
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.url, bytes.NewReader(buf))
	if err != nil {
		return fmt.Errorf("bridge: build %s request: %w", method, err)
	}
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := c.client.Do(httpReq)
	if err != nil {
		return fmt.Errorf("bridge: %s request: %w", method, err)
	}
	defer resp.Body.Close()

	var rpcResp rpcResponse
	if err := json.NewDecoder(resp.Body).Decode(&rpcResp); err != nil {
		return fmt.Errorf("bridge: decode %s response: %w", method, err)
	}
	if rpcResp.Error != nil {
		return rpcResp.Error
	}
	if out != nil {
		if err := json.Unmarshal(rpcResp.Result, out); err != nil {
			return fmt.Errorf("bridge: unmarshal %s result: %w", method, err)
		}
	}
	return nil
}

// BlockNumber returns the latest block height via eth_blockNumber.
func (c *HTTPEthClient) BlockNumber(ctx context.Context) (uint64, error) {
	var hexNum string
	if err := c.call(ctx, "eth_blockNumber", []interface{}{}, &hexNum); err != nil {
		return 0, err
	}
	n, err := parseHexUint(hexNum)
	if err != nil {
		return 0, fmt.Errorf("bridge: parse block number %q: %w", hexNum, err)
	}
	return n, nil
}

// jsonEthLog is the wire shape of an eth_getLogs entry. Only the fields the
// decoder needs are captured.
type jsonEthLog struct {
	Topics      []string `json:"topics"`
	Data        string   `json:"data"`
	TxHash      string   `json:"transactionHash"`
	LogIndex    string   `json:"logIndex"`
	BlockNumber string   `json:"blockNumber"`
}

// FilterBurnedLogs returns the Burned logs from `contract` in [from,to] via
// eth_getLogs, converting each wire log into the decoder's EthLog shape.
func (c *HTTPEthClient) FilterBurnedLogs(ctx context.Context, contract Address, from, to uint64) ([]EthLog, error) {
	topic := BurnedEventTopic()
	filter := map[string]interface{}{
		"fromBlock": toHexUint(from),
		"toBlock":   toHexUint(to),
		"address":   contract.Hex(),
		"topics":    []interface{}{"0x" + hex.EncodeToString(topic[:])},
	}
	var raw []jsonEthLog
	if err := c.call(ctx, "eth_getLogs", []interface{}{filter}, &raw); err != nil {
		return nil, err
	}
	logs := make([]EthLog, 0, len(raw))
	for _, rl := range raw {
		el, err := rl.toEthLog()
		if err != nil {
			return nil, err
		}
		logs = append(logs, el)
	}
	return logs, nil
}

// toEthLog converts a wire log into the decoder's EthLog, parsing the 0x-hex
// topics and data into raw bytes.
func (rl jsonEthLog) toEthLog() (EthLog, error) {
	var el EthLog
	el.TxHash = rl.TxHash
	logIndex, err := parseHexUint(rl.LogIndex)
	if err != nil {
		return el, fmt.Errorf("bridge: parse log index %q: %w", rl.LogIndex, err)
	}
	el.LogIndex = logIndex

	el.Topics = make([][wordLen]byte, 0, len(rl.Topics))
	for _, t := range rl.Topics {
		b, err := decodeHex(t)
		if err != nil {
			return el, fmt.Errorf("bridge: parse topic %q: %w", t, err)
		}
		if len(b) != wordLen {
			return el, fmt.Errorf("%w: topic is %d bytes, want %d", ErrMalformedLog, len(b), wordLen)
		}
		var w [wordLen]byte
		copy(w[:], b)
		el.Topics = append(el.Topics, w)
	}

	data, err := decodeHex(rl.Data)
	if err != nil {
		return el, fmt.Errorf("bridge: parse log data: %w", err)
	}
	el.Data = data
	return el, nil
}

// parseHexUint parses a 0x-prefixed (or bare) hex string into a uint64.
func parseHexUint(s string) (uint64, error) {
	s = strings.TrimPrefix(strings.TrimPrefix(s, "0x"), "0X")
	if s == "" {
		return 0, nil
	}
	v, ok := new(big.Int).SetString(s, 16)
	if !ok {
		return 0, fmt.Errorf("invalid hex %q", s)
	}
	if !v.IsUint64() {
		return 0, fmt.Errorf("hex %q out of uint64 range", s)
	}
	return v.Uint64(), nil
}

// toHexUint renders v as a 0x-prefixed hex string, the form Ethereum JSON-RPC
// expects for block numbers.
func toHexUint(v uint64) string {
	return fmt.Sprintf("0x%x", v)
}

// decodeHex decodes a 0x-optional hex string into bytes, treating "0x"/"" as
// empty.
func decodeHex(s string) ([]byte, error) {
	s = strings.TrimPrefix(strings.TrimPrefix(s, "0x"), "0X")
	if s == "" {
		return nil, nil
	}
	return hex.DecodeString(s)
}
