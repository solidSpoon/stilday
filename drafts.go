package main

import (
	"reflect"
	"strings"

	"stilday/internal/schedule"
)

// The task page edits drafts: the schedule fields and switches change the
// draft only, and the card's 保存 applies it (writes the config, saves the
// plugin settings, re-syncs the scheduler). Nothing takes effect before
// that, so a time edited in passing does not fire.

// initCollectorDrafts starts the drafts from what is applied.
func (a *app) initCollectorDrafts() {
	a.autoDraftOn = !a.cfg.AutoOff
	a.autoDraftDays = append([]int(nil), a.cfg.AutoDays...)
	a.autoDraftCustom = a.cfg.AutoCustom
	a.autoDraftTime = a.cfg.AutoTime
	a.autoDraftTimes = append([]string(nil), a.cfg.AutoTimes...)
	a.pluginDrafts = map[string]collectorSchedule{}
	for _, p := range a.collectorList {
		if s := p.Schedule(); s != nil {
			a.pluginDrafts[p.ID()] = *s
		}
	}
	a.autoDraftReady = true
}

func (a *app) analysisDraftDirty() bool {
	want := schedule.Spec{Days: a.autoDraftDays, Time: a.autoDraftTime, Times: a.autoDraftTimes}
	have := schedule.Spec{Days: a.cfg.AutoDays, Time: a.cfg.AutoTime, Times: a.cfg.AutoTimes}
	return a.autoDraftOn != !a.cfg.AutoOff || a.autoDraftCustom != a.cfg.AutoCustom ||
		!reflect.DeepEqual(want, have)
}

func (a *app) saveAnalysisDraft() {
	times := make([]string, 0, len(a.autoDraftTimes))
	for _, at := range a.autoDraftTimes {
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
	at := normalizeClock(strings.TrimSpace(a.autoDraftTime))
	if _, err := clockOn(a.day, at); err != nil {
		a.settingsErr = "每日分析时间要写成 HH:MM"
		return
	}
	a.cfg.AutoOff = !a.autoDraftOn
	a.cfg.AutoDays = append([]int(nil), a.autoDraftDays...)
	a.cfg.AutoCustom = a.autoDraftCustom
	a.cfg.AutoTime = at
	a.cfg.AutoTimes = times
	a.saveCollectorPageConfig()
	// The saved, normalized values become the new draft.
	a.autoDraftOn = !a.cfg.AutoOff
	a.autoDraftDays = append([]int(nil), a.cfg.AutoDays...)
	a.autoDraftCustom = a.cfg.AutoCustom
	a.autoDraftTime = a.cfg.AutoTime
	a.autoDraftTimes = append([]string(nil), a.cfg.AutoTimes...)
}

func (a *app) pluginDraftDirty(p collector) bool {
	if p.SettingsChanged() {
		return true
	}
	s := p.Schedule()
	if s == nil {
		return false
	}
	return !reflect.DeepEqual(a.pluginDrafts[p.ID()], *s)
}

func (a *app) savePluginDraft(p collector) {
	d, ok := a.pluginDrafts[p.ID()]
	if !ok {
		return
	}
	d.Time = normalizeClock(strings.TrimSpace(d.Time))
	if _, err := clockOn(a.day, d.Time); err != nil {
		a.settingsErr = "采集时间要写成 HH:MM：" + p.ID()
		return
	}
	times := make([]string, 0, len(d.Times))
	for _, at := range d.Times {
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
	d.Times = times
	a.pluginDrafts[p.ID()] = d
	if s := p.Schedule(); s != nil {
		*s = d
	}
	if err := p.SaveSettings(); err != nil {
		a.settingsErr = "保存 " + p.Name() + " 的设置：" + err.Error()
		return
	}
	a.syncHeads()
}
