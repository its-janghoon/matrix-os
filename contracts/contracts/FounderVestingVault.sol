// SPDX-License-Identifier: MIT
pragma solidity 0.8.24;

import {IERC20} from "@openzeppelin/contracts/token/ERC20/IERC20.sol";
import {SafeERC20} from "@openzeppelin/contracts/token/ERC20/utils/SafeERC20.sol";
import {Math} from "@openzeppelin/contracts/utils/math/Math.sol";

/**
 * @title FounderVestingVault
 * @notice Immutable, non-upgradeable vesting for the founder's backed wMATRIX
 *         allocation. There is no owner, admin, recovery path, beneficiary
 *         rotation, or early release authority.
 *
 * Schedule: nothing before start + cliff. At the one-year cliff, the amount
 * vested since start becomes releasable (20% for a one-year cliff over a
 * five-year duration). The remainder then vests continuously until the full
 * duration. Anyone may call release(), but tokens always go to beneficiary.
 *
 * The constructor also pins the reviewed start to a small window around the
 * deployment block. This prevents custody or mempool delay from silently
 * deploying an immutable schedule whose reviewed start has already gone stale.
 *
 * The vault never mints. Launch must first lock the same native MATRIX in the
 * consensus bridge escrow, gather validator attestations, and mint exactly the
 * allocation to this vault. That ceremony keeps founder wMATRIX 1:1 backed.
 */
contract FounderVestingVault {
    using SafeERC20 for IERC20;

    uint64 public constant MAX_START_PAST_SKEW = 5 minutes;
    uint64 public constant MAX_START_FUTURE = 5 minutes;

    IERC20 public immutable token;
    address public immutable beneficiary;
    uint64 public immutable start;
    uint64 public immutable cliffDuration;
    uint64 public immutable duration;
    uint256 public immutable totalAllocation;

    uint256 public released;

    event Released(address indexed beneficiary, uint256 amount, uint256 totalReleased);

    error ZeroAddress();
    error InvalidStart();
    error InvalidSchedule();
    error ZeroAllocation();
    error NothingReleasable();

    constructor(
        IERC20 token_,
        address beneficiary_,
        uint64 start_,
        uint64 cliffDuration_,
        uint64 duration_,
        uint256 totalAllocation_
    ) {
        if (address(token_) == address(0) || beneficiary_ == address(0)) revert ZeroAddress();
        if (
            uint256(start_) + MAX_START_PAST_SKEW < block.timestamp ||
            uint256(start_) > block.timestamp + MAX_START_FUTURE
        ) revert InvalidStart();
        if (duration_ == 0 || cliffDuration_ > duration_) revert InvalidSchedule();
        if (totalAllocation_ == 0) revert ZeroAllocation();

        token = token_;
        beneficiary = beneficiary_;
        start = start_;
        cliffDuration = cliffDuration_;
        duration = duration_;
        totalAllocation = totalAllocation_;
    }

    /** @notice Allocation vested at timestamp, independent of vault funding. */
    function vestedAmount(uint64 timestamp) public view returns (uint256) {
        if (timestamp < start + cliffDuration) return 0;
        if (timestamp >= start + duration) return totalAllocation;
        return Math.mulDiv(totalAllocation, timestamp - start, duration);
    }

    /** @notice Amount currently available for partial release. */
    function releasable() public view returns (uint256) {
        return vestedAmount(uint64(block.timestamp)) - released;
    }

    /**
     * @notice Releases all currently vested, unreleased tokens to beneficiary.
     *         Any caller may trigger it; no caller can redirect the transfer.
     */
    function release() external returns (uint256 amount) {
        amount = releasable();
        if (amount == 0) revert NothingReleasable();

        released += amount;
        token.safeTransfer(beneficiary, amount);
        emit Released(beneficiary, amount, released);
    }
}
