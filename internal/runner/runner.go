// Package runner is the only seam to os/exec; everything else takes a Runner.
package runner

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"path/filepath"
)

type Runner interface {
	Run(ctx context.Context, name string, args ...string) ([]byte, error)
}

// Exec runs real commands and returns the tool's own error text on failure.
type Exec struct{}

func (Exec) Run(ctx context.Context, name string, args ...string) ([]byte, error) {
	var stdout, stderr bytes.Buffer
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		msg := stderr.String()
		if len(msg) > 2000 {
			msg = msg[len(msg)-2000:]
		}
		return nil, fmt.Errorf("%s failed: %w\n%s", filepath.Base(name), err, msg)
	}
	return stdout.Bytes(), nil
}

// Fake records calls; tests script results with OnRun.
type Fake struct {
	Calls [][]string
	OnRun func(name string, args []string) ([]byte, error)
}

func (f *Fake) Run(_ context.Context, name string, args ...string) ([]byte, error) {
	f.Calls = append(f.Calls, append([]string{name}, args...))
	if f.OnRun != nil {
		return f.OnRun(name, args)
	}
	return nil, nil
}
