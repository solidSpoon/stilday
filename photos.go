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

	// A day that keeps what it has starts from its manifest: the photos
	// the library no longer has stay, and only new ones are added.
	var kept *manifest
	if o.KeepRemoved {
		if old, err := loadManifest(dayDir); err == nil && old.Day == dayKey(o.Day) {
			kept = old
		}
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
			if kept != nil && len(kept.Photos) > 0 {
				return kept, nil
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
	sampled := len(selected) > o.MaxPhotos
	if sampled {
		even := make([]candidate, 0, o.MaxPhotos)
		step := float64(len(selected)) / float64(o.MaxPhotos)
		for i := 0; i < o.MaxPhotos; i++ {
			even = append(even, selected[int(float64(i)*step)])
		}
		selected = even
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
	// A re-run replaces the day's generated files, unless the day keeps
	// what it has: then the previews stay, and only the sheets are drawn
	// again. The old photos/ folder (original copies) is not part of the
	// layout either way.
	drop := []string{"photos", "sheets"}
	if o.KeepRemoved && kept != nil {
		m.Photos = append(m.Photos, kept.Photos...)
	} else if !o.KeepRemoved {
		drop = append(drop, "previews", "thumbs")
	}
	for _, d := range drop {
		_ = os.RemoveAll(filepath.Join(dayDir, d))
	}
	for _, d := range []string{"previews", "thumbs", "sheets"} {
		if err := os.MkdirAll(filepath.Join(dayDir, d), 0o755); err != nil {
			return nil, err
		}
	}
	// New photos are written under temporary numbers, after the ones the
	// day already has, and take their place in the order once the day is
	// complete (see renumberPhotos).
	next := 1
	for _, p := range m.Photos {
		next = max(next, p.Index+1)
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
	keptCount, matched := len(m.Photos), 0
	for i, c := range selected {
		var id string
		if c.pk != nil {
			id = c.pk.ID
		}
		if keptCount > 0 {
			if k := findPhoto(m.Photos[:keptCount], c.at, id, c.w, c.h); k >= 0 {
				// A photo collected before the identifier was stored gets
				// it now, so the next run recognises it exactly.
				if id != "" && m.Photos[k].SourceID == "" {
					m.Photos[k].SourceID = id
				}
				matched++
				continue
			}
		}
		// The number is the file's name: a photo that cannot be rendered
		// takes no number, so the labels, the manifest and the two
		// folders never drift apart.
		idx := next
		ext := strings.ToLower(filepath.Ext(c.path))
		if ext == "" {
			ext = ".jpg"
		}
		preview := previewName(idx)
		if err := makePreview(c.path, filepath.Join(dayDir, preview), 1600); err != nil {
			_ = os.Remove(filepath.Join(dayDir, preview))
			continue
		}
		thumb := thumbName(idx)
		if err := makePreview(filepath.Join(dayDir, preview), filepath.Join(dayDir, thumb), 360); err != nil {
			_ = os.Remove(filepath.Join(dayDir, preview))
			_ = os.Remove(filepath.Join(dayDir, thumb))
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
			Index:      idx,
			SourceID:   id,
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
		next++
		if i%5 == 0 {
			progress("正在生成预览", i+1, len(selected))
		}
	}
	// The day in capture order, its files named after their place. A photo
	// added later pushes the others along, so the label on the sheet, the
	// manifest's index and the file name are always one number.
	if err := renumberPhotos(dayDir, m.Photos); err != nil {
		return nil, err
	}
	if gone := keptCount - matched; o.KeepRemoved && gone > 0 && !sampled {
		note := fmt.Sprintf("有 %d 张照片已不在相册里，按设置保留。", gone)
		if strings.TrimSpace(m.PhotoNote) != "" {
			m.PhotoNote += "；" + note
		} else {
			m.PhotoNote = note
		}
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

// findPhoto is the index of the photo the day already collected, or -1.
// The PhotoKit asset's identifier makes it exact; without one, the capture
// second and the original size tell two photos apart, as well as they can.
func findPhoto(photos []photo, at time.Time, id string, w, h int) int {
	for i, p := range photos {
		if id != "" && p.SourceID != "" {
			if p.SourceID == id {
				return i
			}
			continue
		}
		if p.CapturedAt != at.Format(time.RFC3339) {
			continue
		}
		if p.Width > 0 && w > 0 && (p.Width != w || p.Height != h) {
			continue
		}
		return i
	}
	return -1
}

// previewName and thumbName are the files of the photo in a place of the
// day, counting from 1.
func previewName(i int) string {
	return filepath.ToSlash(filepath.Join("previews", fmt.Sprintf("%03d.jpg", i)))
}

func thumbName(i int) string {
	return filepath.ToSlash(filepath.Join("thumbs", fmt.Sprintf("%03d.jpg", i)))
}

// photoTime is a photo's capture time, zero when it has none.
func photoTime(p photo) time.Time {
	at, _ := time.Parse(time.RFC3339, p.CapturedAt)
	return at
}

// renumberPhotos puts the day in capture order and renames the pictures to
// the numbers of their places. Nothing is renamed when a photo keeps its
// place, so a day that gains nothing is not touched.
func renumberPhotos(dayDir string, photos []photo) error {
	sort.SliceStable(photos, func(i, j int) bool {
		return photoTime(photos[i]).Before(photoTime(photos[j]))
	})
	type move struct{ from, to string }
	var moves []move
	for i := range photos {
		p := &photos[i]
		preview, thumb := previewName(i+1), thumbName(i+1)
		if p.Index == i+1 && p.Preview == preview && p.Thumb == thumb {
			p.Read = preview
			continue
		}
		moves = append(moves,
			move{from: p.Preview, to: preview},
			move{from: p.Thumb, to: thumb},
		)
		p.Index = i + 1
		p.Preview, p.Read, p.Thumb = preview, preview, thumb
	}
	if len(moves) == 0 {
		return nil
	}
	// Every moving file leaves its old name for a temporary one first:
	// 005.jpg may be going to 001.jpg while 001.jpg is going to 006.jpg.
	temps := make([]string, len(moves))
	for i, m := range moves {
		temps[i] = filepath.ToSlash(filepath.Join(filepath.Dir(m.from), ".renumber-"+filepath.Base(m.from)))
		if err := os.Rename(filepath.Join(dayDir, m.from), filepath.Join(dayDir, temps[i])); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	for i, m := range moves {
		if err := os.Rename(filepath.Join(dayDir, temps[i]), filepath.Join(dayDir, m.to)); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	return nil
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
