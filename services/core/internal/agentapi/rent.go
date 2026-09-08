package agentapi

import (
	"context"
	"fmt"
	"math/big"
	"sort"
	"time"

	"github.com/cockroachdb/pebble"
)

// Storage rent: what a deployer pays for the bytes a node keeps for them.
//
// THE GAP THIS CLOSES. The manager charged for a RUN and nothing for STORAGE,
// so a stored module was the one resource a deployer took for free and kept
// indefinitely. `agent/module/<id>` holds raw wasm up to DefaultMaxModuleBytes
// (32 MiB) each and AgentService is served on the network. While deploy keys are
// operator-only that is an operator's own disk; the moment keys are issued to
// paying deployers it is a bill nobody sends.
//
// WHY RENT RATHER THAN A CAP. A per-account deployment cap was the first
// proposal and it was wrong three times over. The resource is BYTES, not
// deployments - a cap of ten permits 320 MiB and a cap of a thousand permits
// 32 GiB, so a count bounds disk only if every module is assumed maximal. There
// was no owner on a record to count against. And in a market the answer to heavy
// resource use is to price it: a cap turns a paying customer into a refused one
// and bounds revenue at the same time. Rent still bounds disk, through eviction
// of what nobody will pay for, but it keeps the customer who will.
//
// WHAT IT IS NOT. Rent is per-node state, like the deployments themselves. Two
// nodes hosting the same deployer bill separately for their own bytes. Only the
// CHARGE is consensus-ordered, through the same signed transfer the per-run
// meter uses, so no per-node ledger write happens here either.

const (
	// DefaultRentInterval is how often rent is swept when the operator sets no
	// interval. Hourly is frequent enough that a delinquent deployment is noticed
	// the same day and rare enough that the sweep's consensus transfers are
	// nothing against ordinary traffic.
	DefaultRentInterval = time.Hour

	// DefaultRentGrace is how long a deployment whose rent went unpaid survives
	// before eviction. Three days spans a weekend, which is the realistic gap
	// between a balance running dry and a human noticing.
	DefaultRentGrace = 72 * time.Hour

	// bytesPerMiB and nsPerDay are the units StoragePrice is quoted in. They are
	// named rather than inlined because the accrual arithmetic below is only
	// checkable against the unit it claims.
	bytesPerMiB = 1 << 20
	nsPerDay    = int64(24 * time.Hour)
)

// rentOwed computes the whole credits owed for holding sizeBytes over elapsed,
// and the portion of elapsed those credits exactly pay for.
//
// THE ROUNDING IS THE WHOLE PROBLEM. Rent for one hour on a small module is a
// fraction of a credit. Truncating per sweep would charge zero forever - a
// silent free ride that grows with how often the sweep runs, which is the
// opposite of what a shorter interval should mean. So the watermark advances
// only by the time the paid credits actually cover, and the sub-credit remainder
// stays unpaid and keeps accruing until it crosses one credit. Nothing is lost
// and nothing is charged twice: covered <= elapsed always, because it is derived
// by dividing the truncated credits back through the same rate.
//
// big.Int rather than uint64 because the intermediate product is
// bytes x price x nanoseconds: 32 MiB over a year is already ~10^24 before the
// price is applied, so a uint64 would silently wrap. This runs once per
// deployment per hour, where exactness is free.
func rentOwed(sizeBytes, pricePerMiBPerDay uint64, elapsed time.Duration) (credits uint64, covered time.Duration) {
	if sizeBytes == 0 || pricePerMiBPerDay == 0 || elapsed <= 0 {
		return 0, 0
	}
	rate := new(big.Int).Mul(new(big.Int).SetUint64(sizeBytes), new(big.Int).SetUint64(pricePerMiBPerDay))
	num := new(big.Int).Mul(rate, big.NewInt(int64(elapsed)))
	den := new(big.Int).Mul(big.NewInt(bytesPerMiB), big.NewInt(nsPerDay))

	whole := new(big.Int).Quo(num, den)
	if !whole.IsUint64() {
		// Unreachable with a sane price, but a saturating answer is better than a
		// wrapped one: the charge will simply fail as unaffordable.
		return ^uint64(0), elapsed
	}
	credits = whole.Uint64()
	if credits == 0 {
		return 0, 0
	}
	coveredNS := new(big.Int).Quo(new(big.Int).Mul(whole, den), rate)
	if !coveredNS.IsInt64() {
		return credits, elapsed
	}
	covered = time.Duration(coveredNS.Int64())
	if covered > elapsed {
		// Cannot happen given the truncation above; clamp rather than let a
		// watermark run past now if it ever does.
		covered = elapsed
	}
	return credits, covered
}

// SweepOutcome reports what one rent sweep did. Every field is named so an
// operator log line can say what happened without the caller re-deriving it.
type SweepOutcome struct {
	// Considered is how many stored deployments the sweep looked at.
	Considered int
	// Charged is the total credits settled through consensus.
	Charged uint64
	// Billed lists the deployment ids that paid something this sweep.
	Billed []string
	// Unowned lists deployments with no deployer to bill. A record written
	// before rent existed has none; it is never evicted for that.
	Unowned []string
	// Delinquent lists deployments whose charge did not settle this sweep.
	Delinquent []string
	// Evicted lists deployments deleted for being delinquent past the grace
	// period. Their module bytes are gone.
	Evicted []string
}

// SweepRent charges the rent accrued by every stored deployment up to now, and
// evicts the ones whose rent has gone unpaid past the grace period.
//
// It takes `now` rather than reading the clock so a test can drive a year of
// accrual without waiting for it.
//
// A record with no RentPaidThroughNS is a record written before rent existed.
// The sweep starts its clock at `now` and charges nothing for it: back-charging
// to CreatedAt would present a deployer with a bill for a period during which
// storage was advertised as free, which is a worse failure than a day of
// uncharged rent.
func (m *Manager) SweepRent(ctx context.Context, now time.Time) (SweepOutcome, error) {
	var out SweepOutcome
	if !m.meter.RentEnabled() {
		return out, nil
	}

	m.mu.Lock()
	ids := make([]string, 0, len(m.records))
	for id := range m.records {
		ids = append(ids, id)
	}
	m.mu.Unlock()
	// Deterministic order, so a log of two sweeps is comparable and a test does
	// not depend on map iteration.
	sort.Strings(ids)
	out.Considered = len(ids)

	for _, id := range ids {
		m.mu.Lock()
		rec, ok := m.records[id]
		m.mu.Unlock()
		if !ok {
			continue // removed between the snapshot and here
		}

		if rec.Deployer == "" {
			out.Unowned = append(out.Unowned, id)
			continue
		}

		if rec.RentPaidThroughNS == 0 {
			rec.RentPaidThroughNS = now.UnixNano()
			if err := m.saveRecord(rec); err != nil {
				return out, err
			}
			continue
		}

		elapsed := time.Duration(now.UnixNano() - rec.RentPaidThroughNS)
		credits, covered := rentOwed(rec.ModuleSize, m.meter.StoragePrice, elapsed)
		if credits == 0 {
			continue
		}

		if err := m.chargeRent(ctx, rec.Deployer, credits); err != nil {
			// Unpaid. Start the grace clock if it is not already running, and
			// evict once it has run out. Eviction is the only thing that makes
			// rent a bound on disk rather than an unpayable debt that grows.
			if rec.DelinquentSinceNS == 0 {
				rec.DelinquentSinceNS = now.UnixNano()
			}
			rec.LastError = fmt.Sprintf("storage rent unpaid: %v", err)
			overdue := time.Duration(now.UnixNano() - rec.DelinquentSinceNS)
			if overdue >= m.rentGrace() {
				if evictErr := m.evict(id); evictErr != nil {
					return out, evictErr
				}
				out.Evicted = append(out.Evicted, id)
				continue
			}
			if saveErr := m.saveRecord(rec); saveErr != nil {
				return out, saveErr
			}
			out.Delinquent = append(out.Delinquent, id)
			continue
		}

		rec.RentPaidThroughNS += int64(covered)
		rec.RentPaid += credits
		rec.DelinquentSinceNS = 0
		if err := m.saveRecord(rec); err != nil {
			return out, err
		}
		out.Charged += credits
		out.Billed = append(out.Billed, id)
	}
	return out, nil
}

// rentGrace is the configured grace period, or the default.
func (m *Manager) rentGrace() time.Duration {
	if m.meter.RentGrace > 0 {
		return m.meter.RentGrace
	}
	return DefaultRentGrace
}

// rentInterval is the configured sweep interval, or the default.
func (m *Manager) rentInterval() time.Duration {
	if m.meter.RentInterval > 0 {
		return m.meter.RentInterval
	}
	return DefaultRentInterval
}

// chargeRent settles `credits` from the deployer to the metering recipient
// through consensus. It is the per-run charge's path with a different amount:
// same settler, same nonce sequence per payer, same honest-refusal rule that a
// charge which commits but is skipped as unaffordable counts as NOT paid.
func (m *Manager) chargeRent(ctx context.Context, deployer string, credits uint64) error {
	if credits == 0 {
		return nil
	}
	acct, ok := m.accounts.Account(deployer)
	if !ok {
		return fmt.Errorf("%w: %q", ErrNoSigningAccount, deployer)
	}
	nonce := m.nextNonce(deployer)
	tx, err := m.settler.SubmitAccountTransfer(acct, m.meter.Recipient, credits, nonce)
	if err != nil {
		return fmt.Errorf("submit rent charge: %w", err)
	}
	committed, applied, err := m.settler.WaitForSettlement(ctx, tx)
	if err != nil {
		return fmt.Errorf("awaiting rent settlement: %w", err)
	}
	if !committed || !applied {
		return fmt.Errorf("charge %d from %s: %w", credits, deployer, ErrMeterNotApplied)
	}
	return nil
}

// evict deletes a deployment's record AND its module bytes, and forgets its
// inbox. This is the destructive half of rent, so it is one function with one
// caller: nothing else in this package deletes a deployment.
func (m *Manager) evict(id string) error {
	batch := m.store.NewBatch()
	defer batch.Close()
	if err := batch.Delete([]byte(recordPrefix+id), nil); err != nil {
		return fmt.Errorf("agentapi: stage evict record %q: %w", id, err)
	}
	if err := batch.Delete([]byte(modulePrefix+id), nil); err != nil {
		return fmt.Errorf("agentapi: stage evict module %q: %w", id, err)
	}
	if err := batch.Commit(pebble.Sync); err != nil {
		return fmt.Errorf("agentapi: commit evict %q: %w", id, err)
	}
	m.mu.Lock()
	delete(m.records, id)
	delete(m.inbox, id)
	delete(m.inboxBytes, id)
	m.mu.Unlock()
	return nil
}

// saveRecord persists a deployment record without rewriting its module bytes.
// The rent sweep touches only accounting fields, and rewriting up to 32 MiB per
// deployment per hour to record that a few credits moved would make the sweep
// itself the largest write on the node.
func (m *Manager) saveRecord(rec Deployment) error {
	payload, err := marshalDeployment(rec)
	if err != nil {
		return err
	}
	if err := m.store.Put([]byte(recordPrefix+rec.ID), payload); err != nil {
		return fmt.Errorf("agentapi: save deployment record %q: %w", rec.ID, err)
	}
	m.mu.Lock()
	m.records[rec.ID] = rec
	m.mu.Unlock()
	return nil
}

// StartRentSweeper runs SweepRent on a ticker until ctx is done. It returns
// immediately, and is a no-op when rent is disabled, so the node can call it
// unconditionally.
func (m *Manager) StartRentSweeper(ctx context.Context) {
	if !m.meter.RentEnabled() {
		return
	}
	interval := m.rentInterval()
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case now := <-ticker.C:
				out, err := m.SweepRent(ctx, now)
				if err != nil {
					fmt.Printf("agentapi: rent sweep failed: %v\n", err)
					continue
				}
				// Silence on a quiet sweep: an hourly line saying nothing happened
				// is how an operator learns to stop reading the log.
				if out.Charged > 0 || len(out.Delinquent) > 0 || len(out.Evicted) > 0 {
					fmt.Printf("agentapi: rent swept %d deployments, charged %d credits, "+
						"delinquent %v, evicted %v\n",
						out.Considered, out.Charged, out.Delinquent, out.Evicted)
				}
			}
		}
	}()
}
