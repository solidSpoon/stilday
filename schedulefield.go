package main

import (
	"github.com/egoist/mygo/ui"
)

// scheduleDays edits which days a scheduled task runs on, as a graphical
// cron: every day, the weekdays, or days picked by hand. custom is kept
// next to the days because a custom choice can be the same five days as
// the weekdays preset; without it, "自定义" would snap back to "工作日".
func scheduleDays(c *ui.Context, label string, days *[]int, custom *bool) bool {
	t := c.Theme()
	mode := 0
	switch {
	case *custom:
		mode = 2
	case len(*days) == 0:
		mode = 0
	default:
		mode = 1
	}
	changed := false
	if ui.Segmented(c, &mode, "每天", "工作日", "自定义").Label(label).Changed() {
		changed = true
	}
	switch mode {
	case 0:
		*days = nil
		*custom = false
	case 1:
		*days = []int{1, 2, 3, 4, 5}
		*custom = false
	case 2:
		*custom = true
		if len(*days) == 0 {
			// A fresh custom choice starts from the weekdays, and that
			// default must be saved too, not just shown.
			*days = []int{1, 2, 3, 4, 5}
			changed = true
		}
	}
	if mode == 2 {
		names := []string{"一", "二", "三", "四", "五", "六", "日"}
		for i, name := range names {
			d := i + 1
			on := hasDay(*days, d)
			b := ui.Button(c, name).FontSize(10).Padding(2, 6).Radius(5)
			if on {
				b.Background(t.Accent).TextColor(t.AccentText)
			}
			if b.Clicked() {
				*days = toggleDay(*days, d)
				changed = true
			}
		}
	}
	return changed
}

// scheduleTimes edits the clocks a day runs at: one or more, each with a
// remove button and a + for another.
func scheduleTimes(c *ui.Context, clock *string, more *[]string) bool {
	changed := false
	times := append([]string{""}, (*more)...)
	times[0] = *clock
	if times[0] == "" {
		times = []string{"22:00"}
	}
	kept := times[:0]
	for _, at := range times {
		at := at
		if ui.TextInput(c, &at).Width(64).Placeholder("21:00").Changed() {
			changed = true
		}
		if len(times) > 1 {
			if ui.Button(c, "×").FontSize(10).Padding(1, 5).Radius(5).Clicked() {
				changed = true
				continue
			}
		}
		kept = append(kept, at)
	}
	times = kept
	if ui.Button(c, "+").FontSize(11).Padding(1, 6).Radius(5).Clicked() {
		last := "22:00"
		if len(times) > 0 {
			last = times[len(times)-1]
		}
		times = append(times, last)
		changed = true
	}
	if len(times) == 0 {
		times = []string{*clock}
	}
	*clock = times[0]
	*more = append([]string(nil), times[1:]...)
	return changed
}
