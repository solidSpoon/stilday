package main

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/egoist/mygo"
)

// piVersion is the standalone Pi release Stilday installs on first use. It
// is pinned because the RPC protocol and the extension API are tested with
// this version; bump it with the app.
const piVersion = "1.4.0"

// piRelease says where a version's archive and checksums are.
type piRelease struct {
	version string
	baseURL string
	client  *http.Client
}

func defaultPiRelease() piRelease {
	return piRelease{
		version: piVersion,
		baseURL: "https://github.com/earendil-works/pi/releases/download/v" + piVersion,
		client:  &http.Client{Timeout: 10 * time.Minute},
	}
}

// piProgress reports a download or extraction stage. done and total are
// bytes, and zero when neither is known.
type piProgress func(stage string, done, total int64)

// piArchiveName is the release archive of this platform.
func piArchiveName() (string, error) {
	arch := runtime.GOARCH
	if arch == "amd64" {
		arch = "x64"
	}
	switch runtime.GOOS {
	case "darwin", "linux":
		if arch != "arm64" && arch != "x64" {
			return "", fmt.Errorf("Pi has no %s/%s build", runtime.GOOS, runtime.GOARCH)
		}
		return fmt.Sprintf("pi-%s-%s.tar.gz", runtime.GOOS, arch), nil
	case "windows":
		if arch != "arm64" && arch != "x64" {
			return "", fmt.Errorf("Pi has no %s/%s build", runtime.GOOS, runtime.GOARCH)
		}
		return fmt.Sprintf("pi-%s-%s.zip", runtime.GOOS, arch), nil
	}
	return "", fmt.Errorf("Pi has no %s build", runtime.GOOS)
}

func piBinaryName() string {
	if runtime.GOOS == "windows" {
		return "pi.exe"
	}
	return "pi"
}

// piInstallDir is the private directory of this Pi version:
// <app data>/pi/1.4.0.
func piInstallDir() (string, error) {
	data, err := mygo.App.Path(mygo.PathUserData)
	if err != nil {
		return "", err
	}
	return filepath.Join(data, "pi", piVersion), nil
}

// installedPiBinary is the downloaded Pi, or "". Older dev runs put it
// under the temporary executable's name, so those folders are searched
// too.
func installedPiBinary() string {
	if dir, err := piInstallDir(); err == nil {
		if p := piBinaryAt(dir); p != "" {
			return p
		}
	}
	// Earlier dev runs stored the runtime under the temporary executable's
	// name, such as ___go_build_stilday. Keep using it where it is.
	if legacy := legacyPiDir(); legacy != "" {
		return piBinaryAt(legacy)
	}
	return ""
}

// legacyPiDir finds a Pi installed by a go run build, under the temporary
// executable's name.
func legacyPiDir() string {
	data, err := mygo.App.Path(mygo.PathAppData)
	if err != nil {
		return ""
	}
	entries, _ := os.ReadDir(data)
	for _, e := range entries {
		if !e.IsDir() || !strings.Contains(strings.ToLower(e.Name()), "stilday") {
			continue
		}
		dir := filepath.Join(data, e.Name(), "pi", piVersion)
		if piBinaryAt(dir) != "" {
			return dir
		}
	}
	return ""
}

func piBinaryAt(dir string) string {
	p := filepath.Join(dir, piBinaryName())
	fi, err := os.Stat(p)
	if err != nil || fi.IsDir() || fi.Size() == 0 {
		return ""
	}
	return p
}

// installPi downloads the release archive, verifies its checksum, extracts
// it into dest, and returns the executable's path.
//
// The archive is unpacked in a temporary directory beside dest and renamed
// into place only after everything succeeded, so a failed download never
// leaves a half-installed Pi behind.
func installPi(ctx context.Context, rel piRelease, dest string, progress piProgress) (string, error) {
	if progress == nil {
		progress = func(string, int64, int64) {}
	}
	archiveName, err := piArchiveName()
	if err != nil {
		return "", err
	}
	parent := filepath.Dir(dest)
	if err := os.MkdirAll(parent, 0o755); err != nil {
		return "", err
	}
	tmp, err := os.MkdirTemp(parent, ".pi-install-")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(tmp)

	base := strings.TrimRight(rel.baseURL, "/")
	progress("正在下载 Pi "+rel.version, 0, 0)
	archivePath := filepath.Join(tmp, archiveName)
	if _, err := downloadTo(ctx, rel.client, base+"/"+archiveName, archivePath, progress); err != nil {
		return "", err
	}

	sums, err := httpBytes(ctx, rel.client, base+"/SHA256SUMS")
	if err != nil {
		return "", fmt.Errorf("校验文件：%w", err)
	}
	want, err := checksumFor(sums, archiveName)
	if err != nil {
		return "", err
	}
	got, err := fileSHA256(archivePath)
	if err != nil {
		return "", err
	}
	if !strings.EqualFold(want, got) {
		return "", fmt.Errorf("下载的文件与 SHA256SUMS 不一致")
	}

	out := filepath.Join(tmp, "out")
	if err := os.MkdirAll(out, 0o755); err != nil {
		return "", err
	}
	progress("正在解压 Pi", 0, 0)
	if strings.HasSuffix(archiveName, ".zip") {
		err = extractZip(archivePath, out)
	} else {
		err = extractTarGz(archivePath, out)
	}
	if err != nil {
		return "", err
	}
	bin := filepath.Join(out, piBinaryName())
	fi, err := os.Stat(bin)
	if err != nil || fi.IsDir() {
		return "", fmt.Errorf("压缩包里找不到 %s", piBinaryName())
	}
	if err := os.Chmod(bin, 0o755); err != nil {
		return "", err
	}

	if err := os.RemoveAll(dest); err != nil {
		return "", err
	}
	if err := os.Rename(out, dest); err != nil {
		return "", err
	}
	progress("Pi 已安装", 1, 1)
	return filepath.Join(dest, piBinaryName()), nil
}

func downloadTo(ctx context.Context, client *http.Client, url, dst string, progress piProgress) (int64, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return 0, err
	}
	req.Header.Set("User-Agent", "Stilday/"+mygo.Version)
	resp, err := client.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("下载 Pi：%s", resp.Status)
	}
	f, err := os.Create(dst)
	if err != nil {
		return 0, err
	}
	defer f.Close()
	total := resp.ContentLength
	r := &countingReader{r: resp.Body, on: func(n int64) {
		progress("正在下载 Pi", n, total)
	}}
	if _, err := io.Copy(f, r); err != nil {
		return 0, err
	}
	return r.n, f.Close()
}

type countingReader struct {
	r  io.Reader
	n  int64
	on func(int64)
}

func (c *countingReader) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	if n > 0 {
		c.n += int64(n)
		if c.on != nil {
			c.on(c.n)
		}
	}
	return n, err
}

func httpBytes(ctx context.Context, client *http.Client, url string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "Stilday/"+mygo.Version)
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%s", resp.Status)
	}
	return io.ReadAll(io.LimitReader(resp.Body, 1<<20))
}

func checksumFor(sums []byte, name string) (string, error) {
	for _, line := range strings.Split(string(sums), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		if strings.TrimPrefix(fields[len(fields)-1], "*") == name {
			return fields[0], nil
		}
	}
	return "", fmt.Errorf("SHA256SUMS 里没有 %s", name)
}

func fileSHA256(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func extractTarGz(src, dst string) error {
	f, err := os.Open(src)
	if err != nil {
		return err
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return err
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
		target, err := archiveTarget(dst, hdr.Name)
		if err != nil {
			return err
		}
		if target == "" {
			continue
		}
		switch hdr.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, 0o755); err != nil {
				return err
			}
		case tar.TypeReg:
			if err := writeArchiveFile(target, tr, hdr.Mode); err != nil {
				return err
			}
		case tar.TypeSymlink, tar.TypeLink:
			// Pi's archives have no links; refuse them rather than guess.
			return fmt.Errorf("压缩包里不允许链接：%s", hdr.Name)
		}
	}
	return nil
}

func extractZip(src, dst string) error {
	zr, err := zip.OpenReader(src)
	if err != nil {
		return err
	}
	defer zr.Close()
	for _, f := range zr.File {
		target, err := archiveTarget(dst, f.Name)
		if err != nil {
			return err
		}
		if target == "" {
			continue
		}
		if f.FileInfo().IsDir() {
			if err := os.MkdirAll(target, 0o755); err != nil {
				return err
			}
			continue
		}
		rc, err := f.Open()
		if err != nil {
			return err
		}
		err = writeArchiveFile(target, rc, int64(f.Mode().Perm()))
		rc.Close()
		if err != nil {
			return err
		}
	}
	return nil
}

// archiveTarget maps an archive entry into dst, dropping the archive's
// leading "pi/" directory and rejecting anything that escapes dst.
func archiveTarget(dst, name string) (string, error) {
	clean := path.Clean(name)
	if clean == "." || path.IsAbs(clean) || strings.HasPrefix(clean, "../") {
		return "", fmt.Errorf("压缩包里有非法路径：%s", name)
	}
	if clean == "pi" {
		return "", nil
	}
	if !strings.HasPrefix(clean, "pi/") {
		return "", fmt.Errorf("压缩包根目录不是 pi：%s", name)
	}
	rel := strings.TrimPrefix(clean, "pi/")
	target := filepath.Join(dst, filepath.FromSlash(rel))
	root := filepath.Clean(dst) + string(os.PathSeparator)
	if !strings.HasPrefix(target, root) {
		return "", fmt.Errorf("压缩包里有非法路径：%s", name)
	}
	return target, nil
}

func writeArchiveFile(target string, r io.Reader, mode int64) error {
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return err
	}
	perm := os.FileMode(mode).Perm()
	if perm == 0 {
		perm = 0o644
	}
	f, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, perm)
	if err != nil {
		return err
	}
	defer f.Close()
	if _, err := io.Copy(f, r); err != nil {
		return err
	}
	return f.Close()
}

// openPiLogin opens the installed Pi in Terminal on macOS, where the user
// runs /login once. Pi keeps the login in ~/.pi/agent, which the app then
// uses too.
func openPiLogin(piPath string) error {
	if runtime.GOOS != "darwin" {
		return errors.New("自动打开终端只支持 macOS")
	}
	cmdline := shellQuote(piPath)
	script := fmt.Sprintf(`tell application "Terminal" to do script %s`, strconv.Quote(cmdline))
	out, err := exec.Command("osascript", "-e", script).CombinedOutput()
	if err != nil {
		return fmt.Errorf("打开终端：%v：%s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
