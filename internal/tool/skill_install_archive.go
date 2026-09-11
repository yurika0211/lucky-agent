package tool

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"
	"syscall"
)

// extractStats accumulates the totals the ratio and count limits are checked
// against.
type extractStats struct {
	Files            int
	UncompressedSize int64
	CompressedSize   int64
}

// extractError is a rejected archive entry. The install pipeline turns these
// into block-severity findings.
type extractError struct {
	Rule   string
	Path   string
	Detail string
}

func (e *extractError) Error() string {
	if e.Path != "" {
		return fmt.Sprintf("%s: %s (%s)", e.Rule, e.Detail, e.Path)
	}
	return fmt.Sprintf("%s: %s", e.Rule, e.Detail)
}

func rejectf(rule, entryPath, format string, args ...any) *extractError {
	return &extractError{Rule: rule, Path: entryPath, Detail: fmt.Sprintf(format, args...)}
}

// safeJoin resolves an archive entry name against dest and rejects anything that
// would escape it. This is the zip-slip guard, and it runs before any bytes are
// written — a rejected entry never touches the filesystem.
func safeJoin(dest, name string, limits InstallLimits) (string, *extractError) {
	if strings.ContainsRune(name, '\x00') {
		return "", rejectf("nul_in_path", name, "entry name contains a NUL byte")
	}
	// Archives always use forward slashes; normalize the Windows form too so a
	// "dir\\..\\..\\x" entry cannot slip past on a POSIX host.
	clean := strings.ReplaceAll(name, "\\", "/")
	if path.IsAbs(clean) {
		return "", rejectf("path_traversal", name, "absolute path")
	}
	// Windows drive prefix, e.g. "C:/evil".
	if len(clean) >= 2 && clean[1] == ':' {
		return "", rejectf("path_traversal", name, "drive-letter path")
	}
	clean = path.Clean(clean)
	if clean == "." || clean == "/" {
		return "", rejectf("path_traversal", name, "empty entry path")
	}
	for _, seg := range strings.Split(clean, "/") {
		if seg == ".." {
			return "", rejectf("path_traversal", name, "parent-directory segment")
		}
	}
	if depth := len(strings.Split(clean, "/")); depth > limits.MaxPathDepth {
		return "", rejectf("path_too_deep", name, "%d segments exceeds the %d limit", depth, limits.MaxPathDepth)
	}
	if len(clean) > limits.MaxPathLen {
		return "", rejectf("path_too_long", name, "%d bytes exceeds the %d limit", len(clean), limits.MaxPathLen)
	}

	target := filepath.Join(dest, filepath.FromSlash(clean))
	// Belt and braces against anything path.Clean did not catch.
	rel, err := filepath.Rel(dest, target)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
		return "", rejectf("path_traversal", name, "resolves outside the destination")
	}
	return target, nil
}

// fileModeFor forces a conservative mode regardless of what the archive claimed.
// Files directly inside a scripts/ directory stay executable because the loader
// runs them; everything else does not need to be. Setuid/setgid/sticky bits are
// dropped unconditionally.
//
// The check is on the immediate parent, not on a path prefix: an archive may or
// may not wrap the skill in a top-level directory, so relPath is either
// "scripts/run.sh" or "my-skill/scripts/run.sh". It also matches SkillLoader,
// which only looks at direct children of <skillDir>/scripts.
func fileModeFor(relPath string) os.FileMode {
	if path.Base(path.Dir(filepath.ToSlash(relPath))) == "scripts" {
		return 0o700
	}
	return 0o600
}

// ExtractArchive unpacks a zip or gzipped tar into dest, enforcing limits on
// every entry. dest must already exist. It returns the first violation as an
// *extractError; partial output is left for the caller to clean up.
func ExtractArchive(archivePath, dest string, limits InstallLimits) (*extractStats, error) {
	limits = limits.withDefaults()

	info, err := os.Stat(archivePath)
	if err != nil {
		return nil, err
	}
	if info.Size() > limits.MaxArchiveBytes {
		return nil, rejectf("archive_too_large", "", "%d bytes exceeds the %d limit", info.Size(), limits.MaxArchiveBytes)
	}

	stats := &extractStats{CompressedSize: info.Size()}

	if isZipArchive(archivePath) {
		err = extractZip(archivePath, dest, limits, stats)
	} else {
		err = extractTarGz(archivePath, dest, limits, stats)
	}
	if err != nil {
		return stats, err
	}

	// Ratio is only meaningful once the whole archive is accounted for.
	if stats.CompressedSize > 0 && limits.MaxCompressionRatio > 0 {
		if ratio := stats.UncompressedSize / stats.CompressedSize; ratio > int64(limits.MaxCompressionRatio) {
			return stats, rejectf("compression_ratio", "", "%dx expansion exceeds the %dx limit", ratio, limits.MaxCompressionRatio)
		}
	}
	return stats, nil
}

func isZipArchive(p string) bool {
	f, err := os.Open(p)
	if err != nil {
		return false
	}
	defer f.Close()
	var magic [4]byte
	if _, err := io.ReadFull(f, magic[:]); err != nil {
		return false
	}
	return magic[0] == 'P' && magic[1] == 'K'
}

func extractZip(archivePath, dest string, limits InstallLimits, stats *extractStats) error {
	zr, err := zip.OpenReader(archivePath)
	if err != nil {
		return rejectf("unreadable_archive", "", "not a valid zip: %v", err)
	}
	defer zr.Close()

	for _, entry := range zr.File {
		mode := entry.Mode()
		if mode&os.ModeSymlink != 0 {
			return rejectf("symlink_entry", entry.Name, "archives may not contain symlinks")
		}
		if mode&os.ModeType != 0 && !mode.IsDir() {
			return rejectf("special_file", entry.Name, "entry is neither a regular file nor a directory")
		}
		if mode&(os.ModeSetuid|os.ModeSetgid|os.ModeSticky) != 0 {
			return rejectf("setuid_bit", entry.Name, "entry carries setuid/setgid/sticky bits")
		}

		target, rejErr := safeJoin(dest, entry.Name, limits)
		if rejErr != nil {
			return rejErr
		}

		if entry.FileInfo().IsDir() {
			if err := os.MkdirAll(target, 0o700); err != nil {
				return err
			}
			continue
		}

		if err := countEntry(stats, int64(entry.UncompressedSize64), limits, entry.Name); err != nil {
			return err
		}
		rc, err := entry.Open()
		if err != nil {
			return err
		}
		err = writeEntry(target, rc, int64(entry.UncompressedSize64), limits, entry.Name, dest)
		rc.Close()
		if err != nil {
			return err
		}
	}
	return nil
}

func extractTarGz(archivePath, dest string, limits InstallLimits, stats *extractStats) error {
	f, err := os.Open(archivePath)
	if err != nil {
		return err
	}
	defer f.Close()

	gz, err := gzip.NewReader(f)
	if err != nil {
		return rejectf("unreadable_archive", "", "not a valid zip or gzip archive: %v", err)
	}
	defer gz.Close()

	tr := tar.NewReader(gz)
	for {
		header, err := tr.Next()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return rejectf("unreadable_archive", "", "malformed tar: %v", err)
		}

		switch header.Typeflag {
		case tar.TypeSymlink, tar.TypeLink:
			return rejectf("symlink_entry", header.Name, "archives may not contain symlinks or hard links")
		case tar.TypeChar, tar.TypeBlock, tar.TypeFifo:
			return rejectf("special_file", header.Name, "archives may not contain device or FIFO entries")
		case tar.TypeDir, tar.TypeReg:
			// handled below
		default:
			return rejectf("special_file", header.Name, "unsupported tar entry type %q", header.Typeflag)
		}

		if os.FileMode(header.Mode)&(os.ModeSetuid|os.ModeSetgid|os.ModeSticky) != 0 {
			return rejectf("setuid_bit", header.Name, "entry carries setuid/setgid/sticky bits")
		}

		target, rejErr := safeJoin(dest, header.Name, limits)
		if rejErr != nil {
			return rejErr
		}

		if header.Typeflag == tar.TypeDir {
			if err := os.MkdirAll(target, 0o700); err != nil {
				return err
			}
			continue
		}

		if err := countEntry(stats, header.Size, limits, header.Name); err != nil {
			return err
		}
		if err := writeEntry(target, tr, header.Size, limits, header.Name, dest); err != nil {
			return err
		}
	}
}

// countEntry applies the per-file and whole-archive limits before any bytes are
// written, so a declared-size bomb is rejected without spending disk.
func countEntry(stats *extractStats, size int64, limits InstallLimits, name string) error {
	if size > limits.MaxSingleFileBytes {
		return rejectf("file_too_large", name, "%d bytes exceeds the %d limit", size, limits.MaxSingleFileBytes)
	}
	stats.Files++
	if stats.Files > limits.MaxFiles {
		return rejectf("too_many_files", name, "more than %d entries", limits.MaxFiles)
	}
	stats.UncompressedSize += size
	if stats.UncompressedSize > limits.MaxUncompressedBytes {
		return rejectf("uncompressed_too_large", name, "total expansion exceeds the %d byte limit", limits.MaxUncompressedBytes)
	}
	return nil
}

// writeEntry copies at most declaredSize bytes. The limit is re-applied here
// because a header's size field is attacker-controlled and may understate the
// real stream.
func writeEntry(target string, src io.Reader, declaredSize int64, limits InstallLimits, name, dest string) error {
	if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
		return err
	}
	rel, err := filepath.Rel(dest, target)
	if err != nil {
		return err
	}

	// O_EXCL: a duplicate entry must not silently overwrite an earlier one.
	out, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_EXCL, fileModeFor(rel))
	if err != nil {
		if os.IsExist(err) {
			return rejectf("duplicate_entry", name, "archive contains the same path twice")
		}
		return err
	}
	defer out.Close()

	limit := declaredSize
	if limit <= 0 || limit > limits.MaxSingleFileBytes {
		limit = limits.MaxSingleFileBytes
	}
	written, err := io.Copy(out, io.LimitReader(src, limit+1))
	if err != nil {
		return err
	}
	if written > limit {
		return rejectf("file_too_large", name, "stream exceeds its declared size")
	}
	return nil
}

// CopyLocalDir copies a directory tree into dest under the same limits as
// archive extraction. Symlinks are rejected rather than followed: following them
// would let a local-path install pull in arbitrary files from outside the source.
func CopyLocalDir(src, dest string, limits InstallLimits) (*extractStats, error) {
	limits = limits.withDefaults()
	stats := &extractStats{}

	srcAbs, err := filepath.Abs(src)
	if err != nil {
		return nil, err
	}

	err = filepath.WalkDir(srcAbs, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(srcAbs, p)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}

		info, err := d.Info()
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return rejectf("symlink_entry", rel, "source tree may not contain symlinks")
		}

		target, rejErr := safeJoin(dest, filepath.ToSlash(rel), limits)
		if rejErr != nil {
			return rejErr
		}

		if d.IsDir() {
			return os.MkdirAll(target, 0o700)
		}
		if !info.Mode().IsRegular() {
			return rejectf("special_file", rel, "source tree may not contain device or FIFO entries")
		}
		if err := countEntry(stats, info.Size(), limits, rel); err != nil {
			return err
		}

		in, err := os.Open(p)
		if err != nil {
			return err
		}
		defer in.Close()
		return writeEntry(target, in, info.Size(), limits, rel, dest)
	})
	if err != nil {
		return stats, err
	}
	// A directory copy has no compression, so the ratio check does not apply.
	stats.CompressedSize = stats.UncompressedSize
	return stats, nil
}

// NormalizeRoot arranges extracted content so that root holds exactly one child
// directory containing SKILL.md. That shape is required because SkillLoader
// iterates a directory and descends one level into each child, so the loader
// must be pointed at the *parent* of the skill.
//
// It handles both common archive layouts: SKILL.md at the archive root, and
// SKILL.md under a single wrapping directory.
func NormalizeRoot(root, skillDirName string) (string, error) {
	entries, err := os.ReadDir(root)
	if err != nil {
		return "", err
	}

	// Ignore junk some archivers add at the top level.
	var dirs, files []os.DirEntry
	for _, e := range entries {
		name := e.Name()
		if name == "__MACOSX" || name == ".DS_Store" {
			_ = os.RemoveAll(filepath.Join(root, name))
			continue
		}
		if e.IsDir() {
			dirs = append(dirs, e)
		} else {
			files = append(files, e)
		}
	}

	target := filepath.Join(root, skillDirName)

	// Layout A: SKILL.md sits at the archive root. Move everything down a level.
	if hasSkillMD(files) {
		staged, err := os.MkdirTemp(filepath.Dir(root), "normalize-")
		if err != nil {
			return "", err
		}
		inner := filepath.Join(staged, skillDirName)
		if err := os.Rename(root, inner); err != nil {
			return "", err
		}
		if err := os.Rename(staged, root); err != nil {
			return "", err
		}
		return target, nil
	}

	// Layout B: a single wrapping directory. Rename it to the canonical name.
	if len(dirs) == 1 && len(files) == 0 {
		current := filepath.Join(root, dirs[0].Name())
		if _, err := os.Stat(filepath.Join(current, "SKILL.md")); err != nil {
			return "", fmt.Errorf("no SKILL.md found in the archive")
		}
		if current != target {
			if err := os.Rename(current, target); err != nil {
				return "", err
			}
		}
		return target, nil
	}

	return "", fmt.Errorf("no SKILL.md found at the archive root or in a single top-level directory")
}

func hasSkillMD(files []os.DirEntry) bool {
	for _, f := range files {
		if f.Name() == "SKILL.md" {
			return true
		}
	}
	return false
}

// MoveDir renames src to dst, falling back to a copy when the two live on
// different filesystems (os.Rename returns EXDEV). dst must not already exist:
// os.Rename over a non-empty directory fails with ENOTEMPTY on Linux, so callers
// snapshot-then-move rather than overwriting in place.
func MoveDir(src, dst string) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0o700); err != nil {
		return err
	}
	if err := os.Rename(src, dst); err == nil {
		return nil
	} else if !isCrossDevice(err) {
		return err
	}

	if _, err := CopyLocalDir(src, dst, InstallLimits{}.withDefaults()); err != nil {
		_ = os.RemoveAll(dst)
		return err
	}
	return os.RemoveAll(src)
}

func isCrossDevice(err error) bool {
	var linkErr *os.LinkError
	if !errors.As(err, &linkErr) || linkErr.Err == nil {
		return false
	}
	return errors.Is(linkErr.Err, syscall.EXDEV) ||
		strings.Contains(strings.ToLower(linkErr.Err.Error()), "cross-device")
}
