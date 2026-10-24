package main

import (
	"os"
	"path/filepath"
	"time"
)

// reloadCalendar scans the storage tree for days with work or artifacts,
// and lays out the months newest first.
func (a *app) reloadCalendar() {
	a.dataDays = map[string]bool{}
	a.calendarChanged = map[string]bool{}
	if a.cfg.configured() {
		entries, err := a.store.LoadAll()
		a.fileErr = err
		for _, e := range entries {
			if key := e.DayKey(); key != "" {
				a.dataDays[key] = true
			}
		}
		a.scanRawDays(a.cfg.workRoot(), a.dataDays)
		a.scanChangedDays(a.cfg.workRoot(), a.calendarChanged)
	}
	a.rebuildCalendarRows()
}

// scanWorkDays visits work/YYYY/MM/DD folders.
func scanWorkDays(root string, fn func(key, dir string)) {
	if root == "" {
		return
	}
	years, _ := os.ReadDir(root)
	for _, y := range years {
		if !y.IsDir() || !allDigits(y.Name(), 4) {
			continue
		}
		months, _ := os.ReadDir(filepath.Join(root, y.Name()))
		for _, mo := range months {
			if !mo.IsDir() || !allDigits(mo.Name(), 2) {
				continue
			}
			days, _ := os.ReadDir(filepath.Join(root, y.Name(), mo.Name()))
			for _, d := range days {
				if !d.IsDir() || !allDigits(d.Name(), 2) {
					continue
				}
				dir := filepath.Join(root, y.Name(), mo.Name(), d.Name())
				fn(y.Name()+"-"+mo.Name()+"-"+d.Name(), dir)
			}
		}
	}
}

func (a *app) scanRawDays(root string, out map[string]bool) {
	scanWorkDays(root, func(key, dir string) {
		// A plugin's fingerprint is its own answer to "did I collect
		// anything this day": the folder hash is empty when nothing is
		// there, so one state read per day answers for every plugin.
		state := loadCollectorDay(dir)
		for _, p := range a.collectorList {
			if state.Items[p.ID()].Fingerprint != "" {
				out[key] = true
				return
			}
		}
	})
}

func (a *app) scanChangedDays(root string, out map[string]bool) {
	scanWorkDays(root, func(key, dir string) {
		state := loadCollectorDay(dir)
		for _, p := range a.collectorList {
			if state.Items[p.ID()].pendingAnalysis() {
				out[key] = true
				return
			}
		}
	})
}

// refreshChangedDays recomputes the days that need analysis, for the
// calendar dots and the task rail.
func (a *app) refreshChangedDays() {
	a.calendarChanged = map[string]bool{}
	if a.cfg.configured() {
		a.scanChangedDays(a.cfg.workRoot(), a.calendarChanged)
	}
	a.syncAnalysisTasks()
}

// calendarStartYear is the oldest year the sidebar shows. Past years are
// collapsed into one header each; the current year opens on this month.
const calendarStartYear = 2020

// rebuildCalendarRows builds the sidebar rows from the current month back
// to 2020, including one collapsible header per year.
func (a *app) rebuildCalendarRows() {
	now := a.clockNow()
	a.months = nil
	for y := now.Year(); y >= calendarStartYear; y-- {
		// The current year is named, with its counts, in the fixed header;
		// older years keep a header of their own to fold them away.
		if y != now.Year() {
			a.months = append(a.months, month{Year: y})
		}
		start := 12
		if y == now.Year() {
			start = int(now.Month())
		}
		for m := start; m >= 1; m-- {
			a.months = append(a.months, month{Year: y, Month: time.Month(m)})
		}
	}
}

func allDigits(s string, n int) bool {
	if len(s) != n {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}
