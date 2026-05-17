package fgm

import (
	"fmt"
	"os"
	"path/filepath"
)

// LogFunc is called by Manager to report progress. The CLI layer provides
// the implementation; the core package never writes to stdout/stderr directly.
type LogFunc func(format string, args ...any)

type Manager struct {
	root           string
	log            LogFunc
	Verbose        bool
	cachedManifest []releaseManifest
}

func NewManager(root string, log LogFunc) (*Manager, error) {
	if root == "" {
		root = os.Getenv("FGM_DIR")
	}
	if root == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return nil, fmt.Errorf("resolve home dir: %w", err)
		}
		root = filepath.Join(home, ".fgm")
	}
	if log == nil {
		log = func(string, ...any) {} // silent by default
	}

	return &Manager{root: root, log: log}, nil
}

// Root returns the fgm home directory path.
func (m *Manager) Root() string { return m.root }

// logv logs a message only when verbose mode is enabled.
func (m *Manager) logv(format string, args ...any) {
	if m.Verbose {
		m.log("[verbose] " + fmt.Sprintf(format, args...))
	}
}
