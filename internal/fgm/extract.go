package fgm

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// maxExtractSize is the maximum total bytes extracted from an archive (2 GiB).
// The largest official Go archive is ~200 MB; this is a safety cap.
const maxExtractSize int64 = 2 << 30

// maxArchiveEntries caps entries per archive (real Go archives have ~10k).
const maxArchiveEntries = 100_000

// normalizedFileMode collapses extracted entries to 0755 (if any exec bit was
// set) or 0644. This drops setuid/setgid/sticky and any group/other-writable
// bits — Go archives never need them, and a normalized mode is one less
// surface to worry about.
func normalizedFileMode(mode os.FileMode) os.FileMode {
	if mode&0o111 != 0 {
		return 0o755
	}
	return 0o644
}

// sanitizePath validates that an archive entry name resolves within dest.
// Rejects absolute paths (Unix and Windows drive/UNC forms) outright, then
// uses filepath.Rel which CodeQL recognizes as a Zip Slip sanitizer.
func sanitizePath(dest, name string) (string, error) {
	if filepath.IsAbs(name) || filepath.VolumeName(name) != "" || isWindowsAbsolutePath(name) {
		return "", fmt.Errorf("archive entry escapes destination: %s", name)
	}
	dest = filepath.Clean(dest)
	target := filepath.Join(dest, name)
	rel, err := filepath.Rel(dest, target)
	if err != nil {
		return "", fmt.Errorf("archive entry escapes destination: %s", name)
	}
	if strings.HasPrefix(rel, ".."+string(os.PathSeparator)) || rel == ".." {
		return "", fmt.Errorf("archive entry escapes destination: %s", name)
	}
	return target, nil
}

func isWindowsAbsolutePath(name string) bool {
	if strings.HasPrefix(name, `\\`) || strings.HasPrefix(name, "//") {
		return true
	}
	if len(name) < 3 {
		return false
	}
	drive := name[0]
	return ((drive >= 'A' && drive <= 'Z') || (drive >= 'a' && drive <= 'z')) &&
		name[1] == ':' &&
		(name[2] == '\\' || name[2] == '/')
}

func extractArchive(ctx context.Context, archivePath, dest, ext string) error {
	switch ext {
	case ".tar.gz":
		return extractTarGz(ctx, archivePath, dest)
	case ".zip":
		return extractZip(ctx, archivePath, dest)
	default:
		return fmt.Errorf("unsupported archive type %q", ext)
	}
}

func extractTarFile(ctx context.Context, tr *tar.Reader, header *tar.Header, target string, totalWritten int64) (int64, error) {
	if header.Size < 0 {
		return totalWritten, fmt.Errorf("entry %s has negative declared size %d", header.Name, header.Size)
	}
	if header.Size > maxExtractSize || totalWritten+header.Size > maxExtractSize {
		return totalWritten, fmt.Errorf("archive exceeds maximum extraction size (%d bytes)", maxExtractSize)
	}
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return totalWritten, fmt.Errorf("create file dir %s: %w", target, err)
	}
	out, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, normalizedFileMode(os.FileMode(header.Mode)))
	if err != nil {
		return totalWritten, fmt.Errorf("create file %s: %w", target, err)
	}
	n, copyErr := io.Copy(out, io.LimitReader(&ctxReader{ctx: ctx, r: tr}, header.Size+1))
	if closeErr := out.Close(); closeErr != nil && copyErr == nil {
		return totalWritten, fmt.Errorf("close file %s: %w", target, closeErr)
	}
	if copyErr != nil {
		return totalWritten, fmt.Errorf("write file %s: %w", target, canceledErr(ctx, copyErr))
	}
	if n > header.Size {
		return totalWritten, fmt.Errorf("entry %s wrote more bytes than declared size", header.Name)
	}
	return totalWritten + n, nil
}

func extractTarGz(ctx context.Context, archivePath, dest string) error {
	file, err := os.Open(archivePath)
	if err != nil {
		return fmt.Errorf("open archive: %w", err)
	}
	defer func() { _ = file.Close() }()

	gzr, err := gzip.NewReader(&ctxReader{ctx: ctx, r: file})
	if err != nil {
		return fmt.Errorf("read gzip stream: %w", err)
	}
	defer func() { _ = gzr.Close() }()

	tr := tar.NewReader(gzr)
	var totalWritten int64
	var entries int
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		header, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("read tar entry: %w", canceledErr(ctx, err))
		}
		entries++
		if entries > maxArchiveEntries {
			return fmt.Errorf("archive entry count exceeds maximum (%d)", maxArchiveEntries)
		}

		target, err := sanitizePath(dest, header.Name)
		if err != nil {
			return err
		}

		// Verify no parent directory component is a symlink to prevent
		// writes through symlinks created by earlier archive entries.
		if err := ensureNoSymlinkParent(dest, target); err != nil {
			return err
		}

		switch header.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, 0o755); err != nil {
				return fmt.Errorf("create dir %s: %w", target, err)
			}
		case tar.TypeReg:
			totalWritten, err = extractTarFile(ctx, tr, header, target, totalWritten)
			if err != nil {
				return err
			}
		case tar.TypeSymlink, tar.TypeLink:
			// Official Go archives never contain symlinks or hard links.
			// Skip these entry types to eliminate any symlink-based attack
			// surface (zip-slip via symlinks, etc.).
			continue
		default:
			continue
		}
	}
}

func extractZip(ctx context.Context, archivePath, dest string) error {
	reader, err := zip.OpenReader(archivePath)
	if err != nil {
		return fmt.Errorf("open zip archive: %w", err)
	}
	defer func() { _ = reader.Close() }()

	if len(reader.File) > maxArchiveEntries {
		return fmt.Errorf("archive entry count %d exceeds maximum (%d)", len(reader.File), maxArchiveEntries)
	}

	var totalWritten int64
	for _, file := range reader.File {
		if err := ctx.Err(); err != nil {
			return err
		}
		n, err := extractZipEntry(ctx, file, dest, totalWritten)
		if err != nil {
			return err
		}
		totalWritten += n
	}

	return nil
}

func extractZipEntry(ctx context.Context, file *zip.File, dest string, totalWritten int64) (int64, error) {
	target, err := sanitizePath(dest, file.Name)
	if err != nil {
		return 0, err
	}

	if err := ensureNoSymlinkParent(dest, target); err != nil {
		return 0, err
	}

	if file.FileInfo().IsDir() {
		return 0, os.MkdirAll(target, 0o755)
	}

	// Skip non-regular entries (symlinks, devices, etc.) for parity with the
	// tar path. Official Go zips never contain them.
	if file.Mode()&os.ModeType != 0 {
		return 0, nil
	}

	// Check the uncompressed size as uint64 before narrowing — narrowing first
	// would let a malicious archive declaring a >2^63 size overflow to negative
	// and bypass the cap.
	if file.UncompressedSize64 > uint64(maxExtractSize) {
		return 0, fmt.Errorf("archive exceeds maximum extraction size (%d bytes)", maxExtractSize)
	}
	size := int64(file.UncompressedSize64)
	if totalWritten+size > maxExtractSize {
		return 0, fmt.Errorf("archive exceeds maximum extraction size (%d bytes)", maxExtractSize)
	}

	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return 0, fmt.Errorf("create file dir %s: %w", target, err)
	}

	in, err := file.Open()
	if err != nil {
		return 0, fmt.Errorf("open zip entry %s: %w", file.Name, err)
	}
	defer func() { _ = in.Close() }()

	out, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, normalizedFileMode(file.Mode()))
	if err != nil {
		return 0, fmt.Errorf("create file %s: %w", target, err)
	}

	n, copyErr := io.Copy(out, io.LimitReader(&ctxReader{ctx: ctx, r: in}, size+1))
	if closeErr := out.Close(); closeErr != nil && copyErr == nil {
		return 0, fmt.Errorf("close file %s: %w", target, closeErr)
	}
	if copyErr != nil {
		return 0, fmt.Errorf("write file %s: %w", target, canceledErr(ctx, copyErr))
	}
	if n > size {
		return 0, fmt.Errorf("entry %s wrote more bytes than declared size", file.Name)
	}
	return n, nil
}

// ensureNoSymlinkParent walks from dest up to target's parent and checks that
// no intermediate directory component is a symlink. This prevents a malicious
// archive from creating a symlink in an earlier entry and then writing through
// it in a later entry.
func ensureNoSymlinkParent(dest, target string) error {
	dest = filepath.Clean(dest)
	dir := filepath.Dir(target)
	// Walk each component between dest and the target's parent.
	for dir != dest && len(dir) > len(dest) {
		info, err := os.Lstat(dir)
		if errors.Is(err, os.ErrNotExist) {
			dir = filepath.Dir(dir)
			continue
		}
		if err != nil {
			return fmt.Errorf("stat parent dir %s: %w", dir, err)
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("refusing to write through symlink parent: %s", dir)
		}
		dir = filepath.Dir(dir)
	}
	return nil
}
