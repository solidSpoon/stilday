package main

import (
	"fmt"
	"strings"

	"github.com/egoist/mygo/ui"
)

// collectorPage is the collectors' daily plan: one listening row per
// plugin, the analysis time above them, and the task rail on the right.
func (a *app) collectorPage(c *ui.Context) {
	if !a.cfg.configured() {
		a.openSettings()
		return
	}
	t := c.Theme()
	subtitle := "插件按各自计划采集，到点分析右侧待分析的天"
	pageShell(c, "采集器", subtitle, func() {
		if a.settingsErr != "" {
			ui.Text(c, a.settingsErr).FontSize(11).TextColor(t.Danger).SingleLine()
		}
	}, func() {
		beforeCfg := a.cfg
		beforePhotos := a.collectorCfg.Photos
		beforeSchedules := cloneSchedules(a.collectorCfg.Schedules)
		ui.Row(c).Fill().Children(func() {
			ui.Scroll(c).Grow(1).MinWidth(0).FillHeight().
				Padding(barHeight+24, 28, 24).ScrollbarInsets(barHeight+8, 0, 0).Gap(16).Children(func() {
				a.collectorAnalysisPlan(c)
				a.collectorPluginList(c)
			})
			a.collectorSidebar(c)
		})
		if a.cfg != beforeCfg {
			a.collectorAppDirty = true
		}
		if a.collectorCfg.Photos != beforePhotos ||
			!schedulesEqual(a.collectorCfg.Schedules, beforeSchedules) {
			a.collectorCfgDirty = true
		}
		switch {
		case a.win == nil:
			// No frame loop (tests): persist now so the result is visible.
			a.flushCollectorPage()
		case a.collectorAppDirty || a.collectorCfgDirty:
			a.scheduleCollectorFlush()
		}
	})
}

// collectorAnalysisPlan is the one system-wide time: when the day's
// pending analysis starts, after the enabled plugins have run.
func (a *app) collectorAnalysisPlan(c *ui.Context) {
	t := c.Theme()
	group(c, func() {
		ui.Row(c).Gap(10).AlignItems(ui.Center).Children(func() {
			groupTitle(c, "每日分析")
			ui.Text(c, "所有启用的插件完成后，到点分析右侧待分析的天。").
				FontSize(11).TextColor(t.TextMuted).Grow(1).MinWidth(0).SingleLine()
			ui.Spacer(c)
			ui.Text(c, "每天").FontSize(11).TextColor(t.TextMuted)
			ui.TextInput(c, &a.cfg.AutoTime).Width(80).Placeholder("10:00").Label("每日分析时间")
		})
	})
}

// collectorPluginList is the plugin list: one card, a row per plugin.
func (a *app) collectorPluginList(c *ui.Context) {
	t := c.Theme()
	ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
		groupTitle(c, "采集插件")
		ui.Text(c, fmt.Sprintf("%d 个", len(a.collectorList))).FontSize(11).TextColor(t.TextMuted)
		ui.Spacer(c)
		ui.Text(c, "启用后按各自时间自动执行").FontSize(11).TextColor(t.TextMuted)
	})
	group(c, func() {
		for i, p := range a.collectorList {
			if i > 0 {
				ui.Divider(c)
			}
			a.collectorRow(c, p)
		}
	})
}

// collectorSidebar is the right rail: only the analysis tasks and their
// clear action live here.
func (a *app) collectorSidebar(c *ui.Context) {
	t := c.Theme()
	ui.Column(c).Width(420).Shrink(0).FillHeight().Background(t.Background).
		BorderWidth(0, 0, 0, 1).BorderColor(hairline(t)).Children(func() {
		ui.Column(c).Padding(barHeight+24, 20, 0, 20).Children(func() {
			ui.Scroll(c).Grow(1).ScrollbarInsets(0, 0, 0).Children(func() {
				a.analysisTasks(c)
			})
		})
	})
}

// saveCollectorPageConfig writes the photo window and the daily analysis
// time edited on this page.
func (a *app) saveCollectorPageConfig() {
	c := a.cfg
	c.AutoTime = normalizeClock(c.AutoTime)
	if _, err := clockOn(a.day, c.AutoTime); err != nil {
		a.settingsErr = "每日分析时间要写成 HH:MM"
		return
	}
	if err := saveConfig(c); err != nil {
		a.settingsErr = "保存设置：" + err.Error()
		return
	}
	a.cfg = c
	a.chat.forcePrep = true
	a.settingsErr = ""
}

// analysisTasks lists the background analysis as it moves: the day being
// analyzed, what waits behind it, what the user cancelled, and what just
// finished. The queue used to be invisible from here.
func (a *app) analysisTasks(c *ui.Context) {
	t := c.Theme()
	pending := a.analysis.Pending()
	running := a.analysis.Running && a.analysis.Current != ""
	group(c, func() {
		ui.Row(c).Gap(4).AlignItems(ui.Center).Children(func() {
			groupTitle(c, "分析任务")
			ui.Spacer(c)
			switch {
			case a.analysis.Paused:
				if ui.PrimaryButton(c, "继续").Clicked() {
					a.analysis.Resume()
				}
			case a.analysis.Running || len(a.analysis.Queue) > 0:
				a.taskIcon(c, iconPause, "暂停全部", "当前这天会跑完，不再开新的一天", a.analysis.Pause)
			case len(pending) > 0:
				if ui.PrimaryButton(c, fmt.Sprintf("分析 %d 天", len(pending))).Clicked() {
					a.analysis.StartPending()
				}
			}
			if len(pending) > 0 || a.analysis.Running || len(a.analysis.Queue) > 0 || len(a.analysis.Cancelled) > 0 {
				a.taskIcon(c, iconRetry, "重试全部", "清掉取消状态，重新排队所有待分析的天", a.analysis.Restart)
			}
			if a.analysis.Running || len(a.analysis.Queue) > 0 || len(pending) > 0 {
				a.taskIcon(c, iconStop, "停止全部", "停止当前这天并取消所有任务", a.analysis.CancelAll)
			}
		})
		if !running && len(pending) == 0 && len(a.analysis.Cancelled) == 0 && len(a.analysis.Done) == 0 {
			ui.Text(c, "没有待分析的任务。").FontSize(11.5).TextColor(t.TextMuted)
			return
		}

		shown := map[string]bool{}
		if running {
			a.analysisRow(c, a.analysis.Current, "分析中", "正在看照片、写事件", "")
			shown[a.analysis.Current] = true
		}
		for i, key := range a.analysis.Queue {
			if shown[key] {
				continue
			}
			if a.analysis.IsCancelled(key) {
				continue
			}
			status, detail := "待分析", ""
			if a.analysis.Running {
				status, detail = "排队中", fmt.Sprintf("第 %d 个", i+1)
			}
			a.analysisRow(c, key, status, detail, "cancel")
			shown[key] = true
		}
		for _, key := range a.analysis.CancelledDays() {
			if shown[key] {
				continue
			}
			at, _ := a.analysis.CancelledAt(key)
			a.analysisRow(c, key, "已取消", "取消于 "+at.Format("15:04"), "restore")
			shown[key] = true
		}
		for _, key := range pending {
			if shown[key] {
				continue
			}
			a.analysisRow(c, key, "待分析", "", "cancel")
			shown[key] = true
		}
		done := 0
		for i := len(a.analysis.Done) - 1; i >= 0 && done < 3; i-- {
			entry := a.analysis.Done[i]
			if shown[entry.Day] {
				continue
			}
			a.analysisRow(c, entry.Day, "已完成", entry.At.Format("15:04"), "")
			shown[entry.Day] = true
			done++
		}
		if len(a.analysis.Done) > 0 {
			ui.Row(c).Gap(6).Children(func() {
				ui.Spacer(c)
				if ui.Button(c, "清除已完成").FontSize(10.5).Padding(2, 6).Radius(5).Clicked() {
					a.analysis.Done = nil
				}
			})
		}
	})
}

// taskIcon is a quiet 30px icon button for the task rail, with the
// explanation in its tooltip.
func (a *app) taskIcon(c *ui.Context, icon *ui.SVG, label, tip string, fn func()) {
	t := c.Theme()
	b := ui.Box(c).Width(30).Height(30).Radius(8).Center().
		Cursor(ui.CursorPointer).Label(label).Tooltip(tip)
	b.Children(func() {
		ui.Icon(c, icon).FontSize(14).TextColor(t.TextMuted)
	})
	if b.Hovered() {
		b.Background(t.Background)
	}
	if b.Clicked() {
		fn()
	}
}

// analysisRow is one day in the analysis task list.
func (a *app) analysisRow(c *ui.Context, day, status, detail, action string) {
	t := c.Theme()
	color := t.TextMuted
	switch status {
	case "分析中", "排队中":
		color = t.Accent
	case "已完成":
		color = t.Success
	}
	ui.Row(c).Gap(10).Height(30).AlignItems(ui.Center).Children(func() {
		// Every row uses the same 12px marker slot, so the day and status
		// columns line up whatever the marker is.
		ui.Row(c).Width(12).Height(12).AlignItems(ui.Center).Justify(ui.Center).Children(func() {
			if status == "分析中" {
				ui.Spinner(c).Size(11, 11)
			} else {
				dot(c, color, 6)
			}
		})
		ui.Text(c, day).Font("monospace").FontSize(12).TextColor(t.Text).Width(92)
		ui.Text(c, status).FontSize(11.5).TextColor(color).Width(54)
		if detail != "" {
			ui.Text(c, detail).FontSize(11).TextColor(t.TextMuted).Grow(1).MinWidth(0).SingleLine()
		} else {
			ui.Spacer(c)
		}
		switch action {
		case "cancel":
			if ui.Button(c, "取消").FontSize(11).Padding(3, 8).Radius(6).Clicked() {
				a.analysis.Cancel(day)
			}
		case "restore":
			if ui.Button(c, "恢复").FontSize(11).Padding(3, 8).Radius(6).Clicked() {
				a.analysis.Restore(day)
			}
		}
	})
}

// collectorRow is one plugin in the list: identity, then the controls
// the user cares about, then the plugin's own settings.
func (a *app) collectorRow(c *ui.Context, p collector) {
	t := c.Theme()
	ui.Column(c).Gap(12).Children(func() {
		ui.Row(c).Gap(10).AlignItems(ui.Start).Children(func() {
			ui.Icon(c, p.Icon()).FontSize(20).TextColor(t.Accent)
			ui.Column(c).Gap(3).Grow(1).MinWidth(0).Children(func() {
				ui.Text(c, p.Name()).FontSize(13).Bold()
				ui.Text(c, p.Description()).FontSize(11).TextColor(t.TextMuted).LineHeight(1.4)
			})
			if run, ok := a.collectorDay.Items[p.ID()]; ok {
				status := "上次完成"
				color := t.Success
				switch {
				case run.Status == "error":
					status, color = "上次失败", t.Danger
				case run.Status == "running":
					status, color = "运行中", t.Accent
				}
				ui.Text(c, status).FontSize(11).TextColor(color)
			}
		})

		s := a.collectorSchedule(p.ID())
		ui.Row(c).Gap(10).AlignItems(ui.Center).Children(func() {
			ui.Switch(c, &s.Enabled).Label("启用")
			if s.Enabled {
				ui.Text(c, "已启用").FontSize(11).TextColor(t.Success)
			} else {
				ui.Text(c, "已禁用").FontSize(11).TextColor(t.TextMuted)
			}
			ui.Text(c, "每天").FontSize(11).TextColor(t.TextMuted)
			ui.TextInput(c, &s.Time).Width(80).Placeholder("21:00").Label(p.Name() + "采集时间")
			ui.Text(c, "自动执行").FontSize(11).TextColor(t.TextMuted)
			ui.Spacer(c)
			if ui.PrimaryButton(c, "立即执行").Disabled(a.collecting).Clicked() {
				a.runCollectorNow(p.ID())
			}
		})
		p.View(c, collectorView{Photos: &a.collectorCfg.Photos, Manifest: a.chat.manifest})
	})
}

func (a *app) saveCollectorCfg() {
	for id, s := range a.collectorCfg.Schedules {
		if s == nil || strings.TrimSpace(s.Time) == "" {
			continue
		}
		s.Time = normalizeClock(s.Time)
		if _, err := clockOn(a.day, s.Time); err != nil {
			a.settingsErr = "采集时间要写成 HH:MM：" + id
			return
		}
	}
	if err := saveCollectorSettings(a.cfg.storageRoot(), a.collectorCfg); err != nil {
		a.settingsErr = "保存采集器设置：" + err.Error()
		return
	}
	a.settingsErr = ""
}

// openCollectorPage is the sidebar destination.
func (a *app) openCollectorPage() {
	if !a.cfg.configured() {
		a.openSettings()
		return
	}
	a.page = pageCollectors
}
