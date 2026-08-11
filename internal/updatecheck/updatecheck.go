// Package updatecheck compares the running portly-server's build commit
// against the latest commit on GitHub's main branch, so the panel can show
// "update available" without the admin having to check by hand.
package updatecheck

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

// CommitsAPI is the GitHub API endpoint queried for the latest commit on
// main. A package-level var (rather than a constant) so tests can point it
// at a local mock server instead of the real GitHub API.
var CommitsAPI = "https://api.github.com/repos/JxstColin/Portly/commits/main"

type Status struct {
	CurrentCommit   string    `json:"current_commit"`
	LatestCommit    string    `json:"latest_commit,omitempty"`
	UpdateAvailable bool      `json:"update_available"`
	CheckedAt       time.Time `json:"checked_at"`
	CheckError      string    `json:"check_error,omitempty"`
}

// Check fetches the latest commit SHA on GitHub's main branch and compares
// it against currentCommit (the commit this binary was built from, via
// -ldflags). currentCommit == "" or "dev" (a from-source build without
// ldflags) always reports no update available — there's nothing meaningful
// to compare a from-source build against.
func Check(ctx context.Context, currentCommit string) Status {
	status := Status{CurrentCommit: currentCommit, CheckedAt: time.Now()}
	if currentCommit == "" || currentCommit == "dev" {
		return status
	}

	latest, err := fetchLatestCommit(ctx)
	if err != nil {
		status.CheckError = err.Error()
		return status
	}
	status.LatestCommit = latest
	status.UpdateAvailable = latest != currentCommit
	return status
}

func fetchLatestCommit(ctx context.Context) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, CommitsAPI, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Accept", "application/vnd.github+json")

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("github api returned HTTP %d", resp.StatusCode)
	}

	var body struct {
		SHA string `json:"sha"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return "", fmt.Errorf("decode response: %w", err)
	}
	if body.SHA == "" {
		return "", fmt.Errorf("github api response missing sha")
	}
	return body.SHA, nil
}
