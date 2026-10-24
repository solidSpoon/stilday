package main

import (
	"bytes"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// cleanupLegacyPhotoOriginals removes the photos/ folders that older
// collectors copied into work days. The current layout keeps only
// previews/, thumbs/ and sheets/.
func cleanupLegacyPhotoOriginals(workRoot string) {
	if workRoot == "" {
		return
	}
	_ = filepath.WalkDir(workRoot, func(path string, d fs.DirEntry, err error) error {
		if err != nil || !d.IsDir() {
			return nil
		}
		if d.Name() == "photos" {
			_ = os.RemoveAll(path)
			return filepath.SkipDir
		}
		return nil
	})
}

// imageFiles finds the images under a folder, or under the originals of a
// macOS Photos library.

// imageFiles finds the images under a folder, or under the originals of a
// macOS Photos library.
func imageFiles(root string) ([]string, error) {
	root = expandPath(root)
	info, err := os.Stat(root)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("%s 不是文件夹", root)
	}
	if strings.HasSuffix(strings.ToLower(filepath.Base(root)), ".photoslibrary") {
		originals := filepath.Join(root, "originals")
		if st, err := os.Stat(originals); err == nil && st.IsDir() {
			root = originals
		}
	}
	var files []string
	err = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil // unreadable folder: skip it
		}
		if d.IsDir() {
			name := d.Name()
			if strings.HasPrefix(name, ".") && path != root {
				return filepath.SkipDir
			}
			return nil
		}
		switch strings.ToLower(filepath.Ext(path)) {
		case ".jpg", ".jpeg", ".png", ".heic", ".heif", ".tif", ".tiff", ".webp":
			files = append(files, path)
		}
		return nil
	})
	return files, err
}

// captureTimes reads the image's own EXIF/creation date. There is no
// import-date mode: a photo belongs to the day it was taken. Missing EXIF
// falls back to Spotlight's content date and then to the file's date.

// captureTimes reads the image's own EXIF/creation date. There is no
// import-date mode: a photo belongs to the day it was taken. Missing EXIF
// falls back to Spotlight's content date and then to the file's date.
func captureTimes(paths []string) []time.Time {
	out := sipsTimes(paths)
	const batch = 100
	for i := 0; i < len(paths); i += batch {
		end := min(i+batch, len(paths))
		chunk := paths[i:end]
		missing := false
		for j := i; j < end; j++ {
			if out[j].IsZero() {
				missing = true
				break
			}
		}
		if !missing {
			continue
		}
		at, err := mdlsTimes(chunk)
		if err != nil {
			for j, p := range chunk {
				if out[i+j].IsZero() {
					out[i+j] = captureTime(p)
				}
			}
			continue
		}
		for j := i; j < end; j++ {
			if out[j].IsZero() {
				out[j] = at[j-i]
			}
		}
	}
	return out
}

// sipsTimes reads the EXIF/creation date in batches. The Photos library's
// originals are protected by TCC, so this runs in the app, not a shell.

// sipsTimes reads the EXIF/creation date in batches. The Photos library's
// originals are protected by TCC, so this runs in the app, not a shell.
func sipsTimes(paths []string) []time.Time {
	out := make([]time.Time, len(paths))
	if _, err := exec.LookPath("sips"); err != nil {
		return out
	}
	const batch = 80
	for i := 0; i < len(paths); i += batch {
		end := min(i+batch, len(paths))
		chunk := paths[i:end]
		args := append([]string{"-g", "creation"}, chunk...)
		raw, err := exec.Command("sips", args...).Output()
		if err != nil {
			continue
		}
		index := make(map[string]int, len(chunk))
		for j, p := range chunk {
			index[p] = i + j
		}
		current := ""
		for _, line := range strings.Split(string(raw), "\n") {
			trim := strings.TrimSpace(line)
			if trim == "" {
				continue
			}
			if line == trim && strings.Contains(trim, "/") {
				current = trim
				continue
			}
			if strings.HasPrefix(trim, "creation:") && current != "" {
				v := strings.TrimSpace(strings.TrimPrefix(trim, "creation:"))
				if v == "" || v == "(null)" {
					continue
				}
				if t, err := time.ParseInLocation("2006:01:02 15:04:05", v, time.Local); err == nil {
					if j, ok := index[current]; ok {
						out[j] = t
					}
				}
			}
		}
	}
	return out
}

// mdlsTimes reads one attribute for a batch of files.

// mdlsTimes reads one attribute for a batch of files.
func mdlsTimes(paths []string) ([]time.Time, error) {
	if _, err := exec.LookPath("mdls"); err != nil {
		return nil, err
	}
	args := append([]string{"-name", "kMDItemContentCreationDate", "-raw", "-nullMarker", "NULL"}, paths...)
	raw, err := exec.Command("mdls", args...).Output()
	if err != nil {
		return nil, err
	}
	parts := bytes.Split(bytes.TrimRight(raw, "\x00\n"), []byte{0})
	if len(parts) != len(paths) {
		return nil, fmt.Errorf("mdls returned %d values for %d files", len(parts), len(paths))
	}
	out := make([]time.Time, len(paths))
	for i, part := range parts {
		s := strings.TrimSpace(string(part))
		if s == "" || s == "NULL" || s == "(null)" {
			out[i] = fallbackTime(paths[i])
			continue
		}
		t, err := time.Parse("2006-01-02 15:04:05 -0700", s)
		if err != nil {
			out[i] = fallbackTime(paths[i])
			continue
		}
		out[i] = t.Local()
	}
	return out, nil
}

// fallbackTime is a file's modification time, when no capture date is
// known.

// fallbackTime is a file's modification time, when no capture date is
// known.
func fallbackTime(path string) time.Time {
	if fi, err := os.Stat(path); err == nil {
		return fi.ModTime()
	}
	return time.Time{}
}

// captureTime reads when one photo was taken.

// captureTime reads when one photo was taken.
func captureTime(path string) time.Time {
	out, err := exec.Command("mdls", "-name", "kMDItemContentCreationDate", "-raw", path).Output()
	if err == nil {
		s := strings.TrimSpace(string(out))
		if s != "" && s != "(null)" {
			if t, err := time.Parse("2006-01-02 15:04:05 -0700", s); err == nil {
				return t.Local()
			}
		}
	}
	return fallbackTime(path)
}

// makePreview writes a JPEG no larger than limit. sips reads HEIC and
// every other image the system knows; Go's decoders cover the formats it
// cannot on other platforms.

// originalSizes reads the source photos' pixel sizes in batches. mdls knows
// HEIC, which Go's decoders do not; fileDimensions is the fallback.
func originalSizes(paths []string) [][2]int {
	out := make([][2]int, len(paths))
	if len(paths) == 0 {
		return out
	}
	if _, err := exec.LookPath("mdls"); err != nil {
		for i, p := range paths {
			out[i] = fileDimensions(p)
		}
		return out
	}
	const batch = 100
	for i := 0; i < len(paths); i += batch {
		end := min(i+batch, len(paths))
		chunk := paths[i:end]
		args := []string{"-name", "kMDItemPixelWidth", "-name", "kMDItemPixelHeight"}
		args = append(args, chunk...)
		raw, err := exec.Command("mdls", args...).Output()
		if err != nil {
			for j, p := range chunk {
				out[i+j] = fileDimensions(p)
			}
			continue
		}
		lines := strings.Split(strings.TrimSpace(string(raw)), "\n")
		at := 0
		for j := range chunk {
			var w, h int
			for k := 0; k < 2 && at < len(lines); k, at = k+1, at+1 {
				line := strings.TrimSpace(lines[at])
				if strings.HasPrefix(line, "kMDItemPixelWidth") {
					w = numericValue(line)
				}
				if strings.HasPrefix(line, "kMDItemPixelHeight") {
					h = numericValue(line)
				}
			}
			if w == 0 || h == 0 {
				d := fileDimensions(chunk[j])
				w, h = d[0], d[1]
			}
			out[i+j] = [2]int{w, h}
		}
	}
	return out
}

func numericValue(line string) int {
	if i := strings.LastIndex(line, "="); i >= 0 {
		line = strings.TrimSpace(line[i+1:])
	}
	n := 0
	for _, r := range line {
		if r < '0' || r > '9' {
			break
		}
		n = n*10 + int(r-'0')
	}
	return n
}

func fileDimensions(path string) [2]int {
	w, h := imageSize(path)
	return [2]int{w, h}
}
