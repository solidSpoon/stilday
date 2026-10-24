package main

import (
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/jpeg"
	"os"
	"path/filepath"
	"time"

	xdraw "golang.org/x/image/draw"
	"golang.org/x/image/font"
	"golang.org/x/image/font/gofont/gomono"
	"golang.org/x/image/font/gofont/goregular"
	"golang.org/x/image/font/opentype"
	fixedp "golang.org/x/image/math/fixed"
)

// groupPhotos packs the day into as few sheets as fit: one image when the
// day has up to sheetCols*sheetRows photos, more only when it does not.
// A sheet is an overview, so it is filled before another is started.
func groupPhotos(photos []photo, size int) [][]photo {
	if size <= 0 {
		size = sheetCols * sheetRows
	}
	var groups [][]photo
	for len(photos) > 0 {
		n := min(size, len(photos))
		groups = append(groups, photos[:n])
		photos = photos[n:]
	}
	return groups
}

// composeSheet draws numbered thumbnails on one contact sheet.

// composeSheet draws numbered thumbnails on one contact sheet.
func composeSheet(day time.Time, sh sheet, total int, photos []photo, dst string) error {
	rows := (len(photos) + sheetCols - 1) / sheetCols
	width := sheetPad*2 + sheetCols*cellW + (sheetCols-1)*cellGap
	height := sheetHead + sheetPad + rows*(cellH+labelH) + max(rows-1, 0)*cellGap + sheetPad
	img := image.NewRGBA(image.Rect(0, 0, width, height))

	// A light sheet reads well in both light and dark themes, and keeps
	// the thumbnails themselves as the only strong colors.
	draw.Draw(img, img.Bounds(), &image.Uniform{color.RGBA{0xF6, 0xF6, 0xF7, 0xFF}}, image.Point{}, draw.Src)

	regularFont, err := opentype.Parse(goregular.TTF)
	if err != nil {
		return err
	}
	monoFont, err := opentype.Parse(gomono.TTF)
	if err != nil {
		return err
	}
	regular, err := opentype.NewFace(regularFont, &opentype.FaceOptions{Size: 22, DPI: 72, Hinting: font.HintingFull})
	if err != nil {
		return err
	}
	defer regular.Close()
	small, err := opentype.NewFace(regularFont, &opentype.FaceOptions{Size: 15, DPI: 72, Hinting: font.HintingFull})
	if err != nil {
		return err
	}
	defer small.Close()
	mono, err := opentype.NewFace(monoFont, &opentype.FaceOptions{Size: 14, DPI: 72, Hinting: font.HintingFull})
	if err != nil {
		return err
	}
	defer mono.Close()

	ink := color.RGBA{0x18, 0x18, 0x1B, 0xFF}
	muted := color.RGBA{0x71, 0x71, 0x7A, 0xFF}

	drawText(img, regular, fmt.Sprintf("%s  ·  Sheet %d/%d", day.Format("2006-01-02"), sh.Index, total), sheetPad, 36, ink)
	from, _ := time.Parse(time.RFC3339, sh.From)
	to, _ := time.Parse(time.RFC3339, sh.To)
	drawText(img, small, fmt.Sprintf("%s–%s  ·  #%d–#%d", from.Format("15:04"), to.Format("15:04"), sh.First, sh.Last), sheetPad, 54, muted)

	for i, p := range photos {
		col, row := i%sheetCols, i/sheetCols
		x := sheetPad + col*(cellW+cellGap)
		y := sheetHead + sheetPad + row*(cellH+labelH+cellGap)
		rect := image.Rect(x, y, x+cellW, y+cellH)
		f, err := os.Open(filepath.Join(filepath.Dir(filepath.Dir(dst)), p.Preview))
		if err == nil {
			src, _, derr := image.Decode(f)
			f.Close()
			if derr == nil {
				drawCover(img, rect, src)
			}
		}
		// The label bar sits under the thumbnail, so the number never
		// covers a face.
		label := image.Rect(x, y+cellH, x+cellW, y+cellH+labelH)
		draw.Draw(img, label, &image.Uniform{color.RGBA{0x1C, 0x1C, 0x1F, 0xFF}}, image.Point{}, draw.Src)
		at, _ := time.Parse(time.RFC3339, p.CapturedAt)
		drawText(img, mono, fmt.Sprintf("#%d  %s", p.Index, at.Format("15:04")), x+8, y+cellH+16, color.RGBA{0xF4, 0xF4, 0xF5, 0xFF})
	}

	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer out.Close()
	return jpeg.Encode(out, img, &jpeg.Options{Quality: 86})
}

// drawCover crops a source image to the cell's aspect and scales it in.

// drawCover crops a source image to the cell's aspect and scales it in.
func drawCover(dst draw.Image, r image.Rectangle, src image.Image) {
	sb := src.Bounds()
	sw, sh := sb.Dx(), sb.Dy()
	if sw == 0 || sh == 0 {
		return
	}
	cellAspect := float64(r.Dx()) / float64(r.Dy())
	var crop image.Rectangle
	if float64(sw)/float64(sh) > cellAspect {
		// Too wide: crop the sides.
		w := int(float64(sh) * cellAspect)
		x := sb.Min.X + (sw-w)/2
		crop = image.Rect(x, sb.Min.Y, x+w, sb.Max.Y)
	} else {
		h := int(float64(sw) / cellAspect)
		y := sb.Min.Y + (sh-h)/2
		crop = image.Rect(sb.Min.X, y, sb.Max.X, y+h)
	}
	xdraw.ApproxBiLinear.Scale(dst, r, src, crop, draw.Src, nil)
}

func drawText(dst draw.Image, face font.Face, s string, x, y int, c color.Color) {
	d := font.Drawer{
		Dst:  dst,
		Src:  image.NewUniform(c),
		Face: face,
		Dot:  fixedp.P(x, y),
	}
	d.DrawString(s)
}
