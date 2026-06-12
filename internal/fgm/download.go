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

// httpClient is shared by manifest fetches and archive downloads so the
// connection to go.dev is reused within a single run. There is deliberately
// no overall timeout: archive downloads on slow links can take arbitrarily
// long, and cancellation comes from the request context instead.
var httpClient = &http.Client{
	Transport: &http.Transport{
		Proxy:                 http.ProxyFromEnvironment,
		ForceAttemptHTTP2:     true,
		TLSHandshakeTimeout:   10 * time.Second,
		ResponseHeaderTimeout: 30 * time.Second,
	},
}

type ctxReader struct {
	ctx context.Context
	r   io.Reader
}

func canceledErr(ctx context.Context, err error) error {
	if ctxErr := ctx.Err(); ctxErr != nil {
		return ctxErr
	}
	return err
}

func (r *ctxReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	n, err := r.r.Read(p)
	if err == nil {
		if ctxErr := r.ctx.Err(); ctxErr != nil {
			return n, ctxErr
		}
	}
	return n, err
}

func fetchManifest(ctx context.Context) ([]releaseManifest, error) {
	// The manifest is small; bound the whole fetch.
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, manifestURL, nil)
	if err != nil {
		return nil, fmt.Errorf("create manifest request: %w", err)
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch Go downloads manifest: %w", canceledErr(ctx, err))
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("fetch Go downloads manifest: unexpected status %s", resp.Status)
	}

	// Limit manifest reads to 32 MiB to prevent unbounded memory usage.
	const maxManifestSize = 32 << 20
	var releases []releaseManifest
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxManifestSize)).Decode(&releases); err != nil {
		return nil, fmt.Errorf("decode Go downloads manifest: %w", canceledErr(ctx, err))
	}
	return releases, nil
}

type platformParts struct {
	osName, arch, ext string
}

// supportedPlatforms enumerates the GOOS/GOARCH combinations fgm targets
// and the archive extension served by go.dev for each.
var supportedPlatforms = map[string]platformParts{
	"linux/amd64":   {"linux", "amd64", ".tar.gz"},
	"linux/arm64":   {"linux", "arm64", ".tar.gz"},
	"darwin/arm64":  {"darwin", "arm64", ".tar.gz"},
	"windows/amd64": {"windows", "amd64", ".zip"},
	"windows/arm64": {"windows", "arm64", ".zip"},
}

func platformReleaseParts() (osName, arch, ext string, err error) {
	key := runtime.GOOS + "/" + runtime.GOARCH
	if p, ok := supportedPlatforms[key]; ok {
		return p.osName, p.arch, p.ext, nil
	}
	return "", "", "", fmt.Errorf("unsupported platform: %s", key)
}

// partialPrefix names in-flight download temp files in the downloads dir.
// Leftovers from killed processes are swept by cleanStalePartials.
const partialPrefix = ".partial-"

// downloadAndVerify downloads url into dest, hashing the body as it streams
// and verifying it against expectedSHA256 before the file is moved into
// place. dest never exists in a partial or corrupt state: the body is
// written to a temp file in the same directory and only renamed once the
// download completed and the checksum matched.
func downloadAndVerify(ctx context.Context, url, dest, expectedSHA256 string) error {
	if err := validateSHA256(expectedSHA256); err != nil {
		return fmt.Errorf("invalid checksum for %s: %w", filepath.Base(dest), err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return fmt.Errorf("create download request: %w", err)
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("download %s: %w", url, canceledErr(ctx, err))
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("download %s: unexpected status %s", url, resp.Status)
	}

	tmp, err := os.CreateTemp(filepath.Dir(dest), partialPrefix+"*")
	if err != nil {
		return fmt.Errorf("create download temp file: %w", err)
	}
	tmpName := tmp.Name()
	cleanup := func() { _ = os.Remove(tmpName) }

	// Limit archive downloads to 512 MiB. The largest official Go archive
	// is ~200 MB; this is a safety cap against runaway responses.
	const maxDownloadSize = 512 << 20
	sum := sha256.New()
	n, copyErr := io.Copy(io.MultiWriter(tmp, sum), io.LimitReader(&ctxReader{ctx: ctx, r: resp.Body}, maxDownloadSize+1))
	if closeErr := tmp.Close(); closeErr != nil && copyErr == nil {
		copyErr = closeErr
	}
	if copyErr != nil {
		cleanup()
		return fmt.Errorf("write archive: %w", canceledErr(ctx, copyErr))
	}
	if n > maxDownloadSize {
		cleanup()
		return fmt.Errorf("download %s: response exceeds maximum size (%d bytes)", url, maxDownloadSize)
	}

	actual := hex.EncodeToString(sum.Sum(nil))
	if !strings.EqualFold(actual, expectedSHA256) {
		cleanup()
		return fmt.Errorf("checksum mismatch for %s: expected %s, got %s", filepath.Base(dest), expectedSHA256, actual)
	}

	if err := os.Rename(tmpName, dest); err != nil {
		cleanup()
		return fmt.Errorf("move verified archive into place: %w", err)
	}
	return nil
}

// verifyChecksum re-hashes an already-downloaded archive (the cached-archive
// path; fresh downloads are verified inline by downloadAndVerify).
func verifyChecksum(ctx context.Context, path, expected string) error {
	if expected == "" {
		return fmt.Errorf("missing checksum for %s", filepath.Base(path))
	}
	if err := validateSHA256(expected); err != nil {
		return fmt.Errorf("invalid checksum for %s: %w", filepath.Base(path), err)
	}

	file, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("open archive for checksum: %w", err)
	}
	defer func() { _ = file.Close() }()

	sum := sha256.New()
	if _, err := io.Copy(sum, &ctxReader{ctx: ctx, r: file}); err != nil {
		return fmt.Errorf("hash archive: %w", canceledErr(ctx, err))
	}

	actual := hex.EncodeToString(sum.Sum(nil))
	if !strings.EqualFold(actual, expected) {
		return fmt.Errorf("checksum mismatch for %s: expected %s, got %s", filepath.Base(path), expected, actual)
	}
	return nil
}

func validateSHA256(value string) error {
	if len(value) != sha256.Size*2 {
		return fmt.Errorf("expected %d hex characters, got %d", sha256.Size*2, len(value))
	}
	if _, err := hex.DecodeString(value); err != nil {
		return fmt.Errorf("not valid hex: %w", err)
	}
	return nil
}
