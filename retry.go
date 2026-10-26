package githosts

import (
	"os"
	"strconv"
	"time"

	"gitlab.com/tozd/go/errors"
)

const (
	envVarGitBackupRetries = "GIT_BACKUP_RETRIES"
	defaultBackupRetries   = 3
)

// retryBaseDelay is the wait before the first retry of a failed backup; retry
// n waits n times as long. It is a variable so tests can avoid sleeping.
var retryBaseDelay = 5 * time.Second

// backupRetries returns how many times a failed repository backup is retried,
// honouring the GIT_BACKUP_RETRIES env var and falling back to
// defaultBackupRetries. Zero disables retries; negative or unparsable values
// are ignored.
func backupRetries() int {
	if v := os.Getenv(envVarGitBackupRetries); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= 0 {
			return n
		}
	}

	return defaultBackupRetries
}

// processBackupWithRetry is processBackup under the shared retry policy. Every
// provider worker backs up through it. Retrying is safe because processBackup
// clears the repository's working directory before each attempt.
func processBackupWithRetry(in processBackupInput) errors.E {
	return retryBackup(in.Repo, backupRetries(), retryBaseDelay, func() errors.E {
		return processBackup(in)
	})
}

// retryBackup runs backup, retrying up to retries more times while it fails.
// The wait before retry n is n*baseDelay, giving the host's rate limiter
// progressively longer to recover. If every attempt fails, the last error is
// returned wrapped with the repository and attempt count.
func retryBackup(repo repository, retries int, baseDelay time.Duration, backup func() errors.E) errors.E {
	attempts := retries + 1

	var err errors.E

	for attempt := 1; attempt <= attempts; attempt++ {
		if err = backup(); err == nil {
			return nil
		}

		if attempt == attempts {
			break
		}

		delay := time.Duration(attempt) * baseDelay
		logger.Printf("backup of %s repository %s failed (attempt %d/%d), retrying in %s: %s",
			repo.Domain, repo.PathWithNameSpace, attempt, attempts, delay, err)
		time.Sleep(delay)
	}

	return errors.Errorf("backup of %s failed after %d attempt(s): %w", repo.PathWithNameSpace, attempts, err)
}
