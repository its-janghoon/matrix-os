package bridge

import (
	"fmt"
	"math/big"
)

// ReadinessChallengeLen is the caller-provided entropy required by the bridge
// readiness proof. A fresh 32-byte challenge makes a response current to one
// pre-lock check instead of a replayable health badge.
const ReadinessChallengeLen = 32

// readinessDomain is deliberately unrelated to the mint preimage. Keep this
// fixed string in sync with apps/web/src/lib/bridge/evm.ts.
const readinessDomain = "MATRIX_BRIDGE_READINESS_V1"

// ReadinessDigest returns the readiness-only challenge-response digest. Every
// advertised field is signed:
//
//	domain || challenge || chainId(uint256) || contract || attestor || minLock(uint256)
//
// This layout cannot collide with AttestationDigest: it begins with a fixed
// 26-byte domain while the mint digest begins with a 20-byte recipient, and it
// contains neither a mint recipient, amount, nor lock id.
func ReadinessDigest(challenge [ReadinessChallengeLen]byte, params AttestationParams, attestor Address, minLockNative uint64) []byte {
	return keccak256(
		[]byte(readinessDomain),
		challenge[:],
		bigTo32(params.ChainID),
		params.BridgeContract[:],
		attestor[:],
		bigTo32(new(big.Int).SetUint64(minLockNative)),
	)
}

// SignReadiness proves possession of attestor's secp256k1 key for the exact
// deployment and minimum represented by the readiness digest.
func SignReadiness(attestor *Attestor, challenge [ReadinessChallengeLen]byte, params AttestationParams, minLockNative uint64) ([]byte, error) {
	if attestor == nil {
		return nil, fmt.Errorf("bridge: readiness requires an attestor key")
	}
	return attestor.SignDigest(ReadinessDigest(challenge, params, attestor.Address(), minLockNative))
}
