package navigation

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"html"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/Suhaibinator/Next.go/internal/ascii"
	"github.com/Suhaibinator/Next.go/internal/keyed"
	"github.com/Suhaibinator/Next.go/internal/wirejson"
)

const (
	MergeVersion       = 1
	MergeAttribute     = "data-nextgo-merge"
	MergeNodeAttribute = "data-nextgo-merge-node"
	MaxMergeDescriptor = 64 << 10
	MaxMergeOwners     = 128
	MaxMergeIslands    = 64
	MaxMergeDepth      = 32
	MaxMergeProps      = 64 << 10
	MaxMergeCapture    = 1 << 20
)

var ErrMerge = errors.New("nextgo: invalid merge descriptor")

type MergeDescriptor struct {
	Version    int           `json:"version"`
	Partition  string        `json:"partition"`
	BodySHA256 string        `json:"bodySHA256"`
	Owners     []MergeOwner  `json:"owners"`
	Islands    []MergeIsland `json:"islands"`
}
type MergeOwner struct {
	Parent int    `json:"parent"`
	Kind   string `json:"kind"`
	ID     string `json:"id"`
	Tag    string `json:"tag,omitempty"`
}
type MergeIsland struct {
	Node           int    `json:"node"`
	Owner          int    `json:"owner"`
	Key            string `json:"key"`
	Component      string `json:"component"`
	Schema         string `json:"schema"`
	Start          int    `json:"start"`
	BodyStart      int    `json:"bodyStart"`
	BodyEnd        int    `json:"bodyEnd"`
	End            int    `json:"end"`
	PropsSHA256    string `json:"propsSHA256"`
	FallbackSHA256 string `json:"fallbackSHA256"`
}

func MergeDigest(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}
func mergeHex(value string) bool {
	if len(value) != 64 {
		return false
	}
	for _, c := range value {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return false
		}
	}
	return true
}
func MergeSchema(value string) bool {
	if len(value) < 1 || len(value) > 64 {
		return false
	}
	for _, c := range value {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '.' || c == '_' || c == '-') {
			return false
		}
	}
	return true
}
func mergeComponent(value string) bool {
	if len(value) < 1 || len(value) > 512 || !utf8.ValidString(value) {
		return false
	}
	for _, c := range value {
		if c <= 32 || c == 127 {
			return false
		}
	}
	return true
}

// MergeIdentity frames every field; content/render/history/query do not enter
// this helper unless explicitly supplied as an application key by its caller.
func MergeIdentity(domain string, fields ...string) string {
	h := sha256.New()
	for _, field := range append([]string{domain}, fields...) {
		var size [8]byte
		binary.BigEndian.PutUint64(size[:], uint64(len(field)))
		_, _ = h.Write(size[:])
		_, _ = h.Write([]byte(field))
	}
	return hex.EncodeToString(h.Sum(nil))
}
func (d *MergeDescriptor) ownerPath(owner int) ([]string, error) {
	if d == nil || owner < 1 || owner > len(d.Owners) || len(d.Owners) > MaxMergeOwners {
		return nil, ErrMerge
	}
	var chain []MergeOwner
	for owner != 0 {
		if len(chain) >= MaxMergeDepth || owner < 1 || owner > len(d.Owners) {
			return nil, ErrMerge
		}
		frame := d.Owners[owner-1]
		if frame.Parent < 0 || frame.Parent >= owner {
			return nil, ErrMerge
		}
		switch frame.Kind {
		case "page", "layout":
			if !mergeHex(frame.ID) || frame.Tag != "" {
				return nil, ErrMerge
			}
		case "keyed":
			if frame.Parent == 0 || keyed.ValidateKey(frame.ID) != nil || len(frame.Tag) > 64 || !ascii.HTMLName(frame.Tag) || frame.Tag != strings.ToLower(frame.Tag) {
				return nil, ErrMerge
			}
		default:
			return nil, ErrMerge
		}
		chain = append(chain, frame)
		owner = frame.Parent
	}
	fields := make([]string, 0, len(chain)*3)
	for i := len(chain) - 1; i >= 0; i-- {
		fields = append(fields, chain[i].Kind, chain[i].ID, chain[i].Tag)
	}
	return fields, nil
}
func (d *MergeDescriptor) Address(island MergeIsland) (string, error) {
	fields, err := d.ownerPath(island.Owner)
	if err != nil || keyed.ValidateKey(island.Key) != nil || !mergeComponent(island.Component) || !MergeSchema(island.Schema) {
		return "", ErrMerge
	}
	return MergeIdentity("nextgo-merge-address-v1", append(fields, "client", island.Key, island.Component, island.Schema)...), nil
}
func (d *MergeDescriptor) ValidateDescriptor() error {
	if d == nil || d.Version != MergeVersion || !retentionPartition(d.Partition) || !mergeHex(d.BodySHA256) || len(d.Owners) < 1 || len(d.Owners) > MaxMergeOwners || len(d.Islands) < 1 || len(d.Islands) > MaxMergeIslands {
		return ErrMerge
	}
	owners := map[string]bool{}
	for i := range d.Owners {
		path, err := d.ownerPath(i + 1)
		if err != nil {
			return err
		}
		id := MergeIdentity("nextgo-merge-owner-v1", path...)
		if owners[id] {
			return ErrMerge
		}
		owners[id] = true
	}
	nodes := map[int]bool{}
	keys := map[string]bool{}
	ranges := append([]MergeIsland{}, d.Islands...)
	for _, island := range d.Islands {
		if island.Node < 1 || island.Node > MaxMergeIslands || nodes[island.Node] || island.Start < 0 || island.BodyStart <= island.Start || island.BodyEnd < island.BodyStart || island.End <= island.BodyEnd || island.End > MaxRetentionHTML || !mergeHex(island.PropsSHA256) || !mergeHex(island.FallbackSHA256) {
			return ErrMerge
		}
		nodes[island.Node] = true
		if _, err := d.Address(island); err != nil {
			return err
		}
		path, _ := d.ownerPath(island.Owner)
		key := MergeIdentity("nextgo-merge-sibling-v1", append(path, island.Key)...)
		if keys[key] {
			return ErrMerge
		}
		keys[key] = true
	}
	sort.Slice(ranges, func(i, j int) bool { return ranges[i].Start < ranges[j].Start })
	for i := 1; i < len(ranges); i++ {
		if ranges[i].Start < ranges[i-1].End {
			return ErrMerge
		}
	}
	encoded, err := json.Marshal(d)
	if err != nil || len(encoded) > MaxMergeDescriptor {
		return ErrMerge
	}
	return nil
}
func (d *MergeDescriptor) Validate(body string) error {
	if d.ValidateDescriptor() != nil || len(body) > MaxRetentionHTML || !utf8.ValidString(body) || MergeDigest(body) != d.BodySHA256 {
		return ErrMerge
	}
	total := 0
	for _, island := range d.Islands {
		if island.End > len(body) || body[island.BodyEnd:island.End] != "</div>" || MergeDigest(body[island.BodyStart:island.BodyEnd]) != island.FallbackSHA256 {
			return ErrMerge
		}
		prefix := `<div data-nextgo-client="1" data-nextgo-island="` + html.EscapeString(island.Component) + `" ` + MergeNodeAttribute + `="` + strconv.Itoa(island.Node) + `" data-nextgo-props="`
		opening := body[island.Start:island.BodyStart]
		if !strings.HasPrefix(opening, prefix) || !strings.HasSuffix(opening, `">`) {
			return ErrMerge
		}
		escaped := opening[len(prefix) : len(opening)-2]
		raw := html.UnescapeString(escaped)
		total += len(raw)
		if len(raw) > MaxMergeProps || total > MaxRetentionHTML || !utf8.ValidString(raw) || html.EscapeString(raw) != escaped || !json.Valid([]byte(raw)) || wirejson.Unique([]byte(raw)) != nil || MergeDigest(raw) != island.PropsSHA256 {
			return ErrMerge
		}
	}
	return nil
}

func mergeFields(raw json.RawMessage, required []string, optional ...string) (map[string]json.RawMessage, error) {
	var fields map[string]json.RawMessage
	if json.Unmarshal(raw, &fields) != nil || fields == nil {
		return nil, ErrMerge
	}
	allowed := map[string]bool{}
	for _, key := range append(append([]string{}, required...), optional...) {
		allowed[key] = true
	}
	for _, key := range required {
		if _, ok := fields[key]; !ok {
			return nil, ErrMerge
		}
	}
	for key, value := range fields {
		if !allowed[key] || strings.TrimSpace(string(value)) == "null" {
			return nil, ErrMerge
		}
	}
	return fields, nil
}
func ParseMergeDescriptor(raw string) (*MergeDescriptor, error) {
	if raw == "" {
		return nil, nil
	}
	if len(raw) > MaxMergeDescriptor || !utf8.ValidString(raw) || wirejson.Unique([]byte(raw)) != nil {
		return nil, ErrMerge
	}
	fields, err := mergeFields([]byte(raw), []string{"version", "partition", "bodySHA256", "owners", "islands"})
	if err != nil {
		return nil, err
	}
	var owners, islands []json.RawMessage
	if json.Unmarshal(fields["owners"], &owners) != nil || json.Unmarshal(fields["islands"], &islands) != nil || len(owners) > MaxMergeOwners || len(islands) > MaxMergeIslands {
		return nil, ErrMerge
	}
	for _, rawOwner := range owners {
		fields, err := mergeFields(rawOwner, []string{"parent", "kind", "id"}, "tag")
		if err != nil {
			return nil, err
		}
		var kind string
		if json.Unmarshal(fields["kind"], &kind) != nil {
			return nil, ErrMerge
		}
		_, tag := fields["tag"]
		if (kind == "keyed") != tag {
			return nil, ErrMerge
		}
	}
	for _, rawIsland := range islands {
		if _, err := mergeFields(rawIsland, []string{"node", "owner", "key", "component", "schema", "start", "bodyStart", "bodyEnd", "end", "propsSHA256", "fallbackSHA256"}); err != nil {
			return nil, err
		}
	}
	var value MergeDescriptor
	if wirejson.Strict([]byte(raw), &value) != nil || value.ValidateDescriptor() != nil {
		return nil, ErrMerge
	}
	return &value, nil
}
func ParseMerge(raw, body string) (*MergeDescriptor, error) {
	value, err := ParseMergeDescriptor(raw)
	if err != nil || value == nil {
		return value, err
	}
	if value.Validate(body) != nil {
		return nil, ErrMerge
	}
	return value, nil
}
