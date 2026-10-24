package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/egoist/mygo"
)

// testArchive builds the smallest archive with Pi's layout: pi/<binary>
// and pi/assets/note.txt.
func testArchive(t *testing.T, extra map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	write := func(name string, mode int64, body string) {
		if err := tw.WriteHeader(&tar.Header{
			Name: name, Mode: mode, Size: int64(len(body)), Typeflag: tar.TypeReg,
		}); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write([]byte(body)); err != nil {
			t.Fatal(err)
		}
	}
	write("pi/"+piBinaryName(), 0o755, "#!/bin/sh\necho fake pi\n")
	write("pi/assets/note.txt", 0o644, "assets")
	for name, body := range extra {
		write(name, 0o644, body)
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func testReleaseServer(t *testing.T, archive []byte, sums string) (*httptest.Server, piRelease, string) {
	t.Helper()
	name, err := piArchiveName()
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/"+name, func(w http.ResponseWriter, r *http.Request) {
		w.Write(archive)
	})
	mux.HandleFunc("/SHA256SUMS", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, sums)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv, piRelease{
		version: "test",
		baseURL: srv.URL,
		client:  srv.Client(),
	}, name
}

func TestInstallPi(t *testing.T) {
	archive := testArchive(t, nil)
	sum := sha256.Sum256(archive)
	name, _ := piArchiveName()
	sums := hex.EncodeToString(sum[:]) + "  " + name + "\n"
	_, rel, _ := testReleaseServer(t, archive, sums)

	dest := filepath.Join(t.TempDir(), "1.4.0")
	bin, err := installPi(context.Background(), rel, dest, nil)
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Dir(bin) != dest || filepath.Base(bin) != piBinaryName() {
		t.Fatalf("binary is %s", bin)
	}
	body, err := os.ReadFile(bin)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(body, []byte("fake pi")) {
		t.Errorf("binary content %q", body)
	}
	if fi, err := os.Stat(bin); err != nil || fi.Mode()&0o111 == 0 {
		t.Errorf("binary is not executable: %v %v", fi.Mode(), err)
	}
	if _, err := os.Stat(filepath.Join(dest, "assets", "note.txt")); err != nil {
		t.Errorf("assets were not extracted: %v", err)
	}
}

func TestInstallPiRejectsBadChecksum(t *testing.T) {
	archive := testArchive(t, nil)
	name, _ := piArchiveName()
	_, rel, _ := testReleaseServer(t, archive, "0000  "+name+"\n")
	dest := filepath.Join(t.TempDir(), "1.4.0")
	if _, err := installPi(context.Background(), rel, dest, nil); err == nil {
		t.Fatal("installed an archive with the wrong checksum")
	}
	if _, err := os.Stat(dest); !os.IsNotExist(err) {
		t.Errorf("a failed install left %s behind", dest)
	}
}

func TestInstallPiRejectsTraversal(t *testing.T) {
	archive := testArchive(t, map[string]string{"pi/../../evil.txt": "outside"})
	sum := sha256.Sum256(archive)
	name, _ := piArchiveName()
	sums := hex.EncodeToString(sum[:]) + "  " + name + "\n"
	_, rel, _ := testReleaseServer(t, archive, sums)
	outside := filepath.Join(t.TempDir(), "evil.txt")
	dest := filepath.Join(filepath.Dir(outside), "1.4.0")
	if _, err := installPi(context.Background(), rel, dest, nil); err == nil {
		t.Fatal("installed an archive with a path outside its directory")
	}
	if _, err := os.Stat(outside); !os.IsNotExist(err) {
		t.Errorf("wrote outside the install directory")
	}
}

// The app prefers the Pi it downloaded over one on PATH.
func TestResolvePiUsesDownload(t *testing.T) {
	t.Setenv("STILDAY_PI", "")
	data := t.TempDir()
	mygo.App.SetPath(mygo.PathUserData, data)
	t.Cleanup(func() { mygo.App.SetPath(mygo.PathUserData, "") })
	dir := filepath.Join(data, "pi", piVersion)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	bin := filepath.Join(dir, piBinaryName())
	if err := os.WriteFile(bin, []byte("fake"), 0o755); err != nil {
		t.Fatal(err)
	}
	got, msg := resolvePi()
	if msg != "" || got != bin {
		t.Fatalf("resolvePi = %q, %q; want %q", got, msg, bin)
	}
}

// TestRealPiDownload installs the release into a temporary directory and
// checks that it runs. It needs the network, so it is opt-in.
func TestRealPiDownload(t *testing.T) {
	if os.Getenv("STILDAY_REAL_DOWNLOAD") == "" {
		t.Skip("set STILDAY_REAL_DOWNLOAD=1 to download the Pi release")
	}
	dest := filepath.Join(t.TempDir(), piVersion)
	bin, err := installPi(context.Background(), defaultPiRelease(), dest, nil)
	if err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command(bin, "--version").CombinedOutput()
	if err != nil {
		t.Fatalf("%s --version: %v: %s", bin, err, out)
	}
	if !strings.Contains(string(out), piVersion) {
		t.Errorf("version output %q", out)
	}
}
