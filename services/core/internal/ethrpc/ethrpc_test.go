package ethrpc

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math/big"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/decred/dcrd/dcrec/secp256k1/v4"
	"github.com/ecirlabs/matrix-core/internal/ethsig"
	"github.com/ecirlabs/matrix-core/internal/evmtx"
	"github.com/ecirlabs/matrix-core/internal/kv"
	"github.com/ecirlabs/matrix-core/internal/market"
	"github.com/ecirlabs/matrix-core/internal/token"
)

const testChainID uint64 = 61_337

// fakeChain is a chain just real enough to answer a wallet: a balance, a nonce,
// one block, and a mempool a submitted transaction lands in.
type fakeChain struct {
	balances map[string]uint64
	nonces   map[string]uint64
	blocks   []*BlockView
	// submitted holds what SubmitRaw accepted, keyed by lowercase hex id.
	submitted map[string]TxLookup
	// rejectSubmit makes SubmitRaw refuse, standing in for a chain that will not
	// admit the transaction.
	rejectSubmit error
}

func newFakeChain() *fakeChain {
	return &fakeChain{
		balances:  map[string]uint64{},
		nonces:    map[string]uint64{},
		submitted: map[string]TxLookup{},
	}
}

func (f *fakeChain) ChainHeight() uint64 { return uint64(len(f.blocks)) }

func (f *fakeChain) Balance(id string) (uint64, error) { return f.balances[id], nil }

func (f *fakeChain) NextNonce(id string, pending bool) uint64 {
	n := f.nonces[id]
	if pending {
		n++
	}
	return n
}

func (f *fakeChain) SubmitRaw(raw []byte) ([]byte, error) {
	if f.rejectSubmit != nil {
		return nil, f.rejectSubmit
	}
	tx, err := token.NewTransactionFromEVM(raw, testChainID, nil)
	if err != nil {
		return nil, err
	}
	hash, err := tx.EVMHash()
	if err != nil {
		return nil, err
	}
	f.submitted[hex.EncodeToString(hash)] = TxLookup{Tx: tx}
	return hash, nil
}

func (f *fakeChain) BlockByHeight(h uint64) (*BlockView, bool) {
	if h >= uint64(len(f.blocks)) {
		return nil, false
	}
	return f.blocks[h], true
}

func (f *fakeChain) BlockByHash(hash []byte) (*BlockView, bool) {
	for _, b := range f.blocks {
		if bytes.Equal(b.Hash, hash) {
			return b, true
		}
	}
	return nil, false
}

func (f *fakeChain) TransactionByHash(hashHex string) (TxLookup, bool) {
	l, ok := f.submitted[hashHex]
	return l, ok
}

// commit moves a submitted transaction into a new block, as the chain would.
func (f *fakeChain) commit(t *testing.T, hashHex string, applied bool) {
	t.Helper()
	lookup, ok := f.submitted[hashHex]
	if !ok {
		t.Fatalf("nothing submitted under %s", hashHex)
	}
	height := uint64(len(f.blocks))
	block := &BlockView{
		Height:     height,
		Hash:       bytes.Repeat([]byte{byte(height + 1)}, 32),
		ParentHash: bytes.Repeat([]byte{byte(height)}, 32),
		Timestamp:  1_800_000_000 + int64(height),
		Txs:        []token.Transaction{*lookup.Tx},
	}
	f.blocks = append(f.blocks, block)
	lookup.Committed = true
	lookup.Height = height
	lookup.Index = 0
	lookup.Applied = applied
	lookup.AppliedKnown = true
	f.submitted[hashHex] = lookup
}

func newTestHandler(t *testing.T, chain Backend) http.Handler {
	t.Helper()
	h, err := NewHandler(Config{ChainID: testChainID, Backend: chain, ClientVersion: "matrix-os/test"})
	if err != nil {
		t.Fatalf("NewHandler: %v", err)
	}
	return h
}

// rpc issues one call and returns the decoded response.
func rpc(t *testing.T, h http.Handler, method string, params ...any) rpcResponse {
	t.Helper()
	raw, err := json.Marshal(map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": method, "params": params,
	})
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/", bytes.NewReader(raw)))
	if rec.Code != http.StatusOK {
		t.Fatalf("%s: HTTP %d", method, rec.Code)
	}
	var resp rpcResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("%s: decode response: %v (%s)", method, err, rec.Body.String())
	}
	return resp
}

// resultString runs a call that must succeed and returns its string result.
func resultString(t *testing.T, h http.Handler, method string, params ...any) string {
	t.Helper()
	resp := rpc(t, h, method, params...)
	if resp.Error != nil {
		t.Fatalf("%s: %s", method, resp.Error.Message)
	}
	s, ok := resp.Result.(string)
	if !ok {
		t.Fatalf("%s: result %v is not a string", method, resp.Result)
	}
	return s
}

// signedTransfer produces what a wallet would put on the wire.
func signedTransfer(t *testing.T, nonce uint64, wholeMatrix uint64) ([]byte, ethsig.Address, ethsig.Address) {
	t.Helper()
	priv, err := secp256k1.GeneratePrivateKey()
	if err != nil {
		t.Fatalf("GeneratePrivateKey: %v", err)
	}
	to, err := ethsig.ParseAddress("0x00000000000000000000000000000000000000aa")
	if err != nil {
		t.Fatalf("ParseAddress: %v", err)
	}
	tx := &evmtx.Transaction{
		Type:      evmtx.TxLegacy,
		Nonce:     nonce,
		GasFeeCap: big.NewInt(0),
		Gas:       21000,
		To:        &to,
		Value:     token.NativeToERC20(wholeMatrix * token.NativeUnit),
	}
	if err := tx.Sign(priv, testChainID); err != nil {
		t.Fatalf("Sign: %v", err)
	}
	raw, err := tx.MarshalBinary()
	if err != nil {
		t.Fatalf("MarshalBinary: %v", err)
	}
	return raw, ethsig.AddressFromPubKey(priv.PubKey()), to
}

// TestAWalletCanAddThisNetwork covers the handshake a wallet performs before it
// will show anything. Getting any of these wrong means the network cannot be
// added at all.
func TestAWalletCanAddThisNetwork(t *testing.T) {
	h := newTestHandler(t, newFakeChain())

	// eth_chainId is hex; net_version is DECIMAL. A wallet compares the first
	// against what the user configured and refuses to send on a mismatch.
	if got, want := resultString(t, h, "eth_chainId"), "0xef99"; got != want {
		t.Fatalf("eth_chainId = %s, want %s", got, want)
	}
	if got, want := resultString(t, h, "net_version"), "61337"; got != want {
		t.Fatalf("net_version = %s, want %s", got, want)
	}
	if got := resultString(t, h, "web3_clientVersion"); got != "matrix-os/test" {
		t.Fatalf("web3_clientVersion = %s", got)
	}
	// An empty chain still has to answer, or a freshly launched network looks
	// broken to the first wallet that connects.
	if got := resultString(t, h, "eth_blockNumber"); got != "0x0" {
		t.Fatalf("eth_blockNumber on an empty chain = %s, want 0x0", got)
	}
}

// TestBalanceIsReportedInTheWalletsScale is the conversion a wallet cannot do
// for itself. It divides by 1e18 to show a figure; handing it native base units
// would show a balance a billion times too small.
func TestBalanceIsReportedInTheWalletsScale(t *testing.T) {
	chain := newFakeChain()
	addr, err := ethsig.ParseAddress("0x00000000000000000000000000000000000000aa")
	if err != nil {
		t.Fatalf("ParseAddress: %v", err)
	}
	// Two whole MATRIX, in the ledger's 9-decimal base units.
	chain.balances[token.EthAccountID(addr)] = 2 * token.NativeUnit
	h := newTestHandler(t, chain)

	got := resultString(t, h, "eth_getBalance", addr.Hex(), "latest")
	want := "0x" + token.NativeToERC20(2*token.NativeUnit).Text(16)
	if got != want {
		t.Fatalf("eth_getBalance = %s, want %s", got, want)
	}
	// And that value is two whole coins in the 18-decimal scale.
	parsed, ok := new(big.Int).SetString(got[2:], 16)
	if !ok {
		t.Fatalf("result %s is not hex", got)
	}
	if parsed.Cmp(new(big.Int).Mul(big.NewInt(2), new(big.Int).Exp(big.NewInt(10), big.NewInt(18), nil))) != 0 {
		t.Fatalf("balance = %s, want 2e18", parsed)
	}
	// An address the chain has never seen is zero, not an error: a wallet asks
	// about a fresh account on its first poll.
	if got := resultString(t, h, "eth_getBalance", "0x00000000000000000000000000000000000000bb", "latest"); got != "0x0" {
		t.Fatalf("unknown account balance = %s, want 0x0", got)
	}
}

// TestPendingNonceIncludesTheMempool is what stops two transfers sent seconds
// apart from colliding on one nonce. A wallet asks for the pending count before
// it signs.
func TestPendingNonceIncludesTheMempool(t *testing.T) {
	chain := newFakeChain()
	addr, _ := ethsig.ParseAddress("0x00000000000000000000000000000000000000aa")
	chain.nonces[token.EthAccountID(addr)] = 4
	h := newTestHandler(t, chain)

	if got := resultString(t, h, "eth_getTransactionCount", addr.Hex(), "latest"); got != "0x4" {
		t.Fatalf("latest nonce = %s, want 0x4", got)
	}
	if got := resultString(t, h, "eth_getTransactionCount", addr.Hex(), "pending"); got != "0x5" {
		t.Fatalf("pending nonce = %s, want 0x5", got)
	}
}

// TestSendAndThenPollForTheReceipt walks the whole flow a wallet performs, which
// is the one that decides whether a transfer looks successful or hangs forever.
func TestSendAndThenPollForTheReceipt(t *testing.T) {
	chain := newFakeChain()
	h := newTestHandler(t, chain)
	raw, from, to := signedTransfer(t, 0, 3)

	id := resultString(t, h, "eth_sendRawTransaction", "0x"+hex.EncodeToString(raw))
	if len(id) != 66 {
		t.Fatalf("transaction id %q is not 32 bytes of hex", id)
	}

	// Immediately after sending, the transaction is known and pending. A wallet
	// reads the null block fields as "keep polling"; an unknown transaction
	// would read as dropped.
	resp := rpc(t, h, "eth_getTransactionByHash", id)
	if resp.Error != nil {
		t.Fatalf("eth_getTransactionByHash: %s", resp.Error.Message)
	}
	view, ok := resp.Result.(map[string]any)
	if !ok {
		t.Fatalf("a just-sent transaction must be findable, got %v", resp.Result)
	}
	if view["blockNumber"] != nil {
		t.Fatalf("a pending transaction must report a null blockNumber, got %v", view["blockNumber"])
	}
	if view["from"] != from.Hex() || view["to"] != to.Hex() {
		t.Fatalf("from/to = %v/%v, want %s/%s", view["from"], view["to"], from.Hex(), to.Hex())
	}

	// No receipt until it is in a block. Reporting one earlier would tell a
	// wallet, and an exchange, that a transfer had settled while it was only
	// proposed.
	if resp := rpc(t, h, "eth_getTransactionReceipt", id); resp.Result != nil {
		t.Fatalf("a pending transaction must have no receipt, got %v", resp.Result)
	}

	chain.commit(t, id[2:], true)

	resp = rpc(t, h, "eth_getTransactionReceipt", id)
	receipt, ok := resp.Result.(map[string]any)
	if !ok {
		t.Fatalf("a committed transaction must have a receipt, got %v (%v)", resp.Result, resp.Error)
	}
	if receipt["status"] != "0x1" {
		t.Fatalf("status = %v, want 0x1", receipt["status"])
	}
	if receipt["blockNumber"] != "0x0" {
		t.Fatalf("blockNumber = %v, want 0x0", receipt["blockNumber"])
	}
}

// TestACommittedButSkippedTransferReportsFailure is the distinction an exchange
// depends on. An unaffordable transfer is ordered by the quorum and then
// deterministically skipped by every node; reporting that as a success is how a
// deposit gets credited that never arrived.
func TestACommittedButSkippedTransferReportsFailure(t *testing.T) {
	chain := newFakeChain()
	h := newTestHandler(t, chain)
	raw, _, _ := signedTransfer(t, 0, 1)

	id := resultString(t, h, "eth_sendRawTransaction", "0x"+hex.EncodeToString(raw))
	chain.commit(t, id[2:], false)

	receipt, ok := rpc(t, h, "eth_getTransactionReceipt", id).Result.(map[string]any)
	if !ok {
		t.Fatal("expected a receipt")
	}
	if receipt["status"] != "0x0" {
		t.Fatalf("a skipped transfer reported status %v, want 0x0", receipt["status"])
	}
}

// TestAnUnknownTransactionIsNullNotAnError pins the difference a wallet acts on.
// An error makes it stop polling; null makes it keep waiting, which is correct
// for a transaction that has not reached this node yet.
func TestAnUnknownTransactionIsNullNotAnError(t *testing.T) {
	h := newTestHandler(t, newFakeChain())
	unknown := "0x" + hex.EncodeToString(bytes.Repeat([]byte{0xab}, 32))

	for _, method := range []string{"eth_getTransactionByHash", "eth_getTransactionReceipt"} {
		resp := rpc(t, h, method, unknown)
		if resp.Error != nil {
			t.Fatalf("%s on an unknown hash returned an error: %s", method, resp.Error.Message)
		}
		if resp.Result != nil {
			t.Fatalf("%s on an unknown hash = %v, want null", method, resp.Result)
		}
	}
}

// TestARejectedSendIsAnErrorNotAnID is the other half of the same concern. A
// wallet handed an id for a transaction the chain never admitted shows it
// pending forever.
func TestARejectedSendIsAnErrorNotAnID(t *testing.T) {
	chain := newFakeChain()
	h := newTestHandler(t, chain)

	// Signed for another network.
	priv, _ := secp256k1.GeneratePrivateKey()
	to, _ := ethsig.ParseAddress("0x00000000000000000000000000000000000000aa")
	tx := &evmtx.Transaction{Type: evmtx.TxLegacy, GasFeeCap: big.NewInt(0), Gas: 21000, To: &to, Value: big.NewInt(0)}
	if err := tx.Sign(priv, testChainID+1); err != nil {
		t.Fatalf("Sign: %v", err)
	}
	raw, _ := tx.MarshalBinary()

	resp := rpc(t, h, "eth_sendRawTransaction", "0x"+hex.EncodeToString(raw))
	if resp.Error == nil {
		t.Fatalf("another network's transaction was accepted, id %v", resp.Result)
	}
}

// TestBlocksAreReadable covers what an explorer and a wallet's history read.
func TestBlocksAreReadable(t *testing.T) {
	chain := newFakeChain()
	h := newTestHandler(t, chain)
	raw, _, _ := signedTransfer(t, 0, 1)
	id := resultString(t, h, "eth_sendRawTransaction", "0x"+hex.EncodeToString(raw))
	chain.commit(t, id[2:], true)

	if got := resultString(t, h, "eth_blockNumber"); got != "0x0" {
		t.Fatalf("eth_blockNumber = %s, want 0x0", got)
	}

	block, ok := rpc(t, h, "eth_getBlockByNumber", "latest", false).Result.(map[string]any)
	if !ok {
		t.Fatal("latest block should be readable")
	}
	// The timestamp is the field the chain had none of, and an explorer shows
	// 1970 without it.
	if block["timestamp"] == "0x0" {
		t.Fatal("the block reported no timestamp")
	}
	txs, ok := block["transactions"].([]any)
	if !ok || len(txs) != 1 || txs[0] != id {
		t.Fatalf("transactions = %v, want [%s]", block["transactions"], id)
	}

	// And by hash, which is how a wallet follows a receipt back to its block.
	byHash, ok := rpc(t, h, "eth_getBlockByHash", block["hash"], false).Result.(map[string]any)
	if !ok {
		t.Fatal("the block should be readable by hash")
	}
	if byHash["number"] != block["number"] {
		t.Fatalf("by hash = %v, by number = %v", byHash["number"], block["number"])
	}
}

// TestWhatThisChainCannotDoSaysSo covers the methods that would need an EVM. An
// empty result reads to a wallet as "the call succeeded and returned nothing",
// and it will act on that.
func TestWhatThisChainCannotDoSaysSo(t *testing.T) {
	h := newTestHandler(t, newFakeChain())

	resp := rpc(t, h, "eth_call", map[string]any{"to": "0x00000000000000000000000000000000000000aa"}, "latest")
	if resp.Error == nil {
		t.Fatalf("eth_call returned %v instead of saying there is no EVM", resp.Result)
	}
	if resp := rpc(t, h, "eth_notARealMethod"); resp.Error == nil || resp.Error.Code != codeMethodNotFound {
		t.Fatalf("an unknown method = %v, want method-not-found", resp.Error)
	}
	// No account has code, which is what stops a wallet treating an address as
	// a contract.
	if got := resultString(t, h, "eth_getCode", "0x00000000000000000000000000000000000000aa", "latest"); got != "0x" {
		t.Fatalf("eth_getCode = %s, want 0x", got)
	}
}

// TestBatchedCallsAreAnswerdAsABatch matters because a wallet batches its polls,
// and answering a batch with a single object makes the client discard every
// response in it.
func TestBatchedCallsAreAnswerdAsABatch(t *testing.T) {
	h := newTestHandler(t, newFakeChain())
	raw, err := json.Marshal([]map[string]any{
		{"jsonrpc": "2.0", "id": 1, "method": "eth_chainId"},
		{"jsonrpc": "2.0", "id": 2, "method": "eth_blockNumber"},
	})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/", bytes.NewReader(raw)))

	var out []rpcResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("a batch must be answered with an array: %v (%s)", err, rec.Body.String())
	}
	if len(out) != 2 {
		t.Fatalf("got %d responses for 2 requests", len(out))
	}
}

// TestAChainIDIsRequired guards the constructor. A handler serving chain id zero
// would let a wallet broadcast here a transaction it signed for another network.
func TestAChainIDIsRequired(t *testing.T) {
	if _, err := NewHandler(Config{Backend: newFakeChain()}); err == nil {
		t.Fatal("a handler with no chain id should be refused")
	}
}

// supplyChain adds the matrix_ namespace to the fake, so the proof and supply
// methods can be driven without a consensus engine.
type supplyChain struct {
	*fakeChain
	root   []byte
	proof  *market.AccountProof
	report SupplyReport
	err    error
}

func (s *supplyChain) StateRoot() ([]byte, error) { return s.root, s.err }
func (s *supplyChain) AccountProof(account string) (*market.AccountProof, error) {
	if s.err != nil {
		return nil, s.err
	}
	if s.proof == nil || s.proof.Account != account {
		return nil, fmt.Errorf("market: account has no balance in the ledger: %q", account)
	}
	return s.proof, nil
}
func (s *supplyChain) Supply() (SupplyReport, error) { return s.report, s.err }

// TestTheEscrowCanBeProvenToAnOutsider is the capability a listing review asks
// about. Wrapped supply is supposed to be backed one-for-one by native coins in
// escrow, and the only evidence was the attestors' word; a proof against a root
// the validators signed makes it an audited reserve rather than an asserted one.
func TestTheEscrowCanBeProvenToAnOutsider(t *testing.T) {
	// A real ledger, so the proof under test is one that actually verifies.
	store, err := kv.New(kv.Config{Path: t.TempDir()})
	if err != nil {
		t.Fatalf("kv.New: %v", err)
	}
	defer store.Close()
	ledger := market.NewLedger(store)
	const escrowed = 50_000_000 * token.NativeUnit
	if err := ledger.Credit("bridge/escrow", escrowed); err != nil {
		t.Fatalf("Credit: %v", err)
	}
	if err := ledger.Credit("somebody-else", 123); err != nil {
		t.Fatalf("Credit: %v", err)
	}
	root, err := ledger.StateRoot()
	if err != nil {
		t.Fatalf("StateRoot: %v", err)
	}
	proof, err := ledger.ProveAccount("bridge/escrow")
	if err != nil {
		t.Fatalf("ProveAccount: %v", err)
	}

	h := newTestHandler(t, &supplyChain{fakeChain: newFakeChain(), root: root, proof: proof})

	if got := resultString(t, h, "matrix_getStateRoot"); got != "0x"+hex.EncodeToString(root) {
		t.Fatalf("matrix_getStateRoot = %s", got)
	}

	resp := rpc(t, h, "matrix_getAccountProof", "bridge/escrow")
	if resp.Error != nil {
		t.Fatalf("matrix_getAccountProof: %s", resp.Error.Message)
	}
	view, ok := resp.Result.(map[string]any)
	if !ok {
		t.Fatalf("result = %v", resp.Result)
	}
	// The balance is reported in the wallet's 18-decimal scale, matching
	// eth_getBalance, so a caller comparing the two is not comparing two scales.
	if want := hexBig(token.NativeToERC20(escrowed)); view["balance"] != want {
		t.Fatalf("balance = %v, want %s", view["balance"], want)
	}
	if view["root"] != "0x"+hex.EncodeToString(root) {
		t.Fatalf("the proof is against a different root than matrix_getStateRoot reported")
	}

	// And the proof it served actually verifies, which is the only thing that
	// makes any of this worth serving.
	if err := market.VerifyAccountProof(proof, root); err != nil {
		t.Fatalf("the served proof does not verify: %v", err)
	}
}

// TestSupplyReportsComponentsNotOneNumber pins the reporting choice.
// "Circulating" is a contested definition and a single figure hides which one
// was used, so the components are reported and the escrow is excluded - those
// coins are immobile here and mobile there, and counting them in both places is
// the double-count the split exists to prevent.
func TestSupplyReportsComponentsNotOneNumber(t *testing.T) {
	report := SupplyReport{
		MaxSupply:    token.NativeMaxSupply,
		Issued:       1_000_000_000 * token.NativeUnit,
		RewardPool:   900_000_000 * token.NativeUnit,
		BridgeEscrow: 50_000_000 * token.NativeUnit,
		Circulating:  50_000_000 * token.NativeUnit,
		Decimals:     9,
	}
	h := newTestHandler(t, &supplyChain{fakeChain: newFakeChain(), report: report})

	resp := rpc(t, h, "matrix_getSupply")
	if resp.Error != nil {
		t.Fatalf("matrix_getSupply: %s", resp.Error.Message)
	}
	got, ok := resp.Result.(map[string]any)
	if !ok {
		t.Fatalf("result = %v", resp.Result)
	}
	for _, field := range []string{"max_supply", "issued", "reward_pool", "bridge_escrow", "circulating", "decimals"} {
		if _, present := got[field]; !present {
			t.Fatalf("the report omitted %q: %v", field, got)
		}
	}
	// Circulating must not include the pool or the escrow, or the figure double
	// counts coins nobody can spend here.
	if got["circulating"] == got["issued"] {
		t.Fatal("circulating equals issued, so the pool and escrow were counted as circulating")
	}
}

// TestTheMatrixNamespaceIsRefusedWhenUnsupported covers a node that serves the
// eth_ methods and not these, which must say so rather than answer emptily.
func TestTheMatrixNamespaceIsRefusedWhenUnsupported(t *testing.T) {
	h := newTestHandler(t, newFakeChain())
	for _, method := range []string{"matrix_getStateRoot", "matrix_getSupply", "matrix_getAccountProof"} {
		resp := rpc(t, h, method, "bridge/escrow")
		if resp.Error == nil {
			t.Fatalf("%s answered %v instead of saying it is not served", method, resp.Result)
		}
	}
}

// TestAnUnprovableAccountIsAnErrorNotNull is the opposite call from
// eth_getTransactionByHash. A caller asking for a proof wants one, and a silent
// null reads as "proved nothing", which is not the same as "this account has no
// balance to prove".
func TestAnUnprovableAccountIsAnErrorNotNull(t *testing.T) {
	h := newTestHandler(t, &supplyChain{fakeChain: newFakeChain(), root: bytes.Repeat([]byte{1}, 32)})
	resp := rpc(t, h, "matrix_getAccountProof", "0x00000000000000000000000000000000000000aa")
	if resp.Error == nil {
		t.Fatalf("an unprovable account answered %v, want an error", resp.Result)
	}
}

// TestChainInfoAnswersWhatAnIntegratorAsks covers the questions an exchange puts
// to a chain before it writes any code, in the endpoint they are already
// polling rather than in a document nobody can verify.
func TestChainInfoAnswersWhatAnIntegratorAsks(t *testing.T) {
	chain := &supplyChain{fakeChain: newFakeChain(), root: bytes.Repeat([]byte{0xab}, 32)}
	h := newTestHandler(t, chain)

	resp := rpc(t, h, "matrix_getChainInfo")
	if resp.Error != nil {
		t.Fatalf("matrix_getChainInfo: %s", resp.Error.Message)
	}
	info, ok := resp.Result.(map[string]any)
	if !ok {
		t.Fatalf("result = %v", resp.Result)
	}

	if info["chain_id"] != float64(testChainID) {
		t.Fatalf("chain_id = %v, want %d", info["chain_id"], testChainID)
	}
	// One confirmation, and not a cautious larger number: a block commits under a
	// BFT quorum and is never revisited, so telling an integrator to wait longer
	// would be inventing a risk.
	if info["finality_confirmations"] != float64(1) {
		t.Fatalf("finality_confirmations = %v, want 1", info["finality_confirmations"])
	}
	if info["reorgs"] != false {
		t.Fatalf("reorgs = %v, want false", info["reorgs"])
	}
	// And it says plainly that contracts do not run here, because an integrator
	// who assumes otherwise writes code against a machine that is not present.
	if info["has_evm"] != false {
		t.Fatalf("has_evm = %v, want false", info["has_evm"])
	}
	// The two scales are both reported, because a reader using the wrong one is
	// off by a billion.
	if info["native_decimals"] != float64(9) || info["evm_decimals"] != float64(18) {
		t.Fatalf("decimals = %v / %v, want 9 / 18", info["native_decimals"], info["evm_decimals"])
	}
	if info["state_root"] != "0x"+hex.EncodeToString(chain.root) {
		t.Fatalf("state_root = %v", info["state_root"])
	}
}
