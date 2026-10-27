package client

import (
	"bytes"
	"context"
	"encoding/json"
	"sort"
)

// PropsSnapshot is an owned copy of server props at one accepted document
// revision. Reading does not subscribe; pass the source explicitly to Effect.
type PropsSnapshot[T any] struct {
	Value    T
	Location Location
	Revision uint64
}

// Clone must return an independent value. Hooks are trusted pure, short Go:
// no DOM/JS, I/O, state mutation or navigation. A dishonest shallow Clone is
// application misuse, not framework-enforced deep immutability.
type PropsOptions[T any] struct {
	Clone    func(T) T
	Validate func(PropsSnapshot[T]) error
}

const propsSourceLimit = 128
const propsBytesLimit = 64 << 10
const propsAggregateLimit = 1 << 20

type propsOwner interface {
	propsLive() bool
	propsInstance() uint64
}
type propsDatum struct {
	value any
	raw   []byte
}
type propsBox[T any] struct{ value T }
type propsFrameIdentity struct{ marker byte }
type propsFrame struct {
	identity  *propsFrameIdentity
	location  Location
	revision  uint64
	entries   map[uint64]propsDatum
	authority func() bool
}

func propsIdentity(frame *propsFrame) *propsFrameIdentity {
	if frame.identity == nil {
		frame.identity = &propsFrameIdentity{}
	}
	return frame.identity
}
func propsFrameMatches(identity *propsFrameIdentity) bool {
	return currentPropsFrame != nil && currentPropsFrame.identity == identity
}

var currentPropsFrame = &propsFrame{identity: &propsFrameIdentity{}, revision: 1, entries: map[uint64]propsDatum{}}
var propsNextID uint64
var propsBindings = map[uint64]propsBinding{}

type propsBinding interface {
	propsID() uint64
	propsValid() bool
	propsPrepare(context.Context, json.RawMessage, Location, uint64, func() bool) (propsDatum, error)
	propsQueue()
	propsWeight() int
	propsInstall(propsDatum)
	propsPublished()
	propsRevoke()
	propsClose()
}

// PropsSource is read-only. It owns raw JSON and cloned values, and is bound to
// one non-reused island instance. There is deliberately no public Set/Commit or
// immediate Subscribe API.
type PropsSource[T any] struct {
	owner           propsOwner
	instance, id    uint64
	options         PropsOptions[T]
	initial         propsDatum
	initialRevision uint64
	ctx             context.Context
	cancel          context.CancelFunc
	closed, revoked bool
	onClose         func(int)
	onWeight        func(int, int)
	weight          int
	next            uint64
	dependencies    map[uint64]func()
}

func (p *PropsSource[T]) propsID() uint64 { return p.id }
func (p *PropsSource[T]) propsValid() bool {
	return p != nil && !p.closed && !p.revoked && p.owner != nil && p.owner.propsLive() && p.owner.propsInstance() == p.instance
}
func (p *PropsSource[T]) propsPrepare(ctx context.Context, raw json.RawMessage, location Location, revision uint64, fence func() bool) (propsDatum, error) {
	if !p.propsValid() || revision == 0 || len(raw) == 0 || len(raw) > propsBytesLimit || fence == nil {
		return propsDatum{}, errPropsInvalid
	}
	joined, cancel := context.WithCancel(ctx)
	stop := context.AfterFunc(p.ctx, cancel)
	defer func() { stop(); cancel() }()
	ctx = joined
	valid := func() bool { return p.propsValid() && fence() }
	owned := bytes.Clone(raw)
	clone, validate := p.options.Clone, p.options.Validate
	decoded, err := propsInvokeWeighted(ctx, valid, len(owned), func() (T, error) { var value T; err := json.Unmarshal(owned, &value); return value, err })
	if err != nil {
		return propsDatum{}, err
	}
	stored, err := propsInvokeWeighted(ctx, valid, len(owned), func() (T, error) { return clone(decoded), nil })
	if err != nil {
		return propsDatum{}, err
	}
	if validate != nil {
		validation, err := propsInvokeWeighted(ctx, valid, len(owned), func() (T, error) { return clone(stored), nil })
		if err != nil {
			return propsDatum{}, err
		}
		_, err = propsInvokeWeighted(ctx, valid, len(owned), func() (struct{}, error) {
			return struct{}{}, validate(PropsSnapshot[T]{Value: validation, Location: location, Revision: revision})
		})
		if err != nil {
			return propsDatum{}, err
		}
	}
	if !valid() {
		return propsDatum{}, errPropsStale
	}
	return propsDatum{value: propsBox[T]{value: stored}, raw: owned}, nil
}
func (p *PropsSource[T]) GetSnapshot() (PropsSnapshot[T], error) {
	if !p.propsValid() {
		return PropsSnapshot[T]{}, errPropsClosed
	}
	frame := currentPropsFrame
	if frame == nil || frame.authority != nil && !frame.authority() {
		return PropsSnapshot[T]{}, errPropsStale
	}
	value, ok := frame.entries[p.id]
	if !ok {
		if p.initialRevision != frame.revision {
			return PropsSnapshot[T]{}, errPropsStale
		}
		value = p.initial
	}
	typed, ok := value.value.(propsBox[T])
	if !ok {
		return PropsSnapshot[T]{}, errPropsInvalid
	}
	// Physical workers retain the identity and copied metadata, never this
	// frame's sibling entry map. The selected DTO has its own byte estimate.
	identity, location, revision, authority := propsIdentity(frame), frame.location, frame.revision, frame.authority
	valid := func() bool { return p.propsValid() && propsFrameMatches(identity) && (authority == nil || authority()) }
	clone := p.options.Clone
	cloned, err := propsInvokeWeighted(p.ctx, valid, len(value.raw), func() (T, error) { return clone(typed.value), nil })
	if err != nil {
		return PropsSnapshot[T]{}, err
	}
	return PropsSnapshot[T]{Value: cloned, Location: location, Revision: revision}, nil
}
func (p *PropsSource[T]) subscribeEffect(fn func()) (func(), error) {
	if !p.propsValid() {
		return nil, errPropsClosed
	}
	if fn == nil || len(p.dependencies) >= 256 || p.next == ^uint64(0) {
		return nil, errPropsCapacity
	}
	p.next++
	id := p.next
	p.dependencies[id] = fn
	return func() { delete(p.dependencies, id) }, nil
}
func (p *PropsSource[T]) propsQueue() {
	if !p.propsValid() {
		return
	}
	ids := make([]uint64, 0, len(p.dependencies))
	for id := range p.dependencies {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	for _, id := range ids {
		if fn := p.dependencies[id]; fn != nil && p.propsValid() {
			fn()
		}
	}
}
func (p *PropsSource[T]) propsWeight() int { return p.weight }
func (p *PropsSource[T]) propsInstall(value propsDatum) {
	before := p.weight
	p.weight = len(value.raw)
	if p.onWeight != nil {
		p.onWeight(before, p.weight)
	}
}
func (p *PropsSource[T]) propsPublished() { p.initial, p.initialRevision = propsDatum{}, 0 }
func propsDropFrameEntry(id uint64) {
	if _, ok := currentPropsFrame.entries[id]; !ok {
		return
	}
	frame := *currentPropsFrame
	frame.identity = &propsFrameIdentity{}
	frame.entries = make(map[uint64]propsDatum, len(currentPropsFrame.entries)-1)
	for key, value := range currentPropsFrame.entries {
		if key != id {
			frame.entries[key] = value
		}
	}
	currentPropsFrame = &frame
}
func (p *PropsSource[T]) propsRevoke() {
	if p == nil || p.revoked {
		return
	}
	p.revoked = true
	if p.cancel != nil {
		p.cancel()
	}
}
func (p *PropsSource[T]) propsClose() {
	if p == nil || p.closed {
		return
	}
	p.propsRevoke()
	p.closed = true
	delete(propsBindings, p.id)
	propsDropFrameEntry(p.id)
	p.initial = propsDatum{}
	p.dependencies = nil
	p.owner = nil
	p.options = PropsOptions[T]{}
	p.cancel = nil
	p.onWeight = nil
	if p.onClose != nil {
		release := p.onClose
		p.onClose = nil
		release(p.weight)
	}
	p.weight = 0
}
func propsDocumentRevision() uint64 { return currentPropsFrame.revision }
func propsLocationChanged(location Location) {
	if currentPropsFrame.location == location {
		return
	}
	frame := *currentPropsFrame
	frame.identity = &propsFrameIdentity{}
	frame.location = location
	currentPropsFrame = &frame
}
