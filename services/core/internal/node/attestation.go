package node

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/ecirlabs/matrix-core/internal/marketexchange"
)

// Loading the maintainer-signed statements this node presents.
//
// A node running first-party capacity carries one per provider; every other node
// carries none, which is the ordinary case. Nothing here VERIFIES them - that
// happens in every reader, against the maintainer their own chain names, which
// is the whole point. What this does is fail loudly on a file the operator
// plainly meant to work: a path that does not exist or does not parse is a
// configuration mistake, and starting anyway would leave capacity listed unbadged
// for a reason nobody would think to look for.
func loadAttestations(backends []InferenceBackendConfig) (map[string]*marketexchange.OperatorAttestation, error) {
	var out map[string]*marketexchange.OperatorAttestation
	for _, b := range backends {
		if b.Attestation == "" {
			continue
		}
		body, err := os.ReadFile(b.Attestation)
		if err != nil {
			return nil, fmt.Errorf("inference.backends[%s].attestation: %w", b.ID, err)
		}
		var att marketexchange.OperatorAttestation
		if err := json.Unmarshal(body, &att); err != nil {
			return nil, fmt.Errorf("inference.backends[%s].attestation: not an attestation: %w", b.ID, err)
		}
		// Checked against the provider it is configured for, because this one
		// mistake is silent everywhere else: an attestation for a DIFFERENT
		// provider verifies perfectly well as a document and is refused by every
		// reader, so the operator sees an unbadged listing and no reason why.
		if att.ProviderID != b.ID {
			return nil, fmt.Errorf("inference.backends[%s].attestation: attests provider %s, not this one",
				b.ID, att.ProviderID)
		}
		if out == nil {
			out = make(map[string]*marketexchange.OperatorAttestation, len(backends))
		}
		out[b.ID] = &att
	}
	return out, nil
}
