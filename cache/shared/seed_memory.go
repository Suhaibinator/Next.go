package shared

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"time"
)

type pendingSeed struct {
	namespace string
	created   time.Time
}
type activeSeed struct {
	namespace    string
	created, end time.Time
	seen         map[ID]struct{}
	bytes        int64
}

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
	if len(m.seedTickets) >= MaxSeedTickets {
		oldest := ""
		var at time.Time
		for t, p := range m.seedTickets {
			if oldest == "" || p.created.Before(at) || p.created.Equal(at) && t < oldest {
				oldest, at = t, p.created
			}
		}
		delete(m.seedTickets, oldest)
	}
	m.seedTickets[token] = pendingSeed{namespace, now}
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
	if len(m.seedSessions) >= MaxSeedSessions {
		return SeedSession{}, ErrCapacity
	}
	delete(m.seedTickets, token)
	m.seedSessions[next] = &activeSeed{namespace: namespace, created: t.created, end: now.Add(SeedSessionTTL), seen: map[ID]struct{}{}}
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
	for _, v := range values {
		if _, seen := s.seen[v.ID]; !seen {
			count++
			total += int64(len(v.Value))
		}
	}
	if count > MaxSeedSessionValues || total > MaxSeedSessionBytes {
		return 0, ErrCapacity
	}
	admitted := 0
	for i, v := range values {
		if _, seen := s.seen[v.ID]; seen {
			continue
		}
		s.seen[v.ID] = struct{}{}
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
		e := &memoryEntry{id: v.ID, policy: p, value: bytes.Clone(v.Value), found: true, noExpiry: p.ExpireAfter == Never, loaded: s.created, fresh: s.created.Add(p.FreshFor), generation: generations[i], cost: cost}
		if !e.noExpiry {
			e.expires = s.created.Add(p.ExpireAfter)
		}
		m.entries[v.ID] = m.lru.PushFront(e)
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
