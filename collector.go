package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"
)

// collector is one source of a day's raw material. The app clears Folder
// before Run, and the description and folder go into the model's prompt.
//
// A collector receives values, not the app: its work runs off the UI
// thread and must not reach window or page state.
type collector interface {
	ID() string
	Name() string
	Description() string
	Folder() string
	Icon() *ui.SVG
	Manifest() string // relative to the day folder; "" for the day manifest itself
	// Run writes the day's material into env.Dir.
	Run(env collectorEnv, progress func(stage string, done, total int)) error
	// View draws the collector's settings card. It may edit view.Photos.
	View(c *ui.Context, view collectorView)
	// FingerprintSeed is the config that changes the raw output but is not
	// in Folder, so a moved library or source counts as a change.
	FingerprintSeed() string
}

// collectorEnv is one day's resolved collector input: the day, its folder,
// and the sources a plugin may read. The app derives it from the config on
// the UI thread before starting the background run.
type collectorEnv struct {
	Day    time.Time
	Dir    string
	Photos photoInput
}

// photoSource is the day's photo window in the library.
type photoInput struct {
	Library   string
	Start     time.Time
	End       time.Time
	MaxPhotos int
}

// collectorView is the global state a collector's settings card may read
// and edit.
type collectorView struct {
	Photos   *photosSettings
	Manifest *manifest
}

// photosSettings is the photos collector's global configuration.
type photosSettings struct {
	Days int `json:"days"` // sync this many recent days
}

// collectorSchedule is one plugin's daily run: whether it runs by itself,
// and at what time.
type collectorSchedule struct {
	Enabled bool   `json:"enabled"`
	Time    string `json:"time,omitempty"` // HH:MM
}

// collectorSettings is the global collector configuration, stored at the
// storage root rather than in one day.
type collectorSettings struct {
	Photos photosSettings `json:"photos"`
	// Schedules are pointers so a control can bind to the stored value
	// directly; the JSON shape is unchanged.
	Schedules map[string]*collectorSchedule `json:"schedules,omitempty"`
}

func defaultCollectorSettings() collectorSettings {
	return collectorSettings{
		Photos: photosSettings{Days: 1},
		Schedules: map[string]*collectorSchedule{
			photosCollectorID: {Enabled: true, Time: "22:00"},
		},
	}
}

func collectorSettingsPath(root string) string {
	return filepath.Join(root, "collectors.json")
}

func loadCollectorSettings(root string) collectorSettings {
	out := defaultCollectorSettings()
	if root == "" {
		return out
	}
	b, err := os.ReadFile(collectorSettingsPath(root))
	if err != nil {
		return out
	}
	_ = json.Unmarshal(b, &out)
	if out.Schedules == nil || len(out.Schedules) == 0 {
		out.Schedules = defaultCollectorSettings().Schedules
	}
	for id, s := range out.Schedules {
		if s == nil {
			s = &collectorSchedule{}
			out.Schedules[id] = s
		}
		if strings.TrimSpace(s.Time) == "" {
			s.Time = "22:00"
		}
	}
	if out.Photos.Days < 1 || out.Photos.Days > 365 {
		out.Photos.Days = 1
	}
	return out
}

func saveCollectorSettings(root string, s collectorSettings) error {
	if root == "" {
		return nil
	}
	if err := os.MkdirAll(root, 0o755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(collectorSettingsPath(root), b, 0o644)
}

// collectorRun is the status of one collector for one day.
type collectorRun struct {
	Status              string `json:"status"` // running, done, error
	StartedAt           string `json:"started_at,omitempty"`
	AutoAt              string `json:"auto_at,omitempty"`
	FinishedAt          string `json:"finished_at,omitempty"`
	Error               string `json:"error,omitempty"`
	Fingerprint         string `json:"fingerprint,omitempty"`
	Changed             bool   `json:"changed,omitempty"`
	AnalyzedFingerprint string `json:"analyzed_fingerprint,omitempty"`
	AnalyzedAt          string `json:"analyzed_at,omitempty"`
}

// pendingAnalysis reports whether this collector's raw material changed
// since the AI last consumed it. A day with no fingerprint on either side
// has never been collected and is not pending.
func (r collectorRun) pendingAnalysis() bool {
	return (r.Fingerprint != "" || r.AnalyzedFingerprint != "") &&
		r.Fingerprint != r.AnalyzedFingerprint
}

// recordFingerprint stores a finished run's fingerprint and whether the
// material moved since the previous run.
func (r *collectorRun) recordFingerprint(fp string) {
	r.Changed = fp != "" && r.Fingerprint != fp
	r.Fingerprint = fp
}

// collectorDay is the day's collector state, kept beside the materials.
type collectorDay struct {
	Day    string                  `json:"day"`
	AutoAt string                  `json:"auto_at,omitempty"`
	Items  map[string]collectorRun `json:"items,omitempty"`
}

// collectorRecord is the public metadata of one collector in manifest.json.
type collectorRecord struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	Folder      string `json:"folder"`
	Manifest    string `json:"manifest,omitempty"`
	Status      string `json:"status,omitempty"`
	UpdatedAt   string `json:"updated_at,omitempty"`
}

// defaultCollectors is the plugin list, in run order.
func defaultCollectors() []collector {
	return []collector{photosCollector{}}
}

func (a *app) collectorByID(id string) collector {
	for _, p := range a.collectorList {
		if p.ID() == id {
			return p
		}
	}
	return nil
}

// fingerprintCollector hashes the collector's raw folder and the config
// that affects it. Generated previews, sheets and status live outside the
// folder, so they do not make an unchanged day look changed.
func fingerprintCollector(dayDir string, p collector) (string, error) {
	h := sha256.New()
	_, _ = io.WriteString(h, p.ID()+"\x00"+p.FingerprintSeed()+"\x00")
	root := filepath.Join(dayDir, p.Folder())
	var files []string
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			if os.IsNotExist(err) {
				return nil
			}
			return err
		}
		if d.IsDir() {
			if path != root && strings.HasPrefix(d.Name(), ".") {
				return filepath.SkipDir
			}
			return nil
		}
		if strings.HasPrefix(d.Name(), ".") {
			return nil
		}
		files = append(files, path)
		return nil
	})
	if err != nil && !os.IsNotExist(err) {
		return "", err
	}
	if len(files) == 0 {
		return "", nil
	}
	sort.Strings(files)
	for _, path := range files {
		rel, err := filepath.Rel(dayDir, path)
		if err != nil {
			return "", err
		}
		fi, err := os.Stat(path)
		if err != nil {
			return "", err
		}
		fmt.Fprintf(h, "%s\x00%d\x00", filepath.ToSlash(rel), fi.Size())
		f, err := os.Open(path)
		if err != nil {
			return "", err
		}
		_, err = io.Copy(h, f)
		f.Close()
		if err != nil {
			return "", err
		}
		h.Write([]byte{0})
	}
	return "sha256:" + hex.EncodeToString(h.Sum(nil)), nil
}

// fingerprintOf is fingerprintCollector without the error: a folder that
// cannot be read has no fingerprint, which no comparison treats as a hit.
func fingerprintOf(dayDir string, p collector) string {
	fp, err := fingerprintCollector(dayDir, p)
	if err != nil {
		return ""
	}
	return fp
}

// collectorStatePath is the per-day collector state file.
func collectorStatePath(dayDir string) string {
	return filepath.Join(dayDir, "collectors.json")
}

func loadCollectorDay(dayDir string) collectorDay {
	out := collectorDay{Items: map[string]collectorRun{}}
	if dayDir == "" {
		return out
	}
	b, err := os.ReadFile(collectorStatePath(dayDir))
	if err != nil {
		return out
	}
	_ = json.Unmarshal(b, &out)
	if out.Items == nil {
		out.Items = map[string]collectorRun{}
	}
	return out
}

func saveCollectorDay(dayDir string, s collectorDay) error {
	if dayDir == "" {
		return nil
	}
	if err := os.MkdirAll(dayDir, 0o755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(collectorStatePath(dayDir), b, 0o644)
}

// syncDayManifest publishes the collectors, their folders and their status
// in the day's manifest.json, which the model can read.
func syncDayManifest(dayDir string, day time.Time, state collectorDay, collectors []collector) {
	if dayDir == "" {
		return
	}
	m, err := loadManifest(dayDir)
	if err != nil {
		m = &manifest{Day: dayKey(day), GeneratedAt: time.Now().Format(time.RFC3339)}
	}
	if m.Day == "" {
		m.Day = dayKey(day)
	}
	m.Collectors = nil
	for _, p := range collectors {
		rec := collectorRecord{
			ID: p.ID(), Name: p.Name(), Description: p.Description(),
			Folder: p.Folder(), Manifest: p.Manifest(), Status: "idle",
		}
		if run, ok := state.Items[p.ID()]; ok {
			rec.Status = run.Status
			rec.UpdatedAt = run.FinishedAt
			if rec.UpdatedAt == "" {
				rec.UpdatedAt = run.StartedAt
			}
		}
		m.Collectors = append(m.Collectors, rec)
	}
	// The manifest is derived data: when this write fails the previous copy
	// stays, and the next run rewrites it.
	_ = writeManifest(dayDir, m)
}

// cloneSchedules snapshots a day's schedules for change detection.
func cloneSchedules(in map[string]*collectorSchedule) map[string]collectorSchedule {
	out := make(map[string]collectorSchedule, len(in))
	for id, s := range in {
		if s != nil {
			out[id] = *s
		}
	}
	return out
}

// schedulesEqual reports whether the live schedules still match a snapshot.
func schedulesEqual(live map[string]*collectorSchedule, want map[string]collectorSchedule) bool {
	if len(live) != len(want) {
		return false
	}
	for id, s := range live {
		got := collectorSchedule{}
		if s != nil {
			got = *s
		}
		if got != want[id] {
			return false
		}
	}
	return true
}
