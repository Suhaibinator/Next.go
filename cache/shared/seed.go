package shared

import (
	"context"
	"encoding/json"
	"time"
)

const (
	DefaultControlBytes  int64 = 8 << 20
	maxControlBytes      int64 = 1 << 30
	MaxSeedTickets             = 128
	MaxSeedSessions            = 16
	MaxSeedBatch               = 128
	MaxSeedBatchBytes          = 1 << 20
	MaxSeedEncodedBytes        = (2 << 20) - 1024
	MaxSeedSessionValues       = 10000
	MaxSeedSessionBytes        = 32 << 20
	SeedTicketTTL              = 30 * 24 * time.Hour
	SeedSessionTTL             = 5 * time.Minute
)

// Usage reports retained data and seed-control ledgers. Control accounting
// includes bounded tickets, sessions and fixed-size once-per-key hashes; it is
// independent of DataBytes. Neither ledger is a process heap or RSS limit.
type Usage struct {
	Entries                                 int
	DataBytes, DataLimit                    int64
	SeedTickets, SeedSessions, SeedSeenKeys int
	ControlBytes, ControlLimit              int64
}

// SeedTicket is a one-use authority-issued permission reserved before building.
// It is public build data, but only authenticated trusted writers may import it.
type SeedTicket struct {
	Token     string
	CreatedAt time.Time
}

// SeedSession is an ephemeral bounded import lease. It never survives restart.
type SeedSession struct {
	Token    string
	LeaseFor time.Duration
}

// SeedValue is an owned serialized value. Its age starts at ticket reservation.
type SeedValue struct {
	ID     ID
	Policy Policy
	Value  []byte
}

// SeedBackend optionally supports repeated build imports without resurrecting
// invalidated snapshots. Invalidation revokes all tickets/sessions in its namespace,
// even when no published keys match. Unknown capabilities always deny import.
type SeedBackend interface {
	ReserveSeeds(context.Context, string) (SeedTicket, error)
	BeginSeeds(context.Context, string, string) (SeedSession, error)
	InstallSeeds(context.Context, string, string, []SeedValue) (int, error)
	FinishSeeds(context.Context, string, string) error
}

func ValidateSeedNamespace(namespace string) error {
	if !validName(namespace) {
		return ErrInvalid
	}
	return nil
}
func ValidateSeedToken(token string) error {
	if !validToken(token) {
		return ErrInvalid
	}
	return nil
}

// ValidateSeedValues validates an entire batch before any admission or mutation.
func ValidateSeedValues(namespace string, values []SeedValue) error {
	if !validName(namespace) || len(values) > MaxSeedBatch {
		return ErrInvalid
	}
	seen := map[ID]bool{}
	total := 0
	encodedBytes := 2 // array brackets
	for i, v := range values {
		if validateID(v.ID) != nil || v.ID.Namespace != namespace || seen[v.ID] || len(v.Value) > MaxValueBytes {
			return ErrInvalid
		}
		if _, err := normalize(v.Policy); err != nil {
			return err
		}
		seen[v.ID] = true
		total += len(v.Value)
		if total > MaxSeedBatchBytes {
			return ErrInvalid
		}
		// Bound encoding work one element at a time; a legal count of large
		// policies must not first allocate a many-megabyte aggregate envelope.
		encoded, err := json.Marshal(v)
		if err != nil {
			return ErrInvalid
		}
		encodedBytes += len(encoded)
		if i > 0 {
			encodedBytes++
		}
		if encodedBytes > MaxSeedEncodedBytes {
			return ErrInvalid
		}
	}
	return nil
}
