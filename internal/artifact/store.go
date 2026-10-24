package artifact

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// Store is the artifacts tree: one JSONL file per year under
// <storage>/artifacts/YYYY.jsonl. A line's day says which day's analysis
// wrote it; a re-analysis removes that day's lines from the year file.
type Store struct {
	root string
	mu   sync.Mutex
}

// Open returns the artifacts store rooted at artifactsRoot, folding any
// older per-day files into the year files first.
func Open(artifactsRoot string) *Store {
	s := &Store{root: artifactsRoot}
	s.migrateLegacy()
	return s
}

// File is the year file that holds a day's entries. It is the one place
// that spells the artifact layout, shared with the settings page.
func File(artifactsRoot string, day time.Time) string {
	if artifactsRoot == "" || day.IsZero() {
		return ""
	}
	return filepath.Join(artifactsRoot, day.Format("2006")+".jsonl")
}

// Path returns the year file that holds a day, or "" before storage is
// configured.
func (s *Store) Path(day time.Time) string {
	if s == nil {
		return ""
	}
	return File(s.root, day)
}

func (s *Store) readFile(path, fallbackDay string, fallbackUpdated time.Time) ([]Entry, error) {
	if path == "" {
		return nil, nil
	}
	f, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var out []Entry
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64*1024), 4*1024*1024)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		var r entryRecord
		if json.Unmarshal([]byte(line), &r) != nil {
			continue
		}
		if e, ok := r.entry(fallbackDay, fallbackUpdated); ok {
			out = append(out, e)
		}
	}
	return out, sc.Err()
}

// year reads one year file.
func (s *Store) year(year string) ([]Entry, error) {
	if s == nil || s.root == "" {
		return nil, nil
	}
	return s.readFile(filepath.Join(s.root, year+".jsonl"), "", time.Time{})
}

// Load reads the entries of one analyzed day from the year file.
func (s *Store) Load(day time.Time) ([]Entry, error) {
	all, err := s.year(day.Format("2006"))
	if err != nil {
		return nil, err
	}
	key := DayKey(day)
	out := all[:0]
	for _, e := range all {
		if e.DayKey() == key {
			out = append(out, e)
		}
	}
	return out, nil
}

// Append writes an entry to its analyzed day's year file.
func (s *Store) Append(e Entry) error {
	day := e.OccurredAt
	e.Day = strings.TrimSpace(e.Day)
	if e.Day != "" {
		if t, err := ParseDayKey(e.Day); err == nil {
			day = t
		}
	} else {
		e.Day = DayKey(day)
	}
	path := s.Path(day)
	if path == "" {
		return errors.New("还没有存储目录")
	}
	if e.UpdatedAt.IsZero() {
		e.UpdatedAt = time.Now()
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	if _, err := f.WriteString(e.Line() + "\n"); err != nil {
		return err
	}
	return nil
}

// LoadAll reads every year file, for the global board.
func (s *Store) LoadAll() ([]Entry, error) {
	if s == nil || s.root == "" {
		return nil, nil
	}
	files, _ := filepath.Glob(filepath.Join(s.root, "*.jsonl"))
	var out []Entry
	for _, path := range files {
		entries, err := s.readFile(path, "", time.Time{})
		if err != nil {
			return out, err
		}
		out = append(out, entries...)
	}
	for i := range out {
		out[i].Seq = int64(i + 1)
	}
	return out, nil
}

// ClearDay removes one analyzed day's lines from its year file. The work
// folder and the day's photos stay.
func (s *Store) ClearDay(day time.Time) error {
	path := s.Path(day)
	if path == "" {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	all, err := s.readFile(path, "", time.Time{})
	if err != nil {
		return err
	}
	key := DayKey(day)
	keep := make([]Entry, 0, len(all))
	for _, e := range all {
		if e.DayKey() != key {
			keep = append(keep, e)
		}
	}
	if len(keep) == len(all) {
		return nil
	}
	return s.writeFile(path, keep)
}

func (s *Store) writeFile(path string, entries []Entry) error {
	sort.SliceStable(entries, func(i, j int) bool {
		return entries[i].OccurredAt.Before(entries[j].OccurredAt)
	})
	tmp := path + ".tmp"
	var b strings.Builder
	for _, e := range entries {
		b.WriteString(e.Line())
		b.WriteByte('\n')
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(tmp, []byte(b.String()), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// migrateLegacy folds the old per-day artifacts into the year files.
// Existing year files are merged too, so a half-run migration is safe.
func (s *Store) migrateLegacy() {
	if s == nil || s.root == "" {
		return
	}
	oldFiles, _ := filepath.Glob(filepath.Join(s.root, "*", "*", "*.jsonl"))
	if len(oldFiles) == 0 {
		return
	}
	merged := map[string]Entry{}
	add := func(e Entry) {
		key := fmt.Sprintf("%s\x00%s\x00%s\x00%s", e.Day, e.OccurredAt.Format(time.RFC3339Nano), e.Kind, e.Summary)
		if prev, ok := merged[key]; ok {
			if e.UpdatedAt.After(prev.UpdatedAt) {
				prev.UpdatedAt = e.UpdatedAt
				merged[key] = prev
			}
			return
		}
		merged[key] = e
	}
	years, _ := filepath.Glob(filepath.Join(s.root, "*.jsonl"))
	for _, path := range years {
		es, _ := s.readFile(path, "", time.Time{})
		for _, e := range es {
			add(e)
		}
	}
	for _, path := range oldFiles {
		fi, err := os.Stat(path)
		if err != nil {
			continue
		}
		day := strings.TrimSuffix(filepath.Base(path), ".jsonl")
		es, _ := s.readFile(path, day, fi.ModTime())
		for _, e := range es {
			add(e)
		}
	}
	byYear := map[string][]Entry{}
	for _, e := range merged {
		year := e.OccurredAt.Format("2006")
		if t, err := ParseDayKey(e.Day); err == nil {
			year = t.Format("2006")
		}
		byYear[year] = append(byYear[year], e)
	}
	for year, es := range byYear {
		_ = s.writeFile(filepath.Join(s.root, year+".jsonl"), es)
	}
	for _, path := range oldFiles {
		_ = os.Remove(path)
		_ = os.Remove(filepath.Dir(path))
		_ = os.Remove(filepath.Dir(filepath.Dir(path)))
	}
}
