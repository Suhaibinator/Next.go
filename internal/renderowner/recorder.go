package renderowner

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/Suhaibinator/Next.go/internal/navigation"
)

type capture struct {
	island       navigation.MergeIsland
	root         string
	prefix, body int
}
type Recorder struct {
	owners       []navigation.MergeOwner
	captures     []capture
	bytes, props int
	next         int
	disabled     bool
	keys         map[string]bool
}
type Ticket struct {
	recorder *Recorder
	island   navigation.MergeIsland
}

func New() *Recorder { return &Recorder{keys: map[string]bool{}} }
func (r *Recorder) Context(ctx context.Context) context.Context {
	return context.WithValue(ctx, contextKey{}, state{recorder: r})
}
func (r *Recorder) Disable() {
	r.disabled = true
	r.owners = nil
	r.captures = nil
	r.keys = nil
	r.bytes = 0
	r.props = 0
}
func Begin(ctx context.Context, key, component, schema, props string) (*Ticket, error) {
	value, _ := ctx.Value(contextKey{}).(state)
	r := value.recorder
	if r == nil || r.disabled {
		return nil, nil
	}
	if value.owner < 1 || len(props) > navigation.MaxMergeProps || r.props+len(props) > navigation.MaxRetentionHTML || r.next >= navigation.MaxMergeIslands {
		r.Disable()
		return nil, nil
	}
	shape := navigation.MergeDescriptor{Owners: r.owners}
	island := navigation.MergeIsland{Owner: value.owner, Key: key, Component: component, Schema: schema}
	if _, err := shape.Address(island); err != nil {
		return nil, err
	}
	// Identity without component/schema enforces sibling-key uniqueness even
	// when a caller changes the implementation behind an ambiguous duplicate.
	identity := island
	identity.Component = "__key"
	identity.Schema = "v1"
	address, _ := shape.Address(identity)
	if r.keys[address] {
		return nil, fmt.Errorf("nextgo: duplicate merge island key")
	}
	r.keys[address] = true
	r.next++
	r.props += len(props)
	island.Node = r.next
	island.PropsSHA256 = navigation.MergeDigest(props)
	return &Ticket{recorder: r, island: island}, nil
}
func (t *Ticket) Node() int {
	if t == nil {
		return 0
	}
	return t.island.Node
}
func (t *Ticket) Capture(root string, prefix, body int) error {
	if t == nil || t.recorder.disabled {
		return nil
	}
	r := t.recorder
	if prefix < 1 || body < 0 || prefix+body+len("</div>") != len(root) {
		return navigation.ErrMerge
	}
	if len(root) > navigation.MaxMergeCapture-r.bytes {
		r.Disable()
		return nil
	}
	r.bytes += len(root)
	t.island.FallbackSHA256 = navigation.MergeDigest(root[prefix : prefix+body])
	r.captures = append(r.captures, capture{t.island, root, prefix, body})
	return nil
}
func (r *Recorder) Finish(partition, body string) (*navigation.MergeDescriptor, error) {
	defer r.Disable()
	if r.disabled || len(r.captures) == 0 || len(body) > navigation.MaxRetentionHTML {
		return nil, nil
	}
	d := &navigation.MergeDescriptor{Version: navigation.MergeVersion, Partition: strings.Clone(partition), BodySHA256: navigation.MergeDigest(body), Owners: append([]navigation.MergeOwner{}, r.owners...)}
	for _, item := range r.captures {
		if strings.Count(body, item.root) != 1 {
			return nil, fmt.Errorf("nextgo: merge root was missing, transformed or duplicated")
		}
		start := strings.Index(body, item.root)
		island := item.island
		island.Start = start
		island.BodyStart = start + item.prefix
		island.BodyEnd = island.BodyStart + item.body
		island.End = start + len(item.root)
		d.Islands = append(d.Islands, island)
	}
	sort.Slice(d.Islands, func(i, j int) bool { return d.Islands[i].Start < d.Islands[j].Start })
	encoded, _ := json.Marshal(d)
	if len(encoded) > navigation.MaxMergeDescriptor {
		return nil, nil
	}
	if err := d.Validate(body); err != nil {
		return nil, err
	}
	return d, nil
}
