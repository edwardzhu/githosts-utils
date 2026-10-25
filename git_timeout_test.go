package githosts

import (
	"context"
	"net"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"gitlab.com/tozd/go/errors"
)

func TestGitCommandTimeout(t *testing.T) {
	tests := []struct {
		name string
		val  string
		want time.Duration
	}{
		{name: "unset uses default", val: "", want: defaultCommandTimeout},
		{name: "valid override in seconds", val: "3600", want: time.Hour},
		{name: "zero is ignored", val: "0", want: defaultCommandTimeout},
		{name: "negative is ignored", val: "-5", want: defaultCommandTimeout},
		{name: "non-numeric is ignored", val: "abc", want: defaultCommandTimeout},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv(envVarGitCommandTimeout, tc.val)

			if got := gitCommandTimeout(); got != tc.want {
				t.Fatalf("gitCommandTimeout() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestCloneRepositoryReportsTimeout(t *testing.T) {
	backupDir := t.TempDir()

	ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer cancel()

	err := cloneRepository(cloneRepositoryInput{
		Ctx:         ctx,
		Repo:        repository{Name: "big-repo", PathWithNameSpace: "owner/big-repo"},
		CloneURL:    "https://example.invalid/owner/big-repo.git",
		WorkingPath: filepath.Join(backupDir, "work"),
		BackupDIR:   backupDir,
		Timeout:     42 * time.Second,
	})
	if err == nil {
		t.Fatal("cloneRepository() returned nil error for an expired context")
	}

	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("error does not wrap context.DeadlineExceeded: %v", err)
	}

	for _, want := range []string{"big-repo", "timed out after 42s", envVarGitCommandTimeout} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not contain %q", err.Error(), want)
		}
	}
}

// TestCloneRepositoryKillsStalledClone simulates a stalled network: the remote
// accepts the connection but never replies, so git hangs until the context
// deadline kills it.
func TestCloneRepositoryKillsStalledClone(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to listen: %v", err)
	}
	defer listener.Close()

	// Hold accepted connections open without replying; release them once the
	// listener is closed.
	go func() {
		var conns []net.Conn

		for {
			conn, acceptErr := listener.Accept()
			if acceptErr != nil {
				for _, c := range conns {
					c.Close()
				}

				return
			}

			conns = append(conns, conn)
		}
	}()

	const timeout = 2 * time.Second

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	backupDir := t.TempDir()
	start := time.Now()

	cloneErr := cloneRepository(cloneRepositoryInput{
		Ctx:         ctx,
		Repo:        repository{Name: "stalled-repo", PathWithNameSpace: "owner/stalled-repo"},
		CloneURL:    "http://" + listener.Addr().String() + "/owner/stalled-repo.git",
		WorkingPath: filepath.Join(backupDir, "work"),
		BackupDIR:   backupDir,
		Timeout:     timeout,
	})
	if cloneErr == nil {
		t.Fatal("cloneRepository() returned nil error for a stalled remote")
	}

	if elapsed := time.Since(start); elapsed > timeout+30*time.Second {
		t.Errorf("clone took %v, expected it to be killed shortly after %v", elapsed, timeout)
	}

	if !errors.Is(cloneErr, context.DeadlineExceeded) {
		t.Errorf("error does not wrap context.DeadlineExceeded: %v", cloneErr)
	}

	if !strings.Contains(cloneErr.Error(), "timed out after 2s") {
		t.Errorf("error %q does not report the timeout", cloneErr.Error())
	}
}
