package client

import (
	"errors"
	"fmt"
	"strings"
)

// These are retention-policy ledgers, not measurements of arbitrary app state,
// the browser DOM implementation or the Go heap. The active view counts too.
const (
	retainedViewLimit     = 3
	retainedViewNodes     = 4000
	retainedTotalNodes    = 8000
	retainedViewIslands   = 64
	retainedMetadataBytes = 4 << 20
	retainedHistoryLimit  = 128
)

var errRetainedCapacity = errors.New("nextgo: retained view capacity exhausted")

type retainedInfo struct {
	ID, URL, Partition, Fingerprint string
	Nodes, Islands, Bytes           int
	used                            uint64
}

type retainedIndex struct {
	entries    map[string]retainedInfo
	active     string
	sequence   uint64
	aliases    map[string]string
	aliasOrder []string
}

func retainedID(value string) bool {
	if len(value) != 32 {
		return false
	}
	for _, r := range value {
		if !(r >= '0' && r <= '9') && !(r >= 'a' && r <= 'f') {
			return false
		}
	}
	return true
}

// plan is pure: a rejected candidate cannot evict the currently displayed UI.
func (r *retainedIndex) plan(next retainedInfo) ([]string, error) {
	if !retainedID(next.ID) || len(next.URL) == 0 || len(next.URL) > historyURLLimit || len(next.Partition) == 0 || len(next.Partition) > 128 || len(next.Fingerprint) != 64 || next.Nodes < 1 || next.Nodes > retainedViewNodes || next.Islands < 0 || next.Islands > retainedViewIslands || next.Bytes < 0 || next.Bytes > retainedMetadataBytes {
		return nil, fmt.Errorf("nextgo: retained view exceeds admission policy")
	}
	nodes, size, count := next.Nodes, next.Bytes, 1
	for id, entry := range r.entries {
		if id != next.ID {
			nodes += entry.Nodes
			size += entry.Bytes
			count++
		}
	}
	removed := map[string]bool{}
	var evictions []string
	for count > retainedViewLimit || nodes > retainedTotalNodes || size > retainedMetadataBytes {
		oldest := ""
		var used uint64
		for id, entry := range r.entries {
			if id == next.ID || id == r.active || removed[id] {
				continue
			}
			if oldest == "" || entry.used < used || entry.used == used && id < oldest {
				oldest, used = id, entry.used
			}
		}
		if oldest == "" {
			return nil, errRetainedCapacity
		}
		entry := r.entries[oldest]
		removed[oldest] = true
		evictions = append(evictions, oldest)
		nodes -= entry.Nodes
		size -= entry.Bytes
		count--
	}
	return evictions, nil
}

func (r *retainedIndex) put(next retainedInfo) {
	if r.entries == nil {
		r.entries = map[string]retainedInfo{}
	}
	r.sequence++
	next.ID, next.URL, next.Partition, next.Fingerprint = strings.Clone(next.ID), strings.Clone(next.URL), strings.Clone(next.Partition), strings.Clone(next.Fingerprint)
	next.used = r.sequence
	r.entries[next.ID] = next
	r.active = next.ID
}

func (r *retainedIndex) remove(id string) {
	delete(r.entries, id)
	if r.active == id {
		r.active = ""
	}
	r.dropAliases(id)
}

func (r *retainedIndex) dropAliases(id string) {
	kept := r.aliasOrder[:0]
	for _, key := range r.aliasOrder {
		value, exists := r.aliases[key]
		if !exists || value == id {
			delete(r.aliases, key)
		} else {
			kept = append(kept, key)
		}
	}
	for i := len(kept); i < len(r.aliasOrder); i++ {
		r.aliasOrder[i] = ""
	}
	r.aliasOrder = kept
}

func (r *retainedIndex) bind(key, id string) {
	if !retainedID(key) || !retainedID(id) {
		return
	}
	if _, ok := r.entries[id]; !ok {
		return
	}
	if r.aliases == nil {
		r.aliases = map[string]string{}
	}
	if _, ok := r.aliases[key]; !ok {
		r.aliasOrder = append(r.aliasOrder, strings.Clone(key))
		if len(r.aliasOrder) > retainedHistoryLimit {
			delete(r.aliases, r.aliasOrder[0])
			r.aliasOrder = r.aliasOrder[1:]
		}
	}
	r.aliases[key] = id
}

func (r *retainedIndex) lookup(key, target, partition, fingerprint string) (string, bool) {
	id := r.aliases[key]
	entry, ok := r.entries[id]
	return id, ok && entry.URL == target && entry.Partition == partition && entry.Fingerprint == fingerprint
}
