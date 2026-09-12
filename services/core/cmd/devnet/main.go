// Command devnet brings up a real three-validator network on this machine and
// checks that it works, in one command.
//
// WHY IT EXISTS. "Does this actually run" was a question only three provisioned
// hosts could answer, which meant it was rarely asked, and the answers that came
// back were a person reading log lines and deciding they looked fine. Three
// validators is a PROTOCOL requirement - a quorum needs more than one node to
// mean anything - and not an infrastructure one. Three processes on one machine
// exercise every rule the protocol has: leader rotation, quorum, block sync, the
// state root, and a wallet's whole round trip.
//
// What it does NOT prove is the part that genuinely needs separate hosts: NAT,
// firewalls, clock drift between regions, and disks filling up. Those are
// deployment questions and this tool does not pretend to answer them.
//
// It builds nothing permanent. Everything lands in a temporary directory that is
// removed on the way out unless -keep is given.
package main

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/decred/dcrd/dcrec/secp256k1/v4"
	"gopkg.in/yaml.v3"

	"github.com/ecirlabs/matrix-core/internal/ethsig"
	"github.com/ecirlabs/matrix-core/internal/evmtx"
	"github.com/ecirlabs/matrix-core/internal/market"
	"github.com/ecirlabs/matrix-core/internal/marketexchange"
	"github.com/ecirlabs/matrix-core/internal/token"
)

// devnetChainID is this network's id.
//
// It is deliberately NOT a number anyone uses in production. A devnet that
// borrowed a real chain's id would let a wallet still pointed at it broadcast a
// transaction here that is byte-for-byte valid there, which is the exact replay
// EIP-155 exists to stop - and a test network is precisely where someone has a
// wallet pointed at the wrong thing.
const devnetChainID = 613_370

// Ports are laid out per node so three of them fit on one machine without any
// of the usual "port already in use" archaeology.
const (
	basePort     = 19000
	portsPerNode = 10
)

// Genesis shares, in native base units. Each validator gets twice the minimum
// bond so it can bond and still hold a spendable balance; the escrow gets the
// same fifty million the real network holds, so the supply arithmetic and the
// reserve proof are exercised against a realistic figure rather than a token one.
const (
	perValidator = 2 * minBond
	minBond      = 1_000_000_000_000_000
	buyerFunds   = 1_000_000_000_000_000
	escrowFunds  = 50_000_000 * 1_000_000_000
)

func main() {
	keep := flag.Bool("keep", false, "keep the working directory and leave the nodes running")
	timeout := flag.Duration("timeout", 90*time.Second, "how long to wait for the network to come up")
	flag.Parse()

	if err := run(*keep, *timeout); err != nil {
		fmt.Fprintf(os.Stderr, "\nFAILED: %v\n", err)
		os.Exit(1)
	}
}

func run(keep bool, timeout time.Duration) error {
	dir, err := os.MkdirTemp("", "matrix-devnet-")
	if err != nil {
		return err
	}
	if !keep {
		defer os.RemoveAll(dir)
	}
	fmt.Printf("working directory: %s\n\n", dir)

	step("building the binaries")
	binDir := filepath.Join(dir, "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		return err
	}
	matrixd := filepath.Join(binDir, "matrixd")
	if out, err := exec.Command("go", "build", "-o", matrixd, "./cmd/matrixd").CombinedOutput(); err != nil {
		return fmt.Errorf("build matrixd: %v\n%s", err, out)
	}
	// The CLI too, because the directory check reads the registry through the
	// same command a buyer would rather than through the exchange's own types.
	// A check that bypasses the tooling can pass while the tooling is broken.
	matrixCLI := filepath.Join(binDir, "matrix")
	if out, err := exec.Command("go", "build", "-o", matrixCLI, "./cmd/matrix").CombinedOutput(); err != nil {
		return fmt.Errorf("build matrix: %v\n%s", err, out)
	}
	ok("built matrixd and matrix")

	step("initialising three nodes")
	nodes := make([]*devNode, 3)
	for i := range nodes {
		n, err := initNode(matrixd, dir, i)
		if err != nil {
			return err
		}
		nodes[i] = n
	}
	buyer := newWallet()
	if err := writeConfigs(nodes, buyer.address); err != nil {
		return err
	}
	ok("three configs written, chain id %d", devnetChainID)

	step("starting the network")
	for _, n := range nodes {
		if err := n.start(matrixd, keep); err != nil {
			return err
		}
	}
	if !keep {
		defer func() {
			for _, n := range nodes {
				n.stop()
			}
		}()
	}
	if err := waitForHeight(nodes, 2, timeout); err != nil {
		return err
	}
	ok("all three nodes committed blocks")

	if err := checkAgreement(nodes); err != nil {
		return err
	}
	if err := checkChainInfo(nodes[0]); err != nil {
		return err
	}
	if err := checkSupply(nodes[0]); err != nil {
		return err
	}
	if err := checkReserveProof(nodes[0]); err != nil {
		return err
	}
	if err := checkWalletTransfer(nodes, buyer, timeout); err != nil {
		return err
	}
	if err := checkAgreement(nodes); err != nil {
		return fmt.Errorf("after a transfer: %w", err)
	}
	if err := checkRejections(nodes[0], buyer); err != nil {
		return err
	}
	if err := checkDirectory(matrixCLI, nodes, timeout); err != nil {
		return err
	}
	if !keep {
		if err := checkGenesisSnapshot(matrixd, nodes); err != nil {
			return err
		}
	}

	fmt.Printf("\nPASS - a three-validator network came up, agreed, and settled a wallet-signed transfer.\n")
	if keep {
		fmt.Printf("\nleft running. RPC endpoints:\n")
		for i, n := range nodes {
			fmt.Printf("  node %d: %s\n", i, n.ethRPC)
		}
		fmt.Printf("working directory: %s\n", dir)
	}
	return nil
}

// devNode is one node's identity, ports and process.
type devNode struct {
	index       int
	dir         string
	configPath  string
	consensusID string
	peerID      string
	p2pPort     int
	ethRPC      string
	// market is the gRPC address the CLI talks to, and connect is the address
	// this node publishes to the directory for buyers to dial.
	market  string
	connect string
	// apiKey is the admin key `matrixd -init` generated for this node, read back
	// so the checks can drive the CLI the way an operator does.
	apiKey string
	cmd    *exec.Cmd
	log    *os.File
}

// initNode writes a baseline config and reads back the identity it generated.
func initNode(matrixd, root string, i int) (*devNode, error) {
	dir := filepath.Join(root, fmt.Sprintf("node%d", i))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	configPath := filepath.Join(dir, "config.yaml")
	if out, err := exec.Command(matrixd, "-init", "-config", configPath).CombinedOutput(); err != nil {
		return nil, fmt.Errorf("init node %d: %v\n%s", i, err, out)
	}
	// The storage path has to be this node's BEFORE the identity is read. An
	// identity lives in the store, so reading one against the default path
	// generates it there and the node then makes a different one on first start
	// against its real path - and the validator list names an account that never
	// validates anything. That failure reads as "this node holds nothing",
	// several layers from its cause.
	if err := patchConfig(configPath, func(cfg map[string]any) {
		setPath(cfg, []string{"storage", "path"}, filepath.Join(dir, "data"))
	}); err != nil {
		return nil, err
	}
	// The identity has to exist before the validator list can be written, and it
	// is generated on first touch - which is why this is a separate step rather
	// than something the config could have declared.
	out, err := exec.Command(matrixd, "-init-identities", "-config", configPath).Output()
	if err != nil {
		return nil, fmt.Errorf("read node %d identity: %v", i, err)
	}
	var ids struct {
		ConsensusID string `json:"consensus_id"`
		PeerID      string `json:"peer_id"`
	}
	if err := json.Unmarshal(out, &ids); err != nil {
		return nil, fmt.Errorf("parse node %d identity: %v (%s)", i, err, out)
	}
	apiKey, err := readAPIKey(configPath)
	if err != nil {
		return nil, fmt.Errorf("read node %d api key: %v", i, err)
	}
	base := basePort + i*portsPerNode
	return &devNode{
		apiKey:      apiKey,
		index:       i,
		dir:         dir,
		configPath:  configPath,
		consensusID: ids.ConsensusID,
		peerID:      ids.PeerID,
		p2pPort:     base,
		ethRPC:      fmt.Sprintf("http://127.0.0.1:%d", base+5),
		market:      fmt.Sprintf("127.0.0.1:%d", base+2),
		connect:     fmt.Sprintf("http://127.0.0.1:%d", base+4),
	}, nil
}

// writeConfigs patches every node's config into one network.
func writeConfigs(nodes []*devNode, buyer ethsig.Address) error {
	validators := make([]string, 0, len(nodes))
	for _, n := range nodes {
		validators = append(validators, n.consensusID)
	}

	// Genesis: every validator funded enough to bond, a buyer wallet, and an
	// escrow the size of the real one. The pool takes the remainder so the total
	// is exactly the cap, which is what the supply check below verifies.
	allocations := []map[string]any{
		{"account": "bridge/escrow", "amount": escrowFunds},
		{"account": token.EthAccountID(buyer), "amount": buyerFunds},
	}
	allocated := uint64(escrowFunds + buyerFunds)
	for _, n := range nodes {
		allocations = append(allocations, map[string]any{"account": n.consensusID, "amount": uint64(perValidator)})
		allocated += perValidator
	}
	if allocated > token.NativeMaxSupply {
		return fmt.Errorf("devnet allocations exceed the supply cap")
	}

	for _, n := range nodes {
		n := n
		peers := make([]string, 0, len(nodes)-1)
		for _, other := range nodes {
			if other.index == n.index {
				continue
			}
			peers = append(peers, fmt.Sprintf("/ip4/127.0.0.1/tcp/%d/p2p/%s", other.p2pPort, other.peerID))
		}
		base := basePort + n.index*portsPerNode

		if err := patchConfig(n.configPath, func(cfg map[string]any) {
			setPath(cfg, []string{"network", "listen_addr"}, fmt.Sprintf("/ip4/127.0.0.1/tcp/%d", n.p2pPort))
			setPath(cfg, []string{"network", "bootstrap_peers"}, peers)
			setPath(cfg, []string{"storage", "path"}, filepath.Join(n.dir, "data"))
			setPath(cfg, []string{"admin", "addr"}, fmt.Sprintf("127.0.0.1:%d", base+1))
			setPath(cfg, []string{"market", "addr"}, fmt.Sprintf("127.0.0.1:%d", base+2))
			setPath(cfg, []string{"inference", "addr"}, fmt.Sprintf("127.0.0.1:%d", base+3))
			setPath(cfg, []string{"connect", "addr"}, fmt.Sprintf("127.0.0.1:%d", base+4))
			setPath(cfg, []string{"eth_rpc", "addr"}, fmt.Sprintf("127.0.0.1:%d", base+5))
			setPath(cfg, []string{"agent", "addr"}, fmt.Sprintf("127.0.0.1:%d", base+6))
			// The address this node publishes to the directory, which is the
			// Connect endpoint a buyer's client actually dials. Each node gets a
			// distinct one so the discovery check below can tell whose is whose.
			setPath(cfg, []string{"market", "endpoint"}, fmt.Sprintf("http://127.0.0.1:%d", base+4))
			// Fast enough that the check does not wait out a production interval.
			setPath(cfg, []string{"market", "announce_interval"}, "1s")

			setPath(cfg, []string{"consensus", "chain_id"}, devnetChainID)
			setPath(cfg, []string{"consensus", "validators"}, validators)
			// Long enough that three processes sharing one machine's scheduler do not
			// rotate leaders under load, which looks like a consensus fault and is not.
			setPath(cfg, []string{"consensus", "round_timeout"}, "2s")
			setPath(cfg, []string{"consensus", "epoch_length"}, 10)
			setPath(cfg, []string{"genesis", "allocations"}, allocations)
			setPath(cfg, []string{"genesis", "reward_pool"}, token.NativeMaxSupply-allocated)
		}); err != nil {
			return err
		}
	}
	return nil
}

// patchConfig reads, mutates and rewrites a config file.
func patchConfig(path string, mutate func(cfg map[string]any)) error {
	raw, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	var cfg map[string]any
	if err := yaml.Unmarshal(raw, &cfg); err != nil {
		return err
	}
	mutate(cfg)
	patched, err := yaml.Marshal(cfg)
	if err != nil {
		return err
	}
	return os.WriteFile(path, patched, 0o600)
}

// setPath writes a nested config value, creating maps as needed.
func setPath(cfg map[string]any, path []string, value any) {
	cursor := cfg
	for _, key := range path[:len(path)-1] {
		next, ok := cursor[key].(map[string]any)
		if !ok {
			next = map[string]any{}
			cursor[key] = next
		}
		cursor = next
	}
	cursor[path[len(path)-1]] = value
}

// start launches the node, with its output to a file so a failure has something
// to read rather than a silent exit.
func (n *devNode) start(matrixd string, keep bool) error {
	logPath := filepath.Join(n.dir, "node.log")
	log, err := os.Create(logPath)
	if err != nil {
		return err
	}
	n.log = log
	n.cmd = exec.Command(matrixd, "-config", n.configPath)
	n.cmd.Stdout = log
	n.cmd.Stderr = log
	if err := n.cmd.Start(); err != nil {
		return fmt.Errorf("start node %d: %v", n.index, err)
	}
	return nil
}

func (n *devNode) stop() {
	if n.cmd != nil && n.cmd.Process != nil {
		_ = n.cmd.Process.Kill()
		_ = n.cmd.Wait()
	}
	if n.log != nil {
		_ = n.log.Close()
	}
}

// tail returns the end of a node's log, for a failure message that says what the
// node actually reported.
func (n *devNode) tail(lines int) string {
	raw, err := os.ReadFile(filepath.Join(n.dir, "node.log"))
	if err != nil {
		return "(no log)"
	}
	all := strings.Split(strings.TrimRight(string(raw), "\n"), "\n")
	if len(all) > lines {
		all = all[len(all)-lines:]
	}
	return "  " + strings.Join(all, "\n  ")
}

// rpc issues one JSON-RPC call.
func (n *devNode) rpc(method string, params ...any) (json.RawMessage, error) {
	if params == nil {
		params = []any{}
	}
	body, err := json.Marshal(map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": method, "params": params,
	})
	if err != nil {
		return nil, err
	}
	resp, err := http.Post(n.ethRPC, "application/json", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	var out struct {
		Result json.RawMessage `json:"result"`
		Error  *struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, fmt.Errorf("%s: %v (%s)", method, err, raw)
	}
	if out.Error != nil {
		return nil, fmt.Errorf("%s: %s", method, out.Error.Message)
	}
	return out.Result, nil
}

// rpcString reads a string result.
func (n *devNode) rpcString(method string, params ...any) (string, error) {
	raw, err := n.rpc(method, params...)
	if err != nil {
		return "", err
	}
	var s string
	if err := json.Unmarshal(raw, &s); err != nil {
		return "", fmt.Errorf("%s: %v", method, err)
	}
	return s, nil
}

// waitForHeight blocks until every node reports at least the given height.
func waitForHeight(nodes []*devNode, height uint64, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		ready := 0
		for _, n := range nodes {
			raw, err := n.rpcString("eth_blockNumber")
			if err != nil {
				continue
			}
			if h, err := parseHex(raw); err == nil && h >= height {
				ready++
			}
		}
		if ready == len(nodes) {
			return nil
		}
		time.Sleep(250 * time.Millisecond)
	}
	var report strings.Builder
	for _, n := range nodes {
		fmt.Fprintf(&report, "\nnode %d log tail:\n%s\n", n.index, n.tail(15))
	}
	return fmt.Errorf("the network did not reach height %d within %s%s", height, timeout, report.String())
}

// checkAgreement is the check the whole state root exists for: three nodes that
// applied the same blocks must hold the same ledger, and now they can be asked
// rather than assumed.
func checkAgreement(nodes []*devNode) error {
	step("checking that all three agree on the ledger")
	roots := make([]string, len(nodes))
	for i, n := range nodes {
		root, err := n.rpcString("matrix_getStateRoot")
		if err != nil {
			return err
		}
		roots[i] = root
	}
	for i := 1; i < len(roots); i++ {
		if roots[i] != roots[0] {
			return fmt.Errorf("node 0 and node %d hold different ledgers (%s vs %s): they applied the "+
				"same blocks and reached different balances", i, roots[0][:18], roots[i][:18])
		}
	}
	ok("all three report the same state root %s", roots[0][:18])
	return nil
}

func checkChainInfo(n *devNode) error {
	step("checking what the chain says it is")
	raw, err := n.rpc("matrix_getChainInfo")
	if err != nil {
		return err
	}
	var info struct {
		ChainID               uint64 `json:"chain_id"`
		FinalityConfirmations int    `json:"finality_confirmations"`
		Reorgs                bool   `json:"reorgs"`
		HasEVM                bool   `json:"has_evm"`
	}
	if err := json.Unmarshal(raw, &info); err != nil {
		return err
	}
	if info.ChainID != devnetChainID {
		return fmt.Errorf("chain id is %d, want %d", info.ChainID, devnetChainID)
	}
	if info.FinalityConfirmations != 1 || info.Reorgs {
		return fmt.Errorf("finality reported as %d confirmations, reorgs=%v",
			info.FinalityConfirmations, info.Reorgs)
	}
	if info.HasEVM {
		return fmt.Errorf("the chain claims to run an EVM, which it does not")
	}
	ok("chain %d, final at 1 confirmation, no EVM - as documented", info.ChainID)
	return nil
}

func checkSupply(n *devNode) error {
	step("checking the supply adds up")
	raw, err := n.rpc("matrix_getSupply")
	if err != nil {
		return err
	}
	var supply struct {
		MaxSupply    uint64 `json:"max_supply"`
		Issued       uint64 `json:"issued"`
		RewardPool   uint64 `json:"reward_pool"`
		BridgeEscrow uint64 `json:"bridge_escrow"`
		Circulating  uint64 `json:"circulating"`
	}
	if err := json.Unmarshal(raw, &supply); err != nil {
		return err
	}
	if supply.Issued != token.NativeMaxSupply {
		return fmt.Errorf("issued %d, want the whole cap %d", supply.Issued, token.NativeMaxSupply)
	}
	if supply.BridgeEscrow != escrowFunds {
		return fmt.Errorf("escrow %d, want the %d genesis put there", supply.BridgeEscrow, uint64(escrowFunds))
	}
	// The identity that makes the figure meaningful: what is circulating is what
	// exists minus what nobody can spend here.
	if supply.Circulating != supply.Issued-supply.RewardPool-supply.BridgeEscrow {
		return fmt.Errorf("circulating %d does not equal issued - pool - escrow (%d)",
			supply.Circulating, supply.Issued-supply.RewardPool-supply.BridgeEscrow)
	}
	ok("issued %d, escrow %d, circulating %d", supply.Issued, supply.BridgeEscrow, supply.Circulating)
	return nil
}

// checkReserveProof is the listing question: can an outsider verify the escrow
// without being handed the whole ledger.
func checkReserveProof(n *devNode) error {
	step("verifying the bridge escrow proof as an outsider would")
	rootHex, err := n.rpcString("matrix_getStateRoot")
	if err != nil {
		return err
	}
	root, err := hex.DecodeString(strings.TrimPrefix(rootHex, "0x"))
	if err != nil {
		return err
	}

	raw, err := n.rpc("matrix_getAccountProof", "bridge/escrow")
	if err != nil {
		return err
	}
	var view struct {
		Account       string   `json:"account"`
		Balance       string   `json:"balance"`
		Root          string   `json:"root"`
		Siblings      []string `json:"siblings"`
		SiblingIsLeft []bool   `json:"siblingIsLeft"`
	}
	if err := json.Unmarshal(raw, &view); err != nil {
		return err
	}
	if view.Root != rootHex {
		return fmt.Errorf("the proof is against root %s but the chain reports %s", view.Root, rootHex)
	}

	// Rebuilt from the wire exactly as a third party would, and verified with
	// nothing but the proof and the root.
	balance, ok18 := new(big.Int).SetString(strings.TrimPrefix(view.Balance, "0x"), 16)
	if !ok18 {
		return fmt.Errorf("balance %q is not hex", view.Balance)
	}
	native, err := token.ERC20ToNative(balance)
	if err != nil {
		return fmt.Errorf("balance is not representable natively: %w", err)
	}
	proof := &market.AccountProof{Account: view.Account, Balance: native, Root: root, Left: view.SiblingIsLeft}
	for _, s := range view.Siblings {
		b, err := hex.DecodeString(strings.TrimPrefix(s, "0x"))
		if err != nil {
			return err
		}
		proof.Siblings = append(proof.Siblings, b)
	}
	if err := market.VerifyAccountProof(proof, root); err != nil {
		return fmt.Errorf("the escrow proof does not verify: %w", err)
	}
	if native != escrowFunds {
		return fmt.Errorf("the proof says the escrow holds %d, want %d", native, uint64(escrowFunds))
	}
	ok("escrow of %d proven against the signed root, with %d sibling hashes", native, len(proof.Siblings))
	return nil
}

// wallet is a secp256k1 key, standing in for MetaMask.
type wallet struct {
	priv    *secp256k1.PrivateKey
	address ethsig.Address
}

func newWallet() *wallet {
	priv, err := secp256k1.GeneratePrivateKey()
	if err != nil {
		panic(err)
	}
	return &wallet{priv: priv, address: ethsig.AddressFromPubKey(priv.PubKey())}
}

// checkWalletTransfer is the whole round trip a buyer performs: read a balance,
// read a nonce, sign, send, poll for a receipt, and see the balances move.
func checkWalletTransfer(nodes []*devNode, w *wallet, timeout time.Duration) error {
	step("sending a wallet-signed transfer, as MetaMask would")
	n := nodes[0]

	before, err := balanceOf(n, w.address)
	if err != nil {
		return err
	}
	if before != buyerFunds {
		return fmt.Errorf("the wallet holds %d, want the %d genesis gave it", before, uint64(buyerFunds))
	}

	nonceHex, err := n.rpcString("eth_getTransactionCount", w.address.Hex(), "pending")
	if err != nil {
		return err
	}
	nonce, err := parseHex(nonceHex)
	if err != nil {
		return err
	}

	recipient := newWallet().address
	const sent = 250_000_000_000_000
	env := &evmtx.Transaction{
		Type:      evmtx.TxLegacy,
		Nonce:     nonce,
		GasFeeCap: big.NewInt(0),
		Gas:       21000,
		To:        &recipient,
		Value:     token.NativeToERC20(sent),
	}
	if err := env.Sign(w.priv, devnetChainID); err != nil {
		return err
	}
	raw, err := env.MarshalBinary()
	if err != nil {
		return err
	}
	id, err := n.rpcString("eth_sendRawTransaction", "0x"+hex.EncodeToString(raw))
	if err != nil {
		return err
	}
	ok("submitted %s", id)

	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		receipt, err := n.rpc("eth_getTransactionReceipt", id)
		if err == nil && len(receipt) > 0 && string(receipt) != "null" {
			var r struct {
				Status string `json:"status"`
			}
			if err := json.Unmarshal(receipt, &r); err != nil {
				return err
			}
			if r.Status != "0x1" {
				return fmt.Errorf("the transfer committed but did not apply (status %s)", r.Status)
			}
			ok("receipt reports the transfer applied")

			// The fee comes out of the amount, so the recipient gets the net and the
			// sender is down the gross. Checking both is what catches a fee taken
			// twice or not at all.
			gotRecipient, err := balanceOf(n, recipient)
			if err != nil {
				return err
			}
			if gotRecipient == 0 || gotRecipient > sent {
				return fmt.Errorf("the recipient holds %d, want something at or below the %d sent",
					gotRecipient, uint64(sent))
			}
			after, err := balanceOf(n, w.address)
			if err != nil {
				return err
			}
			if after != before-sent {
				return fmt.Errorf("the sender holds %d, want %d", after, before-sent)
			}
			ok("sender down %d, recipient up %d (the difference is the protocol fee)",
				before-after, gotRecipient)
			return nil
		}
		time.Sleep(250 * time.Millisecond)
	}
	return fmt.Errorf("no receipt for %s within %s:\n%s", id, timeout, n.tail(15))
}

// balanceOf reads an address's balance and converts it back to native units.
func balanceOf(n *devNode, addr ethsig.Address) (uint64, error) {
	raw, err := n.rpcString("eth_getBalance", addr.Hex(), "latest")
	if err != nil {
		return 0, err
	}
	value, ok := new(big.Int).SetString(strings.TrimPrefix(raw, "0x"), 16)
	if !ok {
		return 0, fmt.Errorf("balance %q is not hex", raw)
	}
	return token.ERC20ToNative(value)
}

func parseHex(s string) (uint64, error) {
	return strconv.ParseUint(strings.TrimPrefix(s, "0x"), 16, 64)
}

func step(format string, args ...any) {
	fmt.Printf("\n== "+format+"\n", args...)
}

func ok(format string, args ...any) {
	fmt.Printf("   ok  "+format+"\n", args...)
}

// checkRejections proves the two refusals that carry the most weight, by
// actually making the chain refuse them rather than by reading the code that
// should.
//
// Both are properties the layout this replaced did not have at all: it carried
// no chain identifier, so a testnet signature was byte-for-byte valid on
// mainnet, and it is worth confirming on a running network rather than trusting
// a unit test that never leaves one process.
func checkRejections(n *devNode, w *wallet) error {
	step("checking what the chain refuses")

	nonceHex, err := n.rpcString("eth_getTransactionCount", w.address.Hex(), "pending")
	if err != nil {
		return err
	}
	nonce, err := parseHex(nonceHex)
	if err != nil {
		return err
	}
	recipient := newWallet().address

	sign := func(chainID, nonce uint64) (string, error) {
		env := &evmtx.Transaction{
			Type:      evmtx.TxLegacy,
			Nonce:     nonce,
			GasFeeCap: big.NewInt(0),
			Gas:       21000,
			To:        &recipient,
			Value:     token.NativeToERC20(1_000_000),
		}
		if err := env.Sign(w.priv, chainID); err != nil {
			return "", err
		}
		raw, err := env.MarshalBinary()
		if err != nil {
			return "", err
		}
		return "0x" + hex.EncodeToString(raw), nil
	}

	// Signed for a neighbouring chain. Perfectly valid bytes, a real signature by
	// a funded account, and this chain must not be able to apply it.
	foreign, err := sign(devnetChainID+1, nonce)
	if err != nil {
		return err
	}
	if id, err := n.rpcString("eth_sendRawTransaction", foreign); err == nil {
		return fmt.Errorf("a transaction signed for chain %d was accepted here as %s",
			devnetChainID+1, id)
	}
	ok("a transaction signed for another chain is refused")

	// The same nonce twice. The first is accepted, the second must not be: a
	// nonce a sender has already used cannot authorise a second payment.
	first, err := sign(devnetChainID, nonce)
	if err != nil {
		return err
	}
	if _, err := n.rpcString("eth_sendRawTransaction", first); err != nil {
		return fmt.Errorf("the first transfer at nonce %d was refused: %w", nonce, err)
	}
	// A DIFFERENT transaction at the same nonce, which is the dangerous shape:
	// resending the identical one is idempotent and harmless.
	replayed := &evmtx.Transaction{
		Type:      evmtx.TxLegacy,
		Nonce:     nonce,
		GasFeeCap: big.NewInt(0),
		Gas:       21000,
		To:        &recipient,
		Value:     token.NativeToERC20(9_000_000),
	}
	if err := replayed.Sign(w.priv, devnetChainID); err != nil {
		return err
	}
	raw, err := replayed.MarshalBinary()
	if err != nil {
		return err
	}
	if id, err := n.rpcString("eth_sendRawTransaction", "0x"+hex.EncodeToString(raw)); err == nil {
		return fmt.Errorf("a second, different transfer at nonce %d was accepted as %s", nonce, id)
	}
	ok("a second transfer reusing a spent nonce is refused")
	return nil
}

// checkGenesisSnapshot proves the one irreversible step of a relaunch.
//
// A new genesis is a new ledger: nothing carries over on its own, and a balance
// survives only because somebody wrote it into genesis.allocations. Doing that
// by hand is the step nobody can review and everybody can get wrong, and it
// happens exactly once, under a freeze, with wrapped tokens on the other side of
// a bridge depending on the escrow figure being right.
//
// So this reads the ledger of a chain that actually ran - one that has been
// through a wallet-signed transfer, so the balances are not the ones genesis
// wrote - and checks the file that comes back would be accepted by the same
// preflight a production launch runs. It needs the node stopped, because the
// store is opened directly.
func checkGenesisSnapshot(matrixd string, nodes []*devNode) error {
	step("reading a stopped node's ledger back as a relaunch genesis")

	// The store is a single-writer database, so the snapshot reads what the node
	// has committed rather than racing it.
	for _, n := range nodes {
		n.stop()
	}

	var stdout, stderr bytes.Buffer
	cmd := exec.Command(matrixd, "-genesis-snapshot", "-config", nodes[0].configPath)
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		// A non-zero exit is the snapshot saying it needs a human decision, and
		// what needs deciding is in its REPORT on stdout, not in the error.
		return fmt.Errorf("genesis snapshot failed: %v\n%s\n%s", err, stdout.String(), stderr.String())
	}
	rendered := stdout.String()

	var parsed struct {
		Genesis struct {
			Allocations []struct {
				Account string `yaml:"account"`
				Amount  uint64 `yaml:"amount"`
			} `yaml:"allocations"`
			RewardPool uint64 `yaml:"reward_pool"`
		} `yaml:"genesis"`
	}
	if err := yaml.Unmarshal([]byte(rendered), &parsed); err != nil {
		return fmt.Errorf("the snapshot is not valid yaml: %v\n%s", err, rendered)
	}

	// The supply identity is the property that matters: a relaunch that does not
	// close at the cap is one where somebody's balance was dropped or counted
	// twice, and production preflight refuses it.
	total := parsed.Genesis.RewardPool
	for _, a := range parsed.Genesis.Allocations {
		total += a.Amount
	}
	if total != nativeMaxSupply {
		return fmt.Errorf("the snapshot's supply is %d, want the cap %d - a balance was "+
			"dropped or double counted:\n%s", total, nativeMaxSupply, rendered)
	}
	if !strings.Contains(rendered, "Supply closes exactly at the cap") {
		return fmt.Errorf("the snapshot does not report that supply closes:\n%s", rendered)
	}
	if strings.Contains(rendered, "NOT READY") {
		return fmt.Errorf("the snapshot reports it is incomplete:\n%s", rendered)
	}
	ok("%d allocations plus a reward pool, totalling the supply cap exactly", len(parsed.Genesis.Allocations))

	// Paste it under a consensus block, the way the runbook tells an operator to,
	// and run the production preflight the real launch runs.
	config := fmt.Sprintf(`consensus:
  validators:
    - %s
    - %s
    - %s
  epoch_length: 100
  round_timeout: "3s"
  stake:
    enabled: true
    min_bond: 1000
    bond: 1000
%s`, nodes[0].consensusID, nodes[1].consensusID, nodes[2].consensusID, rendered)

	path := filepath.Join(filepath.Dir(nodes[0].configPath), "relaunch-genesis.yaml")
	if err := os.WriteFile(path, []byte(config), 0o600); err != nil {
		return err
	}
	out, err := exec.Command(matrixd, "-preflight-production", "-config", path).CombinedOutput()
	if err != nil {
		return fmt.Errorf("production preflight refused the snapshot's own output: %v\n%s\n\n%s",
			err, out, config)
	}
	ok("production preflight accepts it as a genesis file, unedited")
	return nil
}

// nativeMaxSupply is the cap a production genesis must close at exactly. It is
// repeated here rather than imported so this stays a black-box check: it runs
// the real binary and reads its output, the way an operator would.
const nativeMaxSupply = 1_000_000_000_000_000_000

// checkDirectory proves a buyer can find a seller without being told its address.
//
// This is the half of the marketplace that was wired and never invoked. Nodes
// subscribed to the announce topic and kept a registry of what they heard, and
// nothing anywhere published: the only caller of the publish path was a test. On
// a real network every registry was empty, so the only way to find a provider was
// to be handed its address by a person - which is not a marketplace, whatever the
// code says.
//
// So it asserts the whole round trip on a real network: node 0 announces the
// provider it is actually serving, the announcement crosses gossip, and node 1
// can name both the seller and the URL to send a prompt to. Reading the registry
// through the same RPC a buyer's client would use, not through the exchange.
func checkDirectory(matrixCLI string, nodes []*devNode, timeout time.Duration) error {
	step("finding a seller the way a buyer would, with nobody handing over an address")

	// Announcements are published on a timer, so the first one may not have been
	// sent when the earlier checks finished.
	deadline := time.Now().Add(timeout)
	var found map[string]any
	for time.Now().Before(deadline) {
		listed, err := listProviders(matrixCLI, nodes[1])
		if err != nil {
			return err
		}
		for _, p := range listed {
			// Node 1's view of node 0, which is the only interesting direction: a
			// node's own listings are not discovery.
			if asString(p["node_id"]) == nodes[0].consensusID {
				found = p
				break
			}
		}
		if found != nil {
			break
		}
		time.Sleep(250 * time.Millisecond)
	}
	if found == nil {
		return fmt.Errorf("node 1 never heard node 0 announce a provider\n%s", nodes[1].tail(15))
	}

	endpoint := asString(found["endpoint"])
	if endpoint == "" {
		return fmt.Errorf("the announcement carried no endpoint, so a buyer has nothing to connect to: %v", found)
	}
	// It has to be the address the SELLER serves on, not the reader's own: an
	// endpoint that is merely present would satisfy a check and route every buyer
	// to the wrong host.
	if endpoint != nodes[0].connect {
		return fmt.Errorf("announced endpoint is %q, want node 0's own %q", endpoint, nodes[0].connect)
	}
	if err := marketexchange.ValidateEndpoint(endpoint); err != nil {
		return fmt.Errorf("the directory carries an endpoint a buyer should not dial: %w", err)
	}
	ok("node 1 found node 0's provider at %s, with nobody configuring it", endpoint)

	// The observed half of a reputation: measured by the reader, not claimed by
	// the seller. It must be counting up, or it says nothing a buyer can use.
	if heard := asUint(found["announcements_heard"]); heard == 0 {
		return fmt.Errorf("the directory entry reports zero announcements heard: %v", found)
	}
	if asString(found["first_seen"]) == "" {
		return fmt.Errorf("the directory entry records no first sighting: %v", found)
	}
	ok("and reports what it observed itself: %d announcements heard since first contact",
		asUint(found["announcements_heard"]))
	return nil
}

// listProviders reads one node's directory through the CLI's own JSON, so the
// check sees exactly what a buyer's tooling would.
func listProviders(matrixCLI string, n *devNode) ([]map[string]any, error) {
	cmd := exec.Command(matrixCLI, "--addr", n.market, "--api-key", n.apiKey, "--json", "provider", "directory")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("provider directory on node %d: %v\n%s", n.index, err, stderr.String())
	}
	var rows []map[string]any
	if err := json.Unmarshal(out, &rows); err != nil {
		return nil, fmt.Errorf("provider directory output is not json: %v\n%s", err, out)
	}
	return rows, nil
}

func asString(v any) string {
	s, _ := v.(string)
	return s
}

func asUint(v any) uint64 {
	f, _ := v.(float64)
	return uint64(f)
}

// readAPIKey pulls the admin key `matrixd -init` generated into a node's config.
// The key is never printed at init - the generated file is 0600 and echoing it
// would put it in shell history - so reading the file is how an operator gets it
// too.
func readAPIKey(configPath string) (string, error) {
	raw, err := os.ReadFile(configPath)
	if err != nil {
		return "", err
	}
	var cfg struct {
		Security struct {
			APIKeys []struct {
				Key string `yaml:"key"`
			} `yaml:"api_keys"`
		} `yaml:"security"`
	}
	if err := yaml.Unmarshal(raw, &cfg); err != nil {
		return "", err
	}
	if len(cfg.Security.APIKeys) == 0 {
		return "", fmt.Errorf("no api key in %s", configPath)
	}
	return cfg.Security.APIKeys[0].Key, nil
}
