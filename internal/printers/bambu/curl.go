package bambu

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/margarit/printpipe/internal/printer"
	"github.com/margarit/printpipe/internal/runner"
)

// CurlUploader talks implicit FTPS (port 990, user bblp) through curl. It can become
// native Go later without touching the Uploader interface.
type CurlUploader struct {
	Runner runner.Runner
}

// curlArgs keeps the access code out of the command line: curl reads it from a
// netrc file, so it never shows in process lists or error messages.
func (u *CurlUploader) base(c Config) ([]string, func(), error) {
	f, err := os.CreateTemp("", "netrc-*")
	if err != nil {
		return nil, nil, err
	}
	fmt.Fprintf(f, "machine %s login bblp password %s\n", c.IP, c.AccessCode)
	f.Close()
	cleanup := func() { os.Remove(f.Name()) }
	return []string{"--silent", "--show-error", "--ssl-reqd", "--insecure", "--ftp-pasv",
		"--connect-timeout", "10", "--netrc-file", f.Name()}, cleanup, nil
}

func (u *CurlUploader) Upload(ctx context.Context, c Config, localPath, remoteName string) error {
	args, cleanup, err := u.base(c)
	if err != nil {
		return err
	}
	defer cleanup()
	args = append(args, "-T", localPath, fmt.Sprintf("ftps://%s:990/%s", c.IP, filepath.Base(remoteName)))
	_, err = u.Runner.Run(ctx, "curl", args...)
	return mapCurl(err)
}

func (u *CurlUploader) List(ctx context.Context, c Config) error {
	args, cleanup, err := u.base(c)
	if err != nil {
		return err
	}
	defer cleanup()
	args = append(args, "--list-only", fmt.Sprintf("ftps://%s:990/", c.IP))
	_, err = u.Runner.Run(ctx, "curl", args...)
	return mapCurl(err)
}

var exitCode = regexp.MustCompile(`exit status (\d+)`)

// mapCurl turns curl's exit codes and FTP replies into shared printer errors.
func mapCurl(err error) error {
	if err == nil {
		return nil
	}
	msg := err.Error()
	code := 0
	if m := exitCode.FindStringSubmatch(msg); m != nil {
		code, _ = strconv.Atoi(m[1])
	}
	switch {
	case code == 67 || strings.Contains(msg, "530") || strings.Contains(msg, "Access denied"):
		return fmt.Errorf("FTPS login: %w", printer.ErrAuth)
	case code == 6 || code == 7 || code == 28 || code == 35:
		return fmt.Errorf("FTPS connect: %w", printer.ErrUnreachable)
	case code == 25 || strings.Contains(msg, "550") || strings.Contains(msg, "552") || strings.Contains(msg, "553"):
		return fmt.Errorf("FTPS upload: %w", printer.ErrNoStorage)
	}
	return fmt.Errorf("FTPS: %s", strings.TrimSpace(msg))
}
