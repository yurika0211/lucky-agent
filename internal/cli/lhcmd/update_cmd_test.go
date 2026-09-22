package lhcmd

import (
	"archive/tar"
	"compress/gzip"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestResolveUpdateAssetName(t *testing.T) {
	tests := []struct {
		goos, goarch string
		wantAsset    string
		wantErr      bool
	}{
		{"linux", "amd64", "lh-linux-amd64.tar.gz", false},
		{"linux", "arm64", "lh-linux-arm64.tar.gz", false},
		{"darwin", "amd64", "lh-darwin-amd64.tar.gz", false},
		{"darwin", "arm64", "lh-darwin-arm64.tar.gz", false},
		{"windows", "amd64", "lh-windows-amd64.zip", false},
		{"linux", "x86_64", "lh-linux-amd64.tar.gz", false},
		{"linux", "aarch64", "lh-linux-arm64.tar.gz", false},
		{"windows", "arm64", "", true},
		{"android", "arm64", "", true},
	}
	for _, tt := range tests {
		_, asset, err := resolveUpdateAssetName(tt.goos, tt.goarch)
		if tt.wantErr {
			if err == nil {
				t.Fatalf("%s/%s: expected error", tt.goos, tt.goarch)
			}
			continue
		}
		if err != nil {
			t.Fatalf("%s/%s: %v", tt.goos, tt.goarch, err)
		}
		if asset != tt.wantAsset {
			t.Fatalf("%s/%s: got %s want %s", tt.goos, tt.goarch, asset, tt.wantAsset)
		}
	}
}

func TestCompareVersionLabels(t *testing.T) {
	tests := []struct {
		a, b string
		want int
	}{
		{"v1.5.1", "1.5.1", 0},
		{"1.5.1", "1.5.2", -1},
		{"1.5.2", "1.5.1", 1},
		{"1.5.2", "1.5.2-rc.1", 1},
		{"1.5.2-rc.1", "1.5.2", -1},
		{"dev", "1.5.2", -1},
		{"unknown", "v1.0.0", -1},
		{"1.5.2+build", "1.5.2", 0},
	}
	for _, tt := range tests {
		got := compareVersionLabels(tt.a, tt.b)
		if got != tt.want {
			t.Fatalf("compare(%q,%q)=%d want %d", tt.a, tt.b, got, tt.want)
		}
	}
}

func TestDetectInstallLayoutManaged(t *testing.T) {
	root := t.TempDir()
	exe := filepath.Join(root, "lh")
	if err := os.WriteFile(exe, []byte("old"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "UI"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "runtime"), 0o755); err != nil {
		t.Fatal(err)
	}
	gotRoot, mode := detectInstallLayout(exe)
	if mode != "managed" {
		t.Fatalf("mode=%s want managed", mode)
	}
	if gotRoot != root {
		t.Fatalf("root=%s want %s", gotRoot, root)
	}
}

func TestDetectInstallLayoutBinary(t *testing.T) {
	root := t.TempDir()
	exe := filepath.Join(root, "lh")
	if err := os.WriteFile(exe, []byte("old"), 0o755); err != nil {
		t.Fatal(err)
	}
	gotRoot, mode := detectInstallLayout(exe)
	if mode != "binary" {
		t.Fatalf("mode=%s want binary", mode)
	}
	if gotRoot != root {
		t.Fatalf("root=%s want %s", gotRoot, root)
	}
}

func TestExtractTarGzAndLocatePayload(t *testing.T) {
	dir := t.TempDir()
	archive := filepath.Join(dir, "lh-linux-amd64.tar.gz")
	if err := writeTestTarGz(archive, map[string]string{
		"lh":            "#!/bin/sh\necho new\n",
		"UI/TUI/a.txt":  "tui",
		"runtime/x.bin": "node",
	}); err != nil {
		t.Fatal(err)
	}
	extractDir := filepath.Join(dir, "out")
	if err := os.MkdirAll(extractDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := extractUpdateArchive(archive, extractDir); err != nil {
		t.Fatal(err)
	}
	root, err := locateUpdatePayloadRoot(extractDir)
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(root, "lh"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "new") {
		t.Fatalf("unexpected binary content: %q", data)
	}
}

func TestReplaceBinaryInstall(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("windows path uses lh.exe")
	}
	dir := t.TempDir()
	exe := filepath.Join(dir, "lh")
	if err := os.WriteFile(exe, []byte("old-binary"), 0o755); err != nil {
		t.Fatal(err)
	}
	payload := filepath.Join(dir, "payload")
	if err := os.MkdirAll(payload, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(payload, "lh"), []byte("new-binary"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := replaceBinaryInstall(exe, payload); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(exe)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "new-binary" {
		t.Fatalf("got %q", data)
	}
}

func TestReplaceManagedInstall(t *testing.T) {
	parent := t.TempDir()
	installRoot := filepath.Join(parent, "luckyagent")
	if err := os.MkdirAll(filepath.Join(installRoot, "UI"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(installRoot, "runtime"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(installRoot, "lh"), []byte("old"), 0o755); err != nil {
		t.Fatal(err)
	}

	payload := filepath.Join(parent, "payload")
	if err := os.MkdirAll(filepath.Join(payload, "UI"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(payload, "runtime"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(payload, "lh"), []byte("new"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(payload, "UI", "marker"), []byte("ui"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := replaceManagedInstall(installRoot, payload); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(installRoot, "lh"))
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "new" {
		t.Fatalf("got %q", data)
	}
	if _, err := os.Stat(filepath.Join(installRoot, "UI", "marker")); err != nil {
		t.Fatal(err)
	}
}

func TestIsWithinDir(t *testing.T) {
	base := t.TempDir()
	ok := filepath.Join(base, "a", "b")
	if !isWithinDir(base, ok) {
		t.Fatal("expected within")
	}
	if isWithinDir(base, filepath.Join(base, "..", "outside")) {
		t.Fatal("expected outside rejected")
	}
}

func writeTestTarGz(path string, files map[string]string) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	gz := gzip.NewWriter(f)
	defer gz.Close()
	tw := tar.NewWriter(gz)
	defer tw.Close()
	for name, content := range files {
		hdr := &tar.Header{
			Name:     name,
			Mode:     0o755,
			Size:     int64(len(content)),
			Typeflag: tar.TypeReg,
		}
		if err := tw.WriteHeader(hdr); err != nil {
			return err
		}
		if _, err := io.WriteString(tw, content); err != nil {
			return err
		}
	}
	return nil
}

func TestUpdateCommandRegistered(t *testing.T) {
	root := newRootCmd()
	found := false
	for _, c := range root.Commands() {
		if c.Name() == "update" {
			found = true
			break
		}
	}
	if !found {
		t.Fatal("update command not registered on root")
	}
}
