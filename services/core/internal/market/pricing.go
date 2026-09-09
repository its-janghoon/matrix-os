package market

import (
	"fmt"
	"math/bits"
	"time"
)

const (
	// BasisPointDenominator is 100%, used for provider markup and protocol fee
	// gross-up calculations.
	BasisPointDenominator uint64 = 10_000
	// DefaultQuoteTTL keeps manually supplied MATRIX prices fresh without
	// pretending an illiquid DEX is a reliable oracle. Providers refresh their
	// quote through config or registration at least once per day.
	DefaultQuoteTTL = 24 * time.Hour
	// MaxQuoteClockSkew tolerates ordinary host-clock drift but rejects a quote
	// whose observation time is materially in the future.
	MaxQuoteClockSkew = 5 * time.Minute
)

// CheckedMul multiplies a positive unit count by a positive per-unit quote and
// refuses overflow. A zero per-unit value is treated as an incomplete legacy
// job snapshot so inference settlement fails with ErrStaleQuote instead of
// silently constructing a zero payment. Pricing must fail closed: wrapped
// arithmetic could turn an unaffordable job into a nearly free one while the
// provider still performs all of the work.
func CheckedMul(a, b uint64) (uint64, error) {
	if b == 0 {
		return 0, fmt.Errorf("missing price_per_unit quote snapshot: %w", ErrStaleQuote)
	}
	hi, lo := bits.Mul64(a, b)
	if hi != 0 {
		return 0, fmt.Errorf("%w: %d * %d", ErrPriceOverflow, a, b)
	}
	return lo, nil
}

// CeilMulDiv computes ceil(a*b/divisor) without overflowing the intermediate
// product. It returns ErrPriceOverflow when the rounded quotient cannot fit in
// uint64.
func CeilMulDiv(a, b, divisor uint64) (uint64, error) {
	if divisor == 0 {
		return 0, fmt.Errorf("%w: division by zero", ErrPriceOverflow)
	}
	hi, lo := bits.Mul64(a, b)
	if hi >= divisor {
		return 0, fmt.Errorf("%w: ceil(%d * %d / %d)", ErrPriceOverflow, a, b, divisor)
	}
	q, r := bits.Div64(hi, lo, divisor)
	if r != 0 {
		if q == ^uint64(0) {
			return 0, fmt.Errorf("%w: rounded quotient exceeds uint64", ErrPriceOverflow)
		}
		q++
	}
	return q, nil
}

// GrossPricePerUnit converts a provider's MATRIX-denominated upstream cost into
// a customer quote that preserves the requested markup after the protocol fee
// is deducted from settlement. There is no stablecoin peg or DEX oracle here:
// costPerUnit is the provider's manually observed final MATRIX cost basis.
func GrossPricePerUnit(costPerUnit uint64, markupBasisPoints, protocolFeeBasisPoints uint32) (uint64, error) {
	if costPerUnit == 0 {
		return 0, fmt.Errorf("cost_per_unit must be > 0: %w", ErrInvalidProvider)
	}
	if uint64(protocolFeeBasisPoints) >= BasisPointDenominator {
		return 0, fmt.Errorf("protocol fee %d basis points leaves no provider proceeds: %w", protocolFeeBasisPoints, ErrInvalidProvider)
	}
	markupFactor := BasisPointDenominator + uint64(markupBasisPoints)
	netTarget, err := CeilMulDiv(costPerUnit, markupFactor, BasisPointDenominator)
	if err != nil {
		return 0, err
	}
	return CeilMulDiv(netTarget, BasisPointDenominator, BasisPointDenominator-uint64(protocolFeeBasisPoints))
}
