package bambu_test

import (
	"bufio"
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/margarit/printpipe/internal/printer"
	"github.com/margarit/printpipe/internal/printers/bambu"
	"github.com/margarit/printpipe/internal/runner"
)

// fakeSD is an implicit-FTPS server that behaves like the A1's: TLS from the first byte,
// user bblp with the LAN access code, passive data connections over TLS, and 550 on
// upload when there is no SD card.
type fakeSD struct {
	addr   string
	code   string
	noCard bool
	mu     sync.Mutex
	files  map[string][]byte
}

func startFakeSD(t *testing.T, code string, noCard bool) *fakeSD {
	t.Helper()
	cfg := &tls.Config{Certificates: []tls.Certificate{selfSigned(t)}}
	l, err := tls.Listen("tcp", "127.0.0.1:0", cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { l.Close() })
	s := &fakeSD{addr: l.Addr().String(), code: code, noCard: noCard, files: map[string][]byte{}}
	go func() {
		for {
			c, err := l.Accept()
			if err != nil {
				return
			}
			go s.serve(c, cfg)
		}
	}()
	return s
}

func (s *fakeSD) serve(c net.Conn, cfg *tls.Config) {
	defer c.Close()
	r := bufio.NewReader(c)
	say := func(f string, a ...any) { fmt.Fprintf(c, f+"\r\n", a...) }
	say("220 Bambu FTP")
	var user string
	authed := false
	var pasv net.Listener
	defer func() {
		if pasv != nil {
			pasv.Close()
		}
	}()
	accept := func() (net.Conn, error) {
		if pasv == nil {
			return nil, errors.New("no passive listener")
		}
		d, err := pasv.Accept()
		pasv.Close()
		pasv = nil
		if err != nil {
			return nil, err
		}
		// Finish the TLS handshake before any data, as a real server does; an empty
		// listing would otherwise close the socket mid-handshake.
		return d, d.(*tls.Conn).Handshake()
	}
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			return
		}
		cmd, arg, _ := strings.Cut(strings.TrimSpace(line), " ")
		switch strings.ToUpper(cmd) {
		case "USER":
			user = arg
			say("331 Password required")
		case "PASS":
			if user == "bblp" && arg == s.code {
				authed = true
				say("230 Logged in")
			} else {
				say("530 Login incorrect")
			}
		case "PBSZ":
			say("200 PBSZ=0")
		case "PROT":
			say("200 Protection set to Private")
		case "PWD":
			say(`257 "/" is the current directory`)
		case "TYPE", "MODE", "STRU":
			say("200 OK")
		case "CWD":
			say("250 OK")
		case "EPSV", "PASV":
			if !authed {
				say("530 Not logged in")
				continue
			}
			var err error
			pasv, err = tls.Listen("tcp", "127.0.0.1:0", cfg)
			if err != nil {
				say("425 Can't open data connection")
				continue
			}
			port := pasv.Addr().(*net.TCPAddr).Port
			if strings.ToUpper(cmd) == "EPSV" {
				say("229 Entering Extended Passive Mode (|||%d|)", port)
			} else {
				say("227 Entering Passive Mode (127,0,0,1,%d,%d)", port/256, port%256)
			}
		case "NLST", "LIST":
			say("150 Opening data connection")
			d, err := accept()
			if err != nil {
				say("425 No data connection")
				continue
			}
			s.mu.Lock()
			for name := range s.files {
				fmt.Fprintf(d, "%s\r\n", name)
			}
			s.mu.Unlock()
			d.Close()
			say("226 Transfer complete")
		case "STOR":
			if s.noCard {
				say("550 Failed to open file (no SD card)")
				continue
			}
			say("150 Ok to send data")
			d, err := accept()
			if err != nil {
				say("425 No data connection")
				continue
			}
			b, _ := io.ReadAll(d)
			d.Close()
			s.mu.Lock()
			s.files[filepath.Base(arg)] = b
			s.mu.Unlock()
			say("226 Transfer complete")
		case "SIZE":
			say("550 No such file")
		case "QUIT":
			say("221 Bye")
			return
		default:
			say("502 Command not implemented")
		}
	}
}

// curlUploader points the real curl at the fake server's port instead of 990.
func curlUploader(t *testing.T, s *fakeSD) (*bambu.CurlUploader, bambu.Config) {
	t.Helper()
	if _, err := exec.LookPath("curl"); err != nil {
		t.Skip("curl not installed")
	}
	host, port, _ := net.SplitHostPort(s.addr)
	return &bambu.CurlUploader{Runner: runner.Exec{}, Port: port}, bambu.Config{IP: host, Serial: "S", AccessCode: "12345678"}
}

func TestCurlUploadsOverImplicitFTPS(t *testing.T) {
	sd := startFakeSD(t, "12345678", false)
	u, cfg := curlUploader(t, sd)
	job := filepath.Join(t.TempDir(), "job.gcode.3mf")
	os.WriteFile(job, []byte("PK sliced job"), 0o644)
	ctx := context.Background()
	if err := u.List(ctx, cfg); err != nil {
		t.Fatalf("login and list: %v", err)
	}
	if err := u.Upload(ctx, cfg, job, "cube_v1.gcode.3mf"); err != nil {
		t.Fatalf("upload: %v", err)
	}
	sd.mu.Lock()
	got := string(sd.files["cube_v1.gcode.3mf"])
	sd.mu.Unlock()
	if got != "PK sliced job" {
		t.Fatalf("stored %q", got)
	}
}

func TestCurlWrongAccessCodeIsErrAuth(t *testing.T) {
	sd := startFakeSD(t, "other-code", false)
	u, cfg := curlUploader(t, sd)
	if err := u.List(context.Background(), cfg); !errors.Is(err, printer.ErrAuth) {
		t.Fatalf("want ErrAuth, got %v", err)
	}
}

func TestCurlNoSDCardIsErrNoStorage(t *testing.T) {
	sd := startFakeSD(t, "12345678", true)
	u, cfg := curlUploader(t, sd)
	job := filepath.Join(t.TempDir(), "job.gcode.3mf")
	os.WriteFile(job, []byte("x"), 0o644)
	if err := u.Upload(context.Background(), cfg, job, "x.gcode.3mf"); !errors.Is(err, printer.ErrNoStorage) {
		t.Fatalf("want ErrNoStorage, got %v", err)
	}
}

func TestCurlNoPrinterIsErrUnreachable(t *testing.T) {
	l, _ := net.Listen("tcp", "127.0.0.1:0")
	addr := l.Addr().String()
	l.Close()
	u, cfg := curlUploader(t, &fakeSD{addr: addr})
	if err := u.List(context.Background(), cfg); !errors.Is(err, printer.ErrUnreachable) {
		t.Fatalf("want ErrUnreachable, got %v", err)
	}
}
