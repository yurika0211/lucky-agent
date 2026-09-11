package tool

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// zipEntry describes one member of a synthetic zip fixture.
type zipEntry struct {
	name string
	body string
	mode os.FileMode
}

func writeZip(t *testing.T, path string, entries []zipEntry) {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, e := range entries {
		header := &zip.FileHeader{Name: e.name, Method: zip.Deflate}
		if e.mode != 0 {
			header.SetMode(e.mode)
		}
		w, err := zw.CreateHeader(header)
		if err != nil {
			t.Fatalf("zip header %s: %v", e.name, err)
		}
		if _, err := w.Write([]byte(e.body)); err != nil {
			t.Fatalf("zip write %s: %v", e.name, err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("zip close: %v", err)
	}
	if err := os.WriteFile(path, buf.Bytes(), 0o600); err != nil {
		t.Fatalf("write zip: %v", err)
	}
}

// tarEntry describes one member of a synthetic tar.gz fixture.
type tarEntry struct {
	name     string
	body     string
	typeflag byte
	linkname string
	mode     int64
	size     int64 // override the declared size; 0 uses len(body)
}

func writeTarGz(t *testing.T, path string, entries []tarEntry) {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for _, e := range entries {
		flag := e.typeflag
		if flag == 0 {
			flag = tar.TypeReg
		}
		size := e.size
		if size == 0 && flag == tar.TypeReg {
			size = int64(len(e.body))
		}
		mode := e.mode
		if mode == 0 {
			mode = 0o644
		}
		header := &tar.Header{Name: e.name, Mode: mode, Size: size, Typeflag: flag, Linkname: e.linkname}
		if err := tw.WriteHeader(header); err != nil {
			t.Fatalf("tar header %s: %v", e.name, err)
		}
		if flag == tar.TypeReg && e.body != "" {
			if _, err := tw.Write([]byte(e.body)); err != nil {
				t.Fatalf("tar write %s: %v", e.name, err)
			}
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatalf("tar close: %v", err)
	}
	if err := gz.Close(); err != nil {
		t.Fatalf("gzip close: %v", err)
	}
	if err := os.WriteFile(path, buf.Bytes(), 0o600); err != nil {
		t.Fatalf("write tar.gz: %v", err)
	}
}

// assertNothingEscaped fails if extraction wrote anything outside dest. Mirrors
// the assertion in upload_handlers_test.go.
func assertNothingEscaped(t *testing.T, sentinel string) {
	t.Helper()
	if _, err := os.Stat(sentinel); err == nil {
		t.Fatalf("extraction wrote outside the destination: %s exists", sentinel)
	}
}

func TestExtractArchiveRejectsMaliciousEntries(t *testing.T) {
	cases := []struct {
		name     string
		wantRule string
		build    func(t *testing.T, path string)
	}{
		{
			name:     "zip slip via parent segments",
			wantRule: "path_traversal",
			build: func(t *testing.T, p string) {
				writeZip(t, p, []zipEntry{{name: "../../escaped.sh", body: "x"}})
			},
		},
		{
			name:     "zip slip via absolute path",
			wantRule: "path_traversal",
			build: func(t *testing.T, p string) {
				writeZip(t, p, []zipEntry{{name: "/etc/passwd", body: "x"}})
			},
		},
		{
			name:     "zip slip via backslash separators",
			wantRule: "path_traversal",
			build: func(t *testing.T, p string) {
				writeZip(t, p, []zipEntry{{name: `..\..\escaped.sh`, body: "x"}})
			},
		},
		{
			name:     "zip symlink entry",
			wantRule: "symlink_entry",
			build: func(t *testing.T, p string) {
				writeZip(t, p, []zipEntry{{name: "link", body: "/etc/shadow", mode: os.ModeSymlink | 0o777}})
			},
		},
		{
			name:     "zip setuid bit",
			wantRule: "setuid_bit",
			build: func(t *testing.T, p string) {
				writeZip(t, p, []zipEntry{{name: "scripts/x.sh", body: "x", mode: os.ModeSetuid | 0o755}})
			},
		},
		{
			name:     "tar symlink entry",
			wantRule: "symlink_entry",
			build: func(t *testing.T, p string) {
				writeTarGz(t, p, []tarEntry{{name: "link", typeflag: tar.TypeSymlink, linkname: "/etc/shadow"}})
			},
		},
		{
			name:     "tar hard link entry",
			wantRule: "symlink_entry",
			build: func(t *testing.T, p string) {
				writeTarGz(t, p, []tarEntry{{name: "link", typeflag: tar.TypeLink, linkname: "/etc/shadow"}})
			},
		},
		{
			name:     "tar fifo entry",
			wantRule: "special_file",
			build: func(t *testing.T, p string) {
				writeTarGz(t, p, []tarEntry{{name: "pipe", typeflag: tar.TypeFifo}})
			},
		},
		{
			name:     "tar char device entry",
			wantRule: "special_file",
			build: func(t *testing.T, p string) {
				writeTarGz(t, p, []tarEntry{{name: "dev", typeflag: tar.TypeChar}})
			},
		},
		{
			name:     "path too deep",
			wantRule: "path_too_deep",
			build: func(t *testing.T, p string) {
				deep := strings.Repeat("a/", 20) + "f.txt"
				writeZip(t, p, []zipEntry{{name: deep, body: "x"}})
			},
		},
		{
			name:     "too many files",
			wantRule: "too_many_files",
			build: func(t *testing.T, p string) {
				entries := make([]zipEntry, 0, 30)
				for i := 0; i < 30; i++ {
					entries = append(entries, zipEntry{name: filepath.Join("d", "f"+string(rune('a'+i%26))+string(rune('a'+i/26))+".txt"), body: "x"})
				}
				writeZip(t, p, entries)
			},
		},
		{
			name:     "single file too large",
			wantRule: "file_too_large",
			build: func(t *testing.T, p string) {
				writeZip(t, p, []zipEntry{{name: "big.bin", body: strings.Repeat("A", 4096)}})
			},
		},
		{
			name:     "duplicate entry",
			wantRule: "duplicate_entry",
			build: func(t *testing.T, p string) {
				writeZip(t, p, []zipEntry{{name: "same.txt", body: "one"}, {name: "same.txt", body: "two"}})
			},
		},
		{
			name:     "tar understates its declared size",
			wantRule: "file_too_large",
			build: func(t *testing.T, p string) {
				// Declare 1 byte but stream far more; the copy limit must catch it.
				writeTarGz(t, p, []tarEntry{{name: "lie.txt", body: strings.Repeat("A", 5000), size: 5000}})
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tmp := t.TempDir()
			archive := filepath.Join(tmp, "pkg.bin")
			dest := filepath.Join(tmp, "dest")
			if err := os.MkdirAll(dest, 0o700); err != nil {
				t.Fatal(err)
			}
			tc.build(t, archive)

			limits := DefaultInstallLimits()
			limits.MaxFiles = 20
			limits.MaxSingleFileBytes = 2048
			limits.MaxPathDepth = 12

			_, err := ExtractArchive(archive, dest, limits)
			if err == nil {
				t.Fatalf("expected rejection, extraction succeeded")
			}
			var ee *extractError
			if !asExtract(err, &ee) {
				t.Fatalf("expected an extractError, got %T: %v", err, err)
			}
			if ee.Rule != tc.wantRule {
				t.Errorf("rule = %q, want %q (detail: %s)", ee.Rule, tc.wantRule, ee.Detail)
			}
			assertNothingEscaped(t, filepath.Join(tmp, "escaped.sh"))
			assertNothingEscaped(t, filepath.Join(filepath.Dir(tmp), "escaped.sh"))
		})
	}
}

func TestExtractArchiveRejectsCompressionBomb(t *testing.T) {
	tmp := t.TempDir()
	archive := filepath.Join(tmp, "bomb.tar.gz")
	dest := filepath.Join(tmp, "dest")
	if err := os.MkdirAll(dest, 0o700); err != nil {
		t.Fatal(err)
	}
	// Highly compressible payload: many small files of repeated bytes.
	entries := make([]tarEntry, 0, 40)
	for i := 0; i < 40; i++ {
		entries = append(entries, tarEntry{name: filepath.Join("d", "f", "x"+string(rune('a'+i))+".txt"), body: strings.Repeat("A", 60000)})
	}
	writeTarGz(t, archive, entries)

	limits := DefaultInstallLimits()
	limits.MaxUncompressedBytes = 1 << 20
	limits.MaxFiles = 100

	if _, err := ExtractArchive(archive, dest, limits); err == nil {
		t.Fatal("expected the bomb to be rejected")
	} else {
		var ee *extractError
		if !asExtract(err, &ee) {
			t.Fatalf("expected an extractError, got %v", err)
		}
		if ee.Rule != "uncompressed_too_large" && ee.Rule != "compression_ratio" {
			t.Errorf("rule = %q, want uncompressed_too_large or compression_ratio", ee.Rule)
		}
	}
}

func TestExtractArchiveAcceptsCleanSkill(t *testing.T) {
	tmp := t.TempDir()
	archive := filepath.Join(tmp, "clean.tar.gz")
	dest := filepath.Join(tmp, "dest")
	if err := os.MkdirAll(dest, 0o700); err != nil {
		t.Fatal(err)
	}
	writeTarGz(t, archive, []tarEntry{
		{name: "my-skill/SKILL.md", body: "---\nname: my_skill\ndescription: A clean skill.\n---\n\n# My Skill\n"},
		{name: "my-skill/scripts/run.sh", body: "#!/bin/sh\necho hi\n", mode: 0o755},
		{name: "my-skill/references/notes.md", body: "notes\n"},
	})

	stats, err := ExtractArchive(archive, dest, DefaultInstallLimits())
	if err != nil {
		t.Fatalf("ExtractArchive: %v", err)
	}
	if stats.Files != 3 {
		t.Errorf("Files = %d, want 3", stats.Files)
	}

	root, err := NormalizeRoot(dest, "staged-skill")
	if err != nil {
		t.Fatalf("NormalizeRoot: %v", err)
	}
	if filepath.Base(root) != "staged-skill" {
		t.Errorf("normalized root = %s, want .../staged-skill", root)
	}
	if _, err := os.Stat(filepath.Join(root, "SKILL.md")); err != nil {
		t.Errorf("SKILL.md missing after normalization: %v", err)
	}
	// Scripts must stay executable; everything else must not.
	script, err := os.Stat(filepath.Join(root, "scripts", "run.sh"))
	if err != nil {
		t.Fatal(err)
	}
	if script.Mode().Perm()&0o100 == 0 {
		t.Error("script lost its execute bit")
	}
	doc, err := os.Stat(filepath.Join(root, "references", "notes.md"))
	if err != nil {
		t.Fatal(err)
	}
	if doc.Mode().Perm()&0o111 != 0 {
		t.Errorf("non-script file is executable: %v", doc.Mode())
	}
}

// TestNormalizeRootHandlesFlatArchive covers the layout where SKILL.md sits at
// the archive root rather than inside a wrapping directory.
func TestNormalizeRootHandlesFlatArchive(t *testing.T) {
	tmp := t.TempDir()
	archive := filepath.Join(tmp, "flat.tar.gz")
	dest := filepath.Join(tmp, "dest")
	if err := os.MkdirAll(dest, 0o700); err != nil {
		t.Fatal(err)
	}
	writeTarGz(t, archive, []tarEntry{
		{name: "SKILL.md", body: "# flat\n\nDesc.\n"},
		{name: "scripts/run.sh", body: "#!/bin/sh\n", mode: 0o755},
	})
	if _, err := ExtractArchive(archive, dest, DefaultInstallLimits()); err != nil {
		t.Fatalf("ExtractArchive: %v", err)
	}
	root, err := NormalizeRoot(dest, "staged-skill")
	if err != nil {
		t.Fatalf("NormalizeRoot: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "SKILL.md")); err != nil {
		t.Errorf("SKILL.md missing: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "scripts", "run.sh")); err != nil {
		t.Errorf("scripts/ missing after normalization: %v", err)
	}
}

// TestCopyLocalDirRejectsSymlinks guards the local-path source: following a
// symlink would let it pull in files from outside the source tree.
func TestCopyLocalDirRejectsSymlinks(t *testing.T) {
	tmp := t.TempDir()
	src := filepath.Join(tmp, "src")
	if err := os.MkdirAll(src, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "SKILL.md"), []byte("# s\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("/etc/passwd", filepath.Join(src, "leak")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}

	dest := filepath.Join(tmp, "dest")
	if err := os.MkdirAll(dest, 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := CopyLocalDir(src, dest, DefaultInstallLimits()); err == nil {
		t.Fatal("expected symlink rejection")
	}
	if _, err := os.Stat(filepath.Join(dest, "leak")); err == nil {
		t.Error("symlink was copied into the destination")
	}
}

// asExtract is a local errors.As helper so the table tests read cleanly.
func asExtract(err error, target **extractError) bool {
	return errors.As(err, target)
}
