// SPDX-License-Identifier: MIT
pragma solidity 0.8.24;

import {ERC20} from "@openzeppelin/contracts/token/ERC20/ERC20.sol";
import {ERC20Permit} from "@openzeppelin/contracts/token/ERC20/extensions/ERC20Permit.sol";

/**
 * @title Wrapped Matrix (wMATRIX) - the bridged mirror of native MATRIX
 * @notice wMATRIX is the Ethereum-side WRAPPED representation of native MATRIX.
 *         Native MATRIX is the canonical coin of the Matrix OS consensus L1 (9
 *         decimals; the single source of truth for balances and supply). This
 *         contract is NOT the primary settlement token: all marketplace and LLM
 *         inference settlement happens in native MATRIX on the L1. wMATRIX only
 *         exists so native MATRIX can be represented on Ethereum (e.g. for a
 *         future exchange listing) while remaining backed 1:1 by native MATRIX
 *         locked on the L1.
 *
 * @dev Lock-and-mint bridge, Ethereum side:
 *
 *      mint  (native lock -> wrapped): the native bridge (internal/bridge) locks
 *            native MATRIX into an on-L1 escrow account and the validator set
 *            produces secp256k1 attestations over the canonical digest
 *            keccak256(recipient || amount || lockId || chainId || this). This
 *            contract mints `amount` wMATRIX to `recipient` only after verifying
 *            a THRESHOLD of signatures from DISTINCT registered attestor
 *            addresses, and only once per lockId (replay protection).
 *
 *      burn  (wrapped -> native unlock): a holder burns wMATRIX and names the
 *            native recipient account. The emitted Burn event is the authority
 *            the native bridge uses to release the escrowed native MATRIX. The
 *            burn amount is denominated in 18-decimal wMATRIX and must be an
 *            exact multiple of ERC20_PER_NATIVE_UNIT so it maps losslessly back
 *            to native base units.
 *
 *      NATIVE <-> WRAPPED SCALE (single source of truth:
 *      services/core/internal/token/supply.go): native has 9 decimals, this
 *      wrapped token has 18, so 1 native base unit == 1e9 wMATRIX base units
 *      (ERC20_PER_NATIVE_UNIT). The native cap of 1e18 base units maps to a
 *      wrapped cap of 1e27 (== 1,000,000,000 whole MATRIX * 1e18), the same
 *      money at both scales.
 *
 *      The ed25519 keys that secure L1 consensus cannot be verified by the EVM,
 *      whose only signature precompile is ecrecover (secp256k1). Each validator
 *      therefore also holds a secp256k1 "attestor" key whose address is
 *      registered here; the Go signer (internal/bridge.Attestor) produces
 *      ecrecover-compatible signatures over the identical digest.
 *
 *      Relationship to MatrixToken.sol: MatrixToken is an earlier standalone
 *      ERC-20 mirror kept for its existing test/deploy surface. WrappedMatrix is
 *      the bridge-backed wrapped token whose supply is minted/burned ONLY through
 *      verified attestations, so its total supply always equals the outstanding
 *      locked native. Prefer WrappedMatrix for the real bridge; MatrixToken
 *      documents the same native-first, wrapped-mirror monetary story.
 */
contract WrappedMatrix is ERC20, ERC20Permit {
    /// @notice ERC-20 base units per native base unit (18 decimals vs 9), the
    ///         one conversion factor shared with supply.go. Burn amounts must be
    ///         exact multiples of this so they map losslessly to native units.
    uint256 public constant ERC20_PER_NATIVE_UNIT = 1e9;

    /// @notice The number of DISTINCT registered attestor signatures required to
    ///         authorize a mint (the m in m-of-n).
    uint256 public immutable threshold;

    /// @notice Registered attestor (validator secp256k1) addresses allowed to
    ///         sign mint attestations.
    mapping(address => bool) public isAttestor;

    /// @notice Number of registered attestors (the n in m-of-n).
    uint256 public immutable attestorCount;

    /// @notice Tracks lockIds that have already been minted, preventing replay.
    mapping(bytes32 => bool) public mintedLockId;

    /// @notice Emitted when a native lock is minted into wrapped tokens.
    event Minted(bytes32 indexed lockId, address indexed recipient, uint256 amount);

    /// @notice Emitted when wrapped tokens are burned to authorize a native
    ///         unlock. `nativeRecipient` is the L1 account id (an opaque string,
    ///         e.g. a 64-hex ed25519 account id) that should receive the released
    ///         native MATRIX; `amount` is the burned wMATRIX (18 decimals).
    event Burned(address indexed burner, string nativeRecipient, uint256 amount);

    /// @notice Raised when the attestor set or threshold is misconfigured.
    error InvalidAttestorSet();
    /// @notice Raised when an attestor address is zero or duplicated at construction.
    error InvalidAttestor(address attestor);
    /// @notice Raised when a lockId has already been minted.
    error LockAlreadyMinted(bytes32 lockId);
    /// @notice Raised when fewer than `threshold` valid, distinct attestor
    ///         signatures were supplied.
    error ThresholdNotMet(uint256 got, uint256 need);
    /// @notice Raised when a signature does not recover to a registered attestor
    ///         or signatures are not in strictly ascending signer order.
    error InvalidSignature();
    /// @notice Raised when a burn amount is not an exact multiple of
    ///         ERC20_PER_NATIVE_UNIT (so it could not map to native base units).
    error NonMultipleBurn(uint256 amount);
    /// @notice Raised when a mint or burn amount is zero.
    error ZeroAmount();

    /**
     * @param attestors The validator secp256k1 attestor addresses (the n).
     * @param threshold_ The number of distinct signatures required to mint (m),
     *        1 <= m <= n.
     */
    constructor(address[] memory attestors, uint256 threshold_)
        ERC20("Wrapped Matrix", "wMATRIX")
        ERC20Permit("Wrapped Matrix")
    {
        uint256 n = attestors.length;
        if (n == 0 || threshold_ == 0 || threshold_ > n) {
            revert InvalidAttestorSet();
        }
        for (uint256 i = 0; i < n; i++) {
            address a = attestors[i];
            if (a == address(0) || isAttestor[a]) {
                revert InvalidAttestor(a);
            }
            isAttestor[a] = true;
        }
        attestorCount = n;
        threshold = threshold_;
    }

    /**
     * @notice The canonical attestation digest validators sign. It is the tight
     *         keccak256(abi.encodePacked(...)) of recipient, amount, lockId, the
     *         chain id, and this contract address, byte-identical to
     *         internal/bridge.AttestationDigest on the Go side.
     */
    function attestationDigest(address recipient, uint256 amount, bytes32 lockId)
        public
        view
        returns (bytes32)
    {
        return keccak256(
            abi.encodePacked(recipient, amount, lockId, block.chainid, address(this))
        );
    }

    /**
     * @notice Mint wrapped tokens for a verified native lock.
     * @dev Requires `threshold` signatures from DISTINCT registered attestors
     *      over attestationDigest(recipient, amount, lockId). Signatures MUST be
     *      ordered by ascending recovered signer address; this both enforces
     *      distinctness cheaply and rejects duplicate signers. Reverts if the
     *      lockId was already minted (replay), on any invalid/foreign signature,
     *      or if fewer than `threshold` valid signatures are supplied.
     * @param recipient Address to receive the minted wMATRIX.
     * @param amount Wrapped amount (18 decimals) == locked native * 1e9.
     * @param lockId Unique native lock identifier (bytes32).
     * @param signatures Array of 65-byte {r,s,v} secp256k1 signatures.
     */
    function mint(
        address recipient,
        uint256 amount,
        bytes32 lockId,
        bytes[] calldata signatures
    ) external {
        if (amount == 0) revert ZeroAmount();
        if (mintedLockId[lockId]) revert LockAlreadyMinted(lockId);
        if (signatures.length < threshold) {
            revert ThresholdNotMet(signatures.length, threshold);
        }

        bytes32 digest = attestationDigest(recipient, amount, lockId);

        uint256 valid;
        address last = address(0);
        for (uint256 i = 0; i < signatures.length; i++) {
            address signer = _recover(digest, signatures[i]);
            // Strictly ascending order guarantees each signer is counted once
            // (no duplicates) without an auxiliary mapping.
            if (signer <= last) revert InvalidSignature();
            if (!isAttestor[signer]) revert InvalidSignature();
            last = signer;
            valid++;
        }
        if (valid < threshold) revert ThresholdNotMet(valid, threshold);

        mintedLockId[lockId] = true;
        _mint(recipient, amount);
        emit Minted(lockId, recipient, amount);
    }

    /**
     * @notice Burn wrapped tokens to authorize releasing native MATRIX on the L1.
     * @dev Emits Burned(msg.sender, nativeRecipient, amount). The native bridge
     *      watches for this event and unlocks `amount / 1e9` native base units to
     *      `nativeRecipient`, exactly once per event. amount must be a nonzero
     *      exact multiple of ERC20_PER_NATIVE_UNIT.
     * @param amount Wrapped amount to burn (18 decimals).
     * @param nativeRecipient The L1 account id to receive the unlocked native.
     */
    function burn(uint256 amount, string calldata nativeRecipient) external {
        if (amount == 0) revert ZeroAmount();
        if (amount % ERC20_PER_NATIVE_UNIT != 0) revert NonMultipleBurn(amount);
        _burn(msg.sender, amount);
        emit Burned(msg.sender, nativeRecipient, amount);
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
