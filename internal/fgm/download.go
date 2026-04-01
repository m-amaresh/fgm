package fgm

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

func fetchManifest() ([]releaseManifest, error) {
	client := &http.Client{Timeout: 2 * time.Minute}
	resp, err := client.Get(manifestURL)
	if err != nil {
		return nil, fmt.Errorf("fetch Go downloads manifest: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("fetch Go downloads manifest: unexpected status %s", resp.Status)
	}

	var releases []releaseManifest
	if err := json.NewDecoder(resp.Body).Decode(&releases); err != nil {
		return nil, fmt.Errorf("decode Go downloads manifest: %w", err)
	}
	return releases, nil
}

func platformReleaseParts() (osName, arch, ext string, err error) {
	switch runtime.GOOS + "/" + runtime.GOARCH {
	case "linux/amd64":
		return "linux", "amd64", ".tar.gz", nil
	case "linux/arm64":
		return "linux", "arm64", ".tar.gz", nil
	case "darwin/arm64":
		return "darwin", "arm64", ".tar.gz", nil
	case "windows/amd64":
		return "windows", "amd64", ".zip", nil
	case "windows/arm64":
		return "windows", "arm64", ".zip", nil
	default:
		return "", "", "", fmt.Errorf("unsupported platform: %s/%s", runtime.GOOS, runtime.GOARCH)
	}
}

func downloadFile(url, dest string) error {
	client := &http.Client{Timeout: 10 * time.Minute}
	resp, err := client.Get(url)
	if err != nil {
		return fmt.Errorf("download %s: %w", url, err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("download %s: unexpected status %s", url, resp.Status)
	}

	out, err := os.Create(dest)
	if err != nil {
		return fmt.Errorf("create archive: %w", err)
	}

	if _, err := io.Copy(out, resp.Body); err != nil {
		_ = out.Close()
		return fmt.Errorf("write archive: %w", err)
	}
	return out.Close()
}

func verifyChecksum(path, expected string) error {
	if expected == "" {
		return fmt.Errorf("missing checksum for %s", filepath.Base(path))
	}

	file, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("open archive for checksum: %w", err)
	}
	defer func() { _ = file.Close() }()

	sum := sha256.New()
	if _, err := io.Copy(sum, file); err != nil {
		return fmt.Errorf("hash archive: %w", err)
	}

	actual := hex.EncodeToString(sum.Sum(nil))
	if !strings.EqualFold(actual, expected) {
		return fmt.Errorf("checksum mismatch for %s: expected %s, got %s", filepath.Base(path), expected, actual)
	}
	return nil
}
