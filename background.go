package main

import (
	_ "embed"
	"log"
	"time"

	"github.com/egoist/mygo"
)

// trayIcon is the menu bar icon: the app's snail, black on transparent so
// macOS tints it for the light and dark menu bars.
//
//go:embed resources/tray.png
var trayIcon []byte

// setupTray puts Stilday in the menu bar: the way back to the window, and
// out of the app, while the window is hidden and the day's schedule keeps
// running.
func (a *app) setupTray() {
	tray, err := mygo.NewTray(mygo.TrayOptions{
		ID:             "stilday",
		Icon:           trayIcon,
		IconIsTemplate: true,
		ToolTip:        "Stilday",
		Menu:           a.trayMenu(),
	})
	if err != nil {
		log.Println("stilday: menu bar icon:", err)
		return
	}
	a.tray = tray
}

// trayMenu builds the menu from the state it shows.
func (a *app) trayMenu() *mygo.Menu {
	return mygo.NewMenu([]*mygo.MenuItem{
		{Label: "打开 Stilday", Click: func(*mygo.MenuItem, *mygo.Window) {
			a.showWindow()
		}},
		{Label: "今天", Click: func(*mygo.MenuItem, *mygo.Window) {
			a.showWindow()
			a.selectDay(midnight(time.Now()))
		}},
		mygo.Separator(),
		{Label: "立即采集", Click: func(*mygo.MenuItem, *mygo.Window) {
			a.runCollectorNow(photosCollectorID)
		}},
		mygo.Separator(),
		{
			Label:   "开机自动启动",
			Type:    mygo.MenuItemCheckbox,
			Checked: a.autoStart,
			Click: func(item *mygo.MenuItem, _ *mygo.Window) {
				a.setOpenAtLogin(item.Checked)
				a.refreshTray()
			},
		},
		mygo.Separator(),
		{Role: mygo.RoleQuit},
	})
}

// refreshTray rebuilds the menu after the state it shows changed.
func (a *app) refreshTray() {
	if a.tray != nil {
		a.tray.SetMenu(a.trayMenu())
	}
}

// setOpenAtLogin registers or drops the login item, and keeps a failure
// for the settings page.
func (a *app) setOpenAtLogin(on bool) {
	if a.win == nil {
		return // no app yet, as in a test
	}
	if err := mygo.App.SetOpenAtLogin(on); err != nil {
		a.settingsErr = "开机自动启动：" + err.Error()
		a.autoStart = mygo.App.OpenAtLogin()
		return
	}
	a.autoStart = on
	a.settingsErr = ""
}

// showWindow brings the window back, from the menu bar, the Dock, or a
// second launch of the app.
func (a *app) showWindow() {
	if a.win == nil {
		return
	}
	// The window brings the Dock icon back with it.
	mygo.App.SetActivationPolicy(mygo.ActivationPolicyRegular)
	a.win.Show()
	a.win.Focus()
}

// keepScheduleTicking runs the clock a window's frames would: while the
// window is hidden, nothing builds it, so collection and analysis would
// otherwise wait for it to show again.
func (a *app) keepScheduleTicking() {
	ticker := time.NewTicker(30 * time.Second)
	go func() {
		defer ticker.Stop()
		for {
			select {
			case <-a.stopped:
				return
			case <-ticker.C:
				if a.win == nil {
					return
				}
				a.win.Update(func() {
					// A hidden app has no day open: open it here, as the
					// day page would, so its collectors can run.
					if a.cfg.configured() && !a.chat.loaded {
						a.openDay(a.day)
					}
					a.tick()
				})
			}
		}
	}()
}
