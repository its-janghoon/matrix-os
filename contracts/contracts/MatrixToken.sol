// SPDX-License-Identifier: MIT
pragma solidity 0.8.24;

import {ERC20} from "@openzeppelin/contracts/token/ERC20/ERC20.sol";
import {ERC20Permit} from "@openzeppelin/contracts/token/ERC20/extensions/ERC20Permit.sol";

/**
 * @title Matrix Compute Token (MATRIX) - a wrapped mirror of native MATRIX
 * @notice A standards-compliant ERC-20 MIRROR of native MATRIX for Ethereum.
 *         MATRIX is native-first: the canonical coin lives on the Matrix OS
 *         consensus L1 (9 decimals) and is the single source of truth for
 *         balances and supply. All marketplace compute and LLM inference
 *         settlement happens in NATIVE MATRIX on the L1 via consensus, NOT in
 *         this ERC-20. This token is a wrapped/bridged representation intended
 *         for a future exchange listing, not the primary settlement currency.
 *
 * @dev Inherits OpenZeppelin's audited ERC20 implementation for the full
 *      standard interface (name, symbol, decimals, totalSupply, balanceOf,
 *      transfer, approve, transferFrom, allowance) and the Transfer/Approval
 *      events. ERC20Permit adds EIP-2612 gasless approvals.
 *
 *      MINTING IS m-of-n AND TIMELOCKED. It used to be `onlyOwner`: one address
 *      could issue up to MAX_SUPPLY (1e27) in a single transaction, with no
 *      warning to holders. That single key was the whole security of the wrapped
 *      supply, which is exactly the trust anchor WrappedMatrix.sol's m-of-n
 *      attestor design was built to avoid - and this contract sat next to it
 *      without the same protection.
 *
 *      Ownable is therefore gone, not merely pointed at a multisig. Pointing an
 *      owner at a Safe is a deploy-time convention that nothing enforces: the
 *      next deploy script, or a later transferOwnership, puts an EOA back in
 *      charge and the contract cannot tell. The threshold is now part of the
 *      contract, so no deployment of it has a single minting key.
 *
 *      Two independent conditions gate every mint:
 *
 *        1. A THRESHOLD of distinct registered minter signatures (m-of-n), the
 *           same construction and the same signature rules as WrappedMatrix.
 *        2. A TIMELOCK. A mint is proposed on-chain, is publicly visible for
 *           MINT_DELAY, and only then can be executed. m-of-n alone bounds who
 *           can mint; it does nothing about speed, and a compromised set that
 *           can issue to the cap in one block leaves holders no time to react.
 *           The delay is what turns a silent issuance into a visible one.
 *
 *      MAX_SUPPLY still bounds the total, and there is deliberately no authority
 *      that can raise it, shorten the delay, or change the minter set: all three
 *      are immutable, so changing any of them means redeploying, which is a
 *      visible, governable act rather than a call nobody sees.
 *
 *      Decimals are 18 (the ERC20 default), matching common exchange
 *      expectations so the token can later be listed.
 *
 *      Relationship to WrappedMatrix.sol: this MatrixToken is the standalone
 *      ERC-20 mirror (kept for its existing deploy/test surface and EIP-2612
 *      permit support), now minted under the same m-of-n discipline as the
 *      bridge token plus a timelock. The lock-and-mint BRIDGE uses WrappedMatrix.sol
 *      (wMATRIX) instead, whose supply is minted/burned ONLY through verified
 *      validator attestations of native locks/burns so it stays backed 1:1 by
 *      locked native MATRIX. Both tell the same native-first, wrapped-mirror
 *      monetary story; WrappedMatrix is the bridge-enforced variant.
 */
contract MatrixToken is ERC20, ERC20Permit {
    /// @notice Hard cap on total supply. Minting can never exceed this amount.
    /// @dev 1,000,000,000 MATRIX expressed with 18 decimals.
    ///
    ///      NATIVE <-> WRAPPED SCALE (single source of truth:
    ///      services/core/internal/token/supply.go). MATRIX is native-first: the
    ///      canonical coin lives on the consensus L1 with 9 decimals (1 whole
    ///      MATRIX = 1e9 native base units, native cap = 1e18 base units, which
    ///      fits a uint64). This ERC-20 is the wrapped mirror with 18 decimals, so
    ///      1 native base unit == 1e9 ERC-20 base units (ERC20PerNativeUnit in
    ///      supply.go) and the caps line up exactly: native 1e18 base units maps
    ///      to this MAX_SUPPLY of 1e9 * 1e18 = 1e27. The lock-and-mint bridge
    ///      (FEAT-004) must use that 1e9 factor so wrapped supply is backed 1:1 by
    ///      locked native.
    uint256 public constant MAX_SUPPLY = 1_000_000_000 * 10 ** 18;

    /// @notice How long a proposed mint must sit, publicly visible, before it
    ///         can be executed.
    /// @dev Immutable and with no shortening authority. An authority that could
    ///      shorten it would make the delay worthless in the exact case it
    ///      exists for - a compromised minter set - because whoever holds the
    ///      set would shorten it first.
    uint256 public constant MINT_DELAY = 2 days;

    /// @notice How long a proposed mint stays executable after its delay
    ///         elapses, after which it expires and must be proposed again.
    /// @dev Without an expiry a proposal is a standing option to mint: signed
    ///      once, executable years later by anyone holding the signatures, long
    ///      after the reason for it has passed.
    uint256 public constant MINT_WINDOW = 7 days;

    /// @notice Distinct registered minter signatures required to propose a mint
    ///         (the m in m-of-n).
    uint256 public immutable threshold;

    /// @notice Number of registered minters (the n in m-of-n).
    uint256 public immutable minterCount;

    /// @notice Addresses whose signatures count toward the threshold.
    /// @dev Immutable in effect: set at construction with no add/remove
    ///      function, for the same reason the delay cannot be shortened.
    mapping(address => bool) public isMinter;

    /// @notice Per-proposal nonce, so two identical mints are two proposals and
    ///         one set of signatures cannot authorize a second mint.
    uint256 public mintNonce;

    /// @notice When each proposed mint became pending. Zero means no such
    ///         proposal (or it was already executed or cancelled).
    mapping(bytes32 => uint256) public mintProposedAt;

    /// @notice Emitted when a mint is proposed and starts its timelock.
    event MintProposed(bytes32 indexed id, address indexed to, uint256 amount, uint256 nonce, uint256 executableAt);
    /// @notice Emitted when a proposed mint is executed after its timelock.
    event MintExecuted(bytes32 indexed id, address indexed to, uint256 amount);
    /// @notice Emitted when a pending mint is cancelled by the threshold.
    event MintCancelled(bytes32 indexed id);

    /// @notice Raised when a mint would push total supply above MAX_SUPPLY.
    error MaxSupplyExceeded(uint256 requested, uint256 cap);
    /// @notice Raised when the minter set or threshold is misconfigured.
    error InvalidMinterSet();
    /// @notice Raised when a minter address is zero or duplicated at construction.
    error InvalidMinter(address minter);
    /// @notice Raised when fewer than `threshold` valid, distinct minter
    ///         signatures were supplied.
    error ThresholdNotMet(uint256 got, uint256 need);
    /// @notice Raised when a signature does not recover to a registered minter
    ///         or signatures are not in strictly ascending signer order.
    error InvalidSignature();
    /// @notice Raised when a mint amount is zero.
    error ZeroAmount();
    /// @notice Raised when the same proposal is submitted while already pending.
    error MintAlreadyProposed(bytes32 id);
    /// @notice Raised when executing or cancelling a mint that was never proposed.
    error MintNotProposed(bytes32 id);
    /// @notice Raised when a mint is executed before its timelock elapses.
    error MintNotReady(bytes32 id, uint256 executableAt);
    /// @notice Raised when a mint is executed after its window has closed.
    error MintExpired(bytes32 id, uint256 expiredAt);

    /**
     * @param initialSupply Amount (in whole token units, already scaled by
     *        10**18) minted at construction. Must not exceed MAX_SUPPLY.
     * @param initialHolder Who receives initialSupply. It is a parameter rather
     *        than msg.sender because the deploying EOA is the one address that
     *        should not end up holding the float by default.
     * @param minters Addresses whose signatures authorize a mint (the n).
     * @param threshold_ Distinct signatures required (m), 1 <= m <= n. A
     *        threshold of 1 with a single minter is accepted - a local dev chain
     *        needs it - but it is the configuration this contract exists to
     *        avoid on a real network, and deploy-mainnet.ts refuses it.
     */
    constructor(
        uint256 initialSupply,
        address initialHolder,
        address[] memory minters,
        uint256 threshold_
    )
        ERC20("Matrix Compute Token", "MATRIX")
        ERC20Permit("Matrix Compute Token")
    {
        if (initialSupply > MAX_SUPPLY) {
            revert MaxSupplyExceeded(initialSupply, MAX_SUPPLY);
        }
        uint256 n = minters.length;
        if (n == 0 || threshold_ == 0 || threshold_ > n) {
            revert InvalidMinterSet();
        }
        for (uint256 i = 0; i < n; i++) {
            address m = minters[i];
            if (m == address(0) || isMinter[m]) {
                revert InvalidMinter(m);
            }
            isMinter[m] = true;
        }
        minterCount = n;
        threshold = threshold_;

        if (initialSupply > 0) {
            if (initialHolder == address(0)) revert InvalidMinter(address(0));
            _mint(initialHolder, initialSupply);
        }
    }

    /**
     * @notice The digest a minter signs to authorize one mint.
     * @dev Bound to this contract and this chain id so a signature cannot be
     *      replayed against another deployment or another chain, and to `nonce`
     *      so it authorizes exactly one mint. Packing is fixed-width
     *      (address, uint256, uint256) with no dynamic field, so no two distinct
     *      inputs can produce the same encoding.
     */
    function mintDigest(address to, uint256 amount, uint256 nonce) public view returns (bytes32) {
        return keccak256(abi.encodePacked(to, amount, nonce, block.chainid, address(this)));
    }

    /// @notice The id a proposed mint is tracked by.
    function mintId(address to, uint256 amount, uint256 nonce) public pure returns (bytes32) {
        return keccak256(abi.encode(to, amount, nonce));
    }

    /**
     * @notice Propose a mint, starting its timelock. It becomes executable
     *         MINT_DELAY later and expires MINT_WINDOW after that.
     * @dev Requires `threshold` signatures from DISTINCT registered minters over
     *      mintDigest(to, amount, mintNonce). Signatures MUST be ordered by
     *      ascending recovered signer address, which enforces distinctness
     *      without an auxiliary mapping. Anyone may submit them; holding the
     *      signatures is the authority, not being the sender.
     *
     *      The cap is checked here AND at execution. Here so an impossible
     *      proposal fails immediately rather than sitting for two days; again at
     *      execution because totalSupply can change in between, and the check
     *      that actually protects holders is the one at the moment of minting.
     */
    function proposeMint(address to, uint256 amount, bytes[] calldata signatures) external returns (bytes32) {
        if (amount == 0) revert ZeroAmount();
        if (to == address(0)) revert InvalidMinter(address(0));
        _requireCapRoom(amount);

        uint256 nonce = mintNonce;
        _verifyMinterSignatures(mintDigest(to, amount, nonce), signatures);

        bytes32 id = mintId(to, amount, nonce);
        if (mintProposedAt[id] != 0) revert MintAlreadyProposed(id);

        mintNonce = nonce + 1;
        mintProposedAt[id] = block.timestamp;
        emit MintProposed(id, to, amount, nonce, block.timestamp + MINT_DELAY);
        return id;
    }

    /**
     * @notice Execute a mint whose timelock has elapsed and whose window is open.
     * @dev Callable by anyone: the authorization was the threshold of signatures
     *      at proposal time, and the delay has run. Requiring a privileged
     *      sender here would add a key that can withhold an authorized mint
     *      without adding one that can cause an unauthorized one.
     */
    function executeMint(address to, uint256 amount, uint256 nonce) external {
        bytes32 id = mintId(to, amount, nonce);
        uint256 proposedAt = mintProposedAt[id];
        if (proposedAt == 0) revert MintNotProposed(id);

        uint256 executableAt = proposedAt + MINT_DELAY;
        if (block.timestamp < executableAt) revert MintNotReady(id, executableAt);
        uint256 expiresAt = executableAt + MINT_WINDOW;
        if (block.timestamp > expiresAt) revert MintExpired(id, expiresAt);

        _requireCapRoom(amount);

        // Cleared before minting so a reentrant call cannot execute it twice.
        // _mint on a plain ERC20 has no external call, so this is belt and
        // braces rather than a live hole - but the ordering costs nothing and
        // this contract may gain hooks.
        delete mintProposedAt[id];
        _mint(to, amount);
        emit MintExecuted(id, to, amount);
    }

    /**
     * @notice Cancel a pending mint. Requires the same threshold that proposed
     *         it, signing over the same digest.
     * @dev This is what makes the delay useful rather than merely slow: a set
     *      that notices a bad or coerced proposal during the two days can stop
     *      it, instead of watching it become executable.
     */
    function cancelMint(address to, uint256 amount, uint256 nonce, bytes[] calldata signatures) external {
        bytes32 id = mintId(to, amount, nonce);
        if (mintProposedAt[id] == 0) revert MintNotProposed(id);
        _verifyMinterSignatures(mintDigest(to, amount, nonce), signatures);
        delete mintProposedAt[id];
        emit MintCancelled(id);
    }

    /// @dev Reverts unless `amount` still fits under MAX_SUPPLY.
    function _requireCapRoom(uint256 amount) private view {
        uint256 wouldBe = totalSupply() + amount;
        if (wouldBe > MAX_SUPPLY) revert MaxSupplyExceeded(wouldBe, MAX_SUPPLY);
    }

    /**
     * @dev Requires `threshold` valid signatures from distinct registered
     *      minters over `digest`, in strictly ascending signer order. Same rules
     *      as WrappedMatrix._recover, deliberately: one signature convention
     *      across both contracts means one thing for an off-chain signer to get
     *      right.
     */
    function _verifyMinterSignatures(bytes32 digest, bytes[] calldata signatures) private view {
        if (signatures.length < threshold) {
            revert ThresholdNotMet(signatures.length, threshold);
        }
        uint256 valid;
        address last = address(0);
        for (uint256 i = 0; i < signatures.length; i++) {
            address signer = _recover(digest, signatures[i]);
            if (signer <= last) revert InvalidSignature();
            if (!isMinter[signer]) revert InvalidSignature();
            last = signer;
            valid++;
        }
        if (valid < threshold) revert ThresholdNotMet(valid, threshold);
    }

    /**
     * @dev Recovers the signer of a 65-byte {r,s,v} secp256k1 signature over
     *      `digest`. v is normalized to {27,28}. Reverts InvalidSignature on a
     *      malformed length, a malleable (high-S) signature, or a zero recovery.
     */
    function _recover(bytes32 digest, bytes calldata sig) private pure returns (address) {
        if (sig.length != 65) revert InvalidSignature();
        bytes32 r;
        bytes32 s;
        uint8 v;
        assembly {
            r := calldataload(sig.offset)
            s := calldataload(add(sig.offset, 32))
            v := byte(0, calldataload(add(sig.offset, 64)))
        }
        if (v < 27) {
            v += 27;
        }
        if (v != 27 && v != 28) revert InvalidSignature();
        // Reject the upper range of s to prevent signature malleability (EIP-2).
        if (uint256(s) > 0x7FFFFFFFFFFFFFFFFFFFFFFFFFFFFFFF5D576E7357A4501DDFE92F46681B20A0) {
            revert InvalidSignature();
        }
        address signer = ecrecover(digest, v, r, s);
        if (signer == address(0)) revert InvalidSignature();
        return signer;
    }
}
