//go:build js && wasm

package client

import (
	"context"
	"encoding/json"
	"errors"
)

var propsPreparedByteCount int
var propsHeldFrameByteCount int

func propsTransientBytes() int { return propsPreparedByteCount + propsHeldFrameByteCount }

type preparedProps struct {
	ctx      context.Context
	cohort   *propsCohort
	charge   int
	closed   bool
	scope    *Scope
	binding  propsBinding
	instance uint64
	prior    *propsFrame
	value    propsDatum
	location Location
	revision uint64
}

func (p *preparedProps) release() {
	if p == nil || p.closed {
		return
	}
	p.closed = true
	propsPreparedByteCount -= p.charge
	p.charge = 0
	p.value, p.prior, p.binding, p.scope, p.ctx, p.cohort = propsDatum{}, nil, nil, nil, nil, nil
}
func (p *preparedProps) Close() {
	if p == nil || p.closed || p.cohort != nil && !p.cohort.closed {
		return
	}
	p.release()
}

type propsFailure struct {
	Scope *Scope
	Err   error
}
type propsGuards struct {
	Precommit func() bool
	Published func() bool
	Retiring  []*Scope
}
type retiringProps struct {
	scope    *Scope
	binding  propsBinding
	instance uint64
}
type propsCohort struct {
	prepared                     []*preparedProps
	scopes                       []*Scope
	instances                    map[*Scope]uint64
	owners                       map[*Lifetime]*Scope
	batch                        *effectPublicationBatch
	prior, frame                 *propsFrame
	guards                       propsGuards
	retiring                     []retiringProps
	installed, published, closed bool
	reportedFailures             int
	priorBytes                   int
}

func prepareScopeProps(ctx context.Context, scope *Scope, raw json.RawMessage, location Location, revision uint64, fence func() bool) (*preparedProps, error) {
	if ctx == nil || scope == nil || !scope.mergeClientReady() || fence == nil || revision <= propsDocumentRevision() {
		return nil, errPropsInvalid
	}
	if len(raw) == 0 || len(raw) > propsBytesLimit || propsPreparedByteCount > propsAggregateLimit-len(raw) {
		return nil, errPropsCapacity
	}
	propsPreparedByteCount += len(raw)
	reserved := true
	defer func() {
		if reserved {
			propsPreparedByteCount -= len(raw)
		}
	}()
	binding, instance, prior := scope.propsSource, scope.mergeInstance(), currentPropsFrame
	identity := propsIdentity(prior)
	valid := func() bool {
		return fence() && scope.propsLive() && scope.mergeID == instance && scope.propsSource == binding && propsFrameMatches(identity)
	}
	value, err := binding.propsPrepare(ctx, raw, location, revision, valid)
	if err != nil {
		return nil, err
	}
	if !valid() {
		return nil, errPropsStale
	}
	reserved = false
	return &preparedProps{ctx: ctx, charge: len(raw), scope: scope, binding: binding, instance: instance, prior: prior, value: value, location: location, revision: revision}, nil
}
func preparePropsCohort(scopes []*Scope, prepared []*preparedProps, location Location, revision uint64, guards propsGuards) (*propsCohort, error) {
	if guards.Precommit == nil || guards.Published == nil || !guards.Precommit() || revision <= propsDocumentRevision() || len(scopes) > 64 || len(prepared) != len(scopes) || len(guards.Retiring) > 64 {
		return nil, errPropsInvalid
	}
	c := &propsCohort{scopes: append([]*Scope(nil), scopes...), prepared: append([]*preparedProps(nil), prepared...), instances: map[*Scope]uint64{}, owners: map[*Lifetime]*Scope{}, prior: currentPropsFrame, guards: guards}
	c.frame = &propsFrame{identity: &propsFrameIdentity{}, location: location, revision: revision, entries: map[uint64]propsDatum{}, authority: guards.Published}
	c.batch = &effectPublicationBatch{token: &lifetimeResumeBarrier{}, valid: c.valid}
	seenPrepared := map[*Scope]bool{}
	bytes := 0
	for _, p := range prepared {
		if p == nil || p.closed || p.cohort != nil || p.ctx == nil || p.ctx.Err() != nil || p.scope == nil || seenPrepared[p.scope] || p.prior != c.prior || p.revision != revision || p.location != location || p.scope.propsSource != p.binding || p.scope.mergeID != p.instance || !p.binding.propsValid() {
			return nil, errPropsStale
		}
		seenPrepared[p.scope] = true
		bytes += len(p.value.raw)
		if bytes > propsAggregateLimit {
			return nil, errPropsCapacity
		}
		c.frame.entries[p.binding.propsID()] = p.value
	}
	seenRetiring := map[*Scope]bool{}
	for _, scope := range guards.Retiring {
		if scope == nil || seenRetiring[scope] || seenPrepared[scope] {
			return nil, errPropsInvalid
		}
		seenRetiring[scope] = true
		if scope.propsSource != nil {
			if scope.closed || scope.mergeID == 0 {
				return nil, errPropsStale
			}
			c.retiring = append(c.retiring, retiringProps{scope: scope, binding: scope.propsSource, instance: scope.mergeID})
		}
	}
	finalBytes := propsRegisteredBytes
	for _, retiring := range c.retiring {
		finalBytes -= retiring.binding.propsWeight()
	}
	for _, p := range prepared {
		finalBytes += len(p.value.raw) - p.binding.propsWeight()
	}
	if finalBytes < 0 || finalBytes > propsAggregateLimit {
		return nil, errPropsCapacity
	}
	var lifetimes []lifetimeActivity
	for _, scope := range scopes {
		if scope == nil || !scope.mergeClientReady() || c.instances[scope] != 0 || !seenPrepared[scope] {
			return nil, errPropsInvalid
		}
		c.instances[scope] = scope.mergeID
		// Only existing trees participate. A fresh Lifetime is not allocated
		// here: preparation must have no cleanup/provider registration effects.
		if scope.lifetime == nil {
			continue
		}
		tree, err := scope.lifetime.activityTree()
		if err != nil {
			return nil, err
		}
		for _, entry := range tree {
			if _, duplicate := c.owners[entry.owner]; duplicate || !entry.owner.visible() || entry.owner.publication != nil {
				return nil, errPropsStale
			}
			c.owners[entry.owner] = scope
			lifetimes = append(lifetimes, entry)
		}
	}
	if len(lifetimes) > 256 {
		return nil, errPropsCapacity
	}
	resources := map[*Resource]bool{}
	for _, entry := range lifetimes {
		for _, resource := range entry.owner.resources {
			resources[resource] = true
		}
	}
	if len(resources) > 512 {
		return nil, errPropsCapacity
	}
	c.batch.resources = resources
	count := 0
	for _, entry := range lifetimes {
		for _, effect := range entry.effects {
			if effect.closed {
				continue
			}
			if effect.busy || effect.resumeBarrier != nil {
				return nil, errPropsStale
			}
			count++
		}
	}
	if count > 512 || !c.valid() {
		return nil, errPropsCapacity
	}
	for _, p := range prepared {
		p.cohort = c
	}
	// The complete bounded forest is validated before installing any token.
	for _, entry := range lifetimes {
		entry.owner.publication = c.batch
		c.batch.owners = append(c.batch.owners, entry.owner)
		for _, effect := range entry.effects {
			if !effect.closed {
				effect.resumeBarrier = c.batch.token
				c.batch.effects = append(c.batch.effects, effect)
			}
		}
	}
	return c, nil
}
func (c *propsCohort) valid() bool {
	if c == nil || c.closed {
		return false
	}
	if c.installed {
		if currentPropsFrame != c.frame || !c.guards.Published() {
			return false
		}
		return true
	} else if currentPropsFrame != c.prior || !c.guards.Precommit() {
		return false
	}
	for scope, instance := range c.instances {
		if !scope.propsLive() || scope.mergeID != instance {
			return false
		}
	}
	return true
}
func (c *propsCohort) InstallFrame() error {
	if c == nil || c.closed || c.installed || !c.valid() {
		return errPropsStale
	}
	for _, p := range c.prepared {
		if p.closed || p.ctx == nil || p.ctx.Err() != nil || p.scope.propsSource != p.binding || p.scope.mergeID != p.instance || !p.binding.propsValid() {
			return errPropsStale
		}
	}
	finalBytes := propsRegisteredBytes
	for _, retiring := range c.retiring {
		if retiring.scope.closed || retiring.scope.mergeID != retiring.instance || retiring.scope.propsSource != retiring.binding {
			return errPropsStale
		}
		finalBytes -= retiring.binding.propsWeight()
	}
	for _, p := range c.prepared {
		finalBytes += len(p.value.raw) - p.binding.propsWeight()
	}
	if finalBytes < 0 || finalBytes > propsAggregateLimit {
		return errPropsCapacity
	}
	priorBytes := 0
	for _, value := range c.prior.entries {
		priorBytes += len(value.raw)
	}
	if priorBytes > propsAggregateLimit || propsHeldFrameByteCount > propsAggregateLimit-priorBytes {
		return errPropsCapacity
	}
	propsHeldFrameByteCount += priorBytes
	c.priorBytes = priorBytes
	// Retiring sources may already be logically revoked. Close only captured
	// bindings, without terminal Scope callbacks or public source lookups.
	for _, retiring := range c.retiring {
		retiring.binding.propsClose()
	}
	// One pointer publication; no authored callback, decoder, clone or notifier.
	for _, p := range c.prepared {
		p.binding.propsInstall(p.value)
	}
	c.installed = true
	currentPropsFrame = c.frame
	for _, p := range c.prepared {
		p.binding.propsPublished()
		propsPreparedByteCount -= p.charge
		p.charge = 0
	}
	return nil
}
func (c *propsCohort) Publish() []propsFailure {
	if c == nil || !c.installed || c.published || c.closed {
		return nil
	}
	c.published = true
	if !c.valid() {
		return nil
	}
	for _, p := range c.prepared {
		if c.valid() && p.binding.propsValid() {
			p.binding.propsQueue()
		}
	}
	return c.collectFailures(c.batch.flush())
}
func (c *propsCohort) collectFailures(failures []effectPublicationFailure) []propsFailure {
	if c.reportedFailures >= len(failures) {
		return nil
	}
	fresh := failures[c.reportedFailures:]
	c.reportedFailures = len(failures)
	joined := map[*Scope][]error{}
	for _, failure := range fresh {
		owner := failure.owner
		var scope *Scope
		for owner != nil {
			if found := c.owners[owner]; found != nil {
				scope = found
				break
			}
			owner = owner.parent
		}
		if scope == nil {
			scope = c.owners[failure.anchor]
		}
		if scope != nil && scope.propsLive() {
			joined[scope] = append(joined[scope], failure.err)
		}
	}
	var result []propsFailure
	for _, scope := range c.scopes {
		if list := joined[scope]; len(list) != 0 {
			result = append(result, propsFailure{Scope: scope, Err: errors.Join(list...)})
		}
	}
	return result
}
func (c *propsCohort) drainValid() bool {
	if c == nil || c.closed {
		return false
	}
	if c.installed {
		return currentPropsFrame == c.frame && c.guards.Published()
	}
	return currentPropsFrame == c.prior && (c.prior.authority == nil || c.prior.authority())
}
func (c *propsCohort) Close() []propsFailure {
	if c == nil || c.closed {
		return nil
	}
	var result []propsFailure
	if c.batch != nil {
		// C request intent may cancel B preparation without revoking current A.
		// Preserve pending visible State work; never seed prepared B props here.
		c.batch.valid = c.drainValid
		if c.drainValid() {
			result = c.collectFailures(c.batch.flush())
		}
		c.batch.close()
	}
	c.closed = true
	for _, p := range c.prepared {
		p.release()
	}
	propsHeldFrameByteCount -= c.priorBytes
	c.priorBytes = 0
	c.prepared, c.scopes = nil, nil
	c.prior, c.frame = nil, nil
	c.instances, c.owners = nil, nil
	c.retiring = nil
	c.guards = propsGuards{}
	if c.batch != nil {
		c.batch.valid = nil
		c.batch = nil
	}
	return result
}
