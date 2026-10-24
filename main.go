// Stilday turns a day into context: the chat writes one JSON line per
// observation to <storage>/artifacts/YYYY.jsonl, the day's records tab
// filters that year file, and an AI assistant reads it to know what its
// owner did.
//
//	go tool mygo dev
package main

import (
	"log"
	"path/filepath"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
)

func main() {
	// Keep the Pi runtime in a stable Application Support folder, rather
	// than in one named after go run's temporary executable.
	if data, err := mygo.App.Path(mygo.PathAppData); err == nil {
		mygo.App.SetPath(mygo.PathUserData, filepath.Join(data, "Stilday"))
	}
	a := newApp()
	mygo.App.WhenReady(func() {
		a.win = mygo.NewWindow(mygo.WindowOptions{
			Title:     "Stilday",
			Width:     1440,
			Height:    900,
			MinWidth:  1200,
			MinHeight: 680,
			// Opens where the user left it last time.
			StateKey: "main",
			// The view draws the title bar, so the sidebar and the content
			// reach the top edge of the window.
			TitleBarStyle: mygo.TitleBarHidden,
			// The sidebar shows the system's translucent sidebar material,
			// as Finder and Mail do. Its content draws no background.
			Vibrancy: mygo.VibrancySidebar,
			// The traffic lights sit in the upper part of the window's 64
			// DIP title bar, at the height of a standard macOS title bar,
			// to the left of the sidebar's wordmark.
			TrafficLightPosition: &mygo.Point{X: 18, Y: 20},
			Content:              ui.View(a.view),
		})
	})
	if err := mygo.App.Run(); err != nil {
		log.Fatal(err)
	}
}
