package main

import (
	"image"
	"image/color"
	"image/jpeg"
	"os"
	"path/filepath"
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
