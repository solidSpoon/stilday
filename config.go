package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/egoist/mygo"

	"stilday/internal/artifact"
)

// config is what the user chose once. Stilday keeps two kinds of files in
// the storage directory:
//
//	<storage>/work/2026/10/23/
//	<storage>/artifacts/2026.jsonl
//
// The work folder holds the day's photos, contact sheets and chat; the
// artifact is one JSON object per line in the year's file.
type config struct {
	// StorageDir is the root the app writes to.
	StorageDir string `json:"storage_dir"`
	// PhotoStart and PhotoEnd are the time window of the day, "HH:MM".
	PhotoStart string `json:"photo_start"`
	PhotoEnd   string `json:"photo_end"`
	// MaxPhotos caps the photos shown on contact sheets; the rest of the
	// day is sampled evenly.
	MaxPhotos int `json:"max_photos"`
	// Provider and Model select Pi's model. OpenCode Go is the default.
	Provider string `json:"provider"`
	Model    string `json:"model"`
	// CollectTime runs the collectors; AutoTime sends the day's analysis.
	// Analysis waits until every collector for the target day is done.
	CollectTime string `json:"collect_time"`
	AutoTime    string `json:"auto_time"`
	// AutoTimes are further analysis runs on the same day, after AutoTime.
	AutoTimes []string `json:"auto_times,omitempty"`
	// AutoDays limits the daily analysis to ISO weekdays (1 Monday to 7
	// Sunday); empty means every day. AutoCustom says the days were picked
	// by hand rather than the every-day or weekdays preset.
	AutoDays   []int `json:"auto_days,omitempty"`
	AutoCustom bool  `json:"auto_custom,omitempty"`
	// AutoOff holds the daily analysis: the waiting days still queue, but
	// start only when the user runs them by hand.
	AutoOff bool `json:"auto_off,omitempty"`
	// CatchUpMinutes is how late a scheduled job may still run after its
	// time when the app starts; 0 means the default.
	CatchUpMinutes int `json:"catch_up_minutes,omitempty"`
	// MCPEnabled serves the query endpoint other assistants connect to,
	// and MCPPort is the localhost port it listens on.
	MCPEnabled bool   `json:"mcp_enabled"`
	MCPPort    string `json:"mcp_port"`
}

// defaultMaxPhotos caps one day's contact sheets; the rest of the day
// is sampled evenly.
const defaultMaxPhotos = 120

func defaultConfig() config {
	return config{
		PhotoStart:     "00:00",
		PhotoEnd:       "23:59",
		MaxPhotos:      defaultMaxPhotos,
		Provider:       providers[0].ID,
		Model:          providers[0].DefaultModel,
		CollectTime:    "21:00",
		AutoTime:       "22:00",
		CatchUpMinutes: 60,
	}
}

func (c config) configured() bool {
	return strings.TrimSpace(c.StorageDir) != ""
}

// configPath is where the settings live, outside the storage directory:
// the app must find them before it knows where storage is.
func configPath() string {
	return filepath.Join(dataDir(), "config.json")
}

func loadConfig() (config, error) {
	c := defaultConfig()
	b, err := os.ReadFile(configPath())
	if err != nil {
		if os.IsNotExist(err) {
			return c, nil
		}
		return c, err
	}
	if err := json.Unmarshal(b, &c); err != nil {
		return defaultConfig(), err
	}
	return c.withDefaults(), nil
}

// withDefaults fills the fields an older or partial config left out, and
// normalizes the two daily clocks. Every consumer works on a config that
// has been through it, so defaults live here and nowhere else.
func (c config) withDefaults() config {
	if c.PhotoStart == "" {
		c.PhotoStart = "00:00"
	}
	if c.PhotoEnd == "" {
		c.PhotoEnd = "23:59"
	}
	if c.MaxPhotos <= 0 {
		c.MaxPhotos = defaultMaxPhotos
	}
	if c.Provider == "" {
		c.Provider = providers[0].ID
	}
	if c.Model == "" {
		c.Model = providerFor(c.Provider).DefaultModel
	}
	if c.CollectTime == "" {
		// Migrate the old one-time pipeline: the old auto_time was the
		// collection time, and the analysis moves to the next morning.
		if c.AutoTime != "" && c.AutoTime != "10:00" {
			c.CollectTime = c.AutoTime
			c.AutoTime = "10:00"
		} else {
			c.CollectTime = "21:00"
		}
	}
	c.CollectTime = normalizeClock(c.CollectTime)
	if c.AutoTime == "" {
		c.AutoTime = "22:00"
	}
	c.AutoTime = normalizeClock(c.AutoTime)
	if c.CatchUpMinutes <= 0 {
		c.CatchUpMinutes = 60
	}
	if c.CatchUpMinutes > 24*60 {
		c.CatchUpMinutes = 24 * 60
	}
	if strings.TrimSpace(c.MCPPort) == "" {
		c.MCPPort = mcpDefaultPort
	}
	return c
}

// validate reports what still has to be fixed before the config can be
// saved. Call withDefaults first; this only rejects what is still wrong.
func (c config) validate() error {
	if !c.configured() {
		return errors.New("先选一个存储目录")
	}
	if _, _, err := c.photoWindow(time.Now()); err != nil {
		return err
	}
	if _, err := clockOn(time.Now(), c.CollectTime); err != nil {
		return errors.New("自动采集时间要写成 HH:MM")
	}
	if _, err := clockOn(time.Now(), c.AutoTime); err != nil {
		return errors.New("自动分析时间要写成 HH:MM")
	}
	if c.MCPEnabled {
		if n, err := strconv.Atoi(strings.TrimSpace(c.MCPPort)); err != nil || n < 1 || n > 65535 {
			return errors.New("MCP 端口要写成 1–65535 的数字")
		}
	}
	return nil
}

func saveConfig(c config) error {
	if err := os.MkdirAll(dataDir(), 0o755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(configPath(), b, 0o644)
}

// dataDir is Stilday's own folder: ~/.stilday, or $STILDAY_HOME. Settings
// live here; the day's files live in the storage directory.
func dataDir() string {
	if dir := os.Getenv("STILDAY_HOME"); dir != "" {
		return dir
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ".stilday"
	}
	if devApp() {
		return filepath.Join(home, ".stilday-dev")
	}
	return filepath.Join(home, ".stilday")
}

// devApp reports whether this is the dev bundle: it has its own settings
// and suggested storage, so development never touches the real day.
func devApp() bool {
	return mygo.App.Name() == "Stilday Dev"
}

// expandPath turns ~ and ~/ into the home directory.
func expandPath(p string) string {
	p = strings.TrimSpace(p)
	if p == "~" {
		if home, err := os.UserHomeDir(); err == nil {
			return home
		}
	}
	if strings.HasPrefix(p, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, p[2:])
		}
	}
	return p
}

func (c config) storageRoot() string {
	if !c.configured() {
		return ""
	}
	p := expandPath(c.StorageDir)
	if abs, err := filepath.Abs(p); err == nil {
		return abs
	}
	return p
}

// workRoot holds one folder per day, grouped by year and month.
func (c config) workRoot() string {
	if root := c.storageRoot(); root != "" {
		return filepath.Join(root, "work")
	}
	return ""
}

// artifactsRoot holds one JSONL file per year.
func (c config) artifactsRoot() string {
	if root := c.storageRoot(); root != "" {
		return filepath.Join(root, "artifacts")
	}
	return ""
}

// workDayDir is the day's working folder: the photos, sheets and chat.
func (c config) workDayDir(day time.Time) string {
	if root := c.workRoot(); root != "" {
		return filepath.Join(root, day.Format("2006"), day.Format("01"), day.Format("02"))
	}
	return ""
}

// artifactPath is the year's JSONL context file.
func (c config) artifactPath(day time.Time) string {
	return artifact.File(c.artifactsRoot(), day)
}

// prepareStorage creates the two trees.
func (c config) prepareStorage() error {
	if !c.configured() {
		return fmt.Errorf("还没有存储目录")
	}
	for _, dir := range []string{c.workRoot(), c.artifactsRoot()} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}
	return nil
}

// photoWindow returns the part of the day the photos cover.
func (c config) photoWindow(day time.Time) (time.Time, time.Time, error) {
	start, err := parseClock(c.PhotoStart)
	if err != nil {
		return time.Time{}, time.Time{}, fmt.Errorf("照片时段开始: %w", err)
	}
	end, err := parseClock(c.PhotoEnd)
	if err != nil {
		return time.Time{}, time.Time{}, fmt.Errorf("照片时段结束: %w", err)
	}
	midnight := time.Date(day.Year(), day.Month(), day.Day(), 0, 0, 0, 0, day.Location())
	s := midnight.Add(start)
	e := midnight.Add(end)
	if !e.After(s) {
		e = s.Add(24 * time.Hour)
	}
	return s, e, nil
}

// photosLibrary is the macOS Photos library. STILDAY_PHOTOS_LIBRARY
// overrides it, for a library somewhere else and for tests.
func photosLibrary() string {
	if p := os.Getenv("STILDAY_PHOTOS_LIBRARY"); p != "" {
		return expandPath(p)
	}
	return detectPhotosLibrary()
}

func detectPhotosLibrary() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	p := filepath.Join(home, "Pictures", "Photos Library.photoslibrary")
	if st, err := os.Stat(p); err == nil && st.IsDir() {
		return p
	}
	return ""
}

// suggestStorageDir is the default the first run offers.
func suggestStorageDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return "Stilday"
	}
	if devApp() {
		return filepath.Join(home, "Stilday-dev")
	}
	return filepath.Join(home, "Stilday")
}

func shortenPath(p string) string {
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		if strings.HasPrefix(p, home+string(os.PathSeparator)) {
			return "~" + p[len(home):]
		}
	}
	return p
}
