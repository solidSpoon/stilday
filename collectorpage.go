package main

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"

	"stilday/internal/schedule"
)

// collectorPage is the collectors' daily plan: one listening row per
// plugin, the analysis time above them, and the task rail on the right.
func (a *app) collectorPage(c *ui.Context) {
	if !a.cfg.configured() {
		a.openSettings()
		return
	}
	t := c.Theme()
	subtitle := "每天定时采集、分析；右边是执行队列"
	pageShell(c, "任务", subtitle, func() {
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

// collectorAnalysisPlan is the daily analysis as a settings card: the
// switch at its top right, the time below it.
func (a *app) collectorAnalysisPlan(c *ui.Context) {
	t := c.Theme()
	group(c, func() {
		ui.Row(c).Gap(12).AlignItems(ui.Start).Children(func() {
			ui.Icon(c, iconAnalysis).FontSize(20).TextColor(t.Accent)
			ui.Column(c).Gap(3).Grow(1).MinWidth(0).Children(func() {
				ui.Text(c, "每日分析").FontSize(13).Bold()
				ui.Text(c, "到点把待分析的日期交给 AI；没有任务就不处理，也不等采集器。").
					FontSize(11).TextColor(t.TextMuted).LineHeight(1.4)
			})
			on := !a.cfg.AutoOff
			ui.Switch(c, &on).Label("启用每日分析")
			if on != !a.cfg.AutoOff {
				a.cfg.AutoOff = !on
				a.collectorAppDirty = true
				if s := a.scheduler(); s != nil {
					s.Hold(a.cfg.AutoOff)
				}
			}
		})
		ui.Divider(c)
		ui.Row(c).Gap(10).AlignItems(ui.Center).Children(func() {
			ui.Text(c, "执行日期").FontSize(11).TextColor(t.TextMuted).Width(76)
			if scheduleDays(c, "每日分析执行日期", &a.cfg.AutoDays, &a.cfg.AutoCustom) {
				a.collectorAppDirty = true
			}
		})
		ui.Row(c).Gap(10).AlignItems(ui.Center).Children(func() {
			ui.Text(c, "执行时间").FontSize(11).TextColor(t.TextMuted).Width(76)
			if scheduleTimes(c, &a.cfg.AutoTime, &a.cfg.AutoTimes) {
				a.collectorAppDirty = true
			}
			ui.Spacer(c)
			if ui.PrimaryButton(c, "立即触发").Clicked() {
				a.startAnalysisNow()
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
	ui.Column(c).Width(480).Shrink(0).FillHeight().Background(t.Background).
		BorderWidth(0, 0, 0, 1).BorderColor(hairline(t)).Children(func() {
		// The whole rail scrolls, header included: the padding lives inside
		// the scroll so the scrollbar sits on the sidebar's edge.
		ui.Scroll(c).Grow(1).FillHeight().ScrollbarInsets(barHeight+8, 0, 12).
			Padding(barHeight+24, 20, 20, 24).Gap(10).Children(func() {
			a.analysisTasks(c)
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
	times := make([]string, 0, len(c.AutoTimes))
	for _, at := range c.AutoTimes {
		at = normalizeClock(strings.TrimSpace(at))
		if at == "" {
			continue
		}
		if _, err := clockOn(a.day, at); err != nil {
			a.settingsErr = "每日分析时间要写成 HH:MM"
			return
		}
		times = append(times, at)
	}
	c.AutoTimes = times
	if err := saveConfig(c); err != nil {
		a.settingsErr = "保存设置：" + err.Error()
		return
	}
	a.cfg = c
	// The selected day is re-prepared even if its state is already cached.
	a.dayState(a.day).forcePrep = true
	// Waiting tasks follow the new daily time, and the switch.
	a.syncHeads()
	if s := a.scheduler(); s != nil {
		s.ReschedulePending(a.analysisDue(a.clockNow()))
	}
	a.settingsErr = ""
}

// analysisTasks is the queue rail: the jobs and the day tasks, in the
// order the scheduler will take them.
func (a *app) analysisTasks(c *ui.Context) {
	t := c.Theme()
	s := a.scheduler()
	if s == nil {
		return
	}
	items := a.railItems(s)
	ui.Row(c).Gap(4).AlignItems(ui.Center).Children(func() {
		groupTitle(c, "队列")
		if n := len(items); n > 0 {
			ui.Badge(c, strconv.Itoa(n))
		}
		ui.Spacer(c)
		glassAction(c, iconPlay, "启动所有", s.CanStart(), ui.Color{}, a.startAllAnalysis)
		glassAction(c, iconStop, "取消所有", s.CanCancel(), ui.Color{}, a.cancelAllAnalysis)
		glassAction(c, iconTrash, "清除已取消", s.HasCancelled(), ui.Color{}, a.clearCancelledTasks)
	})
	if len(items) == 0 {
		ui.Text(c, "没有要执行的任务。").FontSize(11.5).TextColor(t.TextMuted)
		return
	}
	ui.Column(c).Gap(8).Children(func() {
		for _, it := range items {
			a.railCard(c, it, s)
		}
	})
}

// railKind is what a queue row is: a recurring job (a collector, or the
// daily analysis) or the one-day task an analysis job produced.
type railKind int

const (
	kindCollector railKind = iota
	kindAnalysisJob
	kindDayTask
)

// railAction is one queue action. A task's state decides which actions it
// has; the card only draws them.
type railAction string

const (
	actRun     railAction = "run"     // dispatch now
	actCancel  railAction = "cancel"  // take it out of the queue
	actRestart railAction = "restart" // back in the queue at its time
	actDelete  railAction = "delete"  // drop it for good
)

func (act railAction) label() string {
	switch act {
	case actRun:
		return "执行"
	case actCancel:
		return "取消"
	case actRestart:
		return "启动"
	case actDelete:
		return "删除"
	}
	return ""
}

// railItem is one queue row: a job or a task, same shape for the card.
type railItem struct {
	kind     railKind
	id       string
	title    string
	mono     bool
	sub      string
	detail   string
	badge    string // the days a job runs, empty for a day task
	status   string
	actions  []railAction
	due      time.Time
	hasDue   bool
	rank     int
	priority int
	running  bool
	started  time.Time
	elapsed  int64
}

// railItems builds the queue from the scheduler's rows, in the order the
// scheduler takes them. The specs are the scheduler's now; the app only
// names the rows.
func (a *app) railItems(s *schedule.Scheduler) []railItem {
	now := a.clockNow()
	var items []railItem
	for _, t := range s.Queue() {
		switch t.Kind {
		case schedule.Recurring:
			if t.ID == "analysis" {
				it := railItem{kind: kindAnalysisJob, id: t.ID, title: "每日分析",
					sub: "到点分析待处理的日期", actions: []railAction{actRun, actCancel},
					badge: jobBadge(t.Spec, a.cfg.AutoCustom)}
				if !t.Enabled {
					it.status = "已停用"
				}
				it.detail = recurringDetail(s, t, now)
				if n := s.CountPending(); n > 0 {
					it.detail += " · 待分析 " + strconv.Itoa(n) + " 天"
				}
				items = append(items, it)
				continue
			}
			p := a.collectorByJobID(t.ID)
			if p == nil {
				continue
			}
			it := railItem{kind: kindCollector, id: p.ID(), title: p.Name(),
				sub: "从照片图库收集这天的照片", actions: []railAction{actRun, actCancel},
				badge: jobBadge(t.Spec, false)}
			if !t.Enabled {
				it.status = "已停用"
			} else if a.collecting && a.collectID == p.ID() {
				it.status, it.running, it.actions = "执行中", true, nil
				it.started = a.collectStarted
			}
			if it.running {
				it.detail = shortCollectProgress(a.collectProgress)
			} else {
				it.detail = recurringDetail(s, t, now)
			}
			items = append(items, it)
		case schedule.Once:
			it := railItem{kind: kindDayTask, id: t.Day, title: t.Day, mono: true,
				due: t.Due, hasDue: !t.Due.IsZero(), sub: "看照片 · 写事件"}
			switch t.State {
			case schedule.TaskRunning:
				it.status, it.running = "分析中", true
				it.started = t.Started
			case schedule.TaskPending:
				it.actions = []railAction{actRun, actCancel}
				if it.hasDue && !now.Before(it.due) {
					it.status = "就绪"
					if n := s.Ahead(t.ID); n > 0 {
						it.detail = "前方 " + strconv.Itoa(n) + " 个"
					} else {
						it.detail = "下一个"
					}
				} else if t.Due.IsZero() {
					it.detail = "未排期 · 等每日分析"
				} else {
					it.detail = "等 " + clockLabel(t.Due, now)
				}
			case schedule.TaskFailed:
				it.status, it.detail = "分析失败", t.Error
				it.actions = []railAction{actRun}
			case schedule.TaskCancelled:
				it.status = "已取消"
				it.actions = []railAction{actRestart, actDelete}
				if !t.Finished.IsZero() {
					it.detail = "取消于 " + t.Finished.Format("15:04")
				}
			case schedule.TaskDone:
				continue
			}
			items = append(items, it)
		}
	}
	return items
}

// jobBadge is the days badge of a recurring row.
func jobBadge(spec schedule.Spec, custom bool) string {
	if custom {
		return "自定义"
	}
	return specDaysText(spec)
}

// recurringDetail is a recurring row's next run and last run.
func recurringDetail(s *schedule.Scheduler, t schedule.Task, now time.Time) string {
	at, ok := t.Spec.Today(now)
	if !ok {
		at = t.Spec.Next(now)
	} else if last, has := s.Last(t.ID); has && !last.Before(at) {
		at = t.Spec.Next(now)
	}
	if !t.SkipUntil.IsZero() && !at.After(t.SkipUntil) {
		at = t.Spec.Next(t.SkipUntil)
	}
	detail := "下次 " + clockLabel(at, now) + " · " + specDaysText(t.Spec)
	if last, ok := s.Last(t.ID); ok {
		detail += " · 上次 " + last.Format("15:04")
	}
	return detail
}

// collectorByJobID finds the plugin behind a scheduler row ID.
func (a *app) collectorByJobID(jobID string) collector {
	for _, p := range a.collectorList {
		if collectorJob(p.ID()) == jobID {
			return p
		}
	}
	return nil
}

// clockLabel names a time the way the rail does: today, tomorrow, or the
// date.
func clockLabel(at, now time.Time) string {
	switch {
	case sameDay(at, now):
		return "今天 " + at.Format("15:04")
	case sameDay(at, now.AddDate(0, 0, 1)):
		return "明天 " + at.Format("15:04")
	default:
		return at.Format("01-02 15:04")
	}
}

// railCard is one queue row, drawn as the board's cards are: a surface, a
// status pill, the title above a line about the task, and the actions its
// state allows, in fixed columns.
func (a *app) railCard(c *ui.Context, it railItem, s *schedule.Scheduler) {
	t := c.Theme()
	color := t.TextMuted
	switch it.status {
	case "执行中", "分析中":
		// Running is the one you look for: green.
		color = t.Success
	case "就绪":
		color = t.Accent
	case "待执行":
		color = t.Warning
	case "分析失败":
		color = t.Danger
	}
	card := ui.Row(c.Key(it.id)).Gap(12).Padding(14, 16).Radius(12).Background(t.Surface).
		AlignItems(ui.Start).Transition(ui.ElementTransition{
		Position: true, Size: true, Duration: 220 * time.Millisecond})
	card.Children(func() {
		ui.Column(c).Gap(7).Grow(1).MinWidth(0).Children(func() {
			ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
				dot(c, color, 7)
				if it.mono {
					ui.Text(c, it.title).Font("monospace").FontSize(13).Bold().TextColor(t.Text)
				} else {
					ui.Text(c, it.title).FontSize(13).Bold().TextColor(t.Text)
				}
				if it.badge != "" {
					ui.Badge(c, it.badge)
				}
				ui.Spacer(c)
				ui.Row(c).Width(64).Justify(ui.End).Children(func() {
					// A running card already reads as running: the spinner,
					// the green border and the queue line say it, so no badge.
					if it.status != "" && !it.running {
						ui.Text(c, it.status).FontSize(10).Bold().Padding(1, 6).Radius(4).
							TextColor(color).Background(color.Alpha(0.12))
					}
				})
				ui.Row(c).Width(104).Justify(ui.End).Gap(6).Children(func() {
					for _, act := range it.actions {
						enabled := a.railEnabled(it, act)
						label := act.label()
						if act == actRun && it.kind != kindDayTask {
							// A job is triggered outside its schedule; a
							// day's task is just run now.
							label = "立即触发"
						}
						if ui.Button(c, label).FontSize(11).Padding(3, 8).Radius(6).
							Disabled(!enabled).Clicked() && enabled {
							a.railDo(it, act)
						}
					}
				})
			})
			if it.sub != "" {
				ui.Text(c, it.sub).FontSize(11).TextColor(t.TextMuted).LineHeight(1.4).SingleLine()
			}
			if it.detail != "" || (it.running && !it.started.IsZero()) {
				ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
					if it.detail != "" {
						ui.Text(c, it.detail).FontSize(11).TextColor(t.TextMuted).LineHeight(1.4).
							Grow(1).MinWidth(0).SingleLine()
					} else {
						ui.Spacer(c)
					}
					if it.running && !it.started.IsZero() {
						seconds := int64(a.clockNow().Sub(it.started).Seconds())
						if seconds < 0 {
							seconds = 0
						}
						ui.Textf(c, "运行 %ds", seconds).FontSize(10).TextColor(t.TextMuted)
					}
				})
			}
		})
	})
}

// railEnabled reports whether an action applies to a row right now.
func (a *app) railEnabled(it railItem, act railAction) bool {
	if act != actRun {
		return true
	}
	switch it.kind {
	case kindCollector:
		return a.canStartCollector()
	case kindAnalysisJob:
		return true
	}
	return true
}

// railDo runs one queue action on the row it belongs to.
func (a *app) railDo(it railItem, act railAction) {
	switch act {
	case actRun:
		switch it.kind {
		case kindCollector:
			a.runCollectorNow(it.id)
		case kindAnalysisJob:
			a.startAnalysisNow()
		default:
			a.startAnalysisTask(it.id)
		}
	case actCancel:
		if it.kind == kindDayTask {
			a.cancelAnalysisTask(it.id)
		} else {
			a.cancelHead(it.id)
		}
	case actRestart:
		a.restartAnalysisTask(it.id)
	case actDelete:
		a.deleteAnalysisTask(it.id)
	}
}

// collectorRow is one plugin as a settings card: identity and its switch
// on top, the time row, then the plugin's own settings.
func (a *app) collectorRow(c *ui.Context, p collector) {
	t := c.Theme()
	ui.Column(c).Gap(12).Children(func() {
		s := p.Schedule()
		if s == nil {
			s = &collectorSchedule{}
		}
		ui.Row(c).Gap(12).AlignItems(ui.Start).Children(func() {
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
			ui.Switch(c, &s.Enabled).Label("启用")
		})
		ui.Divider(c)
		ui.Row(c).Gap(10).AlignItems(ui.Center).Children(func() {
			ui.Text(c, "执行日期").FontSize(11).TextColor(t.TextMuted).Width(76)
			scheduleDays(c, p.Name()+"执行日期", &s.Days, &s.Custom)
		})
		ui.Row(c).Gap(10).AlignItems(ui.Center).Children(func() {
			ui.Text(c, "执行时间").FontSize(11).TextColor(t.TextMuted).Width(76)
			scheduleTimes(c, &s.Time, &s.Times)
			ui.Spacer(c)
			if ui.PrimaryButton(c, "立即触发").Disabled(!a.canStartCollector()).Clicked() {
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
		times := make([]string, 0, len(s.Times))
		for _, at := range s.Times {
			at = normalizeClock(strings.TrimSpace(at))
			if at == "" {
				continue
			}
			if _, err := clockOn(a.day, at); err != nil {
				a.settingsErr = "采集时间要写成 HH:MM：" + p.ID()
				return
			}
			times = append(times, at)
		}
		s.Times = times
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
	a.syncHeads()
}

// openCollectorPage is the sidebar destination.
func (a *app) openCollectorPage() {
	if !a.cfg.configured() {
		a.openSettings()
		return
	}
	a.page = pageCollectors
}
