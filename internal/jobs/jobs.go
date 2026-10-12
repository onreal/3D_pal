// Package jobs holds the approval gate: a job is printable only after an explicit
// approval of that exact file for that printer (PRD FR-11, FR-29, FR-43).
package jobs

import (
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
)

var (
	ErrNotApproved = errors.New("not approved yet: the user must click Approve in the viewer")
	ErrFileChanged = errors.New("the job file changed after approval: request approval again")
	ErrNotFound    = errors.New("no such job")
)

type Job struct {
	ID         string `json:"id"`
	Project    string `json:"project"`
	PrinterID  string `json:"printer_id"`
	FilePath   string `json:"file_path"`
	FileSHA256 string `json:"file_sha256"`
	Approved   bool   `json:"approved"`
}

type Store struct{ db *sql.DB }

func NewStore(db *sql.DB) *Store { return &Store{db: db} }

// Request records a job for file on printerID, unapproved.
func (s *Store) Request(project, printerID, file string) (Job, error) {
	sum, err := hashFile(file)
	if err != nil {
		return Job{}, err
	}
	id, err := newID()
	if err != nil {
		return Job{}, err
	}
	job := Job{ID: id, Project: project, PrinterID: printerID, FilePath: file, FileSHA256: sum}
	_, err = s.db.Exec(`INSERT INTO jobs (id, project, printer_id, file_path, file_sha256) VALUES (?, ?, ?, ?, ?)`,
		job.ID, job.Project, job.PrinterID, job.FilePath, job.FileSHA256)
	return job, err
}

func (s *Store) Get(id string) (Job, error) {
	var j Job
	var approvedAt sql.NullString
	err := s.db.QueryRow(`SELECT id, project, printer_id, file_path, file_sha256, approved_at FROM jobs WHERE id = ?`, id).
		Scan(&j.ID, &j.Project, &j.PrinterID, &j.FilePath, &j.FileSHA256, &approvedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return Job{}, fmt.Errorf("job %s: %w", id, ErrNotFound)
	}
	j.Approved = approvedAt.Valid
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
	if !j.Approved {
		return ErrNotApproved
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
