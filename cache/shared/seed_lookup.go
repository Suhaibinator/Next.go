package shared

import (
	"context"
	"encoding/json"
	"errors"
	"time"
)

// Seed supplies a typed immutable build value. Its age is assigned by the
// authority-issued ticket, never by a caller-supplied generation timestamp.
type Seed[T any] struct {
	Key    string
	Value  T
	Policy Policy
}

// ReserveSeeds reserves one-use build permission before executing build loaders.
// Any acknowledged invalidation in this namespace revokes pending permission.
func (s *Store) ReserveSeeds(ctx context.Context) (SeedTicket, error) {
	if ctx == nil || s.ScopeID() == "" {
		return SeedTicket{}, ErrInvalid
	}
	backend, ok := s.backend.(SeedBackend)
	if !ok {
		return SeedTicket{}, ErrProtocol
	}
	ctx, cancel := context.WithTimeout(ctx, s.options.Timeout)
	defer cancel()
	if err := ctx.Err(); err != nil {
		return SeedTicket{}, err
	}
	ticket, err := backend.ReserveSeeds(ctx, s.namespace)
	if err != nil {
		return SeedTicket{}, err
	}
	if !validSeedTicket(ticket) {
		return SeedTicket{}, ErrProtocol
	}
	if err := ctx.Err(); err != nil {
		return SeedTicket{}, err
	}
	ticket.CreatedAt = ticket.CreatedAt.UTC()
	return ticket, nil
}

// Bootstrap consumes a build ticket, including for an empty or oversized-only
// batch. Revoked/consumed/unknown tickets skip import without resurrecting data.
// Keys/policies/JSON and complete batch budgets are checked before consumption.
// Oversized individual JSON values are skipped; ordinary runtime loading still
// works. Successful imports do not overwrite runtime generations or retry keys.
// Errors may follow partial accepted batches; the consumed ticket cannot replay.
func Bootstrap[T any](ctx context.Context, s *Store, ticket string, seeds []Seed[T]) (count int, result error) {
	if ctx == nil || s.ScopeID() == "" || ValidateSeedToken(ticket) != nil || len(seeds) > MaxSeedSessionValues {
		return 0, ErrInvalid
	}
	backend, ok := s.backend.(SeedBackend)
	if !ok {
		return 0, ErrProtocol
	}
	ctx, cancel := context.WithTimeout(ctx, s.options.Timeout)
	defer cancel()
	prepared, costs, err := prepareLookupSeeds(ctx, s, seeds)
	if err != nil {
		return 0, err
	}
	started := time.Now()
	session, err := backend.BeginSeeds(ctx, s.namespace, ticket)
	if err != nil {
		return 0, err
	}
	if !validSeedSession(session) {
		return 0, ErrProtocol
	}
	if session.Token == "" {
		if err := ctx.Err(); err != nil {
			return 0, err
		}
		return 0, nil
	}
	defer func() {
		cleanup, stop := context.WithTimeout(context.Background(), 500*time.Millisecond)
		defer stop()
		if err := backend.FinishSeeds(cleanup, s.namespace, session.Token); err != nil {
			result = errors.Join(result, err)
		}
	}()
	work, stop := context.WithDeadline(ctx, started.Add(session.LeaseFor))
	defer stop()
	for begin := 0; begin < len(prepared); {
		if err := work.Err(); err != nil {
			return count, err
		}
		end, raw, encoded := begin, 0, 2
		for end < len(prepared) && end-begin < MaxSeedBatch {
			nextEncoded := encoded + costs[end]
			if end > begin {
				nextEncoded++
			}
			if raw+len(prepared[end].Value) > MaxSeedBatchBytes || nextEncoded > MaxSeedEncodedBytes {
				break
			}
			raw += len(prepared[end].Value)
			encoded = nextEncoded
			end++
		}
		if end == begin {
			return count, ErrInvalid
		}
		installed, err := backend.InstallSeeds(work, s.namespace, session.Token, prepared[begin:end])
		if err != nil {
			return count, err
		}
		if installed < 0 || installed > end-begin {
			return count, ErrProtocol
		}
		count += installed
		begin = end
	}
	if err := work.Err(); err != nil {
		return count, err
	}
	return count, nil
}

func prepareLookupSeeds[T any](ctx context.Context, s *Store, seeds []Seed[T]) ([]SeedValue, []int, error) {
	seen := make(map[string]bool, len(seeds))
	policies := make([]Policy, len(seeds))
	metadataTotal := 0
	for i, seed := range seeds {
		if err := ctx.Err(); err != nil {
			return nil, nil, err
		}
		if validateID(ID{s.namespace, s.schema, seed.Key}) != nil || seen[seed.Key] {
			return nil, nil, ErrInvalid
		}
		seen[seed.Key] = true
		policy, err := normalize(seed.Policy)
		if err != nil {
			return nil, nil, err
		}
		policies[i] = policy
		metadata, err := json.Marshal(SeedValue{ID: ID{s.namespace, s.schema, seed.Key}, Policy: policy})
		if err != nil {
			return nil, nil, ErrInvalid
		}
		metadataTotal += len(metadata)
		if metadataTotal > MaxSeedSessionBytes {
			return nil, nil, ErrInvalid
		}
	}
	prepared := make([]SeedValue, 0, len(seeds))
	costs := make([]int, 0, len(seeds))
	rawTotal, encodedTotal := 0, 0
	for i, seed := range seeds {
		if err := ctx.Err(); err != nil {
			return nil, nil, err
		}
		data, err := encode(seed.Value)
		if err != nil {
			return nil, nil, ErrInvalid
		}
		if len(data) > MaxValueBytes {
			continue
		}
		var owned T
		if decodeJSON(data, &owned) != nil {
			return nil, nil, ErrProtocol
		}
		value := SeedValue{ID: ID{s.namespace, s.schema, seed.Key}, Policy: policies[i], Value: data}
		encoded, err := json.Marshal(value)
		if err != nil || len(encoded)+2 > MaxSeedEncodedBytes {
			return nil, nil, ErrInvalid
		}
		rawTotal += len(data)
		encodedTotal += len(encoded)
		if rawTotal > MaxSeedSessionBytes || encodedTotal > MaxSeedSessionBytes {
			return nil, nil, ErrInvalid
		}
		prepared = append(prepared, value)
		costs = append(costs, len(encoded))
	}
	if err := ctx.Err(); err != nil {
		return nil, nil, err
	}
	return prepared, costs, nil
}
