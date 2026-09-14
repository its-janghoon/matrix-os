package ethrpc

import (
	"encoding/hex"
	"encoding/json"

	"github.com/ecirlabs/matrix-core/internal/ethsig"
	"github.com/ecirlabs/matrix-core/internal/market"
	"github.com/ecirlabs/matrix-core/internal/token"
)

// The three questions Ethereum's JSON-RPC has no place for, under a namespace
// that says whose they are.
//
// WHY NOT eth_getProof. Ethereum's account proof is a Merkle-Patricia trie path,
// and every tool that consumes one expects to verify it as such. This chain's
// proof is a binary Merkle path over balances, which is a different structure
// entirely, so answering eth_getProof with it would hand verifiers something
// their libraries would reject or, worse, mis-verify. A method named for this
// chain is the honest place for a proof shaped like this chain's.
//
// WHAT THE PROOF IS FOR. Wrapped supply on another chain is supposed to be
// backed one-for-one by native coins in the escrow account, and the only evidence
// for that was the attestors' word. matrix_getAccountProof on the escrow gives
// anyone the balance and a path to a root the validators signed into a block: an
// audited reserve rather than an asserted one, which is the question a listing
// review asks.

// SupplyReport is what matrix_getSupply answers.
//
// It reports COMPONENTS rather than one headline number, because "circulating"
// is a contested definition and a single figure hides which one was used. The
// components are facts; the reader can apply whichever definition they are held
// to.
type SupplyReport struct {
	// MaxSupply is the hard cap, in native base units. It can never be exceeded.
	MaxSupply uint64 `json:"max_supply"`
	// Issued is everything ever brought into existence: the genesis allocations
	// plus the reward pool, plus any later issuance.
	Issued uint64 `json:"issued"`
	// RewardPool is what remains unemitted. These coins exist but have never been
	// paid to anyone, so no definition of circulating includes them.
	RewardPool uint64 `json:"reward_pool"`
	// BridgeEscrow is native held against wrapped tokens on another chain. The
	// coins are immobile here and mobile THERE, so they are excluded from the
	// native circulating figure and counted by whoever counts the wrapped supply.
	// Double-counting them is the error this field exists to prevent.
	BridgeEscrow uint64 `json:"bridge_escrow"`
	// Circulating is Issued minus the reward pool and the escrow: coins that are
	// in someone's hands and spendable on THIS chain.
	Circulating uint64 `json:"circulating"`
	// Decimals is the native scale, 9. It is reported because every figure above
	// is in base units and a reader dividing by the wrong power is off by orders
	// of magnitude.
	Decimals int `json:"decimals"`
}

// ChainInfo is what matrix_getChainInfo answers: what this chain is, where it
// is, and what "confirmed" means on it.
//
// The last is the reason it exists. An exchange integrating a chain asks how many
// confirmations to wait, and the honest answer here is one - a block commits
// under a BFT quorum and is not revisited, so there is no reorg depth to wait
// out. That answer is worth nothing said in a document and everything said by the
// endpoint they are already polling.
type ChainInfo struct {
	// ChainID is the EIP-155 id, as a decimal string so a reader is not parsing
	// hex out of a field that is not a quantity elsewhere.
	ChainID uint64 `json:"chain_id"`
	// Height is the number of committed blocks.
	Height uint64 `json:"height"`
	// HeadHash and StateRoot are the current head and the ledger under it.
	HeadHash  string `json:"head_hash"`
	StateRoot string `json:"state_root"`
	// FinalityConfirmations is how many blocks a caller should wait before
	// treating a transfer as settled. It is 1: a committed block is final.
	FinalityConfirmations int `json:"finality_confirmations"`
	// Reorgs says whether committed blocks can be replaced. They cannot, which is
	// the property that makes the number above 1 rather than a guess.
	Reorgs bool `json:"reorgs"`
	// HasEVM says whether contracts run here. They do not, and an integrator who
	// assumes otherwise writes code against a machine that is not present.
	HasEVM bool `json:"has_evm"`
	// NativeDecimals is the ledger's scale, and EVMDecimals what the JSON-RPC
	// reports in. They differ, and a reader using the wrong one is off by a
	// billion.
	NativeDecimals int `json:"native_decimals"`
	EVMDecimals    int `json:"evm_decimals"`
}

// SupplyBackend is the part of the chain the matrix_ methods need beyond the
// Backend above. A Backend that does not implement it answers those methods with
// an error rather than a wrong number.
type SupplyBackend interface {
	// StateRoot is the Merkle root over every balance, the same value a block
	// carries.
	StateRoot() ([]byte, error)
	// AccountProof proves one account's balance against that root.
	AccountProof(accountID string) (*market.AccountProof, error)
	// Supply reports the components above.
	Supply() (SupplyReport, error)
}

// callMatrix handles the matrix_ namespace.
func (h *Handler) callMatrix(method string, params []json.RawMessage) (any, *rpcError) {
	backend, ok := h.cfg.Backend.(SupplyBackend)
	if !ok {
		return nil, &rpcError{Code: codeMethodNotFound,
			Message: "this node does not serve the matrix_ methods"}
	}

	switch method {
	case "matrix_getStateRoot":
		root, err := backend.StateRoot()
		if err != nil {
			return nil, &rpcError{Code: codeInternal, Message: err.Error()}
		}
		return "0x" + hex.EncodeToString(root), nil

	case "matrix_getChainInfo":
		root, err := backend.StateRoot()
		if err != nil {
			return nil, &rpcError{Code: codeInternal, Message: err.Error()}
		}
		head := ""
		if height := h.cfg.Backend.ChainHeight(); height > 0 {
			if block, ok := h.cfg.Backend.BlockByHeight(height - 1); ok {
				head = "0x" + hex.EncodeToString(block.Hash)
			}
		}
		return ChainInfo{
			ChainID:   h.cfg.ChainID,
			Height:    h.cfg.Backend.ChainHeight(),
			HeadHash:  head,
			StateRoot: "0x" + hex.EncodeToString(root),
			// One, and not a cautious larger number. A block commits under a BFT
			// quorum and is never revisited, so waiting longer buys nothing and
			// telling an integrator to wait longer would be inventing a risk.
			FinalityConfirmations: 1,
			Reorgs:                false,
			HasEVM:                false,
			NativeDecimals:        9,
			EVMDecimals:           token.EVMDecimals,
		}, nil

	case "matrix_getSupply":
		report, err := backend.Supply()
		if err != nil {
			return nil, &rpcError{Code: codeInternal, Message: err.Error()}
		}
		return report, nil

	case "matrix_getAccountProof":
		account, rpcErr := proofAccountParam(params)
		if rpcErr != nil {
			return nil, rpcErr
		}
		proof, err := backend.AccountProof(account)
		if err != nil {
			// Not-found is an error here rather than null: a caller asking for a
			// proof wants one, and a silent null reads as "proved nothing", which
			// is not the same as "this account has no balance to prove".
			return nil, &rpcError{Code: codeInvalidParams, Message: err.Error()}
		}
		return accountProofView(proof), nil

	default:
		return nil, &rpcError{Code: codeMethodNotFound, Message: "unsupported method " + method}
	}
}

// proofAccountParam reads the account to prove. It accepts an address, which is
// what a caller holding a wallet has, and a raw account id, which is what a
// caller asking about the escrow or an ed25519 account has.
func proofAccountParam(params []json.RawMessage) (string, *rpcError) {
	if len(params) < 1 {
		return "", &rpcError{Code: codeInvalidParams, Message: "an account is required"}
	}
	var s string
	if err := json.Unmarshal(params[0], &s); err != nil {
		return "", &rpcError{Code: codeInvalidParams, Message: "the account must be a string"}
	}
	if s == "" {
		return "", &rpcError{Code: codeInvalidParams, Message: "an account is required"}
	}
	// An address is the common case and is given in Ethereum's own form, so it is
	// translated here rather than making every caller know this chain's account
	// id spelling.
	if addr, err := ethsig.ParseAddress(s); err == nil {
		return token.EthAccountID(addr), nil
	}
	return s, nil
}

// accountProofView renders a proof for the wire, hex-encoding what is bytes.
func accountProofView(proof *market.AccountProof) map[string]any {
	siblings := make([]string, 0, len(proof.Siblings))
	for _, s := range proof.Siblings {
		siblings = append(siblings, "0x"+hex.EncodeToString(s))
	}
	return map[string]any{
		"account": proof.Account,
		// Reported in the wallet's 18-decimal scale to match eth_getBalance, so a
		// caller comparing the two is not comparing two scales.
		"balance":  hexBig(token.NativeToERC20(proof.Balance)),
		"root":     "0x" + hex.EncodeToString(proof.Root),
		"siblings": siblings,
		// siblingIsLeft says which side each sibling sits on. Without it a verifier
		// would have to guess the order and half the proofs would fold to the wrong
		// root.
		"siblingIsLeft": proof.Left,
	}
}
