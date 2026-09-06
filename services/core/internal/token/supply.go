package token

import (
	"errors"
	"fmt"
	"math/big"
)

// This file is the SINGLE SOURCE OF TRUTH for the native MATRIX monetary scale
// and its exact, bidirectional conversion to the wrapped ERC-20 representation.
// Both the native consensus L1 (this Go module) and the Ethereum bridge/deploy
// math (contracts/) MUST reference the ratios defined here so the native coin
// and its wrapped mirror tell one consistent monetary story.
//
// Why two scales at all:
//
// The wrapped ERC-20 (contracts/contracts/MatrixToken.sol) uses the common
// 18-decimal convention and a hard cap of 1,000,000,000 whole MATRIX, i.e.
// MAX_SUPPLY = 1e9 * 1e18 = 1e27 ERC-20 base units. That value does NOT fit in a
// uint64 (max ~1.844e19), and the native ledger stores every balance as a
// big-endian uint64 under market/balance/<account>. Holding the ERC-20's raw
// base-unit supply in a uint64 is therefore impossible.
//
// The native chain instead uses a SMALLER base-unit scale that fits uint64 and
// maps to the ERC-20 by an exact integer factor:
//
//	whole MATRIX (the human unit)      : 1
//	native base units per whole MATRIX : NativeUnit          = 1e9  (9 native decimals)
//	ERC-20 base units per whole MATRIX : 1e18                       (18 ERC-20 decimals)
//	ERC-20 base units per NATIVE unit  : ERC20PerNativeUnit  = 1e9  (= 1e18 / 1e9)
//
// So the native chain has 9 decimals and the wrapped ERC-20 has 18; the extra 9
// decimals on the wrapped side are pure trailing zeros. Every native base unit
// converts to exactly 1e9 ERC-20 base units, and any ERC-20 amount that is an
// exact multiple of 1e9 base units converts back to a native amount without loss.
//
// Native cap:
//
//	NativeMaxSupply = WholeSupplyCap * NativeUnit = 1e9 * 1e9 = 1e18 native base units
//
// which is < math.MaxUint64, so the entire native supply is representable in the
// uint64 ledger. 1e18 native base units maps to 1e18 * 1e9 = 1e27 ERC-20 base
// units, exactly the ERC-20 MAX_SUPPLY. The two caps are the same money.
const (
	// WholeSupplyCap is the maximum number of WHOLE MATRIX coins that can ever
	// exist, matching the ERC-20's 1,000,000,000 cap. It is the human-facing
	// supply figure shared by the native chain and the wrapped token.
	WholeSupplyCap uint64 = 1_000_000_000

	// NativeUnit is the number of native base units in one whole MATRIX. The
	// native chain has 9 decimal places. All native ledger balances and amounts
	// are expressed in these base units.
	NativeUnit uint64 = 1_000_000_000 // 1e9

	// NativeMaxSupply is the hard cap on native MATRIX supply, in native base
	// units. Genesis allocation plus all subsequent issuance may never exceed
	// this value. It is WholeSupplyCap * NativeUnit = 1e18, which fits uint64.
	NativeMaxSupply uint64 = WholeSupplyCap * NativeUnit // 1e18

	// ERC20PerNativeUnit is the exact integer number of ERC-20 base units (18
	// decimals) that correspond to a single native base unit (9 decimals). It is
	// 1e18 / 1e9 = 1e9. This is the one conversion factor the native<->ERC-20
	// bridge (FEAT-004) and the Solidity mint math must reference.
	ERC20PerNativeUnit uint64 = 1_000_000_000 // 1e9
)

// ErrConversionOverflow is returned when converting an ERC-20 base-unit amount
// to native base units and the value is negative, not an exact multiple of
// ERC20PerNativeUnit (so it cannot be represented without loss), or larger than
// a uint64 can hold. Callers may match it with errors.Is.
var ErrConversionOverflow = errors.New("token: erc20 amount not exactly representable in native base units")

// erc20PerNativeUnitBig is the conversion factor as a *big.Int, computed once.
var erc20PerNativeUnitBig = new(big.Int).SetUint64(ERC20PerNativeUnit)

// NativeToERC20 converts an amount in native base units (9 decimals) to the
// equivalent amount in ERC-20 base units (18 decimals). The result is returned
// as a *big.Int because the ERC-20 scale can exceed uint64 (the full native cap
// maps to 1e27, far beyond uint64). The conversion is exact: it multiplies by
// ERC20PerNativeUnit.
func NativeToERC20(native uint64) *big.Int {
	out := new(big.Int).SetUint64(native)
	return out.Mul(out, erc20PerNativeUnitBig)
}

// ERC20ToNative converts an amount in ERC-20 base units (18 decimals) back to
// native base units (9 decimals). It is the exact inverse of NativeToERC20 and
// requires the ERC-20 amount to be a non-negative, exact multiple of
// ERC20PerNativeUnit that fits in uint64; otherwise it returns
// ErrConversionOverflow so no value is silently rounded or truncated.
func ERC20ToNative(erc20 *big.Int) (uint64, error) {
	if erc20 == nil {
		return 0, fmt.Errorf("%w: nil amount", ErrConversionOverflow)
	}
	if erc20.Sign() < 0 {
		return 0, fmt.Errorf("%w: negative amount %s", ErrConversionOverflow, erc20.String())
	}
	quo := new(big.Int)
	rem := new(big.Int)
	quo.QuoRem(erc20, erc20PerNativeUnitBig, rem)
	if rem.Sign() != 0 {
		return 0, fmt.Errorf("%w: %s is not a multiple of %d", ErrConversionOverflow, erc20.String(), ERC20PerNativeUnit)
	}
	if !quo.IsUint64() {
		return 0, fmt.Errorf("%w: %s exceeds uint64 native base units", ErrConversionOverflow, erc20.String())
	}
	return quo.Uint64(), nil
}
