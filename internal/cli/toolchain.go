package cli

import (
	"fmt"
	"io"
	"os"

	"github.com/m-amaresh/fgm/internal/fgm"
)

// warnModToolchain tells the user when the go.mod or go.work in the working
// directory needs a newer Go than version. Go switches toolchains on its own
// in that case, so `go version` would otherwise silently disagree with fgm.
// It is advisory: any problem finding or reading the file is left to the go
// command to report.
func warnModToolchain(w io.Writer, version string) {
	dir, err := os.Getwd()
	if err != nil {
		return
	}
	if note, err := fgm.ModToolchainNote(dir, version); err == nil && note != nil {
		fmt.Fprintf(w, "%s %s\n", yellow("!"), note)
	}
}
