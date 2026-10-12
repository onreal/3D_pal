package jobs_test

import (
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"testing"

	"github.com/margarit/printpipe/internal/jobs"
	"github.com/margarit/printpipe/internal/store/storetest"
)

func writeFile(t *testing.T, dir, name, body string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func newStore(t *testing.T) (*jobs.Store, string) {
	return jobs.NewStore(storetest.Open(t)), t.TempDir()
}

func TestUnapprovedJobIsNotPrintable(t *testing.T) {
	store, dir := newStore(t)
	file := writeFile(t, dir, "job.gcode.3mf", "v1")
	job, err := store.Request("cube", "a1", file)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.CheckPrintable(job.ID, file); !errors.Is(err, jobs.ErrNotApproved) {
		t.Fatalf("want ErrNotApproved, got %v", err)
	}
}

func TestApprovalIsVoidWhenFileChanges(t *testing.T) {
	store, dir := newStore(t)
	file := writeFile(t, dir, "job.gcode.3mf", "v1")
	job, _ := store.Request("cube", "a1", file)
	if err := store.Approve(job.ID); err != nil {
		t.Fatal(err)
	}
	writeFile(t, dir, "job.gcode.3mf", "v2") // re-sliced after approval
	if err := store.CheckPrintable(job.ID, file); !errors.Is(err, jobs.ErrFileChanged) {
		t.Fatalf("want ErrFileChanged, got %v", err)
	}
}

func TestApprovedJobIsPrintable(t *testing.T) {
	store, dir := newStore(t)
	file := writeFile(t, dir, "job.gcode.3mf", "v1")
	job, _ := store.Request("cube", "a1", file)
	store.Approve(job.ID)
	if err := store.CheckPrintable(job.ID, file); err != nil {
		t.Fatalf("want printable, got %v", err)
	}
}

func TestApprovalCannotBeMovedToAnotherFile(t *testing.T) {
	store, dir := newStore(t)
	file := writeFile(t, dir, "job.gcode.3mf", "v1")
	other := writeFile(t, dir, "other.gcode.3mf", "v1") // same bytes, different file
	job, _ := store.Request("cube", "a1", file)
	store.Approve(job.ID)
	if err := store.CheckPrintable(job.ID, other); !errors.Is(err, jobs.ErrFileChanged) {
		t.Fatalf("want ErrFileChanged, got %v", err)
	}
}

func TestJobRecordsTargetPrinterAndID(t *testing.T) {
	store, dir := newStore(t)
	job, _ := store.Request("cube", "a1", writeFile(t, dir, "job.gcode.3mf", "v1"))
	got, err := store.Get(job.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !regexp.MustCompile(`^[0-9a-f]{8}$`).MatchString(got.ID) || got.PrinterID != "a1" || got.Approved {
		t.Fatalf("got %+v", got)
	}
}

func TestUnknownJob(t *testing.T) {
	store, _ := newStore(t)
	if err := store.Approve("deadbeef"); !errors.Is(err, jobs.ErrNotFound) {
		t.Fatalf("want ErrNotFound, got %v", err)
	}
}

func TestJobIsSentOnlyOnce(t *testing.T) {
	store, dir := newStore(t)
	file := writeFile(t, dir, "job.gcode.3mf", "v1")
	job, _ := store.RequestVersion("cube", 3, "a1", file, map[string]any{"minutes": 21})
	store.Approve(job.ID)
	if err := store.MarkPrinted(job.ID); err != nil {
		t.Fatal(err)
	}
	if err := store.CheckPrintable(job.ID, file); !errors.Is(err, jobs.ErrPrinted) {
		t.Fatalf("want ErrPrinted, got %v", err)
	}
	got, _ := store.Get(job.ID)
	if got.Version != 3 || string(got.Estimate) != `{"minutes":21}` {
		t.Fatalf("%+v", got)
	}
	if p, _ := store.Pending("cube"); len(p) != 0 {
		t.Fatalf("printed job still pending: %+v", p)
	}
}
