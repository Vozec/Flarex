package state

import (
	"encoding/json"
	"fmt"
	"time"

	bolt "go.etcd.io/bbolt"
)

const (
	bucketWorkers       = "workers"
	bucketQuotas        = "quota_history"
	bucketAPIKeys       = "api_keys"
	bucketAuditLog      = "audit_log"
	bucketMetricsSeries = "metrics_series"
	bucketTestHistory   = "test_history"
)

// testHistoryCap bounds the number of entries kept in bucketTestHistory.
// Older rows are pruned on PutTestHistory so we never accumulate forever.
const testHistoryCap = 100

type Store struct {
	db *bolt.DB
}

type WorkerRec struct {
	Name      string    `json:"name"`
	AccountID string    `json:"account_id"`
	URL       string    `json:"url"`
	CreatedAt time.Time `json:"created_at"`
	Requests  uint64    `json:"requests"`

	Backend  string `json:"backend,omitempty"`
	Hostname string `json:"hostname,omitempty"`
	ZoneID   string `json:"zone_id,omitempty"`
	RecordID string `json:"record_id,omitempty"`
}

func Open(path string) (*Store, error) {
	db, err := bolt.Open(path, 0o600, &bolt.Options{Timeout: 2 * time.Second})
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", path, err)
	}
	err = db.Update(func(tx *bolt.Tx) error {
		for _, b := range []string{bucketWorkers, bucketQuotas, bucketAPIKeys, bucketAuditLog, bucketMetricsSeries, bucketTestHistory} {
			if _, e := tx.CreateBucketIfNotExists([]byte(b)); e != nil {
				return e
			}
		}
		return nil
	})
	if err != nil {
		db.Close()
		return nil, err
	}
	return &Store{db: db}, nil
}

func (s *Store) Close() error { return s.db.Close() }

// Backup writes a consistent snapshot of the bbolt file to `w`. Uses a
// read transaction under the hood (no writer block) and produces a file
// that can be opened directly by a future `flarex restore`.
func (s *Store) Backup(w interface{ Write(p []byte) (int, error) }) error {
	return s.db.View(func(tx *bolt.Tx) error {
		_, err := tx.WriteTo(writerShim{w})
		return err
	})
}

// writerShim adapts any Write-only target to io.Writer for bolt.Tx.WriteTo.
type writerShim struct {
	w interface{ Write(p []byte) (int, error) }
}

func (s writerShim) Write(p []byte) (int, error) { return s.w.Write(p) }

// APIKey is a persisted admin-API key. Stored in the `api_keys` bucket
// keyed by ID (ULID). Only the sha256 Hash is persisted; the raw key is
// shown once at creation time. See docs/admin-web-ui.md for the scope
// model.
type APIKey struct {
	ID         string     `json:"id"`
	Name       string     `json:"name"`
	Hash       string     `json:"hash"`
	Prefix     string     `json:"prefix"`
	Scopes     []string   `json:"scopes"`
	Disabled   bool       `json:"disabled"`
	CreatedAt  time.Time  `json:"created_at"`
	LastUsedAt time.Time  `json:"last_used_at,omitempty"`
	ExpiresAt  *time.Time `json:"expires_at,omitempty"` // nil = never
}

func (s *Store) PutAPIKey(k APIKey) error {
	raw, err := json.Marshal(k)
	if err != nil {
		return err
	}
	return s.db.Update(func(tx *bolt.Tx) error {
		return tx.Bucket([]byte(bucketAPIKeys)).Put([]byte(k.ID), raw)
	})
}

func (s *Store) ListAPIKeys() ([]APIKey, error) {
	var out []APIKey
	err := s.db.View(func(tx *bolt.Tx) error {
		b := tx.Bucket([]byte(bucketAPIKeys))
		return b.ForEach(func(_, v []byte) error {
			var k APIKey
			if err := json.Unmarshal(v, &k); err != nil {
				return err
			}
			out = append(out, k)
			return nil
		})
	})
	return out, err
}

func (s *Store) GetAPIKey(id string) (APIKey, error) {
	var k APIKey
	err := s.db.View(func(tx *bolt.Tx) error {
		v := tx.Bucket([]byte(bucketAPIKeys)).Get([]byte(id))
		if v == nil {
			return fmt.Errorf("api key not found")
		}
		return json.Unmarshal(v, &k)
	})
	return k, err
}

func (s *Store) DeleteAPIKey(id string) error {
	return s.db.Update(func(tx *bolt.Tx) error {
		return tx.Bucket([]byte(bucketAPIKeys)).Delete([]byte(id))
	})
}

// MarkAPIKeyUsed updates LastUsedAt for a key by its Hash. Best-effort —
// failures silently swallowed since this is telemetry, not security.
func (s *Store) MarkAPIKeyUsed(hash string) {
	_ = s.db.Update(func(tx *bolt.Tx) error {
		b := tx.Bucket([]byte(bucketAPIKeys))
		return b.ForEach(func(_, v []byte) error {
			var k APIKey
			if err := json.Unmarshal(v, &k); err != nil {
				return nil
			}
			if k.Hash == hash {
				k.LastUsedAt = time.Now().UTC()
				raw, _ := json.Marshal(k)
				return b.Put([]byte(k.ID), raw)
			}
			return nil
		})
	})
}

// AuditEvent is one admin action entry — who did what, when, against which
// target. Keyed by timestamp (UnixMicro, big-endian) so bbolt's cursor
// iterates chronologically.
type AuditEvent struct {
	At     time.Time `json:"at"`
	Who    string    `json:"who"`        // API key name, or "bootstrap", or "session:<name>"
	Action string    `json:"action"`     // e.g. "token.add", "worker.recycle", "apikey.create"
	Target string    `json:"target"`     // entity acted on (account id, worker name, key id)
	Detail string    `json:"detail,omitempty"`
}

func (s *Store) PutAudit(ev AuditEvent) error {
	raw, err := json.Marshal(ev)
	if err != nil {
		return err
	}
	// key = UnixMicro padded to 16 bytes for chronological ordering.
	ts := ev.At.UTC().UnixMicro()
	key := []byte(fmt.Sprintf("%016x", ts))
	return s.db.Update(func(tx *bolt.Tx) error {
		return tx.Bucket([]byte(bucketAuditLog)).Put(key, raw)
	})
}

// ListAudit returns up to `limit` most recent entries, newest first.
// limit <= 0 returns all.
func (s *Store) ListAudit(limit int) ([]AuditEvent, error) {
	var out []AuditEvent
	err := s.db.View(func(tx *bolt.Tx) error {
		c := tx.Bucket([]byte(bucketAuditLog)).Cursor()
		for k, v := c.Last(); k != nil; k, v = c.Prev() {
			var e AuditEvent
			if err := json.Unmarshal(v, &e); err != nil {
				continue
			}
			out = append(out, e)
			if limit > 0 && len(out) >= limit {
				return nil
			}
		}
		return nil
	})
	return out, err
}

// PutMetricsSample writes one Sample to the persistent ring. Keyed by
// UnixMicro so cursor iteration is chronological. Caller is responsible
// for periodic pruning (see PruneMetricsSamples).
func (s *Store) PutMetricsSample(at time.Time, raw []byte) error {
	key := []byte(fmt.Sprintf("%016x", at.UTC().UnixMicro()))
	return s.db.Update(func(tx *bolt.Tx) error {
		return tx.Bucket([]byte(bucketMetricsSeries)).Put(key, raw)
	})
}

// ListMetricsSamples returns raw JSON samples recorded AFTER `since`, in
// chronological order. Caller decodes each element.
func (s *Store) ListMetricsSamples(since time.Time) ([][]byte, error) {
	var out [][]byte
	err := s.db.View(func(tx *bolt.Tx) error {
		cutoff := []byte(fmt.Sprintf("%016x", since.UTC().UnixMicro()))
		c := tx.Bucket([]byte(bucketMetricsSeries)).Cursor()
		for k, v := c.Seek(cutoff); k != nil; k, v = c.Next() {
			cp := make([]byte, len(v))
			copy(cp, v)
			out = append(out, cp)
		}
		return nil
	})
	return out, err
}

// PruneMetricsSamples drops entries older than `before`. Run periodically.
func (s *Store) PruneMetricsSamples(before time.Time) error {
	return s.db.Update(func(tx *bolt.Tx) error {
		cutoff := []byte(fmt.Sprintf("%016x", before.UTC().UnixMicro()))
		b := tx.Bucket([]byte(bucketMetricsSeries))
		c := b.Cursor()
		for k, _ := c.First(); k != nil && string(k) < string(cutoff); k, _ = c.Next() {
			if err := b.Delete(k); err != nil {
				return err
			}
			c = b.Cursor()
		}
		return nil
	})
}

// PutTestHistory records a test-request run. Keyed by UnixMicro so
// cursor iteration is chronological. Old rows past testHistoryCap are
// pruned on each write so the bucket can't grow unbounded.
func (s *Store) PutTestHistory(at time.Time, raw []byte) error {
	key := []byte(fmt.Sprintf("%016x", at.UTC().UnixMicro()))
	return s.db.Update(func(tx *bolt.Tx) error {
		b := tx.Bucket([]byte(bucketTestHistory))
		if err := b.Put(key, raw); err != nil {
			return err
		}
		if n := b.Stats().KeyN; n > testHistoryCap {
			c := b.Cursor()
			toDrop := n - testHistoryCap
			for k, _ := c.First(); k != nil && toDrop > 0; k, _ = c.First() {
				if err := b.Delete(k); err != nil {
					return err
				}
				toDrop--
			}
		}
		return nil
	})
}

// ListTestHistory returns the most recent `limit` entries, newest first.
func (s *Store) ListTestHistory(limit int) ([][]byte, error) {
	var out [][]byte
	err := s.db.View(func(tx *bolt.Tx) error {
		c := tx.Bucket([]byte(bucketTestHistory)).Cursor()
		for k, v := c.Last(); k != nil; k, v = c.Prev() {
			cp := make([]byte, len(v))
			copy(cp, v)
			out = append(out, cp)
			if limit > 0 && len(out) >= limit {
				return nil
			}
		}
		return nil
	})
	return out, err
}

// SetAPIKeyDisabled flips the Disabled flag without destroying the row.
func (s *Store) SetAPIKeyDisabled(id string, disabled bool) error {
	return s.db.Update(func(tx *bolt.Tx) error {
		b := tx.Bucket([]byte(bucketAPIKeys))
		v := b.Get([]byte(id))
		if v == nil {
			return fmt.Errorf("api key not found")
		}
		var k APIKey
		if err := json.Unmarshal(v, &k); err != nil {
			return err
		}
		k.Disabled = disabled
		raw, _ := json.Marshal(k)
		return b.Put([]byte(id), raw)
	})
}

func (s *Store) PutWorker(w WorkerRec) error {
	raw, _ := json.Marshal(w)
	return s.db.Update(func(tx *bolt.Tx) error {
		return tx.Bucket([]byte(bucketWorkers)).Put([]byte(w.Name), raw)
	})
}

func (s *Store) DeleteWorker(name string) error {
	return s.db.Update(func(tx *bolt.Tx) error {
		return tx.Bucket([]byte(bucketWorkers)).Delete([]byte(name))
	})
}

// QuotaDay is one day's quota snapshot for one account. Date is "YYYY-MM-DD" UTC.
type QuotaDay struct {
	Date      string `json:"date"`
	AccountID string `json:"account_id"`
	Used      uint64 `json:"used"`
	Limit     uint64 `json:"limit"`
}

// PutQuotaSnapshot persists one account's daily usage. Key = "{date}|{account}".
// Idempotent within a day — overwrites same-day entry.
func (s *Store) PutQuotaSnapshot(q QuotaDay) error {
	raw, _ := json.Marshal(q)
	key := q.Date + "|" + q.AccountID
	return s.db.Update(func(tx *bolt.Tx) error {
		return tx.Bucket([]byte(bucketQuotas)).Put([]byte(key), raw)
	})
}

// ListQuotaHistory returns all snapshots. Caller filters by date range / account.
// Sorted by bbolt key order = date ascending.
func (s *Store) ListQuotaHistory() ([]QuotaDay, error) {
	var out []QuotaDay
	err := s.db.View(func(tx *bolt.Tx) error {
		b := tx.Bucket([]byte(bucketQuotas))
		return b.ForEach(func(_, v []byte) error {
			var q QuotaDay
			if err := json.Unmarshal(v, &q); err != nil {
				return err
			}
			out = append(out, q)
			return nil
		})
	})
	return out, err
}

func (s *Store) ListWorkers() ([]WorkerRec, error) {
	var out []WorkerRec
	err := s.db.View(func(tx *bolt.Tx) error {
		b := tx.Bucket([]byte(bucketWorkers))
		return b.ForEach(func(_, v []byte) error {
			var r WorkerRec
			if err := json.Unmarshal(v, &r); err != nil {
				return err
			}
			out = append(out, r)
			return nil
		})
	})
	return out, err
}
