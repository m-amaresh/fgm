package fgm

import (
	"context"
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

func fetchManifest(ctx context.Context) ([]releaseManifest, error) {
	client := &http.Client{Timeout: 2 * time.Minute}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, manifestURL, nil)
	if err != nil {
		return nil, fmt.Errorf("create manifest request: %w", err)
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch Go downloads manifest: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("fetch Go downloads manifest: unexpected status %s", resp.Status)
	}

	// Limit manifest reads to 32 MiB to prevent unbounded memory usage.
	const maxManifestSize = 32 << 20
	var releases []releaseManifest
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxManifestSize)).Decode(&releases); err != nil {
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

func downloadFile(ctx context.Context, url, dest string) error {
	client := &http.Client{Timeout: 10 * time.Minute}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return fmt.Errorf("create download request: %w", err)
	}
	resp, err := client.Do(req)
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

	// Limit archive downloads to 512 MiB. The largest official Go archive
	// is ~200 MB; this is a safety cap against runaway responses.
	const maxDownloadSize = 512 << 20
	n, copyErr := io.Copy(out, io.LimitReader(resp.Body, maxDownloadSize+1))
	if closeErr := out.Close(); closeErr != nil && copyErr == nil {
		return fmt.Errorf("close archive: %w", closeErr)
	}
	if copyErr != nil {
		return fmt.Errorf("write archive: %w", copyErr)
	}
	if n > maxDownloadSize {
		_ = os.Remove(dest)
		return fmt.Errorf("download %s: response exceeds maximum size (%d bytes)", url, maxDownloadSize)
	}
	return nil
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
