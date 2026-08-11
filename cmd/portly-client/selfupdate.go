package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"log/slog"
	"math/rand"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"time"
)

const (
	updateCheckInterval = 15 * time.Minute
	updateCheckTimeout  = 20 * time.Second
)

// runSelfUpdateLoop periodically checks apiBase for a newer portly-client
// binary and, if one is found, downloads it, atomically replaces the
// running executable, and re-execs into it in place — so an already
// enrolled machine picks up server-side fixes (like this one) without any
// manual reinstall. It returns (only) when ctx is cancelled; a successful
// update never returns; it replaces the process via syscall.Exec.
func runSelfUpdateLoop(ctx context.Context, apiBase string, logger *slog.Logger) {
	if apiBase == "" {
		// Config predates the api_base field (written by 'portly-client init'
		// or an older enroll) — nothing to check against, and no way to
		// discover the URL safely, so self-update stays off for this
		// machine until it's re-enrolled.
		return
	}

	// Jitter the first check so a fleet of machines enrolled around the same
	// time doesn't all hit the server in the same instant, then settle into
	// the regular interval.
	initialDelay := time.Duration(rand.Int63n(int64(updateCheckInterval)))
	timer := time.NewTimer(initialDelay)
	defer timer.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
		}

		if err := checkAndApplyUpdate(apiBase, logger); err != nil {
			logger.Warn("self-update check failed", "err", err)
		}

		timer.Reset(updateCheckInterval)
	}
}

// checkAndApplyUpdate compares the running binary's sha256 against the
// server's current one for this OS/arch and, on a mismatch, replaces it and
// re-execs. Returns nil (with nothing done) whenever already up to date.
func checkAndApplyUpdate(apiBase string, logger *slog.Logger) error {
	osArch := runtime.GOOS + "-" + runtime.GOARCH

	client := &http.Client{Timeout: updateCheckTimeout}

	remoteSum, err := fetchChecksum(client, apiBase, osArch)
	if err != nil {
		return fmt.Errorf("fetch remote checksum: %w", err)
	}

	exePath, err := os.Executable()
	if err != nil {
		return fmt.Errorf("locate running binary: %w", err)
	}
	exePath, err = filepath.EvalSymlinks(exePath)
	if err != nil {
		return fmt.Errorf("resolve running binary path: %w", err)
	}

	localSum, err := sha256File(exePath)
	if err != nil {
		return fmt.Errorf("hash running binary: %w", err)
	}

	if localSum == remoteSum {
		return nil // already up to date
	}

	logger.Info("update available, downloading new portly-client", "current", localSum[:12], "new", remoteSum[:12])

	newPath, err := downloadBinary(client, apiBase, osArch, exePath, remoteSum)
	if err != nil {
		return fmt.Errorf("download update: %w", err)
	}

	if err := os.Rename(newPath, exePath); err != nil {
		os.Remove(newPath)
		return fmt.Errorf("install update: %w", err)
	}

	logger.Info("updated, restarting into new binary", "path", exePath)
	if err := syscall.Exec(exePath, os.Args, os.Environ()); err != nil {
		return fmt.Errorf("exec updated binary: %w", err)
	}
	panic("unreachable: syscall.Exec only returns on error")
}

func fetchChecksum(client *http.Client, apiBase, osArch string) (string, error) {
	req, err := http.NewRequest(http.MethodGet, strings.TrimRight(apiBase, "/")+"/downloads/"+osArch+"/sha256", nil)
	if err != nil {
		return "", err
	}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("server returned HTTP %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 256))
	if err != nil {
		return "", err
	}
	sum := strings.TrimSpace(string(body))
	if len(sum) != sha256.Size*2 {
		return "", fmt.Errorf("unexpected checksum response: %q", sum)
	}
	return sum, nil
}

// downloadBinary fetches the current binary into a temp file next to
// destPath (so the final rename is atomic and never crosses a filesystem)
// and verifies it matches wantSum before returning its path.
func downloadBinary(client *http.Client, apiBase, osArch, destPath, wantSum string) (string, error) {
	req, err := http.NewRequest(http.MethodGet, strings.TrimRight(apiBase, "/")+"/downloads/"+osArch, nil)
	if err != nil {
		return "", err
	}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("server returned HTTP %d", resp.StatusCode)
	}

	tmp, err := os.CreateTemp(filepath.Dir(destPath), filepath.Base(destPath)+".update-*")
	if err != nil {
		return "", err
	}
	tmpPath := tmp.Name()

	h := sha256.New()
	_, copyErr := io.Copy(io.MultiWriter(tmp, h), resp.Body)
	closeErr := tmp.Close()
	if copyErr != nil {
		os.Remove(tmpPath)
		return "", copyErr
	}
	if closeErr != nil {
		os.Remove(tmpPath)
		return "", closeErr
	}

	gotSum := hex.EncodeToString(h.Sum(nil))
	if gotSum != wantSum {
		os.Remove(tmpPath)
		return "", fmt.Errorf("downloaded binary checksum mismatch (got %s, want %s)", gotSum, wantSum)
	}

	if err := os.Chmod(tmpPath, 0o755); err != nil {
		os.Remove(tmpPath)
		return "", err
	}
	return tmpPath, nil
}

func sha256File(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
