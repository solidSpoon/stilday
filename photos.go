package main

import (
	"errors"
	"fmt"
	"image"
	"image/draw"
	"image/jpeg"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	xdraw "golang.org/x/image/draw"
)

// preparePhotos reads the day's photos, makes JPEG previews and thumbs,
// arranges numbered contact sheets, and writes manifest.json. It keeps no
// original copies in the day folder.
//
// progress is called from the preparing goroutine; callers marshal it to
// the UI thread themselves.
func preparePhotos(dayDir string, o photoOptions, progress func(stage string, done, total int)) (*manifest, error) {
	if o.Source == "" {
		return nil, errors.New("没有选择照片来源")
	}
	if o.MaxPhotos <= 0 {
		o.MaxPhotos = defaultMaxPhotos
	}
	if o.SheetSize <= 0 || o.SheetSize > sheetCols*sheetRows {
		o.SheetSize = sheetCols * sheetRows
	}
	if progress == nil {
		progress = func(string, int, int) {}
	}

	progress("正在查找照片", 0, 0)
	library := strings.HasSuffix(strings.ToLower(filepath.Base(expandPath(o.Source))), ".photoslibrary")
	photoSource := "originals"
	photoNote := ""
	photoKitTried := false
	// writeEmpty records a day with no photos; writeEmptyOrKeep keeps the
	// manifest that still describes copies in the folder when a source is
	// only temporarily unreadable.
	writeEmpty := func() (*manifest, error) {
		m := &manifest{
			Day:         dayKey(o.Day),
			GeneratedAt: time.Now().Format(time.RFC3339),
			Source:      o.Source,
			Window:      [2]string{o.Start.Format("15:04"), o.End.Format("15:04")},
			PhotoSource: photoSource,
			PhotoNote:   photoNote,
		}
		if err := writeManifest(dayDir, m); err != nil {
			return nil, err
		}
		return m, nil
	}
	writeEmptyOrKeep := func() (*manifest, error) {
		if old, err := loadManifest(dayDir); err == nil && len(old.Photos) > 0 {
			return old, nil
		}
		return writeEmpty()
	}

	var (
		files []string
		times []time.Time
		sizes [][2]int
		metas []photokitAsset
	)
	// The official PhotoKit is the only reader for a Photos library. It
	// exports the original data and its EXIF capture date; the helper never
	// scans the library's originals/ folder.
	if library {
		photoKitTried = true
		progress("正在从照片库读取", 0, 0)
		if tmp, err := os.MkdirTemp("", "stilday-photokit-"); err == nil {
			assets, aerr := photokitExport(o.Start, o.End, tmp, 0)
			if aerr == nil {
				defer os.RemoveAll(tmp)
				for _, a := range assets {
					files = append(files, filepath.Join(tmp, a.File))
					at := time.Unix(int64(a.Time), 0).Local()
					if a.Capture > 0 {
						at = time.Unix(int64(a.Capture), 0).Local()
					}
					times = append(times, at)
					sizes = append(sizes, [2]int{a.Width, a.Height})
					metas = append(metas, a)
				}
				if len(files) > 0 {
					photoSource = "photokit"
				} else {
					photoNote = "PhotoKit 没有返回可用的照片"
				}
			} else {
				photoNote = "PhotoKit 不可用：" + aerr.Error()
			}
		} else {
			photoNote = "无法创建照片临时目录：" + err.Error()
		}
	}
	if files == nil {
		// A Photos library must go through PhotoKit. Scanning its originals/
		// folder downloads old iCloud assets and their added date looks like
		// today, which is exactly how years-old photos used to leak in.
		if photoKitTried {
			if photoNote == "" {
				photoNote = "PhotoKit 没有返回照片；没有扫描图库 originals"
			}
			return writeEmpty()
		}
		var err error
		files, err = imageFiles(o.Source)
		if err != nil {
			return nil, err
		}
		if len(files) == 0 {
			// A temporarily unreadable source must not wipe the copies the
			// day folder already describes.
			return writeEmptyOrKeep()
		}
		// Capture times come from Spotlight: it reads EXIF and the Photos
		// library's metadata, and works the same for JPEG and HEIC. One mdls
		// call reads a batch, so a large library is not one process per file.
		progress("正在读取拍摄时间", 0, len(files))
		times = captureTimes(files)
	}
	if len(files) == 0 {
		return writeEmptyOrKeep()
	}
	type candidate struct {
		path string
		at   time.Time
		w, h int
		pk   *photokitAsset
	}
	selected := make([]candidate, 0, len(files))
	for i, p := range files {
		at := times[i]
		if at.IsZero() {
			continue
		}
		if !at.Before(o.Start) && at.Before(o.End) {
			c := candidate{path: p, at: at}
			if i < len(sizes) {
				c.w, c.h = sizes[i][0], sizes[i][1]
			}
			if i < len(metas) {
				pk := metas[i]
				c.pk = &pk
			}
			selected = append(selected, c)
		}
	}
	sort.Slice(selected, func(i, j int) bool { return selected[i].at.Before(selected[j].at) })
	scanned := len(selected)

	// A day can hold thousands of photos. The sheets show an even sample
	// across the day, so every part of it is represented; the detail tool
	// reads whichever sampled photo the model asks for.
	if len(selected) > o.MaxPhotos {
		sampled := make([]candidate, 0, o.MaxPhotos)
		step := float64(len(selected)) / float64(o.MaxPhotos)
		for i := 0; i < o.MaxPhotos; i++ {
			sampled = append(sampled, selected[int(float64(i)*step)])
		}
		selected = sampled
	}

	m := &manifest{
		Day:         dayKey(o.Day),
		GeneratedAt: time.Now().Format(time.RFC3339),
		Source:      o.Source,
		Window:      [2]string{o.Start.Format("15:04"), o.End.Format("15:04")},
		Scanned:     scanned,
		Selected:    len(selected),
		PhotoSource: photoSource,
		PhotoNote:   photoNote,
	}
	// A re-run replaces the day's generated files. The old photos/ folder
	// (original copies) is removed too; it is not part of the new layout.
	for _, d := range []string{"photos", "previews", "thumbs", "sheets"} {
		_ = os.RemoveAll(filepath.Join(dayDir, d))
	}
	for _, d := range []string{"previews", "thumbs", "sheets"} {
		if err := os.MkdirAll(filepath.Join(dayDir, d), 0o755); err != nil {
			return nil, err
		}
	}

	// Original pixel sizes come from the same Spotlight metadata as the
	// times, so a 4000px photo is not described by its 1600px preview.
	paths := make([]string, len(selected))
	for i, c := range selected {
		paths[i] = c.path
	}
	if len(selected) > 0 && (selected[0].w == 0 || selected[0].h == 0) {
		orig := originalSizes(paths)
		for i := range selected {
			selected[i].w, selected[i].h = orig[i][0], orig[i][1]
		}
	}

	// Make the one large JPEG the model reads and the small JPEG the app
	// shows. No original copy is kept: the source can be HEIC or protected,
	// and these two JPEGs are all the model and the UI need.
	progress("正在生成预览", 0, len(selected))
	for i, c := range selected {
		ext := strings.ToLower(filepath.Ext(c.path))
		if ext == "" {
			ext = ".jpg"
		}
		preview := filepath.ToSlash(filepath.Join("previews", fmt.Sprintf("%03d.jpg", i+1)))
		if err := makePreview(c.path, filepath.Join(dayDir, preview), 1600); err != nil {
			continue
		}
		thumb := filepath.ToSlash(filepath.Join("thumbs", fmt.Sprintf("%03d.jpg", i+1)))
		if err := makePreview(filepath.Join(dayDir, preview), filepath.Join(dayDir, thumb), 360); err != nil {
			_ = os.Remove(filepath.Join(dayDir, preview))
			continue
		}
		w, h := imageSize(filepath.Join(dayDir, preview))
		if c.w > 0 && c.h > 0 {
			w, h = c.w, c.h
		}
		format := strings.TrimPrefix(ext, ".")
		if format == "" {
			format = "jpeg"
		}
		var size int64
		if fi, err := os.Stat(filepath.Join(dayDir, preview)); err == nil {
			size = fi.Size()
		}
		m.Photos = append(m.Photos, photo{
			Index:      i + 1,
			Preview:    preview,
			Thumb:      thumb,
			Read:       preview,
			CapturedAt: c.at.Format(time.RFC3339),
			Width:      w,
			Height:     h,
			Bytes:      size,
			Format:     format,
			Meta:       photoMetadata(c.path, c.pk),
		})
		if i%5 == 0 {
			progress("正在生成预览", i+1, len(selected))
		}
	}
	// Skipped photos leave gaps in the numbering; renumber to keep the
	// sheet labels contiguous.
	for i := range m.Photos {
		m.Photos[i].Index = i + 1
	}

	progress("正在拼接合集", 0, 0)
	m.Sheets = make([]sheet, 0, (len(m.Photos)+o.SheetSize-1)/o.SheetSize)
	for _, group := range groupPhotos(m.Photos, o.SheetSize) {
		idx := len(m.Sheets) + 1
		first, last := group[0], group[len(group)-1]
		sh := sheet{
			Index: idx,
			File:  filepath.ToSlash(filepath.Join("sheets", fmt.Sprintf("%02d.jpg", idx))),
			From:  first.CapturedAt,
			To:    last.CapturedAt,
			First: first.Index,
			Last:  last.Index,
		}
		for cell, p := range group {
			row, col := cell/sheetCols, cell%sheetCols
			sh.Photos = append(sh.Photos, p.Index)
			sh.Cells = append(sh.Cells, sheetCell{Photo: p.Index, Row: row + 1, Col: col + 1})
			m.Photos[p.Index-1].Sheet = sh.Index
			m.Photos[p.Index-1].SheetFile = sh.File
			m.Photos[p.Index-1].Cell = cell + 1
			m.Photos[p.Index-1].CellRow = row + 1
			m.Photos[p.Index-1].CellCol = col + 1
		}
		if err := composeSheet(o.Day, sh, len(m.Sheets)+1, group, filepath.Join(dayDir, sh.File)); err != nil {
			return nil, err
		}
		m.Sheets = append(m.Sheets, sh)
	}

	// The complete inventory of the day's material files. The model gets
	// the same information in previews/sheets, but a flat list is easier to
	// scan for a person or a script.
	for _, p := range m.Photos {
		m.Files = append(m.Files,
			fileEntry(dayDir, p.Preview, "preview", p.Index, "", p.CapturedAt),
			fileEntry(dayDir, p.Thumb, "thumb", p.Index, p.Preview, p.CapturedAt),
		)
	}
	for _, sh := range m.Sheets {
		m.Files = append(m.Files, fileEntry(dayDir, sh.File, "sheet", 0, "", "").
			withSheet(sh.Index))
	}

	if err := writeManifest(dayDir, m); err != nil {
		return nil, err
	}
	progress("完成", len(m.Photos), len(m.Photos))
	return m, nil
}

// makePreview writes a JPEG no larger than limit. sips reads HEIC and
// every other image the system knows; Go's decoders cover the formats it
// cannot on other platforms.
func makePreview(src, dst string, limit int) error {
	if path, err := exec.LookPath("sips"); err == nil {
		out, err := exec.Command(path, "-Z", fmt.Sprint(limit), "-s", "format", "jpeg", "--out", dst, src).CombinedOutput()
		if err == nil {
			return nil
		}
		return fmt.Errorf("sips: %v: %s", err, strings.TrimSpace(string(out)))
	}
	f, err := os.Open(src)
	if err != nil {
		return err
	}
	defer f.Close()
	img, _, err := image.Decode(f)
	if err != nil {
		return err
	}
	img = fitImage(img, limit)
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer out.Close()
	return jpeg.Encode(out, img, &jpeg.Options{Quality: 85})
}

// fitImage scales an image down so neither side is longer than max.

// fitImage scales an image down so neither side is longer than max.
func fitImage(src image.Image, limit int) image.Image {
	b := src.Bounds()
	w, h := b.Dx(), b.Dy()
	if w <= limit && h <= limit {
		return src
	}
	scale := float64(limit) / float64(max(w, h))
	nw, nh := int(float64(w)*scale), int(float64(h)*scale)
	dst := image.NewRGBA(image.Rect(0, 0, nw, nh))
	xdraw.ApproxBiLinear.Scale(dst, dst.Bounds(), src, b, draw.Src, nil)
	return dst
}

// imageSize returns a preview's pixel size.
// fileEntry stats a derived image for the manifest's flat file list.

func imageSize(path string) (int, int) {
	f, err := os.Open(path)
	if err != nil {
		return 0, 0
	}
	defer f.Close()
	cfg, _, err := image.DecodeConfig(f)
	if err != nil {
		return 0, 0
	}
	return cfg.Width, cfg.Height
}

// groupPhotos packs the day into as few sheets as fit: one image when the
// day has up to sheetCols*sheetRows photos, more only when it does not.
// A sheet is an overview, so it is filled before another is started.
