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
	subtitle := "插件按各自计划采集；到点批量启动右侧待执行的任务"
	pageShell(c, "采集器", subtitle, func() {
		if a.settingsErr != "" {
			ui.Text(c, a.settingsErr).FontSize(11).TextColor(t.Danger).SingleLine()
		}
	}, func() {
		ui.Row(c).Fill().Children(func() {
			ui.Scroll(c).Grow(1).MinWidth(0).FillHeight().
				Padding(barHeight+24, 28, 24).ScrollbarInsets(barHeight+8, 0, 0).Gap(16).Children(func() {
				a.collectorAnalysisPlan(c)
				a.collectorPluginList(c)
			})
			a.collectorSidebar(c)
		})
		for _, p := range a.collectorList {
			if p.SettingsChanged() {
				a.collectorSettingsDirty = true
			}
		}
		switch {
		case a.win == nil:
			// No frame loop (tests): persist now so the result is visible.
			a.flushCollectorPage()
		case a.collectorAppDirty || a.collectorSettingsDirty:
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
			ui.Text(c, "到点批量启动右侧待执行的任务；没有任务就不处理，也不等采集器。").
				FontSize(11).TextColor(t.TextMuted).Grow(1).MinWidth(0).SingleLine()
			ui.Spacer(c)
			ui.Text(c, "每天").FontSize(11).TextColor(t.TextMuted)
			// Save only a complete time: typing "1" or "10:" is not an
			// edit to hand to the clock.
			when := ui.TextInput(c, &a.cfg.AutoTime).Width(80).Placeholder("10:00").Label("每日分析时间")
			if when.Changed() {
				if _, err := clockOn(a.day, normalizeClock(a.cfg.AutoTime)); err == nil {
					a.collectorAppDirty = true
				}
			}
			if ui.PrimaryButton(c, "立即执行").Disabled(!a.canStartAnalysis()).Clicked() {
				a.analysis.StartPending()
			}
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
			// Appending days to a run already in progress is safe even
			// while it owns the conversation slot; starting a new run is
			// not, so the same lock the manual actions use applies.
			glassAction(c, iconPlay, "启动所有", a.canStartAnalysis(), ui.Color{}, a.analysis.StartPending)
			glassAction(c, iconPause, "暂停所有", a.analysis.CanPause(), ui.Color{}, a.analysis.Pause)
			glassAction(c, iconStop, "取消所有", a.analysis.CanCancel(), ui.Color{}, a.analysis.CancelAll)
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

		s := p.Schedule()
		if s == nil {
			s = &collectorSchedule{}
		}
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
			if ui.PrimaryButton(c, "立即执行").Disabled(!a.canStartCollector()).Clicked() {
				a.runCollectorNow(p.ID())
			}
		})
		p.View(c, collectorView{Manifest: a.chat.manifest})
	})
}

// saveCollectorSettings checks each plugin's schedule, then writes the
// settings that changed to the plugins' own folders.
func (a *app) saveCollectorCfg() {
	for _, p := range a.collectorList {
		s := p.Schedule()
		if s == nil || strings.TrimSpace(s.Time) == "" {
			continue
		}
		s.Time = normalizeClock(s.Time)
		if _, err := clockOn(a.day, s.Time); err != nil {
			a.settingsErr = "采集时间要写成 HH:MM：" + p.ID()
			return
		}
	}
	for _, p := range a.collectorList {
		if !p.SettingsChanged() {
			continue
		}
		if err := p.SaveSettings(); err != nil {
			a.settingsErr = "保存 " + p.Name() + " 的设置：" + err.Error()
			return
		}
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
