// Package jobs holds the approval gate: a job is printable only after an explicit
// approval of that exact file for that printer (PRD FR-11, FR-29, FR-43).
package jobs

import (
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
)

var (
	ErrNotApproved = errors.New("not approved yet: the user must click Approve in the viewer")
	ErrFileChanged = errors.New("the job file changed after approval: request approval again")
	ErrNotFound    = errors.New("no such job")
	ErrPrinted     = errors.New("this job was already sent to the printer: request approval again to print another copy")
	ErrDiscarded   = errors.New("this job was discarded: request approval again")
)

type Job struct {
	ID         string          `json:"id"`
	Project    string          `json:"project"`
	PrinterID  string          `json:"printer_id"`
	FilePath   string          `json:"file_path"`
	FileSHA256 string          `json:"file_sha256"`
	Version    int             `json:"version"`
	Approved   bool            `json:"approved"`
	Printed    bool            `json:"printed"`
	Discarded  bool            `json:"discarded,omitempty"`
	Estimate   json.RawMessage `json:"estimate,omitempty"`
}

type Store struct{ db *sql.DB }

func NewStore(db *sql.DB) *Store { return &Store{db: db} }

// Request records a job for file on printerID, unapproved.
func (s *Store) Request(project, printerID, file string) (Job, error) {
	return s.RequestVersion(project, 0, printerID, file, nil)
}

// RequestVersion records an unapproved job for a project version, with the estimate shown at approval.
func (s *Store) RequestVersion(project string, version int, printerID, file string, estimate any) (Job, error) {
	sum, err := hashFile(file)
	if err != nil {
		return Job{}, err
	}
	id, err := newID()
	if err != nil {
		return Job{}, err
	}
	job := Job{ID: id, Project: project, Version: version, PrinterID: printerID, FilePath: file, FileSHA256: sum}
	var est any
	if estimate != nil {
		b, err := json.Marshal(estimate)
		if err != nil {
			return Job{}, err
		}
		job.Estimate, est = b, string(b)
	}
	_, err = s.db.Exec(`INSERT INTO jobs (id, project, version, printer_id, file_path, file_sha256, estimate) VALUES (?, ?, ?, ?, ?, ?, ?)`,
		job.ID, job.Project, job.Version, job.PrinterID, job.FilePath, job.FileSHA256, est)
	return job, err
}

func (s *Store) Get(id string) (Job, error) {
	var j Job
	var approvedAt, printedAt, discardedAt, est sql.NullString
	err := s.db.QueryRow(`SELECT id, project, version, printer_id, file_path, file_sha256, approved_at, printed_at, discarded_at, estimate FROM jobs WHERE id = ?`, id).
		Scan(&j.ID, &j.Project, &j.Version, &j.PrinterID, &j.FilePath, &j.FileSHA256, &approvedAt, &printedAt, &discardedAt, &est)
	if errors.Is(err, sql.ErrNoRows) {
		return Job{}, fmt.Errorf("job %s: %w", id, ErrNotFound)
	}
	j.Approved, j.Printed, j.Discarded = approvedAt.Valid, printedAt.Valid, discardedAt.Valid
	if est.Valid {
		j.Estimate = json.RawMessage(est.String)
	}
	return j, err
}

// Approve is the only way a job becomes printable. Only the web API calls it.
func (s *Store) Approve(id string) error {
	res, err := s.db.Exec(`UPDATE jobs SET approved_at = strftime('%Y-%m-%dT%H:%M:%fZ','now'),
		updated_at = strftime('%Y-%m-%dT%H:%M:%fZ','now') WHERE id = ?`, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("job %s: %w", id, ErrNotFound)
	}
	return nil
}

// CheckPrintable returns nil only when the job is approved and file is the approved file, unchanged.
func (s *Store) CheckPrintable(id, file string) error {
	j, err := s.Get(id)
	if err != nil {
		return err
	}
	if j.Discarded {
		return ErrDiscarded
	}
	if !j.Approved {
		return ErrNotApproved
	}
	if j.Printed {
		return ErrPrinted
	}
	sum, err := hashFile(file)
	if err != nil {
		return err
	}
	if file != j.FilePath || sum != j.FileSHA256 {
		return ErrFileChanged
	}
	return nil
}

// MarkPrinted records that the job reached the printer, so it cannot be sent twice.
func (s *Store) MarkPrinted(id string) error {
	_, err := s.db.Exec(`UPDATE jobs SET printed_at = strftime('%Y-%m-%dT%H:%M:%fZ','now'),
		updated_at = strftime('%Y-%m-%dT%H:%M:%fZ','now') WHERE id = ?`, id)
	return err
}

// Discard takes a job that was not printed off the table: it can't be approved or sent any more.
func (s *Store) Discard(id string) error {
	j, err := s.Get(id)
	if err != nil {
		return err
	}
	if j.Printed {
		return ErrPrinted
	}
	_, err = s.db.Exec(`UPDATE jobs SET discarded_at = strftime('%Y-%m-%dT%H:%M:%fZ','now'),
		updated_at = strftime('%Y-%m-%dT%H:%M:%fZ','now') WHERE id = ? AND discarded_at IS NULL`, id)
	return err
}

// LastPrinted is the most recent job sent to a printer for a project version.
func (s *Store) LastPrinted(printerID, project string, version int) (Job, error) {
	var id string
	err := s.db.QueryRow(`SELECT id FROM jobs WHERE printer_id = ? AND project = ? AND version = ? AND printed_at IS NOT NULL
		ORDER BY printed_at DESC LIMIT 1`, printerID, project, version).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return Job{}, ErrNotFound
	}
	if err != nil {
		return Job{}, err
	}
	return s.Get(id)
}

// LastApproved is the most recent approved, unsent and not discarded job for a printer and
// project version.
func (s *Store) LastApproved(printerID, project string, version int) (Job, error) {
	var id string
	err := s.db.QueryRow(`SELECT id FROM jobs WHERE printer_id = ? AND project = ? AND version = ? AND approved_at IS NOT NULL
		AND printed_at IS NULL AND discarded_at IS NULL ORDER BY approved_at DESC LIMIT 1`, printerID, project, version).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return Job{}, ErrNotFound
	}
	if err != nil {
		return Job{}, err
	}
	return s.Get(id)
}

// Pending lists approved-or-waiting jobs that were neither printed nor discarded, newest first.
func (s *Store) Pending(project string) ([]Job, error) {
	rows, err := s.db.Query(`SELECT id FROM jobs WHERE project = ? AND printed_at IS NULL AND discarded_at IS NULL ORDER BY created_at DESC`, project)
	if err != nil {
		return nil, err
	}
	var ids []string
	for rows.Next() {
		var id string
		rows.Scan(&id)
		ids = append(ids, id)
	}
	rows.Close()
	out := []Job{}
	for _, id := range ids {
		j, err := s.Get(id)
		if err != nil {
			return nil, err
		}
		out = append(out, j)
	}
	return out, nil
}

// HashFile returns the file's SHA-256 in hex.
func HashFile(path string) (string, error) { return hashFile(path) }

func hashFile(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func newID() (string, error) {
	b := make([]byte, 4)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}
