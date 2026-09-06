// SPDX-License-Identifier: MIT
pragma solidity 0.8.24;

import {ERC20} from "@openzeppelin/contracts/token/ERC20/ERC20.sol";
import {ERC20Permit} from "@openzeppelin/contracts/token/ERC20/extensions/ERC20Permit.sol";
import {Ownable} from "@openzeppelin/contracts/access/Ownable.sol";

/**
 * @title Matrix Compute Token (MATRIX)
 * @notice The settlement and earning currency of the Matrix OS compute
 *         marketplace. Buyers pay for LLM compute and API responses in MATRIX,
 *         and providers who contribute compute earn MATRIX.
 *
 * @dev Inherits OpenZeppelin's audited ERC20 implementation for the full
 *      standard interface (name, symbol, decimals, totalSupply, balanceOf,
 *      transfer, approve, transferFrom, allowance) and the Transfer/Approval
 *      events. ERC20Permit adds EIP-2612 gasless approvals. Ownable gates the
 *      mint function so new supply (used to back marketplace earnings) can only
 *      be created by the controlled owner, and never beyond MAX_SUPPLY.
 *
 *      Decimals are 18 (the ERC20 default), matching common exchange
 *      expectations so the token can later be listed.
 */
contract MatrixToken is ERC20, ERC20Permit, Ownable {
    /// @notice Hard cap on total supply. Minting can never exceed this amount.
    /// @dev 1,000,000,000 MATRIX expressed with 18 decimals.
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
     * @notice Owner-only mint used to back marketplace earnings for providers.
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
