package tui

import (
	"time"

	"github.com/dundee/gdu/v5/internal/common"
	"github.com/dundee/gdu/v5/pkg/path"
)

func (ui *UI) updateProgress() {
	color := "[white:black:b]"
	if ui.UseColors {
		color = "[red:black:b]"
	}

	start := time.Now()

	update := func() {
		ui.app.QueueUpdateDraw(func() {
			delta := time.Since(start).Round(time.Second)
			progress := ui.Analyzer.GetCurrentProgress()
			ui.progress.SetText("Total items: " +
				color +
				common.FormatNumber(int64(progress.ItemCount)) +
				"[white:black:-], size: " +
				color +
				ui.formatSize(progress.TotalSize, false, false) +
				"[white:black:-], elapsed time: " +
				color +
				delta.String() +
				"[white:black:-]\nCurrent item: [white:black:b]" +
				path.ShortenPath(progress.CurrentItemName, ui.currentItemNameMaxLen))
		})
	}
	update() // Update once before waiting for the ticker to fire.

	done := ui.Analyzer.GetDone()
	tick := time.NewTicker(100 * time.Millisecond)
	defer tick.Stop()
	for {
		select {
		case <-done:
			ui.app.QueueUpdateDraw(func() {
				ui.progress.SetTitle(" Finalizing... ")
				ui.progress.SetText("Calculating disk usage...")
			})
			return
		case <-tick.C:
			update()
		}
	}
}
