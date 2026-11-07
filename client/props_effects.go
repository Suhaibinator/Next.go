package client

// One batch spans independent visible lifetime trees. It changes scheduling,
// never visibility or task contexts. Outside an owned batch Effect is unchanged.
type effectPublicationFailure struct {
	owner, anchor *Lifetime
	err           error
}
type effectPublicationBatch struct {
	token            *lifetimeResumeBarrier
	owners           []*Lifetime
	effects          []*Effect
	resources        map[*Resource]bool
	valid            func() bool
	failures         []effectPublicationFailure
	draining, closed bool
}

func (l *Lifetime) publicationBatch() *effectPublicationBatch {
	for owner := l; owner != nil; owner = owner.parent {
		if owner.publication != nil && !owner.publication.closed {
			return owner.publication
		}
	}
	return nil
}
func (b *effectPublicationBatch) addOwner(owner *Lifetime) error {
	if b.closed || !b.valid() || len(b.owners) >= 256 || owner.publication != nil && owner.publication != b {
		return errPropsCapacity
	}
	owner.publication = b
	b.owners = append(b.owners, owner)
	return nil
}
func (b *effectPublicationBatch) addResource(resource *Resource) error {
	if b.closed || !b.valid() || len(b.resources) >= 512 {
		return errPropsCapacity
	}
	if b.resources == nil {
		b.resources = map[*Resource]bool{}
	}
	b.resources[resource] = true
	return nil
}
func (b *effectPublicationBatch) enroll(effect *Effect) error {
	if b.closed || !b.valid() || effect == nil || effect.owner == nil {
		return errPropsStale
	}
	if len(b.effects) >= 512 {
		return errPropsCapacity
	}
	owner := effect.owner
	found := false
	for _, existing := range b.owners {
		if existing == owner {
			found = true
			break
		}
	}
	if !found {
		if err := b.addOwner(owner); err != nil {
			return err
		}
	}
	effect.resumeBarrier = b.token
	b.effects = append(b.effects, effect)
	return nil
}
func (b *effectPublicationBatch) runOne(effect *Effect) error {
	if b.closed || !b.valid() {
		return errPropsStale
	}
	if effect.closed || !effect.pending {
		return nil
	}
	owner := effect.owner
	if owner == nil || !owner.visible() || effect.busy || effect.resumeBarrier != b.token {
		return errPropsStale
	}
	anchor := owner
	for anchor.parent != nil {
		anchor = anchor.parent
	}
	err := effect.drainPending(true)
	if err != nil {
		b.failures = append(b.failures, effectPublicationFailure{owner: owner, anchor: anchor, err: err})
	}
	return err
}
func (b *effectPublicationBatch) flush() []effectPublicationFailure {
	if b.closed {
		return nil
	}
	b.draining = true
	defer func() { b.draining = false }()
	for pass := 0; pass < 64 && b.valid(); pass++ {
		drained := false
		// New visible effects preserve synchronous first setup; enroll appends
		// them within the same fixed budget, and later dirtiness joins a pass.
		for i := 0; i < len(b.effects) && b.valid(); i++ {
			effect := b.effects[i]
			if effect.closed || !effect.pending || effect.owner == nil || !effect.owner.visible() {
				continue
			}
			drained = true
			_ = b.runOne(effect)
		}
		if !drained {
			return append([]effectPublicationFailure(nil), b.failures...)
		}
	}
	if b.valid() {
		for _, effect := range b.effects {
			if effect.closed || !effect.pending || effect.owner == nil || !effect.owner.visible() || !b.valid() {
				continue
			}
			owner := effect.owner
			anchor := owner
			for anchor.parent != nil {
				anchor = anchor.parent
			}
			effect.err = errPropsCapacity
			effect.Close()
			b.failures = append(b.failures, effectPublicationFailure{owner: owner, anchor: anchor, err: errPropsCapacity})
		}
	}
	return append([]effectPublicationFailure(nil), b.failures...)
}
func (b *effectPublicationBatch) close() {
	if b == nil || b.closed {
		return
	}
	b.closed = true
	for _, owner := range b.owners {
		if owner.publication == b {
			owner.publication = nil
		}
	}
	for _, effect := range b.effects {
		if effect.resumeBarrier == b.token {
			effect.resumeBarrier = nil
		}
	}
	b.effects, b.owners, b.resources = nil, nil, nil
}
