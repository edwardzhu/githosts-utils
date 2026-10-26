package githosts

import (
	"path/filepath"
	"strings"
	"testing"

	"gitlab.com/tozd/go/errors"
)

func TestBackupRetries(t *testing.T) {
	tests := []struct {
		name string
		val  string
		want int
	}{
		{name: "unset uses default", val: "", want: defaultBackupRetries},
		{name: "valid override", val: "5", want: 5},
		{name: "zero disables retries", val: "0", want: 0},
		{name: "negative is ignored", val: "-1", want: defaultBackupRetries},
		{name: "non-numeric is ignored", val: "abc", want: defaultBackupRetries},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv(envVarGitBackupRetries, tc.val)

			if got := backupRetries(); got != tc.want {
				t.Fatalf("backupRetries() = %d, want %d", got, tc.want)
			}
		})
	}
}

func TestRetryBackup(t *testing.T) {
	repo := repository{Name: "repo", PathWithNameSpace: "owner/repo"}

	tests := []struct {
		name      string
		retries   int
		failures  int // number of leading attempts that fail
		wantCalls int
		wantErr   bool
	}{
		{name: "succeeds first time", retries: 3, failures: 0, wantCalls: 1},
		{name: "succeeds after retries", retries: 3, failures: 2, wantCalls: 3},
		{name: "succeeds on last retry", retries: 3, failures: 3, wantCalls: 4},
		{name: "fails after all retries", retries: 3, failures: 10, wantCalls: 4, wantErr: true},
		{name: "zero retries attempts once", retries: 0, failures: 10, wantCalls: 1, wantErr: true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			lastErr := errors.New("clone exploded")
			calls := 0

			err := retryBackup(repo, tc.retries, 0, func() errors.E {
				calls++
				if calls <= tc.failures {
					return lastErr
				}

				return nil
			})

			if calls != tc.wantCalls {
				t.Errorf("backup called %d times, want %d", calls, tc.wantCalls)
			}

			if !tc.wantErr {
				if err != nil {
					t.Fatalf("retryBackup() returned unexpected error: %v", err)
				}

				return
			}

			if err == nil {
				t.Fatal("retryBackup() returned nil error after exhausting retries")
			}

			if !errors.Is(err, lastErr) {
				t.Errorf("error does not wrap the last backup error: %v", err)
			}

			for _, want := range []string{"owner/repo", "clone exploded"} {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("error %q does not mention %q", err.Error(), want)
				}
			}
		})
	}
}

// TestWorkersRetryFailedBackups checks every provider worker routes its
// backups through the shared retry policy, so GIT_BACKUP_RETRIES applies to
// all hosts rather than one.
func TestWorkersRetryFailedBackups(t *testing.T) {
	workers := map[string]func(WorkerConfig, <-chan repository, chan<- RepoBackupResults){
		"generic":     genericWorker,
		"github":      gitHubWorker,
		"gitlab":      gitlabWorker,
		"bitbucket":   bitBucketWorker,
		"azuredevops": azureDevOpsWorker,
		"gitea":       giteaWorker,
		"sourcehut":   sourcehutWorker,
	}

	// TestMain zeroes retryBaseDelay, so the retries below do not sleep.
	t.Setenv(envVarGitBackupRetries, "2")

	for name, worker := range workers {
		t.Run(name, func(t *testing.T) {
			backupDir := t.TempDir()
			missingURL := "file://" + filepath.ToSlash(filepath.Join(backupDir, "does-not-exist"))

			jobs := make(chan repository, 1)
			results := make(chan RepoBackupResults, 1)

			jobs <- repository{
				Name:              "repo",
				PathWithNameSpace: "owner/repo",
				Domain:            "example.com",
				HTTPSUrl:          missingURL,
				URLWithBasicAuth:  missingURL, // bitBucketWorker refuses repos without clone auth
			}
			close(jobs)

			worker(WorkerConfig{BackupDir: backupDir, DiffRemoteMethod: cloneMethod}, jobs, results)

			res := <-results
			if res.Error == nil {
				t.Fatal("backup of a missing repository unexpectedly succeeded")
			}

			if want := "after 3 attempt(s)"; !strings.Contains(res.Error.Error(), want) {
				t.Errorf("error %q does not mention %q", res.Error.Error(), want)
			}
		})
	}
}
