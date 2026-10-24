package main

import (
	"path/filepath"
	"reflect"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"stilday/internal/artifact"
)

// settingsPage owns everything that is not a day: where files go, where
// photos come from, the time window, the model, Pi, and the AI entry. It
// is a form in one column, as a Mac's settings are.
func (a *app) settingsPage(c *ui.Context) {
	t := c.Theme()
	pageShell(c, "设置", "存储、模型、Pi 和 MCP", func() {
		if !a.cfg.configured() {
			glassAction(c, iconCheck, "开始使用", true, ui.Color{}, a.saveSettings)
			return
		}
		if a.settingsDirty() {
			glassAction(c, iconCheck, "保存设置", true, ui.Color{}, a.saveSettings)
		}
	}, func() {
		ui.Row(c).Grow(1).Justify(ui.Center).Children(func() {
			ui.Scroll(c).FillHeight().WidthPercent(100).MaxWidth(720).
				Padding(barHeight+24, 28, 36).ScrollbarInsets(barHeight+8, 0, 0).Gap(24).Children(func() {
				if a.settingsErr != "" {
					ui.Row(c).Padding(10, 12).Radius(8).Background(t.Danger.Alpha(0.08)).Children(func() {
						ui.Text(c, a.settingsErr).FontSize(12).TextColor(t.Danger).Selectable()
					})
				}
				if a.cfgErr != nil {
					errorRow(c, "配置文件读取失败："+a.cfgErr.Error())
				}
				if a.fileErr != nil {
					errorRow(c, "文件读写失败："+a.fileErr.Error())
				}
				a.storageSettings(c)
				if a.cfg.configured() {
					a.launchSettings(c)
					a.scheduleSettings(c)
				}
				a.modelSettings(c)
				a.piSettings(c)
				a.mcpSettings(c)
			})
		})
	})
}

// launchSettings is how the app starts, and what closing its window does.
func (a *app) launchSettings(c *ui.Context) {
	t := c.Theme()
	group(c, func() {
		groupTitle(c, "启动与后台")
		ui.Row(c).Gap(12).AlignItems(ui.Center).Children(func() {
			sw := ui.Switch(c, &a.autoStart).Label("开机自动启动")
			sw.OnClick(func() { a.setOpenAtLogin(a.autoStart) })
			ui.Column(c).Gap(3).Grow(1).MinWidth(0).Children(func() {
				ui.Text(c, "登录时在后台启动，只在菜单栏留一个图标。").FontSize(11).TextColor(t.TextMuted)
				ui.Text(c, "关掉窗口不会退出：到点照常采集和分析，点菜单栏或 Dock 图标回来。").FontSize(11).TextColor(t.TextMuted)
			})
		})
	})
}

// scheduleSettings is how late a missed scheduled job may still run.
func (a *app) scheduleSettings(c *ui.Context) {
	t := c.Theme()
	group(c, func() {
		groupTitle(c, "定时任务")
		ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
			ui.Text(c, "补跑窗口").FontSize(12).Width(70)
			input := ui.TextInput(c, &a.catchUpDraft).Width(70).Placeholder("60").Label("补跑窗口（分钟）")
			ui.Text(c, "分钟").FontSize(11).TextColor(t.TextMuted)
			if input.Changed() {
				if n, err := strconv.Atoi(strings.TrimSpace(a.catchUpDraft)); err == nil && n > 0 {
					a.settingsDraft.CatchUpMinutes = n
				}
			}
		})
		ui.Text(c, "错过计划时间后，启动 app 时在窗口内仍会补跑；超过就等下一次。默认 60 分钟。").
			FontSize(11).TextColor(t.TextMuted)
	})
}

func (a *app) storageSettings(c *ui.Context) {
	t := c.Theme()
	group(c, func() {
		groupTitle(c, "存储")
		ui.Text(c, "Stilday 只在这里写文件。工作目录和产物分开，按年/月分组。").FontSize(11).TextColor(t.TextMuted)
		ui.Row(c).Gap(8).Children(func() {
			ui.TextInput(c, &a.settingsDraft.StorageDir).Placeholder("~/Stilday").Grow(1).Label("存储目录")
			if ui.Button(c, "选择…").Clicked() {
				a.pickDir("选择存储目录", func(p string) { a.settingsDraft.StorageDir = p })
			}
		})
		draft := a.settingsDraft
		if !draft.configured() {
			ui.Text(c, "选择后这里会显示目录结构。").FontSize(11).TextColor(t.TextMuted)
			return
		}
		ui.Column(c).Gap(4).Padding(8, 10).Radius(7).Background(t.Background).Children(func() {
			// Under the root the field above shows, so the structure, not
			// the absolute path, is what these rows are for.
			pathRow(c, "工作", relativeTo(draft.storageRoot(), draft.workDayDir(a.day))+"/")
			pathRow(c, "产物", relativeTo(draft.storageRoot(), draft.artifactPath(a.day)))
		})
		ui.Text(c, "换目录不会搬走已有文件；旧目录原样保留。").FontSize(11).TextColor(t.TextMuted)
	})
}

func (a *app) modelSettings(c *ui.Context) {
	t := c.Theme()
	group(c, func() {
		groupTitle(c, "模型")
		spec := providerFor(a.settingsDraft.Provider)
		ui.Form(c, func() {
			ui.Field(c, "Provider", func() {
				ui.Text(c, spec.Name).FontSize(12).Bold()
			})
			ui.Field(c, "模型", func() {
				chooser := ui.Row(c).Gap(8).AlignItems(ui.Center).Padding(6, 10).Radius(8).
					Background(t.Background).Border(1, t.Border.Alpha(0.5)).
					Cursor(ui.CursorPointer).Label("选择模型")
				chooser.Children(func() {
					ui.Text(c, a.modelDisplay(a.settingsDraft.Model)).FontSize(12).
						Grow(1).MinWidth(0).SingleLine()
					ui.Icon(c, iconChevronDn).FontSize(12).TextColor(t.TextMuted)
				})
				if chooser.Clicked() {
					a.modelPickerOpen = !a.modelPickerOpen
					if a.modelPickerOpen && len(a.models) == 0 && !a.modelsLoading {
						a.loadModels()
					}
				}
				ui.Popover(c, chooser, &a.modelPickerOpen, func() {
					a.modelPicker(c, spec)
				})
			})
			ui.Field(c, "API key", func() {
				keyField := ui.TextInput(c, &a.keyDraft).Password().
					Placeholder("留空保留已保存的 key").Grow(1).Label("API key")
				if keyField.Changed() {
					a.scheduleKeySave()
				}
				if keyField.Submitted() {
					a.saveKeyDraft()
				}
			})
		})
		ui.Text(c, spec.KeyHint+"。密钥保存在 macOS Keychain，不写进配置文件。").FontSize(11).TextColor(t.TextMuted)
		if a.keySaved {
			ui.Text(c, "API key 已保存到 Keychain。").FontSize(11).TextColor(t.Success)
		}
	})
}

// scheduleKeySave debounces the key field: the key is written to Keychain
// shortly after the last keystroke, so the field no longer depends on the
// dirty-only save check.
func (a *app) scheduleKeySave() {
	if a.keySaveTimer != nil {
		a.keySaveTimer.Stop()
	}
	a.keySaveTimer = time.AfterFunc(600*time.Millisecond, func() {
		if a.win == nil {
			return
		}
		a.win.Update(func() { a.saveKeyDraft() })
	})
}

func (a *app) saveKeyDraft() {
	key := strings.TrimSpace(a.keyDraft)
	if key == "" {
		return
	}
	spec := providerFor(a.settingsDraft.Provider)
	if err := appSecrets().Set(providerSecretKey(spec.ID), []byte(key)); err != nil {
		a.settingsErr = "保存 API key：" + err.Error()
		return
	}
	a.keySaved = true
	a.settingsErr = ""
}

// modelPicker is the searchable list of OpenCode Go's models, shown in
// the popover under the model row. The list comes from OpenCode's /models
// endpoint; Pi's catalog only supplies the display names.
func (a *app) modelPicker(c *ui.Context, spec provider) {
	t := c.Theme()
	ui.Column(c).Width(340).Gap(8).Children(func() {
		if a.modelsLoading {
			ui.Row(c).Gap(8).AlignItems(ui.Center).Padding(8, 10).Children(func() {
				ui.Spinner(c).Size(12, 12)
				ui.Text(c, "正在获取模型…").FontSize(11).TextColor(t.TextMuted)
			})
			return
		}
		if a.modelsErr != "" {
			ui.Text(c, a.modelsErr).FontSize(11).TextColor(t.Danger)
			if ui.Button(c, "重试").Clicked() {
				a.loadModels()
			}
			return
		}
		if len(a.models) == 0 {
			ui.Text(c, "没有模型").FontSize(11).TextColor(t.TextMuted)
			if ui.Button(c, "重试").Clicked() {
				a.loadModels()
			}
			return
		}
		ui.SearchField(c, &a.modelQuery).Label("搜索模型").Placeholder("搜索模型")
		q := strings.ToLower(strings.TrimSpace(a.modelQuery))
		matches := make([]modelInfo, 0, len(a.models))
		for _, m := range a.models {
			if q == "" || strings.Contains(strings.ToLower(m.Name+" "+m.ID), q) {
				matches = append(matches, m)
			}
		}
		ui.Scroll(c).MaxHeight(260).Gap(2).Children(func() {
			if len(matches) == 0 {
				ui.Text(c, "没有匹配的模型").FontSize(11).TextColor(t.TextMuted).Padding(8, 10)
				return
			}
			for _, m := range matches {
				selected := m.ID == a.settingsDraft.Model
				row := ui.Row(c).Gap(8).AlignItems(ui.Center).Padding(8, 10).Radius(7).
					Cursor(ui.CursorPointer).Label(m.Name)
				if selected {
					row.Background(t.Accent.Alpha(0.10))
				}
				row.Children(func() {
					ui.Column(c).Gap(2).Grow(1).MinWidth(0).Children(func() {
						ui.Text(c, m.Name).FontSize(12).Bold().TextColor(t.Text)
						ui.Text(c, m.ID).Font("monospace").FontSize(10).TextColor(t.TextMuted)
					})
					if selected {
						ui.Icon(c, iconCheck).FontSize(13).TextColor(t.Accent)
					}
				})
				if row.Clicked() {
					a.settingsDraft.Model = m.ID
					a.modelPickerOpen = false
					a.modelQuery = ""
				}
			}
		})
	})
}

func (a *app) piSettings(c *ui.Context) {
	t := c.Theme()
	group(c, func() {
		groupTitle(c, "Pi")
		if a.pi.busy {
			ui.Row(c).Gap(7).AlignItems(ui.Center).Children(func() {
				ui.Spinner(c).Size(13, 13)
				ui.Text(c, "正在下载 Pi "+a.pi.version).FontSize(12)
			})
			fraction := -1.0
			if a.pi.total > 0 {
				fraction = float64(a.pi.done) / float64(a.pi.total)
			}
			ui.Progress(c, fraction).Label("下载 Pi")
			if a.pi.total > 0 {
				ui.Textf(c, "%s · %.1f / %.1f MB", a.pi.stage, mb(a.pi.done), mb(a.pi.total)).
					FontSize(11).TextColor(t.TextMuted)
			} else {
				ui.Text(c, a.pi.stage).FontSize(11).TextColor(t.TextMuted)
			}
			ui.Text(c, "下载完成后还需要在终端登录一次。").FontSize(11).TextColor(t.TextMuted)
			return
		}
		if a.pi.installed {
			ui.Row(c).Gap(7).AlignItems(ui.Center).Children(func() {
				dot(c, t.Success, 6)
				ui.Text(c, "Pi "+a.pi.version+" 已下载").FontSize(12).TextColor(t.Text)
			})
			ui.Text(c, shortenPath(a.pi.path)).Font("monospace").FontSize(11).
				TextColor(t.TextMuted).Selectable()
			ui.Row(c).Gap(8).Children(func() {
				if runtime.GOOS == "darwin" {
					if ui.Button(c, "在终端登录").Clicked() {
						if err := openPiLogin(a.pi.path); err != nil {
							a.pi.err = err.Error()
						}
					}
				}
				if ui.Button(c, "重新检查").Clicked() {
					a.pi.path = installedPiBinary()
					a.pi.installed = a.pi.path != ""
					a.piPath, a.piErr = resolvePi()
				}
			})
			ui.Text(c, "如果不用 API key，可以在这里登录 Pi：在终端里输入 /login。登录信息在 ~/.pi/agent，Stilday 共用。").FontSize(11).TextColor(t.TextMuted)
			if a.pi.err != "" {
				ui.Text(c, a.pi.err).FontSize(11).TextColor(t.Danger).Selectable()
			}
			return
		}
		ui.Text(c, "对话需要一个 Pi 运行环境。下载到本应用的私有目录，不影响系统里的其他 pi。").FontSize(11).TextColor(t.TextMuted)
		ui.Text(c, "约 30–45 MB，需要联网；下载后填上面的 OpenCode API key 即可，不一定要终端登录。").FontSize(11).TextColor(t.TextMuted)
		if ui.PrimaryButton(c, "下载 Pi v"+a.pi.version).AlignSelf(ui.Start).Clicked() {
			a.downloadPi()
		}
		if dir, err := piInstallDir(); err == nil {
			ui.Text(c, "安装到 "+shortenPath(dir)).Font("monospace").FontSize(10.5).
				TextColor(t.TextMuted).SingleLine()
		}
		if a.pi.err != "" {
			ui.Text(c, a.pi.err).FontSize(11).TextColor(t.Danger).Selectable()
		}
	})
}

func mb(n int64) float64 { return float64(n) / (1 << 20) }

// mcpSettings is the local query endpoint other assistants paste a URL
// into. It offers the owner's day log, nothing else.
func (a *app) mcpSettings(c *ui.Context) {
	t := c.Theme()
	group(c, func() {
		groupTitle(c, "MCP 查询")
		ui.Text(c, "别的 AI 可以填一个网址来查主人的日常记录：做过的事、行程安排、吃喝、身体、兴趣和观察。").
			FontSize(11).TextColor(t.TextMuted)
		ui.Row(c).Gap(10).AlignItems(ui.Center).Children(func() {
			ui.Switch(c, &a.settingsDraft.MCPEnabled).Label("启用 MCP 查询")
			ui.Spacer(c)
			if a.settingsDraft.MCPEnabled {
				ui.Text(c, "端口").FontSize(11).TextColor(t.TextMuted)
				ui.TextInput(c, &a.settingsDraft.MCPPort).Width(70).Placeholder(mcpDefaultPort).Label("MCP 端口")
			}
		})
		if !a.settingsDraft.MCPEnabled {
			return
		}
		url := mcpURL(a.settingsDraft)
		ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
			ui.Text(c, url).Font("monospace").FontSize(11).TextColor(t.Text).Selectable().
				Grow(1).MinWidth(0).SingleLine()
			a.copyButton(c, "复制地址", url)
		})
		if !configEqual(a.settingsDraft, a.cfg) {
			ui.Text(c, "保存后生效。").FontSize(11).TextColor(t.TextMuted)
			return
		}
		if url, errText, running := a.mcp.status(); errText != "" {
			ui.Text(c, "启动失败："+errText).FontSize(11).TextColor(t.Danger)
		} else if running {
			ui.Text(c, "正在 "+url+" 上等连接。").FontSize(11).TextColor(t.Success)
		}
	})
}

func pathRow(c *ui.Context, label, value string) {
	t := c.Theme()
	ui.Row(c).Gap(10).AlignItems(ui.Center).Children(func() {
		ui.Text(c, label).FontSize(11).TextColor(t.TextMuted).Width(34)
		ui.Text(c, value).Font("monospace").FontSize(11).TextColor(t.Text).SingleLine().Grow(1).MinWidth(0)
	})
}

// relativeTo shows p under root, where it is, so a path row reads as the
// structure the root holds rather than as a long absolute path.
func relativeTo(root, p string) string {
	if root == "" || p == "" {
		return shortenPath(p)
	}
	if r, err := filepath.Rel(root, p); err == nil && r != "." && !strings.HasPrefix(r, "..") {
		return filepath.ToSlash(r)
	}
	return shortenPath(p)
}

// copyButton copies text and says so for a moment. a.now moves with the
// frame's clock, so the label returns by itself.
func (a *app) copyButton(c *ui.Context, idle, text string) {
	label := idle
	if a.now.Before(a.copiedUntil) {
		label = "已复制"
	}
	if ui.Button(c, label).Clicked() {
		mygo.Clipboard.WriteText(text)
		a.copiedUntil = a.now.Add(1500 * time.Millisecond)
	}
}

// settingsDirty reports whether the page holds changes that are not in the
// saved configuration yet.
func (a *app) settingsDirty() bool {
	return strings.TrimSpace(a.keyDraft) != "" || !configEqual(a.settingsDraft, a.cfg)
}

// configEqual compares configurations, including the day list.
func configEqual(a, b config) bool {
	return reflect.DeepEqual(a, b)
}

// openSettings copies the configuration into a draft the page edits.
func (a *app) openSettings() {
	if a.page == pageSettings {
		return
	}
	a.settingsDraft = a.cfg.withDefaults()
	a.catchUpDraft = strconv.Itoa(a.settingsDraft.CatchUpMinutes)
	if a.settingsDraft.StorageDir == "" {
		a.settingsDraft.StorageDir = suggestStorageDir()
	}
	if a.keySaveTimer != nil {
		a.keySaveTimer.Stop()
		a.keySaveTimer = nil
	}
	a.settingsErr = ""
	a.keyDraft = ""
	a.keySaved = false
	a.page = pageSettings
}

// hasProviderKey reports whether the selected provider has an API key in
// the same Keychain the Pi child will read.
func (a *app) hasProviderKey() bool {
	spec := providerFor(a.cfg.Provider)
	key, err := appSecrets().Get(providerSecretKey(spec.ID))
	return err == nil && strings.TrimSpace(string(key)) != ""
}

// saveSettings writes the draft, creates the storage trees, and opens the
// selected day again.
func (a *app) saveSettings() {
	c := a.settingsDraft
	c.StorageDir = strings.TrimSpace(c.StorageDir)
	c.Model = strings.TrimSpace(c.Model)
	c = c.withDefaults()
	if err := c.validate(); err != nil {
		a.settingsErr = err.Error()
		return
	}
	if key := strings.TrimSpace(a.keyDraft); key != "" {
		if err := appSecrets().Set(providerSecretKey(c.Provider), []byte(key)); err != nil {
			a.settingsErr = "保存 API key：" + err.Error()
			return
		}
		a.keyDraft = ""
		a.keySaved = true
	}
	if err := c.prepareStorage(); err != nil {
		a.settingsErr = err.Error()
		return
	}
	if err := saveConfig(c); err != nil {
		a.settingsErr = err.Error()
		return
	}
	storageChanged := c.storageRoot() != a.cfg.storageRoot()
	windowChanged := c.PhotoStart != a.cfg.PhotoStart || c.PhotoEnd != a.cfg.PhotoEnd
	a.cfg = c
	a.store = artifact.Open(a.cfg.artifactsRoot())
	a.mcp.reload(a.cfg, mygo.App.Version())
	a.loadCollectorSettings()
	a.page = pageBoard
	a.boardDirty = true
	a.settingsErr = ""
	a.resetAllDays()
	a.chat = a.dayState(a.day)
	a.chat.forcePrep = storageChanged || windowChanged
	if storageChanged {
		a.loadEntries()
	}
	a.reloadCalendar()
	a.syncAnalysisTasks()
	a.calendar.ScrollTo(0, ui.Start)
}

// pickDir opens the system's folder picker off the main thread, then
// writes the answer back with Window.Update.
func (a *app) pickDir(title string, set func(string)) {
	if a.win == nil {
		return
	}
	go func() {
		paths, err := mygo.Dialog.Open(mygo.OpenDialogOptions{
			Parent:    a.win,
			Title:     title,
			Directory: true,
		})
		if err != nil || len(paths) == 0 {
			return
		}
		a.win.Update(func() { set(paths[0]) })
	}()
}

// errorRow shows a file-level failure the app recovered from with defaults.
func errorRow(c *ui.Context, text string) {
	t := c.Theme()
	ui.Row(c).Padding(10, 12).Radius(8).Background(t.Danger.Alpha(0.08)).Children(func() {
		ui.Text(c, text).FontSize(12).TextColor(t.Danger).Selectable()
	})
}
