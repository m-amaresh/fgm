package fgm

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"time"
)

const manifestURL = "https://go.dev/dl/?mode=json&include=all"

// manifestCacheTTL is how long a cached manifest is considered fresh.
const manifestCacheTTL = 15 * time.Minute

type releaseManifest struct {
	Version string        `json:"version"`
	Files   []releaseFile `json:"files"`
}

type releaseFile struct {
	Filename string `json:"filename"`
	OS       string `json:"os"`
	Arch     string `json:"arch"`
	Version  string `json:"version"`
	SHA256   string `json:"sha256"`
	Kind     string `json:"kind"`
}

type manifestDiskCache struct {
	FetchedAt time.Time         `json:"fetched_at"`
	Releases  []releaseManifest `json:"releases"`
}

func (m *Manager) manifestCachePath() string {
	return filepath.Join(m.root, "manifest-cache.json")
}

func (m *Manager) manifest(ctx context.Context) ([]releaseManifest, error) {
	if m.cachedManifest != nil {
		return m.cachedManifest, nil
	}

	diskCache := m.loadManifestCache()
	if diskCache != nil && len(diskCache.Releases) > 0 {
		age := time.Since(diskCache.FetchedAt)
		if age >= 0 && age < manifestCacheTTL {
			m.logv("manifest cache hit (age: %s, %d releases)", age.Round(time.Second), len(diskCache.Releases))
			m.cachedManifest = diskCache.Releases
			return diskCache.Releases, nil
		}
		m.logv("manifest cache expired (age: %s)", age.Round(time.Second))
	}

	m.logv("fetching manifest from %s", manifestURL)
	releases, err := fetchManifest(ctx)
	if err != nil {
		// Offline / fetch failure: fall back to stale disk cache if we have one.
		if diskCache != nil && len(diskCache.Releases) > 0 {
			m.log("warning: using stale manifest cache (fetch failed: %v)", err)
			m.cachedManifest = diskCache.Releases
			return diskCache.Releases, nil
		}
		return nil, err
	}
	m.cachedManifest = releases
	m.storeManifestCache(releases)
	return releases, nil
}

// loadManifestCache reads and parses the on-disk manifest cache. Returns nil
// on any error (cache absent or unreadable is not fatal).
func (m *Manager) loadManifestCache() *manifestDiskCache {
	data, err := os.ReadFile(m.manifestCachePath())
	if err != nil {
		return nil
	}
	var cache manifestDiskCache
	if err := json.Unmarshal(data, &cache); err != nil {
		return nil
	}
	return &cache
}

// storeManifestCache writes the manifest to disk. Best-effort; errors are logged
// at verbose level only.
func (m *Manager) storeManifestCache(releases []releaseManifest) {
	cache := manifestDiskCache{FetchedAt: time.Now(), Releases: releases}
	data, err := json.Marshal(cache)
	if err != nil {
		m.logv("marshal manifest cache: %v", err)
		return
	}
	if err := os.MkdirAll(m.root, 0o755); err != nil {
		m.logv("create root for manifest cache: %v", err)
		return
	}
	if err := atomicWriteFile(m.manifestCachePath(), data); err != nil {
		m.logv("write manifest cache: %v", err)
	}
}
