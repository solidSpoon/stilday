package main

import (
	"fmt"
	"image"
	"image/color"
	"image/jpeg"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// makePhoto writes a solid-color JPEG, standing in for a camera file.
func makePhoto(t *testing.T, path string, c color.RGBA) {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 400, 300))
	for y := 0; y < 300; y++ {
		for x := 0; x < 400; x++ {
			img.Set(x, y, c)
		}
	}
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := jpeg.Encode(f, img, &jpeg.Options{Quality: 90}); err != nil {
		t.Fatal(err)
	}
}

// makeSizedPhoto writes a solid-color JPEG of a size of its own, so that
// two photos can be told apart when their capture times are equal.
func makeSizedPhoto(t *testing.T, path string, w, h int, c color.RGBA) {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.Set(x, y, c)
		}
	}
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := jpeg.Encode(f, img, &jpeg.Options{Quality: 90}); err != nil {
		t.Fatal(err)
	}
}

// A day that keeps removed photos only grows: a photo the library lost
// stays, with its preview, and a new one is added after it.
func TestKeepRemoved(t *testing.T) {
	src := t.TempDir()
	now := time.Now()
	start, end := now.Add(-3*time.Hour), now.Add(time.Hour)
	write := func(name string, w int) string {
		p := filepath.Join(src, name)
		makeSizedPhoto(t, p, w, 300, color.RGBA{200, 40, 40, 255})
		return p
	}
	a := write("a.jpg", 400)
	b := write("b.jpg", 401)
	c := write("c.jpg", 402)
	_ = a

	dayDir := t.TempDir()
	opts := photoOptions{Source: src, Day: now, Start: start, End: end, MaxPhotos: 50, SheetSize: 20}
	m, err := preparePhotos(dayDir, opts, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(m.Photos) != 3 {
		t.Fatalf("%d photos, want 3", len(m.Photos))
	}

	// The library loses b and gains d.
	if err := os.Remove(b); err != nil {
		t.Fatal(err)
	}
	_ = c
	d := write("d.jpg", 403)
	opts.KeepRemoved = true
	m, err = preparePhotos(dayDir, opts, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(m.Photos) != 4 {
		t.Fatalf("%d photos, want 4 (a, b, c and d)", len(m.Photos))
	}
	var kept, added *photo
	for i := range m.Photos {
		switch m.Photos[i].Width {
		case 401:
			kept = &m.Photos[i]
		case 403:
			added = &m.Photos[i]
		}
	}
	if kept == nil {
		t.Fatal("the photo the library lost was dropped")
	}
	if _, err := os.Stat(filepath.Join(dayDir, kept.Preview)); err != nil {
		t.Errorf("its preview is gone: %v", err)
	}
	if kept.Index != 2 || kept.Preview != "previews/002.jpg" {
		t.Errorf("the kept photo moved: %+v", *kept)
	}
	if added == nil {
		t.Fatal("the new photo was not added")
	}
	if added.Index != 4 || added.Preview != "previews/004.jpg" {
		t.Errorf("the new photo is %+v, want index 4", *added)
	}
	if _, err := os.Stat(filepath.Join(dayDir, added.Preview)); err != nil {
		t.Errorf("its preview is missing: %v", err)
	}
	if !strings.Contains(m.PhotoNote, "1 张") || !strings.Contains(m.PhotoNote, "保留") {
		t.Errorf("the note does not say a photo is kept: %q", m.PhotoNote)
	}
	_ = d

	// Running again adds nothing: the same photos are recognised.
	again, err := preparePhotos(dayDir, opts, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(again.Photos) != 4 {
		t.Fatalf("%d photos on the second run, want 4", len(again.Photos))
	}

	// Turned off, the day follows the library again.
	opts.KeepRemoved = false
	pruned, err := preparePhotos(dayDir, opts, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(pruned.Photos) != 3 {
		t.Fatalf("%d photos after pruning, want 3", len(pruned.Photos))
	}
	for _, p := range pruned.Photos {
		if p.Width == 401 {
			t.Error("the photo the library lost survived pruning")
		}
	}
}

func TestPreparePhotos(t *testing.T) {
	src := t.TempDir()
	now := time.Now()
	start, end := now.Add(-3*time.Hour), now.Add(time.Hour)
	for i, c := range []color.RGBA{
		{200, 40, 40, 255}, {40, 200, 40, 255}, {40, 40, 200, 255},
		{200, 200, 40, 255}, {40, 200, 200, 255}, {200, 40, 200, 255},
		{120, 120, 120, 255},
	} {
		p := filepath.Join(src, "photo-"+string(rune('a'+i))+".jpg")
		makePhoto(t, p, c)
		// Keep every photo in the window even where Spotlight has no
		// capture date.
		at := now.Add(-time.Duration(i+1) * 10 * time.Minute)
		if err := os.Chtimes(p, at, at); err != nil {
			t.Fatal(err)
		}
	}

	dayDir := t.TempDir()
	m, err := preparePhotos(dayDir, photoOptions{
		Source: src, Day: now, Start: start, End: end, MaxPhotos: 50, SheetSize: 20,
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(m.Photos) != 7 {
		t.Fatalf("%d photos, want 7", len(m.Photos))
	}
	if len(m.Sheets) != 1 {
		t.Fatalf("%d sheets, want 1", len(m.Sheets))
	}
	for i, p := range m.Photos {
		if p.Index != i+1 {
			t.Errorf("photo %d has index %d", i, p.Index)
		}
		if _, err := os.Stat(filepath.Join(dayDir, p.Preview)); err != nil {
			t.Errorf("preview of photo %d: %v", p.Index, err)
		}
		if p.Read != p.Preview {
			t.Errorf("photo %d read path is %q, want preview", p.Index, p.Read)
		}
		if _, err := os.Stat(filepath.Join(dayDir, p.Thumb)); err != nil {
			t.Errorf("thumb of photo %d: %v", p.Index, err)
		}
		if p.Read == "" || p.SheetFile == "" || p.Sheet != 1 || p.Cell != i+1 {
			t.Errorf("photo %d mapping is %+v", p.Index, p)
		}
		if p.CellRow != i/sheetCols+1 || p.CellCol != i%sheetCols+1 {
			t.Errorf("photo %d cell is row %d col %d", p.Index, p.CellRow, p.CellCol)
		}
		if p.Width != 400 || p.Height != 300 {
			t.Errorf("photo %d original is %dx%d", p.Index, p.Width, p.Height)
		}
		if p.Bytes == 0 || p.Format != "jpg" {
			t.Errorf("photo %d file info is %+v", p.Index, p)
		}
	}
	sheetPath := filepath.Join(dayDir, m.Sheets[0].File)
	f, err := os.Open(sheetPath)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	cfg, _, err := image.DecodeConfig(f)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Width < 1000 || cfg.Height < 400 {
		t.Errorf("sheet is %dx%d", cfg.Width, cfg.Height)
	}

	// The manifest is what the Pi extension reads.
	loaded, err := loadManifest(dayDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded.Photos) != 7 || len(loaded.Sheets) != 1 {
		t.Fatalf("manifest has %d photos and %d sheets", len(loaded.Photos), len(loaded.Sheets))
	}
	if p := loaded.photo(4); p == nil || p.Index != 4 {
		t.Errorf("photo(4) = %+v", p)
	}
	if len(loaded.Files) != 7*2+1 {
		t.Errorf("file index has %d entries, want %d", len(loaded.Files), 7*2+1)
	}
	if sh := loaded.Sheets[0]; sh.Index != 1 || len(sh.Cells) != 7 || sh.Cells[3].Photo != 4 {
		t.Errorf("sheet mapping is %+v", sh)
	}
}

func TestGroupPhotos(t *testing.T) {
	photos := make([]photo, 30)
	for i := range photos {
		photos[i] = photo{Index: i + 1}
	}
	groups := groupPhotos(photos, 24)
	if len(groups) != 2 {
		t.Fatalf("%d groups, want 2", len(groups))
	}
	if len(groups[0]) != 24 || len(groups[1]) != 6 {
		t.Errorf("groups are %d and %d photos", len(groups[0]), len(groups[1]))
	}
	one := groupPhotos(photos[:24], 24)
	if len(one) != 1 || len(one[0]) != 24 {
		t.Errorf("24 photos made %d groups", len(one))
	}
}

func TestImageFilesSkipsOtherFiles(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "a.jpg"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "notes.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	files, err := imageFiles(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 1 {
		t.Fatalf("%d files, want 1", len(files))
	}
}

// A photo that cannot be rendered takes no number: the label on the
// sheet, the manifest and the file name stay one number.
func TestNumberMatchesFile(t *testing.T) {
	src := t.TempDir()
	now := time.Now()
	start, end := now.Add(-3*time.Hour), now.Add(time.Hour)
	// A file with an image extension that is not an image: the scan takes
	// it, it sorts first, and the conversion fails. The photos after it
	// must keep 001 and 002 rather than filling its place in the files.
	if err := os.WriteFile(filepath.Join(src, "a.jpg"), []byte("not an image"), 0o644); err != nil {
		t.Fatal(err)
	}
	makePhoto(t, filepath.Join(src, "b.jpg"), color.RGBA{200, 40, 40, 255})
	makePhoto(t, filepath.Join(src, "c.jpg"), color.RGBA{40, 40, 200, 255})

	dayDir := t.TempDir()
	m, err := preparePhotos(dayDir, photoOptions{
		Source: src, Day: now, Start: start, End: end, MaxPhotos: 50, SheetSize: 20,
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if m.Selected != 3 {
		t.Fatalf("scanned %d, want 3 with the unreadable file", m.Selected)
	}
	if len(m.Photos) != 2 {
		t.Fatalf("%d photos, want 2", len(m.Photos))
	}
	for i, p := range m.Photos {
		if p.Index != i+1 {
			t.Errorf("photo %d has index %d", i, p.Index)
		}
		if want := fmt.Sprintf("previews/%03d.jpg", p.Index); p.Preview != want {
			t.Errorf("index %d is file %q, want %q", p.Index, p.Preview, want)
		}
		if want := fmt.Sprintf("thumbs/%03d.jpg", p.Index); p.Thumb != want {
			t.Errorf("index %d has thumb %q, want %q", p.Index, p.Thumb, want)
		}
		if _, err := os.Stat(filepath.Join(dayDir, p.Preview)); err != nil {
			t.Errorf("index %d: %v", p.Index, err)
		}
	}
}

// A day put in capture order takes the numbers of its places, and the
// pictures follow their entries, whatever order they were collected in.
func TestRenumberPhotos(t *testing.T) {
	dayDir := t.TempDir()
	for _, d := range []string{"previews", "thumbs"} {
		if err := os.MkdirAll(filepath.Join(dayDir, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	// Old 001 is the 08:00 photo, 002 the 10:00 one and 003 the 09:00 one:
	// after ordering they must be 001, 003, 002.
	write := func(rel, content string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dayDir, rel), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	read := func(rel string) string {
		t.Helper()
		b, err := os.ReadFile(filepath.Join(dayDir, rel))
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}
	for i, content := range []string{"a", "b", "c"} {
		write(previewName(i+1), content)
		write(thumbName(i+1), "t"+content)
	}
	photos := []photo{
		{Index: 3, Preview: previewName(3), Thumb: thumbName(3), CapturedAt: "2026-10-24T09:00:00+08:00"},
		{Index: 1, Preview: previewName(1), Thumb: thumbName(1), CapturedAt: "2026-10-24T08:00:00+08:00"},
		{Index: 2, Preview: previewName(2), Thumb: thumbName(2), CapturedAt: "2026-10-24T10:00:00+08:00"},
	}
	if err := renumberPhotos(dayDir, photos); err != nil {
		t.Fatal(err)
	}
	// 08:00 was "a", 09:00 "c", 10:00 "b".
	want := []string{"a", "c", "b"}
	for i, p := range photos {
		if p.Index != i+1 {
			t.Errorf("photo %d has index %d", i, p.Index)
		}
		if p.Preview != previewName(i+1) || p.Thumb != thumbName(i+1) || p.Read != previewName(i+1) {
			t.Errorf("photo %d is %+v", i, p)
		}
		if got := read(p.Preview); got != want[i] {
			t.Errorf("preview %d holds %q, want %q", i+1, got, want[i])
		}
		if got := read(p.Thumb); got != "t"+want[i] {
			t.Errorf("thumb %d holds %q", i+1, got)
		}
	}
	// No temporary names stay behind.
	entries, err := os.ReadDir(filepath.Join(dayDir, "previews"))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 3 {
		t.Errorf("%d files in previews, want 3", len(entries))
	}
}
