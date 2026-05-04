//go:build darwin && !ios

package main

import (
	"encoding/base64"

	"fyne.io/systray"
)

// Минимальная PNG-иконка для строки меню (без внешних файлов).
const trayIconPNG64 = "iVBORw0KGgoAAAANSUhEUgAAABAAAAAQCAYAAAAf8/9hAAAAGklEQVQ4T2NkYGD4z0ABYBw1GEGAARgHBKMDAAv9Bj0a6QdKAAAAAElFTkSuQmCC"

func runTrayBlocking(onQuit func()) {
	systray.Run(func() {
		if raw, err := base64.StdEncoding.DecodeString(trayIconPNG64); err == nil && len(raw) > 0 {
			systray.SetIcon(raw)
		}
		systray.SetTooltip("statsfeed → USB CDC. Завершение: пункт «Выход».")
		mQuit := systray.AddMenuItem("Выход", "Остановить statsfeed и закрыть иконку")
		go func() {
			for range mQuit.ClickedCh {
				systray.Quit()
				return
			}
		}()
	}, func() {
		onQuit()
	})
}
