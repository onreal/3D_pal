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
	Port   string // 990 when empty
}

func (u *CurlUploader) url(c Config, path string) string {
	port := u.Port
	if port == "" {
		port = "990"
	}
	return fmt.Sprintf("ftps://%s:%s/%s", c.IP, port, path)
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
	args = append(args, "-T", localPath, u.url(c, filepath.Base(remoteName)))
	_, err = u.Runner.Run(ctx, "curl", args...)
	return mapCurl(err)
}

func (u *CurlUploader) List(ctx context.Context, c Config) error {
	args, cleanup, err := u.base(c)
	if err != nil {
		return err
	}
	defer cleanup()
	args = append(args, "--list-only", u.url(c, ""))
	_, err = u.Runner.Run(ctx, "curl", args...)
	return mapCurl(err)
}

var (
	exitCode = regexp.MustCompile(`exit status (\d+)`)
	// FTP reply codes as curl prints them, e.g. "Access denied: 530" or "(25) ... 550";
	// whole words only, so a port number such as 45307 doesn't read as reply 530.
	replyAuth    = regexp.MustCompile(`\b530\b`)
	replyStorage = regexp.MustCompile(`\b55[023]\b`)
)

// mapCurl turns curl's exit codes (first) and FTP replies into shared printer errors.
func mapCurl(err error) error {
	if err == nil {
		return nil
	}
	msg := err.Error()
	code := 0
	if m := exitCode.FindStringSubmatch(msg); m != nil {
		code, _ = strconv.Atoi(m[1])
	}
	switch code {
	case 67: // login denied
		return fmt.Errorf("FTPS login: %w", printer.ErrAuth)
	case 6, 7, 28, 35: // can't resolve, can't connect, timeout, TLS connect error
		return fmt.Errorf("FTPS connect: %w", printer.ErrUnreachable)
	case 9, 25: // remote access denied, upload rejected (the A1 replies 550 without an SD card)
		return fmt.Errorf("FTPS upload: %w", printer.ErrNoStorage)
	case 0:
		switch {
		case replyAuth.MatchString(msg):
			return fmt.Errorf("FTPS login: %w", printer.ErrAuth)
		case replyStorage.MatchString(msg):
			return fmt.Errorf("FTPS upload: %w", printer.ErrNoStorage)
		}
	}
	return fmt.Errorf("FTPS: %s", strings.TrimSpace(msg))
}
