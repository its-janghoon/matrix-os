// SPDX-License-Identifier: MIT
pragma solidity 0.8.24;

import {ERC20} from "@openzeppelin/contracts/token/ERC20/ERC20.sol";
import {ERC20Permit} from "@openzeppelin/contracts/token/ERC20/extensions/ERC20Permit.sol";
import {Ownable} from "@openzeppelin/contracts/access/Ownable.sol";

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
 *      events. ERC20Permit adds EIP-2612 gasless approvals. Ownable gates the
 *      mint function so wrapped supply is created only by the controlled owner,
 *      and never beyond MAX_SUPPLY.
 *
 *      Decimals are 18 (the ERC20 default), matching common exchange
 *      expectations so the token can later be listed.
 *
 *      Relationship to WrappedMatrix.sol: this MatrixToken is the original,
 *      owner-minted ERC-20 mirror (kept for its existing deploy/test surface and
 *      EIP-2612 permit support). The lock-and-mint BRIDGE uses WrappedMatrix.sol
 *      (wMATRIX) instead, whose supply is minted/burned ONLY through verified
 *      validator attestations of native locks/burns so it stays backed 1:1 by
 *      locked native MATRIX. Both tell the same native-first, wrapped-mirror
 *      monetary story; WrappedMatrix is the bridge-enforced variant.
 */
contract MatrixToken is ERC20, ERC20Permit, Ownable {
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

    /// @notice Raised when a mint would push total supply above MAX_SUPPLY.
    error MaxSupplyExceeded(uint256 requested, uint256 cap);

    /**
     * @param initialSupply Amount (in whole token units, already scaled by
     *        10**18) minted to the deployer on construction. Must not exceed
     *        MAX_SUPPLY.
     */
    constructor(uint256 initialSupply)
        ERC20("Matrix Compute Token", "MATRIX")
        ERC20Permit("Matrix Compute Token")
        Ownable(msg.sender)
    {
        if (initialSupply > MAX_SUPPLY) {
            revert MaxSupplyExceeded(initialSupply, MAX_SUPPLY);
        }
        _mint(msg.sender, initialSupply);
    }

    /**
     * @notice Owner-only mint for this wrapped mirror. Native MATRIX earnings
     *         are settled on the L1; minting here only issues the wrapped ERC-20
     *         representation.
     * @dev Reverts if the new total supply would exceed MAX_SUPPLY.
     * @param to Recipient of the newly minted tokens.
     * @param amount Amount to mint (scaled by 10**18).
     */
    function mint(address to, uint256 amount) external onlyOwner {
        if (totalSupply() + amount > MAX_SUPPLY) {
            revert MaxSupplyExceeded(totalSupply() + amount, MAX_SUPPLY);
        }
        _mint(to, amount);
    }
}
