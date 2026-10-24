package main

import (
	"strings"
	"time"

	"stilday/internal/schedule"
)

// scheduleSpec builds a spec from the edited days and clock.
func scheduleSpec(days []int, clock string) schedule.Spec {
	return schedule.Spec{Days: days, Time: clock}
}

// hasDay reports whether the day list runs on an ISO weekday.
func hasDay(days []int, d int) bool {
	if len(days) == 0 {
		return true
	}
	for _, x := range days {
		if x == d {
			return true
		}
	}
	return false
}

// toggleDay adds or drops one day, keeping the list in ISO order.
func toggleDay(days []int, d int) []int {
	out := make([]int, 0, 7)
	found := false
	for _, x := range days {
		if x == d {
			found = true
			continue
		}
		out = append(out, x)
	}
	if !found {
		out = append(out, d)
	}
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j] < out[j-1]; j-- {
			out[j], out[j-1] = out[j-1], out[j]
		}
	}
	return out
}

// specNext returns the next time a spec runs, strictly after now.
func specNext(now time.Time, spec schedule.Spec) time.Time {
	best := time.Time{}
	for i := 0; i < 9; i++ {
		day := midnight(now).AddDate(0, 0, i)
		if !spec.HasDay(isoWeekday(day)) {
			continue
		}
		for _, clock := range spec.AllTimes() {
			at, err := clockOn(day, clock)
			if err != nil || !at.After(now) {
				continue
			}
			if best.IsZero() || at.Before(best) {
				best = at
			}
		}
		if !best.IsZero() && i >= 1 {
			break
		}
	}
	if best.IsZero() {
		return now.Add(24 * time.Hour)
	}
	return best
}

// specToday returns today's run time when the spec runs today, due already
// or not; ok is false on a day the spec skips.
func specToday(now time.Time, spec schedule.Spec) (time.Time, bool) {
	if !spec.HasDay(isoWeekday(now)) {
		return time.Time{}, false
	}
	var latest, first time.Time
	for _, clock := range spec.AllTimes() {
		at, err := clockOn(midnight(now), clock)
		if err != nil {
			continue
		}
		if !at.After(now) {
			if at.After(latest) {
				latest = at
			}
		} else if first.IsZero() || at.Before(first) {
			first = at
		}
	}
	if !latest.IsZero() {
		return latest, true
	}
	if !first.IsZero() {
		return first, true
	}
	return time.Time{}, false
}

// isoWeekday is time.Weekday in ISO numbering: Monday 1 through Sunday 7.
func isoWeekday(t time.Time) int {
	w := int(t.Weekday())
	if w == 0 {
		return 7
	}
	return w
}

// specText names a spec on a card: when it runs and at what time.
func specText(spec schedule.Spec) string {
	clocks := strings.Join(spec.AllTimes(), ", ")
	switch {
	case spec.EveryDay():
		return "每天 " + clocks
	case spec.Weekdays():
		return "工作日 " + clocks
	}
	names := []string{"", "一", "二", "三", "四", "五", "六", "日"}
	out := "周"
	for i, d := range []int{1, 2, 3, 4, 5, 6, 7} {
		if !spec.HasDay(d) {
			continue
		}
		if i > 0 && out != "周" {
			out += "、"
		}
		out += names[d]
	}
	return out + " " + clocks
}

// specDaysText names the days a spec runs: 每天, 工作日, or 周一、三.
func specDaysText(spec schedule.Spec) string {
	switch {
	case spec.EveryDay():
		return "每天"
	case spec.Weekdays():
		return "工作日"
	}
	names := []string{"", "一", "二", "三", "四", "五", "六", "日"}
	out := "周"
	for d := 1; d <= 7; d++ {
		if !spec.HasDay(d) {
			continue
		}
		if out != "周" {
			out += "、"
		}
		out += names[d]
	}
	return out
}
