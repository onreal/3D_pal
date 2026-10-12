// Package estimates slices any version on request and caches the result by its inputs
// (PRD FR-31 to FR-38). An estimate never creates or approves a job.
package estimates

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/margarit/printpipe/internal/slicer"
)

type Estimate struct {
	Project     string    `json:"project"`
	Version     int       `json:"version"`
	PrinterID   string    `json:"printer_id"`
	Status      string    `json:"status"` // running, done, failed
	Error       string    `json:"error,omitempty"`
	Minutes     int       `json:"minutes"`
	Grams       float64   `json:"grams"`
	Metres      float64   `json:"metres"`
	Filament    string    `json:"filament"`
	Layers      int       `json:"layers"`
	LayerHeight float64   `json:"layer_height_mm"`
	Supports    bool      `json:"supports"`
	Cost        float64   `json:"cost,omitempty"` // grams × price per kg, if a price is set
	Currency    string    `json:"currency,omitempty"`
	ComputedAt  time.Time `json:"computed_at"`
	Key         string    `json:"key"`
	JobPath     string    `json:"-"` // the sliced file, reused at approval
}

// Input is everything that changes a slice. Equal inputs give the cached result.
type Input struct {
	Project       string
	Version       int
	STLHash       string
	PrinterID     string
	ProfileHashes string // hash of the profile files and the job's overrides
	PricePerKg    float64
	Currency      string
}

func (in Input) Key() string {
	h := sha256.Sum256([]byte(strings.Join([]string{in.STLHash, in.PrinterID, in.ProfileHashes}, "\x00")))
	return hex.EncodeToString(h[:])
}

// SliceFunc slices the input's model and returns the slicer's result.
type SliceFunc func(ctx context.Context) (slicer.Result, error)

// Summarise rounds a slicer result for people: whole minutes, grams to 0.1, cost to 0.01.
func Summarise(r slicer.Result, pricePerKg float64) Estimate {
	e := Estimate{Status: "done", Minutes: int(math.Round(float64(r.Seconds) / 60)), Layers: r.Layers,
		LayerHeight: r.LayerHeight, Supports: r.Supports, JobPath: r.Path}
	var grams, metres float64
	var types []string
	for _, f := range r.Filaments {
		grams += f.Grams
		metres += f.Metres
		if f.Type != "" && !contains(types, f.Type) {
			types = append(types, f.Type)
		}
	}
	e.Grams = math.Round(grams*10) / 10
	e.Metres = math.Round(metres*100) / 100
	e.Filament = strings.Join(types, ", ")
	if pricePerKg > 0 {
		e.Cost = math.Round(grams*pricePerKg/1000*100) / 100
	}
	return e
}

func contains(l []string, s string) bool {
	for _, x := range l {
		if x == s {
			return true
		}
	}
	return false
}

type call struct {
	done chan struct{}
	est  Estimate
	err  error
}

type Service struct {
	db     *sql.DB
	onDone func(Estimate)

	mu       sync.Mutex
	inflight map[string]*call
}

// New returns the service; onDone (optional) is told when a background estimate finishes.
func New(db *sql.DB, onDone func(Estimate)) *Service {
	if onDone == nil {
		onDone = func(Estimate) {}
	}
	return &Service{db: db, onDone: onDone, inflight: map[string]*call{}}
}

// Cached returns the stored estimate for in, if there is one (running, done or failed).
func (s *Service) Cached(in Input) (Estimate, bool, error) {
	var status string
	var result, msg, jobPath sql.NullString
	var updated string
	err := s.db.QueryRow(`SELECT status, result, error, job_path, updated_at FROM estimates WHERE key = ?`, in.Key()).
		Scan(&status, &result, &msg, &jobPath, &updated)
	if errors.Is(err, sql.ErrNoRows) {
		return Estimate{}, false, nil
	}
	if err != nil {
		return Estimate{}, false, err
	}
	s.mu.Lock()
	_, running := s.inflight[in.Key()]
	s.mu.Unlock()
	if status == "running" && !running {
		return Estimate{}, false, nil // left over from a restart: treat as missing
	}
	e := Estimate{Status: status, Error: msg.String}
	if status == "done" {
		var r slicer.Result
		if err := json.Unmarshal([]byte(result.String), &r); err != nil {
			return Estimate{}, false, err
		}
		if _, err := os.Stat(r.Path); err != nil {
			return Estimate{}, false, nil // the sliced file is gone: slice again
		}
		e = Summarise(r, in.PricePerKg)
	}
	e.ComputedAt, _ = time.Parse("2006-01-02T15:04:05.999Z", updated)
	return s.label(e, in), true, nil
}

func (s *Service) label(e Estimate, in Input) Estimate {
	e.Project, e.Version, e.PrinterID, e.Key = in.Project, in.Version, in.PrinterID, in.Key()
	if e.Cost > 0 {
		e.Currency = in.Currency
	}
	return e
}

// Get returns the cached estimate or slices now; concurrent calls for the same inputs share one slice.
func (s *Service) Get(ctx context.Context, in Input, slice SliceFunc) (Estimate, error) {
	if e, ok, err := s.Cached(in); err != nil {
		return e, err
	} else if ok && e.Status == "done" {
		return e, nil
	}
	c := s.start(in, slice)
	select {
	case <-c.done:
		return c.est, c.err
	case <-ctx.Done():
		return Estimate{}, ctx.Err()
	}
}

// Start returns the cached estimate if done; otherwise it starts (or joins) a background
// slice and returns a running estimate. onDone fires when it finishes.
func (s *Service) Start(in Input, slice SliceFunc) (Estimate, error) {
	if e, ok, err := s.Cached(in); err != nil {
		return e, err
	} else if ok && e.Status != "failed" {
		return e, nil
	}
	s.start(in, slice)
	return s.label(Estimate{Status: "running"}, in), nil
}

func (s *Service) start(in Input, slice SliceFunc) *call {
	key := in.Key()
	s.mu.Lock()
	if c, ok := s.inflight[key]; ok {
		s.mu.Unlock()
		return c
	}
	c := &call{done: make(chan struct{})}
	s.inflight[key] = c
	s.mu.Unlock()

	s.db.Exec(`INSERT INTO estimates (key, project, version, printer_id, status) VALUES (?, ?, ?, ?, 'running')
		ON CONFLICT (key) DO UPDATE SET status = 'running', error = NULL, updated_at = strftime('%Y-%m-%dT%H:%M:%fZ','now')`,
		key, in.Project, in.Version, in.PrinterID)

	go func() {
		// Slicing outlives the request that started it; a stuck slicer is cut off after 10 minutes.
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
		defer cancel()
		r, err := slice(ctx)
		if err != nil {
			c.err = err
			c.est = s.label(Estimate{Status: "failed", Error: err.Error(), ComputedAt: time.Now().UTC()}, in)
			s.db.Exec(`UPDATE estimates SET status = 'failed', error = ?, updated_at = strftime('%Y-%m-%dT%H:%M:%fZ','now') WHERE key = ?`, err.Error(), key)
		} else {
			raw, _ := json.Marshal(r)
			c.est = s.label(Summarise(r, in.PricePerKg), in)
			c.est.ComputedAt = time.Now().UTC()
			if _, dbErr := s.db.Exec(`UPDATE estimates SET status = 'done', result = ?, job_path = ?, error = NULL,
				updated_at = strftime('%Y-%m-%dT%H:%M:%fZ','now') WHERE key = ?`, string(raw), r.Path, key); dbErr != nil {
				c.err = fmt.Errorf("saving estimate: %w", dbErr)
			}
		}
		s.mu.Lock()
		delete(s.inflight, key)
		s.mu.Unlock()
		close(c.done)
		s.onDone(c.est)
	}()
	return c
}

// ForVersion lists the cached, finished estimates of a version, one per printer and settings.
func (s *Service) ForVersion(project string, version int, price func(printerID string) (float64, string)) ([]Estimate, error) {
	rows, err := s.db.Query(`SELECT key, printer_id, status, result, error, updated_at FROM estimates
		WHERE project = ? AND version = ? ORDER BY updated_at DESC`, project, version)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Estimate{}
	for rows.Next() {
		var key, printerID, status, updated string
		var result, msg sql.NullString
		if err := rows.Scan(&key, &printerID, &status, &result, &msg, &updated); err != nil {
			return nil, err
		}
		e := Estimate{Status: status, Error: msg.String}
		pricePerKg, currency := price(printerID)
		if status == "done" {
			var r slicer.Result
			if json.Unmarshal([]byte(result.String), &r) != nil {
				continue
			}
			e = Summarise(r, pricePerKg)
		}
		e.Project, e.Version, e.PrinterID, e.Key = project, version, printerID, key
		if e.Cost > 0 {
			e.Currency = currency
		}
		e.ComputedAt, _ = time.Parse("2006-01-02T15:04:05.999Z", updated)
		out = append(out, e)
	}
	return out, rows.Err()
}
