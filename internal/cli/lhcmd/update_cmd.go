package lhcmd

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/spf13/cobra"
)

const (
	defaultUpdateRepo     = "yurika0211/lucky-agent"
	githubAPIBase         = "https://api.github.com"
	updateHTTPTimeout     = 60 * time.Second
	updateUserAgent       = "LuckyAgent-Updater"
	managedInstallUIMark  = "UI"
	managedInstallRuntime = "runtime"
)

type updateOptions struct {
	checkOnly bool
	force     bool
	repo      string
	version   string
}

type githubRelease struct {
	TagName string        `json:"tag_name"`
	Draft   bool          `json:"draft"`
	Assets  []githubAsset `json:"assets"`
}

type githubAsset struct {
	Name               string `json:"name"`
	BrowserDownloadURL string `json:"browser_download_url"`
}

type releaseTarget struct {
	Tag         string
	AssetName   string
	DownloadURL string
}

func newUpdateCmd() *cobra.Command {
	opts := &updateOptions{}

	cmd := &cobra.Command{
		Use:   "update [version]",
		Short: "从 GitHub Release 自动更新 LuckyAgent",
		Long: strings.TrimSpace(`
从 GitHub Release 检查并安装 LuckyAgent 更新。

默认仓库: yurika0211/lucky-agent
默认目标: 当前平台对应的 lh-<os>-<arch> 资产

示例:
  lh update
  lh update --check
  lh update v1.5.3
  lh update --force
  lh update --repo yurika0211/lucky-agent
`),
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 1 {
				opts.version = strings.TrimSpace(args[0])
			}
			return runUpdate(cmd.Context(), opts)
		},
	}

	cmd.Flags().BoolVar(&opts.checkOnly, "check", false, "只检查是否有更新，不安装")
	cmd.Flags().BoolVar(&opts.force, "force", false, "即使版本相同也重新安装")
	cmd.Flags().StringVar(&opts.repo, "repo", defaultUpdateRepo, "GitHub 仓库 owner/name")

	return cmd
}

func runUpdate(ctx context.Context, opts *updateOptions) error {
	if opts == nil {
		opts = &updateOptions{}
	}
	repo := strings.TrimSpace(opts.repo)
	if repo == "" {
		repo = strings.TrimSpace(os.Getenv("LH_UPDATE_REPO"))
	}
	if repo == "" {
		repo = defaultUpdateRepo
	}
	if strings.Count(repo, "/") != 1 {
		return fmt.Errorf("invalid repo %q, expected owner/name", repo)
	}

	platform, assetName, err := resolveUpdateAssetName(runtime.GOOS, runtime.GOARCH)
	if err != nil {
		return err
	}

	current := normalizeVersionLabel(buildVersion)
	fmt.Printf("Current:  %s (%s/%s)\n", displayVersion(buildVersion), runtime.GOOS, runtime.GOARCH)
	fmt.Printf("Platform: %s\n", platform)
	fmt.Printf("Repo:     %s\n", repo)

	target, err := fetchReleaseTarget(ctx, repo, opts.version, assetName)
	if err != nil {
		return err
	}
	latest := normalizeVersionLabel(target.Tag)
	fmt.Printf("Latest:   %s\n", displayVersion(target.Tag))
	fmt.Printf("Asset:    %s\n", target.AssetName)

	cmp := compareVersionLabels(current, latest)
	if cmp == 0 && !opts.force {
		fmt.Println("Already up to date.")
		return nil
	}
	if cmp > 0 && !opts.force && strings.TrimSpace(opts.version) == "" {
		fmt.Printf("Local version %s is newer than release %s; use --force to reinstall.\n", displayVersion(buildVersion), displayVersion(target.Tag))
		return nil
	}
	if opts.checkOnly {
		if cmp < 0 {
			fmt.Printf("Update available: %s -> %s\n", displayVersion(buildVersion), displayVersion(target.Tag))
			return nil
		}
		if opts.force {
			fmt.Printf("Would reinstall %s (--force).\n", displayVersion(target.Tag))
			return nil
		}
		fmt.Println("No update needed.")
		return nil
	}

	exePath, err := currentExecutablePath()
	if err != nil {
		return err
	}
	installRoot, mode := detectInstallLayout(exePath)
	fmt.Printf("Install:  %s (%s)\n", installRoot, mode)

	tmpDir, err := os.MkdirTemp("", "luckyagent-update-*")
	if err != nil {
		return fmt.Errorf("create temp dir: %w", err)
	}
	defer os.RemoveAll(tmpDir)

	archivePath := filepath.Join(tmpDir, target.AssetName)
	fmt.Printf("Downloading %s ...\n", target.DownloadURL)
	if err := downloadFile(ctx, target.DownloadURL, archivePath); err != nil {
		return err
	}

	extractDir := filepath.Join(tmpDir, "extract")
	if err := os.MkdirAll(extractDir, 0o755); err != nil {
		return err
	}
	if err := extractUpdateArchive(archivePath, extractDir); err != nil {
		return err
	}

	payloadRoot, err := locateUpdatePayloadRoot(extractDir)
	if err != nil {
		return err
	}

	switch mode {
	case "managed":
		if err := replaceManagedInstall(installRoot, payloadRoot); err != nil {
			return err
		}
	case "binary":
		if err := replaceBinaryInstall(exePath, payloadRoot); err != nil {
			return err
		}
	default:
		return fmt.Errorf("unknown install mode %q", mode)
	}

	fmt.Printf("Updated to %s\n", displayVersion(target.Tag))
	fmt.Println("Tip: restart long-running processes (serve / msg-gateway / dashboard) to load the new binary.")
	return nil
}

func resolveUpdateAssetName(goos, goarch string) (platform string, assetName string, err error) {
	goos = strings.ToLower(strings.TrimSpace(goos))
	goarch = strings.ToLower(strings.TrimSpace(goarch))
	switch goarch {
	case "x86_64":
		goarch = "amd64"
	case "aarch64":
		goarch = "arm64"
	}
	switch goos {
	case "linux", "darwin":
		if goarch != "amd64" && goarch != "arm64" {
			return "", "", fmt.Errorf("unsupported arch for %s: %s", goos, goarch)
		}
		return goos + "/" + goarch, fmt.Sprintf("lh-%s-%s.tar.gz", goos, goarch), nil
	case "windows":
		if goarch != "amd64" {
			return "", "", fmt.Errorf("unsupported windows arch: %s (expected amd64)", goarch)
		}
		return "windows/amd64", "lh-windows-amd64.zip", nil
	default:
		return "", "", fmt.Errorf("unsupported platform %s/%s", goos, goarch)
	}
}

func fetchReleaseTarget(ctx context.Context, repo, version, assetName string) (*releaseTarget, error) {
	version = strings.TrimSpace(version)
	apiURL := githubAPIBase + "/repos/" + repo + "/releases/latest"
	if version != "" && !strings.EqualFold(version, "latest") {
		tag := version
		if !strings.HasPrefix(tag, "v") && looksLikeDottedVersion(tag) {
			tag = "v" + tag
		}
		apiURL = githubAPIBase + "/repos/" + repo + "/releases/tags/" + tag
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, apiURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", updateUserAgent)
	if token := firstNonEmpty(os.Getenv("GH_TOKEN"), os.Getenv("GITHUB_TOKEN")); token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}

	client := &http.Client{Timeout: updateHTTPTimeout}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("query github release: %w", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("github release api %s: %s", resp.Status, strings.TrimSpace(string(body)))
	}

	var release githubRelease
	if err := json.Unmarshal(body, &release); err != nil {
		return nil, fmt.Errorf("decode github release: %w", err)
	}
	if release.Draft {
		return nil, errors.New("latest release is a draft")
	}
	if strings.TrimSpace(release.TagName) == "" {
		return nil, errors.New("github release missing tag_name")
	}

	for _, asset := range release.Assets {
		if asset.Name == assetName && strings.TrimSpace(asset.BrowserDownloadURL) != "" {
			return &releaseTarget{
				Tag:         release.TagName,
				AssetName:   asset.Name,
				DownloadURL: asset.BrowserDownloadURL,
			}, nil
		}
	}
	return nil, fmt.Errorf("release %s has no asset %q", release.TagName, assetName)
}

func downloadFile(ctx context.Context, url, dest string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", updateUserAgent)
	if token := firstNonEmpty(os.Getenv("GH_TOKEN"), os.Getenv("GITHUB_TOKEN")); token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}

	client := &http.Client{Timeout: 5 * time.Minute}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("download asset: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
		return fmt.Errorf("download asset %s: %s", resp.Status, strings.TrimSpace(string(body)))
	}

	f, err := os.OpenFile(dest, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	if _, err := io.Copy(f, resp.Body); err != nil {
		return fmt.Errorf("write asset: %w", err)
	}
	return f.Close()
}

func extractUpdateArchive(archivePath, destDir string) error {
	name := strings.ToLower(archivePath)
	switch {
	case strings.HasSuffix(name, ".tar.gz") || strings.HasSuffix(name, ".tgz"):
		return extractTarGz(archivePath, destDir)
	case strings.HasSuffix(name, ".zip"):
		return extractZip(archivePath, destDir)
	default:
		return fmt.Errorf("unsupported archive format: %s", filepath.Base(archivePath))
	}
}

func extractTarGz(archivePath, destDir string) error {
	f, err := os.Open(archivePath)
	if err != nil {
		return err
	}
	defer f.Close()

	gz, err := gzip.NewReader(f)
	if err != nil {
		return fmt.Errorf("gzip: %w", err)
	}
	defer gz.Close()

	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return fmt.Errorf("tar: %w", err)
		}
		if err := writeTarEntry(destDir, hdr, tr); err != nil {
			return err
		}
	}
}

func writeTarEntry(destDir string, hdr *tar.Header, r io.Reader) error {
	clean := filepath.Clean(hdr.Name)
	if clean == "." || clean == "" {
		return nil
	}
	if strings.HasPrefix(clean, ".."+string(os.PathSeparator)) || clean == ".." || filepath.IsAbs(clean) {
		return fmt.Errorf("refusing unsafe tar path: %s", hdr.Name)
	}
	target := filepath.Join(destDir, clean)
	if !isWithinDir(destDir, target) {
		return fmt.Errorf("refusing tar path outside destination: %s", hdr.Name)
	}

	mode := os.FileMode(hdr.Mode).Perm()
	if mode == 0 {
		mode = 0o644
	}

	switch hdr.Typeflag {
	case tar.TypeDir:
		return os.MkdirAll(target, 0o755)
	case tar.TypeReg, tar.TypeRegA:
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		f, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode)
		if err != nil {
			return err
		}
		_, copyErr := io.Copy(f, r)
		closeErr := f.Close()
		if copyErr != nil {
			return copyErr
		}
		return closeErr
	case tar.TypeSymlink:
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		_ = os.Remove(target)
		return os.Symlink(hdr.Linkname, target)
	default:
		// Skip pax/global/other entries.
		return nil
	}
}

func extractZip(archivePath, destDir string) error {
	r, err := zip.OpenReader(archivePath)
	if err != nil {
		return err
	}
	defer r.Close()

	for _, f := range r.File {
		clean := filepath.Clean(f.Name)
		if clean == "." || clean == "" {
			continue
		}
		if strings.HasPrefix(clean, ".."+string(os.PathSeparator)) || clean == ".." || filepath.IsAbs(clean) {
			return fmt.Errorf("refusing unsafe zip path: %s", f.Name)
		}
		target := filepath.Join(destDir, clean)
		if !isWithinDir(destDir, target) {
			return fmt.Errorf("refusing zip path outside destination: %s", f.Name)
		}
		if f.FileInfo().IsDir() {
			if err := os.MkdirAll(target, 0o755); err != nil {
				return err
			}
			continue
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		rc, err := f.Open()
		if err != nil {
			return err
		}
		out, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, f.Mode().Perm())
		if err != nil {
			rc.Close()
			return err
		}
		_, copyErr := io.Copy(out, rc)
		closeErr := out.Close()
		rc.Close()
		if copyErr != nil {
			return copyErr
		}
		if closeErr != nil {
			return closeErr
		}
	}
	return nil
}

func locateUpdatePayloadRoot(extractDir string) (string, error) {
	candidates := []string{extractDir}
	entries, err := os.ReadDir(extractDir)
	if err != nil {
		return "", err
	}
	for _, entry := range entries {
		if entry.IsDir() {
			candidates = append(candidates, filepath.Join(extractDir, entry.Name()))
		}
	}
	for _, root := range candidates {
		if updatePayloadHasBinary(root) {
			return root, nil
		}
	}
	return "", fmt.Errorf("extracted archive does not contain lh binary under %s", extractDir)
}

func updatePayloadHasBinary(root string) bool {
	for _, name := range []string{"lh", "lh.exe"} {
		info, err := os.Stat(filepath.Join(root, name))
		if err == nil && !info.IsDir() {
			return true
		}
	}
	return false
}

func currentExecutablePath() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("resolve executable: %w", err)
	}
	resolved, err := filepath.EvalSymlinks(exe)
	if err == nil {
		exe = resolved
	}
	abs, err := filepath.Abs(exe)
	if err != nil {
		return exe, nil
	}
	return abs, nil
}

func detectInstallLayout(exePath string) (installRoot string, mode string) {
	dir := filepath.Dir(exePath)
	uiDir := filepath.Join(dir, managedInstallUIMark)
	runtimeDir := filepath.Join(dir, managedInstallRuntime)
	if dirExists(uiDir) && dirExists(runtimeDir) {
		return dir, "managed"
	}
	return dir, "binary"
}

func replaceManagedInstall(installRoot, payloadRoot string) error {
	parent := filepath.Dir(installRoot)
	if err := os.MkdirAll(parent, 0o755); err != nil {
		return err
	}

	stageDir, err := os.MkdirTemp(parent, ".luckyagent-update-stage-*")
	if err != nil {
		return fmt.Errorf("create stage dir: %w", err)
	}
	defer os.RemoveAll(stageDir)

	if err := copyUpdateTree(payloadRoot, stageDir); err != nil {
		return fmt.Errorf("stage payload: %w", err)
	}
	if !updatePayloadHasBinary(stageDir) {
		return errors.New("staged payload missing lh binary")
	}

	backupDir := ""
	if dirExists(installRoot) {
		backupDir = filepath.Join(parent, fmt.Sprintf(".luckyagent-update-backup-%d", time.Now().Unix()))
		if err := os.Rename(installRoot, backupDir); err != nil {
			return fmt.Errorf("backup current install: %w", err)
		}
	}

	if err := os.Rename(stageDir, installRoot); err != nil {
		if backupDir != "" {
			_ = os.Rename(backupDir, installRoot)
		}
		return fmt.Errorf("activate updated install: %w", err)
	}

	if backupDir != "" {
		_ = os.RemoveAll(backupDir)
	}
	return nil
}

func replaceBinaryInstall(exePath, payloadRoot string) error {
	src := filepath.Join(payloadRoot, "lh")
	if runtime.GOOS == "windows" {
		src = filepath.Join(payloadRoot, "lh.exe")
	}
	if _, err := os.Stat(src); err != nil {
		// tolerate cross-check during tests / unusual packages
		alt := filepath.Join(payloadRoot, "lh.exe")
		if runtime.GOOS != "windows" {
			if _, altErr := os.Stat(alt); altErr == nil {
				src = alt
			} else {
				return fmt.Errorf("payload binary not found: %w", err)
			}
		} else {
			return fmt.Errorf("payload binary not found: %w", err)
		}
	}

	info, err := os.Stat(exePath)
	mode := os.FileMode(0o755)
	if err == nil {
		mode = info.Mode().Perm()
		if mode == 0 {
			mode = 0o755
		}
	}

	dir := filepath.Dir(exePath)
	tmp, err := os.CreateTemp(dir, "."+filepath.Base(exePath)+".update-*")
	if err != nil {
		return fmt.Errorf("create temp binary: %w", err)
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)

	in, err := os.Open(src)
	if err != nil {
		tmp.Close()
		return err
	}
	_, copyErr := io.Copy(tmp, in)
	in.Close()
	chmodErr := tmp.Chmod(mode)
	closeErr := tmp.Close()
	if copyErr != nil {
		return copyErr
	}
	if chmodErr != nil {
		return chmodErr
	}
	if closeErr != nil {
		return closeErr
	}

	backup := exePath + ".bak"
	_ = os.Remove(backup)
	if err := os.Rename(exePath, backup); err != nil {
		// On Windows, rename of running exe may fail; try direct overwrite path later.
		if runtime.GOOS != "windows" {
			return fmt.Errorf("backup current binary: %w", err)
		}
		backup = ""
	}
	if err := os.Rename(tmpName, exePath); err != nil {
		if backup != "" {
			_ = os.Rename(backup, exePath)
		}
		return fmt.Errorf("replace binary: %w", err)
	}
	if backup != "" {
		_ = os.Remove(backup)
	}
	return nil
}

func copyUpdateTree(srcRoot, dstRoot string) error {
	return filepath.Walk(srcRoot, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(srcRoot, path)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}
		// Skip nested install helpers if desired? Keep them; harmless.
		target := filepath.Join(dstRoot, rel)
		if info.Mode()&os.ModeSymlink != 0 {
			link, err := os.Readlink(path)
			if err != nil {
				return err
			}
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return err
			}
			_ = os.Remove(target)
			return os.Symlink(link, target)
		}
		if info.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		return copyFile(path, target, info.Mode().Perm())
	})
}

func copyFile(src, dst string, mode os.FileMode) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode)
	if err != nil {
		return err
	}
	_, copyErr := io.Copy(out, in)
	closeErr := out.Close()
	if copyErr != nil {
		return copyErr
	}
	return closeErr
}

func dirExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}

func isWithinDir(base, target string) bool {
	baseAbs, err := filepath.Abs(base)
	if err != nil {
		return false
	}
	targetAbs, err := filepath.Abs(target)
	if err != nil {
		return false
	}
	rel, err := filepath.Rel(baseAbs, targetAbs)
	if err != nil {
		return false
	}
	return rel == "." || !strings.HasPrefix(rel, "..")
}

func displayVersion(v string) string {
	v = strings.TrimSpace(v)
	if v == "" {
		return "unknown"
	}
	return v
}

func normalizeVersionLabel(v string) string {
	v = strings.TrimSpace(v)
	v = strings.TrimPrefix(v, "v")
	v = strings.TrimPrefix(v, "V")
	if v == "" {
		return "0.0.0-dev"
	}
	// strip build metadata
	if i := strings.IndexByte(v, '+'); i >= 0 {
		v = v[:i]
	}
	return v
}

func compareVersionLabels(a, b string) int {
	a = normalizeVersionLabel(a)
	b = normalizeVersionLabel(b)
	if a == b {
		return 0
	}
	// Non-semver local builds (dev/unknown/git describe) are treated as older than a release tag.
	if !looksLikeDottedVersion(a) && looksLikeDottedVersion(b) {
		return -1
	}
	if looksLikeDottedVersion(a) && !looksLikeDottedVersion(b) {
		return 1
	}
	ap := splitVersionCorePre(a)
	bp := splitVersionCorePre(b)
	if c := compareIntParts(ap.core, bp.core); c != 0 {
		return c
	}
	// no pre-release > has pre-release
	if ap.pre == "" && bp.pre != "" {
		return 1
	}
	if ap.pre != "" && bp.pre == "" {
		return -1
	}
	if ap.pre == bp.pre {
		return 0
	}
	if ap.pre < bp.pre {
		return -1
	}
	return 1
}

type versionParts struct {
	core []int
	pre  string
}

func splitVersionCorePre(v string) versionParts {
	core, pre, _ := strings.Cut(v, "-")
	parts := strings.Split(core, ".")
	nums := make([]int, 0, len(parts))
	for _, p := range parts {
		n := 0
		for _, ch := range p {
			if ch < '0' || ch > '9' {
				break
			}
			n = n*10 + int(ch-'0')
		}
		nums = append(nums, n)
	}
	for len(nums) < 3 {
		nums = append(nums, 0)
	}
	return versionParts{core: nums, pre: pre}
}

func compareIntParts(a, b []int) int {
	n := len(a)
	if len(b) > n {
		n = len(b)
	}
	for i := 0; i < n; i++ {
		av, bv := 0, 0
		if i < len(a) {
			av = a[i]
		}
		if i < len(b) {
			bv = b[i]
		}
		if av < bv {
			return -1
		}
		if av > bv {
			return 1
		}
	}
	return 0
}

func looksLikeDottedVersion(v string) bool {
	v = normalizeVersionLabel(v)
	if v == "" || strings.EqualFold(v, "dev") || strings.EqualFold(v, "unknown") {
		return false
	}
	core, _, _ := strings.Cut(v, "-")
	parts := strings.Split(core, ".")
	if len(parts) < 2 {
		return false
	}
	for _, p := range parts {
		if p == "" {
			return false
		}
		for _, ch := range p {
			if ch < '0' || ch > '9' {
				return false
			}
		}
	}
	return true
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}
