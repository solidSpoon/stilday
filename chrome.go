package main

import (
	"github.com/egoist/mygo/plugins/glass"
	"github.com/egoist/mygo/ui"
)

// The window's measurements. The toolbar is the window's chrome: every
// page draws its content under it, and the scroll edge frosts what passes
// beneath it, as macOS draws a window's toolbar.
const (
	wideSidebarWidth   = 380
	narrowSidebarWidth = 320
	barHeight          = 64
)

// appleTheme is the system theme with Apple's colors: a quiet gray canvas,
// white content, iOS blue accents and separators. Dark mode keeps the same
// hierarchy.
func appleTheme(base *ui.Theme) *ui.Theme {
	t := *base
	if t.Dark {
		t.Background = ui.Hex("#1c1c1e")
		t.Surface = ui.Hex("#2c2c2e")
		t.SurfaceHover = ui.Hex("#3a3a3c")
		t.SurfacePressed = ui.Hex("#48484a")
		t.Border = ui.Hex("#3a3a3c")
		t.Text = ui.Hex("#ffffff")
		t.TextMuted = ui.Hex("#98989d")
		t.Accent = ui.Hex("#0a84ff")
		t.AccentHover = ui.Hex("#409cff")
		t.AccentPressed = ui.Hex("#64b5ff")
		t.Danger = ui.Hex("#ff453a")
		t.Warning = ui.Hex("#ff9f0a")
		t.Success = ui.Hex("#30d158")
		t.Selection = t.Accent.Alpha(0.32)
		t.Focus = t.Accent.Alpha(0.6)
		t.Scrollbar = ui.RGBA(255, 255, 255, 0.28)
	} else {
		t.Background = ui.Hex("#f2f2f7")
		t.Surface = ui.Hex("#ffffff")
		t.SurfaceHover = ui.Hex("#e9e9ee")
		t.SurfacePressed = ui.Hex("#dcdce1")
		t.Border = ui.Hex("#c6c6c8")
		t.Text = ui.Hex("#1c1c1e")
		t.TextMuted = ui.Hex("#8e8e93")
		t.Accent = ui.Hex("#007aff")
		t.AccentHover = ui.Hex("#0071e3")
		t.AccentPressed = ui.Hex("#0062cc")
		t.Danger = ui.Hex("#ff3b30")
		t.Warning = ui.Hex("#ff9500")
		t.Success = ui.Hex("#34c759")
		t.Selection = t.Accent.Alpha(0.24)
		t.Focus = t.Accent.Alpha(0.5)
		t.Scrollbar = ui.RGBA(0, 0, 0, 0.28)
	}
	t.AccentText = ui.Hex("#ffffff")
	// AppKit's controls, and the framework's, are 8 DIPs round. Surfaces
	// that hold content round a little more, where they ask for it.
	t.Radius = 8
	return &t
}

// hairline is the border color of groups and rows: the theme's border, a
// little quieter.
func hairline(t *ui.Theme) ui.Color { return t.Border.Alpha(0.55) }

// pageShell lays out a page under the window's one toolbar: the title, an
// optional line about it, the page's controls, and its content, which
// scrolls under the toolbar as a macOS window's does. Content must leave
// room at the top: barHeight and a little air, with the scroll bar inset
// the same.
//
// The toolbar is not a pane of glass over nothing: it is the window's
// edge, and the scroll edge the framework draws for macOS frosts only
// what is actually passing beneath it. Glass itself belongs to controls
// that float over content, as the day's composer does.
func pageShell(c *ui.Context, title, subtitle string, actions func(), body func()) {
	t := c.Theme()
	ui.Box(c).Fill().Children(func() {
		body()

		// What scrolls under the bar fades into the window as it does on
		// macOS; at rest this is the page's own background.
		ui.Box(c).Absolute().Top(0).Left(0).Right(0).Height(barHeight).
			PassThrough().Material(glass.ScrollEdge{Hard: true})

		ui.Row(c).Absolute().Top(0).Left(0).Right(0).Height(barHeight).
			Padding(0, 12, 0, 24).Gap(14).AlignItems(ui.Center).DragWindow().Children(func() {
			ui.Column(c).Gap(2).Grow(1).MinWidth(0).Children(func() {
				ui.Text(c, title).FontSize(16).Bold()
				if subtitle != "" {
					ui.Text(c, subtitle).FontSize(12).TextColor(t.TextMuted).SingleLine()
				}
			})
			if actions != nil {
				ui.Toolbar(c, actions).Label(title)
			}
		})
	})
}

// group is the panel that settings and collector pages are made of: one
// surface per group of rows, quiet enough to read as a Mac form, with the
// air macOS 26 leaves around the controls inside it.
func group(c *ui.Context, fn func()) ui.Element {
	t := c.Theme()
	return ui.Column(c).Padding(18, 20).Gap(12).Radius(12).
		Background(t.Surface).Children(fn)
}

// sectionLabel is the small heading above a group or a group of rows.
func sectionLabel(c *ui.Context, s string) ui.Element {
	return ui.Text(c, s).FontSize(11).Bold().LetterSpacing(0.3).TextColor(c.Theme().TextMuted)
}

// groupTitle names a group of settings inside its surface.
func groupTitle(c *ui.Context, s string) ui.Element {
	return ui.Text(c, s).FontSize(13).Bold()
}

// dot is the small filled circle that marks a source.
func dot(c *ui.Context, color ui.Color, size float32) ui.Element {
	return ui.Box(c).Size(size, size).Radius(size / 2).Background(color)
}

// glassPill is a Liquid Glass control with an icon and a label: tinted for
// a primary action, as a save is. A disabled pill keeps its size and place,
// so turning a page dirty never moves the layout.
func glassPill(c *ui.Context, icon *ui.SVG, label string, tint ui.Color, enabled bool, action func()) ui.Element {
	t := c.Theme()
	g := glass.Glass{Interactive: enabled}
	fg := t.Text
	if enabled && tint.A > 0 {
		g.Tint = tint
		fg = t.AccentText
	}
	b := ui.Row(c).Gap(6).Padding(6, 14).Radius(999).AlignItems(ui.Center).
		Material(g).Label(label).Tooltip(label)
	if enabled {
		b.Cursor(ui.CursorPointer)
	} else {
		b.Cursor(ui.CursorDefault).Opacity(0.4)
	}
	b.Children(func() {
		ui.Icon(c, icon).FontSize(13).TextColor(fg)
		ui.Text(c, label).FontSize(12).Bold().TextColor(fg)
	})
	if enabled && b.Clicked() {
		action()
	}
	return b
}

// glassAction is a round control of Liquid Glass, as Messages draws the
// buttons on either side of its composer.
func glassAction(c *ui.Context, icon *ui.SVG, label string, enabled bool, tint ui.Color, action func()) ui.Element {
	t := c.Theme()
	g := glass.Glass{Interactive: enabled}
	fg := t.Text
	if tint.A > 0 {
		g.Tint = tint
		fg = t.AccentText
	} else {
		// A light fill, so the circle reads without an outline.
		g.Tint = t.Text.Alpha(0.07)
	}
	b := ui.Box(c).Size(32, 32).Radius(16).Center().Material(g).
		Label(label).Tooltip(label)
	if enabled {
		b.Cursor(ui.CursorPointer)
	} else {
		b.Cursor(ui.CursorDefault).Opacity(0.4)
	}
	b.Children(func() {
		ui.Icon(c, icon).FontSize(15).TextColor(fg)
	})
	if enabled && b.Clicked() {
		action()
	}
	return b
}
