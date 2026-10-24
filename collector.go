package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"
)

// collector is one source of a day's raw material. A plugin owns its own
// settings and the fingerprint of what it collected: the app asks for
// both, and keeps them where the plugin says.
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
	// View draws the plugin's settings card, editing its own settings.
	View(c *ui.Context, view collectorView)

	// LoadSettings reads the plugin's settings from its own folder under
	// root, SaveSettings writes them back, and SettingsChanged reports an
	// edit that has not been saved yet.
	LoadSettings(root string) error
	SaveSettings() error
	SettingsChanged() bool

	// Schedule is when the plugin runs by itself, from its settings.
	Schedule() *collectorSchedule

	// Fingerprint says whether the day's material changed since the last
	// run; the default hashes the plugin's folder and its seed.
	Fingerprint(env collectorEnv) string

	// KeepsFolder keeps what its folder already holds instead of having
	// the app clear it before Run.
	KeepsFolder() bool
}

// collectorEnv is one day's resolved collector input: the day, its folder,
// the plugin's own folder, and the sources a plugin may read. The app
// derives it on the UI thread before starting the background run.
type collectorEnv struct {
	Day    time.Time
	Dir    string // the day's folder
	Folder string // the plugin's folder in it
	// StateDir is the plugin's own folder under the storage root, where
	// its settings and any state only it understands live.
	StateDir string
	Photos   photoInput
}

// photoSource is the day's photo window in the library.
type photoInput struct {
	Library   string
	Start     time.Time
	End       time.Time
	MaxPhotos int
}

// collectorView is the day's state a collector's settings card may read.
type collectorView struct {
	Manifest *manifest
}

// collectorSchedule is one plugin's daily run: whether it runs by itself,
// and at what time.
type collectorSchedule struct {
	Enabled bool   `json:"enabled"`
	Time    string `json:"time,omitempty"` // HH:MM
}

// pluginDir is the folder a plugin owns under the storage root: its
// settings, and whatever state only it understands.
func pluginDir(root, id string) string {
	return filepath.Join(root, "plugins", id)
}

// fileSettings is a plugin's settings file. The plugin holds its defaults
// in value before loading, so a file written by another build keeps the
// fields it did not have, and a file that is not there starts from them.
type fileSettings[T any] struct {
	id       string
	dir      string
	path     string
	defaults T
	value    T
	saved    T
}

func newFileSettings[T any](id string, defaults T) fileSettings[T] {
	return fileSettings[T]{id: id, defaults: defaults, value: defaults, saved: defaults}
}

// LoadSettings reads <root>/plugins/<id>/settings.json over the defaults.
func (s *fileSettings[T]) LoadSettings(root string) error {
	s.dir = pluginDir(root, s.id)
	s.path = filepath.Join(s.dir, "settings.json")
	// Start from the defaults, so switching storage roots does not carry
	// the previous root's values, and a field a file lacks keeps its own.
	s.value = s.defaults
	b, err := os.ReadFile(s.path)
	if err != nil {
		if os.IsNotExist(err) {
			s.saved = s.value
			return nil
		}
		return err
	}
	if err := json.Unmarshal(b, &s.value); err != nil {
		s.saved = s.value
		return fmt.Errorf("%s 的设置读不了：%w", s.id, err)
	}
	s.saved = s.value
	return nil
}

// SaveSettings writes the settings atomically, so a crash keeps the last
// good file.
func (s *fileSettings[T]) SaveSettings() error {
	if s.path == "" {
		return nil
	}
	b, err := json.MarshalIndent(s.value, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(s.dir, 0o755); err != nil {
		return err
	}
	if err := writeFileAtomic(s.path, b); err != nil {
		return err
	}
	s.saved = s.value
	return nil
}

// SettingsChanged reports an edit the settings page has not saved.
func (s *fileSettings[T]) SettingsChanged() bool {
	return !reflect.DeepEqual(s.value, s.saved)
}

// writeFileAtomic writes through a temporary file, so a reader sees the
// old content or the new, never half of either.
func writeFileAtomic(path string, b []byte) error {
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// loadCollectorSettings reads each plugin's settings from the storage
// root, and moves the old shared file into them, once.
func (a *app) loadCollectorSettings() {
	root := a.cfg.storageRoot()
	for _, p := range a.collectorList {
		if err := p.LoadSettings(root); err != nil {
			a.fileErr = err
		}
	}
	migrateLegacySettings(root, a.collectorList)
}

// legacyCollectorSettings is the shape of collectors.json, the one file
// the plugins shared before each kept its own. It is read once, to move
// what it says into the plugin's folder.
type legacyCollectorSettings struct {
	Photos struct {
		Days        int   `json:"days"`
		KeepRemoved *bool `json:"keep_removed"`
	} `json:"photos"`
	Schedules map[string]*collectorSchedule `json:"schedules"`
}

// legacySettingsAdopter is a plugin that knows what the old shared file
// said about it.
type legacySettingsAdopter interface {
	adoptLegacySettings(old legacyCollectorSettings)
}

// migrateLegacySettings moves collectors.json into the plugins' own
// folders, once, and keeps the old file beside it as .migrated.
func migrateLegacySettings(root string, plugins []collector) {
	path := filepath.Join(root, "collectors.json")
	b, err := os.ReadFile(path)
	if err != nil {
		return
	}
	var old legacyCollectorSettings
	if err := json.Unmarshal(b, &old); err != nil {
		return
	}
	for _, p := range plugins {
		if m, ok := p.(legacySettingsAdopter); ok {
			m.adoptLegacySettings(old)
		}
	}
	_ = os.Rename(path, path+".migrated")
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
	return []collector{newPhotosCollector()}
}

func (a *app) collectorByID(id string) collector {
	for _, p := range a.collectorList {
		if p.ID() == id {
			return p
		}
	}
	return nil
}

// folderFingerprint is the default Fingerprint: the plugin's folder hashed
// with the seed of the config that is not in it, so a moved library or a
// changed source counts as a change.
type folderFingerprint struct {
	id   string
	seed func() string
}

func (f folderFingerprint) Fingerprint(env collectorEnv) string {
	seed := ""
	if f.seed != nil {
		seed = f.seed()
	}
	fp, err := fingerprintFolder(env.Dir, env.Folder, f.id, seed)
	if err != nil {
		return ""
	}
	return fp
}

// fingerprintFolder hashes the plugin's raw folder and its seed. Generated
// previews, sheets and status live outside the folder, so they do not make
// an unchanged day look changed. A folder that cannot be read has no
// fingerprint, which no comparison treats as a hit.
func fingerprintFolder(dayDir, folder, id, seed string) (string, error) {
	h := sha256.New()
	_, _ = io.WriteString(h, id+"\x00"+seed+"\x00")
	root := filepath.Join(dayDir, folder)
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
