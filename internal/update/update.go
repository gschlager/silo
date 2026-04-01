package update

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"time"

	"github.com/gschlager/silo/internal/config"
)

// UpdateState tracks the last update check.
type UpdateState struct {
	LastCheck     time.Time `json:"last_check"`
	LatestVersion string    `json:"latest_version"`
}

const (
	checkInterval = 24 * time.Hour
	repoOwner     = "gschlager"
	repoName      = "silo"
)

func statePath() string {
	return filepath.Join(config.GlobalConfigDir(), "update-state.json")
}

// CheckForUpdate checks if a newer version is available.
// Returns the new version string if available, empty string otherwise.
func CheckForUpdate(currentVersion string) string {
	state := loadState()

	if time.Since(state.LastCheck) < checkInterval {
		if state.LatestVersion != "" && state.LatestVersion != currentVersion {
			return state.LatestVersion
		}
		return ""
	}

	// Fetch latest release from GitHub.
	latest, err := fetchLatestVersion()
	if err != nil {
		return ""
	}

	state.LastCheck = time.Now()
	state.LatestVersion = latest
	saveState(state)

	if latest != currentVersion && latest != "" {
		return latest
	}
	return ""
}

// Upgrade downloads and replaces the current binary with the latest release.
func Upgrade() error {
	latest, err := fetchLatestVersion()
	if err != nil {
		return fmt.Errorf("checking for updates: %w", err)
	}

	if latest == "" {
		return fmt.Errorf("could not determine latest version")
	}

	// Build download URL.
	goos := runtime.GOOS
	goarch := runtime.GOARCH
	url := fmt.Sprintf("https://github.com/%s/%s/releases/download/%s/silo-%s-%s",
		repoOwner, repoName, latest, goos, goarch)

	fmt.Printf("Downloading %s...\n", latest)

	resp, err := http.Get(url)
	if err != nil {
		return fmt.Errorf("downloading %s: %w", url, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("download failed: HTTP %d", resp.StatusCode)
	}

	// Get current binary path.
	exe, err := os.Executable()
	if err != nil {
		return fmt.Errorf("finding current binary: %w", err)
	}

	// Write to a temporary file next to the current binary.
	tmpFile := exe + ".new"
	out, err := os.Create(tmpFile)
	if err != nil {
		return fmt.Errorf("creating temp file: %w", err)
	}

	if _, err := io.Copy(out, resp.Body); err != nil {
		out.Close()
		os.Remove(tmpFile)
		return fmt.Errorf("writing binary: %w", err)
	}
	out.Close()

	// Make executable.
	if err := os.Chmod(tmpFile, 0755); err != nil {
		os.Remove(tmpFile)
		return fmt.Errorf("setting permissions: %w", err)
	}

	// Replace the current binary.
	if err := os.Rename(tmpFile, exe); err != nil {
		os.Remove(tmpFile)
		return fmt.Errorf("replacing binary: %w", err)
	}

	fmt.Printf("Upgraded to %s\n", latest)
	return nil
}

func fetchLatestVersion() (string, error) {
	url := fmt.Sprintf("https://api.github.com/repos/%s/%s/releases/latest", repoOwner, repoName)
	resp, err := http.Get(url)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("GitHub API returned %d", resp.StatusCode)
	}

	var release struct {
		TagName string `json:"tag_name"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&release); err != nil {
		return "", err
	}
	return release.TagName, nil
}

func loadState() UpdateState {
	data, err := os.ReadFile(statePath())
	if err != nil {
		return UpdateState{}
	}
	var state UpdateState
	json.Unmarshal(data, &state)
	return state
}

func saveState(state UpdateState) {
	data, err := json.Marshal(state)
	if err != nil {
		return
	}
	os.MkdirAll(filepath.Dir(statePath()), 0755)
	os.WriteFile(statePath(), data, 0644)
}
