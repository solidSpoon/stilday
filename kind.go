package main

import "github.com/egoist/mygo/ui"

// kindColor styles a card by what kind of event it is.
func kindColor(kind string, t *ui.Theme) ui.Color {
	switch kind {
	case "place":
		return ui.Oklch(0.66, 0.14, 250)
	case "activity":
		return ui.Oklch(0.70, 0.14, 155)
	case "person":
		return ui.Oklch(0.68, 0.14, 330)
	case "task":
		return ui.Oklch(0.70, 0.12, 75)
	case "mood":
		return ui.Oklch(0.66, 0.14, 305)
	case "decision":
		return ui.Oklch(0.70, 0.14, 30)
	case "food":
		return ui.Oklch(0.72, 0.13, 60)
	case "health":
		return ui.Oklch(0.70, 0.11, 195)
	}
	return t.Accent
}

// kindLabel names a card's kind in the interface.
func kindLabel(kind string) string {
	switch kind {
	case "place":
		return "地点"
	case "activity":
		return "活动"
	case "person":
		return "人物"
	case "task":
		return "任务"
	case "mood":
		return "心情"
	case "decision":
		return "决定"
	case "observation":
		return "观察"
	case "food":
		return "饮食"
	case "health":
		return "健康"
	}
	if kind == "" {
		return "事件"
	}
	return kind
}
