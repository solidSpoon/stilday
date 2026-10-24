package main

import "github.com/egoist/mygo/ui"

// kindColor styles a card by what kind of event it is. The kinds are the ones
// day_log_schema.go declares; kinds from before that list are grey, so they read
// as leftovers instead of competing with the current six.
func kindColor(kind string, t *ui.Theme) ui.Color {
	switch kind {
	case "observation":
		return t.Accent
	case "plan":
		return ui.Oklch(0.68, 0.13, 330)
	case "activity":
		return ui.Oklch(0.70, 0.14, 155)
	case "interest":
		return ui.Oklch(0.68, 0.14, 280)
	case "food":
		return ui.Oklch(0.72, 0.13, 60)
	case "health":
		return ui.Oklch(0.70, 0.11, 195)
	}
	return t.TextMuted
}

// kindLabel names a card's kind in the interface.
func kindLabel(kind string) string {
	switch kind {
	case "plan":
		return "日程"
	case "activity":
		return "活动"
	case "interest":
		return "兴趣"
	case "food":
		return "饮食"
	case "health":
		return "健康"
	case "observation":
		return "观察"
	}
	if kind == "" {
		return "事件"
	}
	return kind
}
