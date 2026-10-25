package offline

import "sync"

// Stored is input from the storage adapter after reading/hashing every required
// entry. A persisted commit marker alone must never be passed as this evidence.
type Stored struct {
	Catalog   *Catalog
	Inventory []Observation
}

// Reservation is an opaque handle bound to its originating ledger.
type Reservation struct {
	ledger     *Ledger
	sequence   uint64
	generation string
}
type staging struct {
	catalog  *Catalog
	token    Reservation
	verified map[string]bool
	complete bool
}

// Ledger reserves the entire candidate before any fetch/write. It performs no
// deletion and does not infer physical completion from cancellation. The future
// adapter must await physical fetch/body/cache settlement and exact namespace
// deletion before calling ConfirmStagingRemoved or ConfirmPreviousRemoved.
type Ledger struct {
	mu               sync.Mutex
	policy           *Policy
	policyIdentity   string
	active, previous *Catalog
	staging          *staging
	sequence         uint64
}

type State struct {
	Active, Previous, Staging string
	StagingComplete           bool
	Reserved                  Cost
}

// Restore rechecks complete inventories after eviction/corruption instead of
// trusting a completion marker. Corrupt previous data is an error, not an
// implicit deletion; callers must reconcile physical storage explicitly.
func Restore(policy *Policy, active, previous *Stored, pending ...*Stored) (*Ledger, error) {
	if policy == nil || active == nil && previous != nil || len(pending) > 1 {
		return nil, ErrInvalid
	}
	l := &Ledger{policy: policy, policyIdentity: digest(policyCanonical(policy.config))}
	for i, stored := range []*Stored{active, previous} {
		if stored == nil {
			continue
		}
		if stored.Catalog == nil || stored.Catalog.policyIdentity != l.policyIdentity {
			return nil, ErrInvalid
		}
		if err := stored.Catalog.VerifyInventory(stored.Inventory); err != nil {
			return nil, err
		}
		if i == 0 {
			l.active = stored.Catalog
		} else {
			l.previous = stored.Catalog
		}
	}
	if l.active != nil && l.previous != nil && l.active.generation == l.previous.generation {
		return nil, ErrInvalid
	}
	if !l.fits(l.reserved()) {
		return nil, ErrQuota
	}
	// A discovered interrupted stage must be charged before new admission.
	// Invalid/unreadable stage metadata requires physical cleanup by the adapter;
	// dropping its record and pretending its storage disappeared is not recovery.
	if len(pending) == 1 && pending[0] != nil {
		stored := pending[0]
		token, err := l.Reserve(stored.Catalog)
		if err != nil {
			return nil, err
		}
		if len(stored.Inventory) > len(stored.Catalog.entries) {
			return nil, ErrIncomplete
		}
		for _, observed := range stored.Inventory {
			if err := l.Observe(token, observed); err != nil {
				return nil, err
			}
		}
	}
	return l, nil
}

func (l *Ledger) Pending() (Reservation, bool) {
	if l == nil {
		return Reservation{}, false
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.staging == nil {
		return Reservation{}, false
	}
	return l.staging.token, true
}

func (l *Ledger) Reserve(candidate *Catalog) (Reservation, error) {
	if l == nil || candidate == nil {
		return Reservation{}, ErrInvalid
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if candidate.policyIdentity != l.policyIdentity {
		return Reservation{}, ErrInvalid
	}
	if l.staging != nil || l.active != nil && candidate.generation == l.active.generation || l.previous != nil && candidate.generation == l.previous.generation || l.sequence == ^uint64(0) {
		return Reservation{}, ErrState
	}
	want := l.reserved()
	want.Bytes += candidate.cost.Bytes
	want.Entries += candidate.cost.Entries
	if !l.fits(want) {
		return Reservation{}, ErrQuota
	}
	l.sequence++
	token := Reservation{ledger: l, sequence: l.sequence, generation: candidate.generation}
	l.staging = &staging{catalog: candidate, token: token, verified: make(map[string]bool, len(candidate.entries))}
	return token, nil
}

// Observe accepts only a fully consumed/hash-checked declared entry. Duplicate,
// extra, mismatching or post-completion observations do not change ledger state.
func (l *Ledger) Observe(token Reservation, item Observation) error {
	if l == nil {
		return ErrState
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	stage := l.stage(token)
	if stage == nil || stage.complete {
		return ErrState
	}
	if stage.verified[item.URL] || !stage.catalog.matches(item) {
		return ErrIncomplete
	}
	stage.verified[item.URL] = true
	return nil
}

// Complete verifies the full required inventory. It neither activates the stage
// nor releases any reservation. Storage adapters write their bounded completion
// marker last and must re-verify the full inventory on each later restoration.
func (l *Ledger) Complete(token Reservation) error {
	if l == nil {
		return ErrState
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	stage := l.stage(token)
	if stage == nil || stage.complete {
		return ErrState
	}
	if len(stage.verified) != len(stage.catalog.entries) {
		return ErrIncomplete
	}
	stage.complete = true
	return nil
}

// Activate requires actual closed-client evidence from the lifecycle adapter;
// a user message, navigator.onLine, single-tab consent or commit marker does
// not supply it. This policy cannot inspect browser clients itself. A previous
// generation remains charged until its physical deletion is confirmed first.
func (l *Ledger) Activate(token Reservation, clientsClosed bool) error {
	if l == nil {
		return ErrState
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	stage := l.stage(token)
	if stage == nil || !stage.complete || !clientsClosed || l.previous != nil {
		return ErrState
	}
	l.previous, l.active, l.staging = l.active, stage.catalog, nil
	return nil
}

func (l *Ledger) ConfirmPreviousRemoved(generation string) error {
	if l == nil {
		return ErrState
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.previous == nil || l.previous.generation != generation {
		return ErrState
	}
	l.previous = nil
	return nil
}

func (l *Ledger) ConfirmStagingRemoved(token Reservation) error {
	if l == nil {
		return ErrState
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.stage(token) == nil {
		return ErrState
	}
	l.staging = nil
	return nil
}

func (l *Ledger) Snapshot() State {
	if l == nil {
		return State{}
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	s := State{Reserved: l.reserved()}
	if l.active != nil {
		s.Active = l.active.generation
	}
	if l.previous != nil {
		s.Previous = l.previous.generation
	}
	if l.staging != nil {
		s.Staging = l.staging.catalog.generation
		s.StagingComplete = l.staging.complete
	}
	return s
}

func (l *Ledger) stage(token Reservation) *staging {
	if token.ledger != l || l.staging == nil || token != l.staging.token {
		return nil
	}
	return l.staging
}
func (l *Ledger) reserved() Cost {
	var cost Cost
	for _, c := range []*Catalog{l.active, l.previous} {
		if c != nil {
			cost.Bytes += c.cost.Bytes
			cost.Entries += c.cost.Entries
		}
	}
	if l.staging != nil {
		cost.Bytes += l.staging.catalog.cost.Bytes
		cost.Entries += l.staging.catalog.cost.Entries
	}
	return cost
}
func (l *Ledger) fits(cost Cost) bool {
	return cost.Bytes <= l.policy.config.Budget.TotalBytes && cost.Entries <= l.policy.config.Budget.Entries
}
