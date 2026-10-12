// Package project keeps one folder per part with a numbered folder per compile
// (PRD FR-2, FR-31), and records each version in the database.
package project

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

var nameRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]{0,63}$`)

var ErrBadName = errors.New("project name must be 1 to 64 letters, digits, - or _, starting with a letter or digit")

var ErrNotFound = errors.New("not found")

func ValidName(name string) error {
	if !nameRe.MatchString(name) {
		return fmt.Errorf("%q: %w", name, ErrBadName)
	}
	return nil
}

// File names inside a version folder (and, for the latest version, the project folder).
const (
	SCAD   = "model.scad"
	STL    = "model.stl"
	PNG    = "preview.png"
	Params = "params.json"
	Job    = "job.gcode.3mf"
	RunLog = "run.log"
)

type Version struct {
	Project   string         `json:"project"`
	Number    int            `json:"version"`
	Dir       string         `json:"-"`
	STLHash   string         `json:"stl_sha256"`
	Params    map[string]any `json:"params"`
	CreatedAt time.Time      `json:"created_at"`
}

func (v Version) Path(file string) string { return filepath.Join(v.Dir, file) }

type Store struct {
	db   *sql.DB
	work string
}

func NewStore(db *sql.DB, work string) *Store { return &Store{db: db, work: work} }

func (s *Store) Work() string { return s.work }

// Dir is the project's folder in the workspace.
func (s *Store) Dir(project string) string { return filepath.Join(s.work, project) }

// Next creates the folder for the project's next version. It is not recorded until Commit,
// so a failed compile leaves no version behind (the folder is removed by Discard).
func (s *Store) Next(project string) (Version, error) {
	if err := ValidName(project); err != nil {
		return Version{}, err
	}
	if _, err := s.db.Exec(`INSERT INTO projects (name) VALUES (?) ON CONFLICT (name) DO NOTHING`, project); err != nil {
		return Version{}, err
	}
	var last int
	if err := s.db.QueryRow(`SELECT coalesce(max(version), 0) FROM versions WHERE project = ?`, project).Scan(&last); err != nil {
		return Version{}, err
	}
	n := last + 1
	// A folder may exist from a failed compile; skip past it rather than mixing files.
	for {
		if _, err := os.Stat(filepath.Join(s.Dir(project), "v"+strconv.Itoa(n))); errors.Is(err, os.ErrNotExist) {
			break
		}
		n++
	}
	v := Version{Project: project, Number: n, Dir: filepath.Join(s.Dir(project), "v"+strconv.Itoa(n)), Params: map[string]any{}}
	return v, os.MkdirAll(v.Dir, 0o755)
}

// Discard removes a version folder that never compiled.
func (s *Store) Discard(v Version) { os.RemoveAll(v.Dir) }

// Commit records a compiled version and copies its files to the project folder as the current ones.
func (s *Store) Commit(v Version) (Version, error) {
	params, _ := json.Marshal(v.Params)
	if err := os.WriteFile(v.Path(Params), params, 0o644); err != nil {
		return v, err
	}
	v.CreatedAt = time.Now().UTC()
	_, err := s.db.Exec(`INSERT INTO versions (project, version, scad_path, stl_path, png_path, stl_sha256, params, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		v.Project, v.Number, v.Path(SCAD), v.Path(STL), v.Path(PNG), v.STLHash, string(params), v.CreatedAt.Format(time.RFC3339Nano))
	if err != nil {
		return v, err
	}
	s.db.Exec(`UPDATE projects SET updated_at = strftime('%Y-%m-%dT%H:%M:%fZ','now') WHERE name = ?`, v.Project)
	for _, f := range []string{SCAD, STL, PNG, Params} {
		if err := copyFile(v.Path(f), filepath.Join(s.Dir(v.Project), f)); err != nil && !errors.Is(err, os.ErrNotExist) {
			return v, err
		}
	}
	return v, nil
}

// Promote copies a version's file (such as the approved job) to the project folder.
func (s *Store) Promote(v Version, src, name string) error {
	return copyFile(src, filepath.Join(s.Dir(v.Project), name))
}

func (s *Store) scan(rows interface{ Scan(...any) error }, project string) (Version, error) {
	var v Version
	var params, created string
	if err := rows.Scan(&v.Number, &v.STLHash, &params, &created); err != nil {
		return v, err
	}
	v.Project = project
	v.Dir = filepath.Join(s.Dir(project), "v"+strconv.Itoa(v.Number))
	json.Unmarshal([]byte(params), &v.Params)
	v.CreatedAt, _ = time.Parse(time.RFC3339Nano, created)
	return v, nil
}

// Get returns version n; n <= 0 means the latest.
func (s *Store) Get(project string, n int) (Version, error) {
	if err := ValidName(project); err != nil {
		return Version{}, err
	}
	q := `SELECT version, stl_sha256, params, created_at FROM versions WHERE project = ? AND version = ?`
	args := []any{project, n}
	if n <= 0 {
		q = `SELECT version, stl_sha256, params, created_at FROM versions WHERE project = ? ORDER BY version DESC LIMIT 1`
		args = args[:1]
	}
	v, err := s.scan(s.db.QueryRow(q, args...), project)
	if errors.Is(err, sql.ErrNoRows) {
		if n <= 0 {
			return v, fmt.Errorf("project %q has no compiled versions: %w", project, ErrNotFound)
		}
		return v, fmt.Errorf("project %q has no version %d: %w", project, n, ErrNotFound)
	}
	return v, err
}

func (s *Store) List(project string) ([]Version, error) {
	if err := ValidName(project); err != nil {
		return nil, err
	}
	rows, err := s.db.Query(`SELECT version, stl_sha256, params, created_at FROM versions WHERE project = ? ORDER BY version`, project)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Version
	for rows.Next() {
		v, err := s.scan(rows, project)
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

type Summary struct {
	Name      string    `json:"name"`
	Latest    int       `json:"latest_version"`
	UpdatedAt time.Time `json:"updated_at"`
}

// Projects lists projects with at least one version, most recently changed first.
func (s *Store) Projects() ([]Summary, error) {
	rows, err := s.db.Query(`SELECT p.name, max(v.version), p.updated_at FROM projects p
		JOIN versions v ON v.project = p.name GROUP BY p.name ORDER BY p.updated_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Summary
	for rows.Next() {
		var p Summary
		var at string
		if err := rows.Scan(&p.Name, &p.Latest, &at); err != nil {
			return nil, err
		}
		p.UpdatedAt, _ = time.Parse("2006-01-02T15:04:05.999Z", at)
		out = append(out, p)
	}
	return out, rows.Err()
}

// Log appends a line to the project's run.log (also kept in the runs table by the pipeline).
func (s *Store) Log(project, line string) {
	if ValidName(project) != nil {
		return
	}
	os.MkdirAll(s.Dir(project), 0o755)
	f, err := os.OpenFile(filepath.Join(s.Dir(project), RunLog), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	defer f.Close()
	fmt.Fprintf(f, "%s %s\n", time.Now().UTC().Format(time.RFC3339), strings.TrimSpace(line))
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	tmp := dst + ".tmp"
	out, err := os.Create(tmp)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	if err := out.Close(); err != nil {
		return err
	}
	return os.Rename(tmp, dst)
}
