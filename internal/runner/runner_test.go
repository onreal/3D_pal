package runner_test

import (
	"context"
	"strings"
	"testing"

	"github.com/margarit/printpipe/internal/runner"
)

func TestExecReturnsStdout(t *testing.T) {
	out, err := runner.Exec{}.Run(context.Background(), "sh", "-c", "echo hi")
	if err != nil || strings.TrimSpace(string(out)) != "hi" {
		t.Fatalf("got %q, %v", out, err)
	}
}

func TestExecErrorCarriesStderr(t *testing.T) {
	_, err := runner.Exec{}.Run(context.Background(), "sh", "-c", "echo boom >&2; exit 3")
	if err == nil || !strings.Contains(err.Error(), "boom") || !strings.Contains(err.Error(), "sh failed") {
		t.Fatalf("got %v", err)
	}
}

func TestFakeRecordsCalls(t *testing.T) {
	f := &runner.Fake{}
	f.Run(context.Background(), "openscad", "-o", "x.stl")
	if len(f.Calls) != 1 || strings.Join(f.Calls[0], " ") != "openscad -o x.stl" {
		t.Fatalf("got %v", f.Calls)
	}
}

func TestExecRunLogKeepsBothStreams(t *testing.T) {
	out, errOut, err := runner.Exec{}.RunLog(context.Background(), "sh", "-c", "echo out; echo warn >&2")
	if err != nil || strings.TrimSpace(string(out)) != "out" || strings.TrimSpace(string(errOut)) != "warn" {
		t.Fatalf("%q %q %v", out, errOut, err)
	}
	_, _, err = runner.Exec{}.RunLog(context.Background(), "sh", "-c", "printf '%3000s' x | tr ' ' y >&2; exit 2")
	if err == nil || !strings.Contains(err.Error(), "sh failed") || len(err.Error()) > 2100 {
		t.Fatalf("error not trimmed: %d bytes", len(err.Error()))
	}
}
