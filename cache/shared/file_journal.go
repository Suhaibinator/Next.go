package shared

import (
	"bufio"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"hash/crc32"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/Suhaibinator/Next.go/internal/wirejson"
)

const fileMagic = "NEXTGO-CACHE-WAL-1\n"
const fileSeedMagic = "NEXTGO-CACHE-WAL-2\n"
const maxFrame = 2 << 20

var frameCRC = crc32.MakeTable(crc32.Castagnoli)

type transactionStart struct {
	Kind     string
	Sequence uint64
	At       time.Time
}
type transactionEnd struct {
	Kind   string
	Count  int
	Digest string
}

func encodeFrame(v any) ([]byte, error) {
	b, e := json.Marshal(v)
	if e != nil {
		return nil, e
	}
	if len(b) > maxFrame {
		return nil, ErrCapacity
	}
	return b, nil
}
func writeFrame(w io.Writer, b []byte) error {
	var h [8]byte
	binary.BigEndian.PutUint32(h[:4], uint32(len(b)))
	binary.BigEndian.PutUint32(h[4:], crc32.Checksum(b, frameCRC))
	if _, e := w.Write(h[:]); e != nil {
		return e
	}
	_, e := w.Write(b)
	return e
}
func readFrame(r io.Reader) ([]byte, error) {
	var h [8]byte
	if _, e := io.ReadFull(r, h[:]); e != nil {
		return nil, e
	}
	n := binary.BigEndian.Uint32(h[:4])
	if n == 0 || n > maxFrame {
		return nil, ErrProtocol
	}
	b := make([]byte, int(n))
	if _, e := io.ReadFull(r, b); e != nil {
		return nil, e
	}
	if crc32.Checksum(b, frameCRC) != binary.BigEndian.Uint32(h[4:]) {
		return nil, ErrProtocol
	}
	return b, nil
}
func strictDiskJSON(b []byte, v any) error {
	if err := wirejson.Unique(b); err != nil {
		return err
	}
	return wirejson.Strict(b, v)
}
func transactionSize(seq uint64, at time.Time, records []diskRecord) (int64, error) {
	return emitTransaction(io.Discard, seq, at, records)
}
func emitTransaction(w io.Writer, seq uint64, at time.Time, records []diskRecord) (int64, error) {
	h := sha256.New()
	total := int64(0)
	emit := func(v any, digest bool) error {
		b, e := encodeFrame(v)
		if e != nil {
			return e
		}
		if digest {
			_, _ = h.Write(b)
		}
		if e = writeFrame(w, b); e != nil {
			return e
		}
		total += int64(len(b) + 8)
		return nil
	}
	if e := emit(transactionStart{"begin", seq, at.UTC()}, true); e != nil {
		return 0, e
	}
	for _, r := range records {
		if e := emit(r, true); e != nil {
			return 0, e
		}
	}
	if e := emit(transactionEnd{"commit", len(records), hex.EncodeToString(h.Sum(nil))}, false); e != nil {
		return 0, e
	}
	return total, nil
}
func (f *File) persist(candidate *Memory, records []diskRecord, now time.Time) error {
	if f.sequence == ^uint64(0) || now.UTC().Before(f.lastWrite.UTC()) {
		f.poisoned = true
		return ErrUnavailable
	}
	seq := f.sequence + 1
	seedFormat := f.seedFormat
	for _, record := range records {
		if record.Kind == "seed-put" || record.Kind == "seed-delete" {
			seedFormat = true
		}
	}
	size, e := transactionSize(seq, now, records)
	if e != nil {
		return e
	}
	if f.size+size > f.maxDisk || seedFormat && !f.seedFormat {
		records = snapshotRecords(candidate)
		size, e = transactionSize(seq, now, records)
		if e != nil {
			return e
		}
		if size > f.maxDisk-int64(len(fileMagic)) {
			return ErrCapacity
		}
		e = f.compact(seq, now, records, size, seedFormat)
	} else {
		writer := bufio.NewWriterSize(f.log, 64<<10)
		_, e = emitTransaction(writer, seq, now, records)
		if e == nil {
			e = writer.Flush()
		}
		if e == nil {
			e = f.syncFile(f.log)
		}
		if e == nil {
			f.size += size
		}
	}
	if e != nil {
		f.poisoned = true
		return fmt.Errorf("%w: durable write failed", ErrUnavailable)
	}
	f.sequence = seq
	f.lastWrite = now
	f.seedFormat = seedFormat
	return nil
}
func (f *File) compact(seq uint64, now time.Time, records []diskRecord, size int64, seedFormat bool) error {
	path := filepath.Join(f.dir, "cache.next")
	temp, e := os.OpenFile(path, os.O_RDWR|os.O_CREATE|os.O_EXCL, 0600)
	if e != nil {
		return e
	}
	adopted := false
	defer func() {
		if !adopted {
			_ = temp.Close()
		}
	}()
	writer := bufio.NewWriterSize(temp, 64<<10)
	magic := fileMagic
	if seedFormat {
		magic = fileSeedMagic
	}
	if _, e = writer.WriteString(magic); e != nil {
		return e
	}
	if _, e = emitTransaction(writer, seq, now, records); e != nil {
		return e
	}
	if e = writer.Flush(); e != nil {
		return e
	}
	if e = f.syncFile(temp); e != nil {
		return e
	}
	if e = f.rename(path, filepath.Join(f.dir, "cache.wal")); e != nil {
		return e
	}
	if e = f.syncDir(f.dir); e != nil {
		return e
	}
	if _, e = temp.Seek(0, io.SeekEnd); e != nil {
		return e
	}
	old := f.log
	f.log = temp
	f.size = int64(len(fileMagic)) + size
	// Keep the renamed descriptor open; the deferred close above must not close it.
	adopted = true
	return old.Close()
}

func (f *File) recover() error {
	r := bufio.NewReaderSize(f.log, 64<<10)
	header := make([]byte, len(fileMagic))
	if _, e := io.ReadFull(r, header); e != nil || string(header) != fileMagic && string(header) != fileSeedMagic {
		return ErrProtocol
	}
	f.seedFormat = string(header) == fileSeedMagic
	now := f.now()
	previous := uint64(0)
	var last time.Time
	for {
		b, e := readFrame(r)
		if errors.Is(e, io.EOF) {
			break
		}
		if e != nil {
			return e
		}
		var start transactionStart
		if e = strictDiskJSON(b, &start); e != nil || start.Kind != "begin" || start.Sequence == 0 || previous != 0 && start.Sequence != previous+1 || start.At.IsZero() || start.At.Year() < 1 || start.At.Year() > 9999 || start.At.Before(last) || start.At.After(now) {
			return ErrProtocol
		}
		digest := sha256.New()
		_, _ = digest.Write(b)
		count := 0
		for {
			b, e = readFrame(r)
			if e != nil {
				return e
			}
			var kind struct{ Kind string }
			if e = json.Unmarshal(b, &kind); e != nil {
				return e
			}
			if kind.Kind == "commit" {
				var end transactionEnd
				if e = strictDiskJSON(b, &end); e != nil || end.Count != count || end.Digest != hex.EncodeToString(digest.Sum(nil)) {
					return ErrProtocol
				}
				break
			}
			var record diskRecord
			if e = strictDiskJSON(b, &record); e != nil {
				return e
			}
			if e = f.restoreRecord(record, start.At, now); e != nil {
				return e
			}
			_, _ = digest.Write(b)
			count++
			if count > 2_000_000 {
				return ErrProtocol
			}
		}
		previous = start.Sequence
		last = start.At
	}
	f.sequence = previous
	f.lastWrite = last
	// Drop expired values only after validating the complete journal. Restored
	// deadlines retain original ages; no restart grants a new freshness window.
	for _, elem := range f.memory.entries {
		if !viewFound(elem.Value.(*memoryEntry), now) {
			f.memory.remove(elem)
		}
	}
	f.memory.pruneSeeds(now)
	return nil
}
func (f *File) restoreRecord(r diskRecord, at, now time.Time) error {
	if r.Kind == "seed-put" || r.Kind == "seed-delete" {
		if !f.seedFormat || !validName(r.ID.Namespace) || r.ID.Schema != "seed" || !validToken(r.ID.Key) || r.Entry != nil {
			return ErrProtocol
		}
		if r.Kind == "seed-delete" {
			if r.Ticket != nil {
				return ErrProtocol
			}
			if old, ok := f.memory.seedTickets[r.ID.Key]; ok && old.namespace != r.ID.Namespace {
				return ErrProtocol
			}
			delete(f.memory.seedTickets, r.ID.Key)
			return nil
		}
		if r.Ticket == nil || r.Ticket.CreatedAt.IsZero() || r.Ticket.CreatedAt.Year() < 1 || r.Ticket.CreatedAt.Year() > 9999 || r.Ticket.CreatedAt.After(at) {
			return ErrProtocol
		}
		if _, exists := f.memory.seedTickets[r.ID.Key]; exists || len(f.memory.seedTickets) >= MaxSeedTickets {
			return ErrProtocol
		}
		f.memory.seedTickets[r.ID.Key] = pendingSeed{r.ID.Namespace, now.Add(r.Ticket.CreatedAt.Sub(now))}
		return nil
	}
	if r.Ticket != nil {
		return ErrProtocol
	}
	if validateID(r.ID) != nil {
		return ErrProtocol
	}
	elem := f.memory.entries[r.ID]
	if r.Kind == "delete" {
		if r.Entry != nil {
			return ErrProtocol
		}
		if elem != nil {
			f.memory.remove(elem)
		}
		return nil
	}
	d := r.Entry
	if d == nil || d.ID != r.ID || !validToken(d.Generation) || len(d.Value) > MaxValueBytes {
		return ErrProtocol
	}
	p, e := normalize(d.Policy)
	if e != nil || !equalPolicy(p, d.Policy) {
		return ErrProtocol
	}
	if d.Loaded.IsZero() || d.Loaded.Year() < 1 || d.Loaded.Year() > 9999 || d.Loaded.After(at) || d.Fresh.Before(d.Loaded) || d.Fresh.Year() > 9999 || d.Fresh.After(at) && d.Fresh.After(d.Loaded.Add(p.FreshFor)) || !d.NoExpiry && (d.Expires.IsZero() || d.Expires.Year() > 9999 || d.Expires.Before(d.Fresh) || p.ExpireAfter != Never && d.Expires.After(d.Loaded.Add(p.ExpireAfter)) || p.ExpireAfter == Never && d.Expires.After(at.Add(maxDuration))) || d.NoExpiry && p.ExpireAfter != Never {
		return ErrProtocol
	}
	value := d.Value
	if r.Kind == "mark" {
		if elem == nil || d.Value != nil {
			return ErrProtocol
		}
		old := elem.Value.(*memoryEntry)
		if d.Generation != old.generation || !equalPolicy(p, old.policy) || !d.Loaded.Equal(old.loaded) || d.Fresh.After(at) || !old.noExpiry && (d.NoExpiry || d.Expires.After(old.expires)) {
			return ErrProtocol
		}
		value = old.value
	} else if r.Kind != "put" {
		return ErrProtocol
	}
	cost := metadataCost(r.ID, p) + int64(len(value))
	oldCost := int64(0)
	if elem != nil {
		oldCost = elem.Value.(*memoryEntry).cost
	}
	if cost > f.memory.maxBytes-f.memory.used+oldCost || elem == nil && len(f.memory.entries) >= f.memory.capacity {
		return errors.New("journal exceeds configured memory limits")
	}
	rebase := func(t time.Time) time.Time {
		if t.IsZero() {
			return t
		}
		return now.Add(t.Sub(now))
	}
	entry := &memoryEntry{id: r.ID, policy: p, value: value, generation: d.Generation, found: true, noExpiry: d.NoExpiry, loaded: rebase(d.Loaded), fresh: rebase(d.Fresh), expires: rebase(d.Expires), cost: cost}
	if elem != nil {
		f.memory.remove(elem)
	}
	f.memory.entries[r.ID] = f.memory.lru.PushFront(entry)
	f.memory.used += cost
	return nil
}
