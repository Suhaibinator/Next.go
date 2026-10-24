package shared

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"sort"
	"strings"
	"time"
)

type pendingSeed struct {
	namespace string
	created   time.Time
}
type activeSeed struct {
	namespace    string
	created, end time.Time
	seen         map[[32]byte]struct{}
	seenShared   bool
	bytes        int64
}

const (
	seedTicketBase  int64 = 512
	seedSessionBase int64 = 768
	seedSeenCost    int64 = 96
)

func seedTicketCost(namespace string) int64  { return seedTicketBase + int64(len(namespace)) }
func seedSessionCost(namespace string) int64 { return seedSessionBase + int64(len(namespace)) }
func seedKeyHash(id ID) [32]byte {
	return sha256.Sum256([]byte("nextgo.seed.seen.v1\x00" + id.Namespace + "\x00" + id.Schema + "\x00" + id.Key))
}
func (m *Memory) usageLocked() Usage {
	u := Usage{Entries: len(m.entries), DataBytes: m.used, DataLimit: m.maxBytes, SeedTickets: len(m.seedTickets), SeedSessions: len(m.seedSessions), ControlLimit: m.maxControlBytes}
	for _, t := range m.seedTickets {
		u.ControlBytes += seedTicketCost(t.namespace)
	}
	for _, s := range m.seedSessions {
		u.SeedSeenKeys += len(s.seen)
		u.ControlBytes += seedSessionCost(s.namespace) + int64(len(s.seen))*seedSeenCost
	}
	return u
}
func (m *Memory) Usage() Usage { m.mu.Lock(); defer m.mu.Unlock(); return m.usageLocked() }

func seedToken() (string, error) {
	var b [32]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", ErrUnavailable
	}
	return hex.EncodeToString(b[:]), nil
}
func (m *Memory) pruneSeeds(now time.Time) {
	for token, t := range m.seedTickets {
		if !now.Before(t.created.Add(SeedTicketTTL)) {
			delete(m.seedTickets, token)
		}
	}
	for token, s := range m.seedSessions {
		if !now.Before(s.end) {
			delete(m.seedSessions, token)
		}
	}
}
func (m *Memory) revokeSeeds(namespace string) {
	for token, t := range m.seedTickets {
		if t.namespace == namespace {
			delete(m.seedTickets, token)
		}
	}
	for token, s := range m.seedSessions {
		if s.namespace == namespace {
			delete(m.seedSessions, token)
		}
	}
}
func (m *Memory) ReserveSeeds(ctx context.Context, namespace string) (SeedTicket, error) {
	if ctx == nil || !validName(namespace) {
		return SeedTicket{}, ErrInvalid
	}
	token, err := seedToken()
	if err != nil {
		return SeedTicket{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if err = ctx.Err(); err != nil {
		return SeedTicket{}, err
	}
	now := m.now()
	m.pruneSeeds(now)
	needed := seedTicketCost(namespace)
	if needed > m.maxControlBytes {
		return SeedTicket{}, ErrCapacity
	}
	used, count := m.usageLocked().ControlBytes, len(m.seedTickets)
	var evict []string
	if count >= MaxSeedTickets || used > m.maxControlBytes-needed {
		ordered := make([]string, 0, count)
		for token := range m.seedTickets {
			ordered = append(ordered, token)
		}
		sort.Slice(ordered, func(i, j int) bool {
			a, b := m.seedTickets[ordered[i]], m.seedTickets[ordered[j]]
			if a.created.Equal(b.created) {
				return ordered[i] < ordered[j]
			}
			return a.created.Before(b.created)
		})
		for _, old := range ordered {
			if count < MaxSeedTickets && used <= m.maxControlBytes-needed {
				break
			}
			evict = append(evict, old)
			used -= seedTicketCost(m.seedTickets[old].namespace)
			count--
		}
		if count >= MaxSeedTickets || used > m.maxControlBytes-needed {
			return SeedTicket{}, ErrCapacity
		}
	}
	for _, old := range evict {
		delete(m.seedTickets, old)
	}
	m.seedTickets[token] = pendingSeed{strings.Clone(namespace), now}
	return SeedTicket{token, now.UTC()}, nil
}
func (m *Memory) BeginSeeds(ctx context.Context, namespace, token string) (SeedSession, error) {
	if ctx == nil || !validName(namespace) || !validToken(token) {
		return SeedSession{}, ErrInvalid
	}
	next, err := seedToken()
	if err != nil {
		return SeedSession{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if err = ctx.Err(); err != nil {
		return SeedSession{}, err
	}
	now := m.now()
	m.pruneSeeds(now)
	t, ok := m.seedTickets[token]
	if !ok || t.namespace != namespace {
		return SeedSession{}, nil
	}
	used := m.usageLocked().ControlBytes
	extra := seedSessionCost(namespace) - seedTicketCost(namespace)
	if len(m.seedSessions) >= MaxSeedSessions || used > m.maxControlBytes-extra {
		return SeedSession{}, ErrCapacity
	}
	delete(m.seedTickets, token)
	m.seedSessions[next] = &activeSeed{namespace: t.namespace, created: t.created, end: now.Add(SeedSessionTTL), seen: map[[32]byte]struct{}{}}
	return SeedSession{next, SeedSessionTTL}, nil
}
func (m *Memory) InstallSeeds(ctx context.Context, namespace, token string, values []SeedValue) (int, error) {
	if ctx == nil || !validToken(token) {
		return 0, ErrInvalid
	}
	if err := ValidateSeedValues(namespace, values); err != nil {
		return 0, err
	}
	// Obtain entropy before changing any value or per-key import evidence.
	generations := make([]string, len(values))
	for i := range generations {
		var err error
		generations[i], err = seedToken()
		if err != nil {
			return 0, err
		}
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	now := m.now()
	m.pruneSeeds(now)
	s := m.seedSessions[token]
	if s == nil || s.namespace != namespace {
		return 0, nil
	}
	count, total := len(s.seen), s.bytes
	hashes := make([][32]byte, len(values))
	newHashes := map[[32]byte]struct{}{}
	for i, v := range values {
		hash := seedKeyHash(v.ID)
		hashes[i] = hash
		_, seen := s.seen[hash]
		_, repeated := newHashes[hash]
		if !seen && !repeated {
			newHashes[hash] = struct{}{}
			count++
			total += int64(len(v.Value))
		}
	}
	extra := int64(len(newHashes)) * seedSeenCost
	if count > MaxSeedSessionValues || total > MaxSeedSessionBytes || m.usageLocked().ControlBytes > m.maxControlBytes-extra {
		return 0, ErrCapacity
	}
	if len(newHashes) > 0 && s.seenShared {
		owned := make(map[[32]byte]struct{}, count)
		for key := range s.seen {
			owned[key] = struct{}{}
		}
		s.seen = owned
		s.seenShared = false
	}
	admitted := 0
	for i, v := range values {
		if _, seen := s.seen[hashes[i]]; seen {
			continue
		}
		s.seen[hashes[i]] = struct{}{}
		s.bytes += int64(len(v.Value))
		p, _ := normalize(v.Policy)
		if p.ExpireAfter != Never && !now.Before(s.created.Add(p.ExpireAfter)) {
			continue
		}
		elem := m.entries[v.ID]
		if elem != nil {
			e := elem.Value.(*memoryEntry)
			if viewFound(e, now) || e.token != "" && now.Before(e.leaseEnd) {
				continue
			}
		}
		cost := metadataCost(v.ID, p) + int64(len(v.Value))
		oldCost := int64(0)
		if elem != nil {
			oldCost = elem.Value.(*memoryEntry).cost
		}
		if !m.makeRoom(cost-oldCost, elem == nil, elem, now) {
			continue
		}
		if elem != nil {
			m.remove(elem)
		}
		e := &memoryEntry{id: ownedID(v.ID), policy: ownedPolicy(p), value: bytes.Clone(v.Value), found: true, noExpiry: p.ExpireAfter == Never, loaded: s.created, fresh: s.created.Add(p.FreshFor), generation: generations[i], cost: cost}
		if !e.noExpiry {
			e.expires = s.created.Add(p.ExpireAfter)
		}
		m.entries[e.id] = m.lru.PushFront(e)
		m.used += cost
		admitted++
	}
	return admitted, nil
}
func (m *Memory) FinishSeeds(ctx context.Context, namespace, token string) error {
	if ctx == nil || !validName(namespace) || !validToken(token) {
		return ErrInvalid
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	if s := m.seedSessions[token]; s != nil && s.namespace == namespace {
		delete(m.seedSessions, token)
	}
	return nil
}
