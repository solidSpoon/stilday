package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"time"
)

// photo is one selected photo of the day, numbered from 1 in capture
// order. The numbers are what the model sees on the contact sheets and
// reads as previews/NNN.jpg.
type photoLocation struct {
	Latitude  float64 `json:"latitude"`
	Longitude float64 `json:"longitude"`
}

// photoMeta is what the Photos library knows beyond pixels: albums, flags,
// camera, place and edit state. The model reads it from manifest.json.

// photoMeta is what the Photos library knows beyond pixels: albums, flags,
// camera, place and edit state. The model reads it from manifest.json.
type photoMeta struct {
	Filename     string         `json:"filename,omitempty"`
	Favorite     bool           `json:"favorite,omitempty"`
	Hidden       bool           `json:"hidden,omitempty"`
	Albums       []string       `json:"albums,omitempty"`
	Location     *photoLocation `json:"location,omitempty"`
	Camera       string         `json:"camera,omitempty"`
	Lens         string         `json:"lens,omitempty"`
	FNumber      string         `json:"f_number,omitempty"`
	Exposure     string         `json:"exposure,omitempty"`
	ISO          int            `json:"iso,omitempty"`
	FocalLength  string         `json:"focal_length,omitempty"`
	Live         bool           `json:"live,omitempty"`
	Screenshot   bool           `json:"screenshot,omitempty"`
	Panorama     bool           `json:"panorama,omitempty"`
	HDR          bool           `json:"hdr,omitempty"`
	Depth        bool           `json:"depth_effect,omitempty"`
	Burst        bool           `json:"burst,omitempty"`
	Adjustments  bool           `json:"adjustments,omitempty"`
	Source       string         `json:"source,omitempty"`
	CapturedFrom string         `json:"captured_from,omitempty"` // exif or photos
	ModifiedAt   string         `json:"modified_at,omitempty"`
}

type photo struct {
	Index      int    `json:"index"`
	Preview    string `json:"preview"` // previews/NNN.jpg, up to 1600px
	Thumb      string `json:"thumb"`   // thumbs/NNN.jpg, up to 360px for the UI
	Read       string `json:"read"`    // the 1600px preview, the only image the read tool needs
	CapturedAt string `json:"captured_at"`
	Sheet      int    `json:"sheet"`            // 1-based
	SheetFile  string `json:"sheet_file"`       // sheets/NN.jpg
	Cell       int    `json:"cell"`             // 1-based, row-major within the sheet
	CellRow    int    `json:"cell_row"`         // 1-based
	CellCol    int    `json:"cell_col"`         // 1-based
	Width      int    `json:"width,omitempty"`  // original width
	Height     int    `json:"height,omitempty"` // original height
	Bytes      int64  `json:"bytes,omitempty"`
	Format     string `json:"format,omitempty"`
	// SourceID is the PhotoKit asset's identifier, which tells the same
	// photo apart across scans; a folder source has none.
	SourceID string     `json:"source_id,omitempty"`
	Meta     *photoMeta `json:"meta,omitempty"`
}

// sheet is one contact sheet: numbered thumbnails in capture order.

// sheet is one contact sheet: numbered thumbnails in capture order.
type sheet struct {
	Index  int         `json:"index"` // 1-based
	File   string      `json:"file"`  // sheets/01.jpg
	From   string      `json:"from"`
	To     string      `json:"to"`
	First  int         `json:"first"`
	Last   int         `json:"last"`
	Photos []int       `json:"photos"`
	Cells  []sheetCell `json:"cells"`
}

// sheetCell is one cell of a contact sheet and the photo in it.

// sheetCell is one cell of a contact sheet and the photo in it.
type sheetCell struct {
	Photo int `json:"photo"`
	Row   int `json:"row"` // 1-based
	Col   int `json:"col"` // 1-based
}

// manifestFile indexes one file in the day folder. previews, thumbs and
// sheets all appear here, with the photo or sheet they belong to, so the
// manifest is the folder's complete inventory.

// manifestFile indexes one file in the day folder. previews, thumbs and
// sheets all appear here, with the photo or sheet they belong to, so the
// manifest is the folder's complete inventory.
type manifestFile struct {
	Path       string `json:"path"`
	Kind       string `json:"kind"` // preview, thumb, sheet
	Photo      int    `json:"photo,omitempty"`
	Sheet      int    `json:"sheet,omitempty"`
	Of         string `json:"of,omitempty"` // preview/thumb: the file they derive from
	Bytes      int64  `json:"bytes,omitempty"`
	Width      int    `json:"width,omitempty"`
	Height     int    `json:"height,omitempty"`
	CapturedAt string `json:"captured_at,omitempty"`
}

// manifest is the day's photo inventory, stored beside the photos so the
// Pi extension and any human can read it.

// manifest is the day's photo inventory, stored beside the photos so the
// Pi extension and any human can read it.
type manifest struct {
	Day         string            `json:"day"`
	GeneratedAt string            `json:"generated_at"`
	Source      string            `json:"source"`
	Window      [2]string         `json:"window"`
	Scanned     int               `json:"scanned"`
	Selected    int               `json:"selected"`
	Photos      []photo           `json:"photos"`
	Sheets      []sheet           `json:"sheets"`
	Files       []manifestFile    `json:"files"`
	Collectors  []collectorRecord `json:"collectors,omitempty"`
	PhotoSource string            `json:"photo_source,omitempty"` // photokit, originals
	PhotoNote   string            `json:"photo_note,omitempty"`   // why it fell back
}

// photoOptions are the day's boundaries and how many photos the sheets show.
type photoOptions struct {
	Source    string
	Day       time.Time
	Start     time.Time
	End       time.Time
	MaxPhotos int
	SheetSize int
	// KeepRemoved keeps the photos the day already has when the library
	// no longer holds them.
	KeepRemoved bool
}

const (
	sheetCols = 6
	sheetRows = 5
	cellW     = 300
	cellH     = 225
	labelH    = 22
	cellGap   = 8
	sheetPad  = 24
	sheetHead = 58
)

// preparePhotos reads the day's photos, makes JPEG previews and thumbs,
// arranges numbered contact sheets, and writes manifest.json. It keeps no
// original copies in the day folder.
//
// progress is called from the preparing goroutine; callers marshal it to
// the UI thread themselves.

func (m photoMeta) empty() bool {
	return !m.Favorite && !m.Hidden && len(m.Albums) == 0 && m.Location == nil &&
		m.Filename == "" && m.Camera == "" && m.Lens == "" && m.FNumber == "" &&
		m.Exposure == "" && m.ISO == 0 && m.FocalLength == "" && !m.Live &&
		!m.Screenshot && !m.Panorama && !m.HDR && !m.Depth && !m.Burst &&
		!m.Adjustments && m.Source == "" && m.CapturedFrom == "" && m.ModifiedAt == ""
}

func (f manifestFile) withSheet(index int) manifestFile {
	f.Sheet = index
	return f
}

// originalSizes reads the source photos' pixel sizes in batches. mdls knows
// HEIC, which Go's decoders do not; fileDimensions is the fallback.

func writeManifest(dayDir string, m *manifest) error {
	if err := os.MkdirAll(dayDir, 0o755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dayDir, "manifest.json"), b, 0o644)
}

func loadManifest(dayDir string) (*manifest, error) {
	b, err := os.ReadFile(filepath.Join(dayDir, "manifest.json"))
	if err != nil {
		return nil, err
	}
	var m manifest
	if err := json.Unmarshal(b, &m); err != nil {
		return nil, err
	}
	return &m, nil
}

func (m *manifest) photo(index int) *photo {
	for i := range m.Photos {
		if m.Photos[i].Index == index {
			return &m.Photos[i]
		}
	}
	return nil
}

// fileEntry stats a derived image for the manifest's flat file list.
func fileEntry(dayDir, rel, kind string, photo int, of, captured string) manifestFile {
	f := manifestFile{Path: rel, Kind: kind, Photo: photo, Of: of, CapturedAt: captured}
	if fi, err := os.Stat(filepath.Join(dayDir, rel)); err == nil {
		f.Bytes = fi.Size()
	}
	f.Width, f.Height = imageSize(filepath.Join(dayDir, rel))
	return f
}
