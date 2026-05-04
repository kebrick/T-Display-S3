//go:build (darwin && !ios) || windows

package main

import (
	"bytes"
	"fmt"
	"image"
	"log"
	"image/color"
	"image/png"
	"math"
	"strconv"
	"strings"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/app"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/driver/desktop"
	"fyne.io/fyne/v2/widget"
)

// appIconPNG — иконка «шкала + USB»: тёмный фон, дуга, игла, разъём (читаемо в 16–128 px в трее).
func appIconPNG() []byte {
	const s = 128
	img := image.NewRGBA(image.Rect(0, 0, s, s))
	bg := color.RGBA{24, 32, 48, 255}
	for y := 0; y < s; y++ {
		for x := 0; x < s; x++ {
			img.Set(x, y, bg)
		}
	}
	cx, cy := s/2, s/2+6
	r := 44
	ac := color.RGBA{0, 190, 210, 255}
	setThick := func(x, y int, c color.RGBA) {
		for dy := -1; dy <= 1; dy++ {
			for dx := -1; dx <= 1; dx++ {
				px, py := x+dx, y+dy
				if px >= 0 && px < s && py >= 0 && py < s {
					img.Set(px, py, c)
				}
			}
		}
	}
	for deg := 200.0; deg <= 340.0; deg += 0.4 {
		rad := deg * math.Pi / 180
		x := int(float64(cx) + float64(r)*math.Cos(rad))
		y := int(float64(cy) - float64(r)*math.Sin(rad))
		setThick(x, y, ac)
	}
	needle := 232.0 * math.Pi / 180
	nl := 36
	nc := color.RGBA{255, 145, 40, 255}
	for t := 2; t < nl; t++ {
		x := int(float64(cx) + float64(t)*math.Cos(needle))
		y := int(float64(cy) - float64(t)*math.Sin(needle))
		setThick(x, y, nc)
	}
	usb := color.RGBA{235, 238, 248, 255}
	for y := cy + 18; y < cy+46; y++ {
		for x := cx - 10; x < cx+10; x++ {
			if x >= 0 && x < s && y >= 0 && y < s {
				img.Set(x, y, usb)
			}
		}
	}
	for y := cy + 18; y < cy+32; y++ {
		for x := cx - 6; x < cx+6; x++ {
			if x >= 0 && x < s && y >= 0 && y < s {
				img.Set(x, y, bg)
			}
		}
	}
	var buf bytes.Buffer
	_ = png.Encode(&buf, img)
	return buf.Bytes()
}

func openSettingsWindow(a fyne.App, rt *feedRuntime) {
	snap := rt.snapshot()
	portE := widget.NewEntry()
	portE.SetText(snap.portHint)
	portE.SetPlaceHolder("пусто — авто (ESP32 / usbmodem)")

	baudE := widget.NewEntry()
	baudE.SetText(strconv.Itoa(snap.baud))

	intervalMsE := widget.NewEntry()
	intervalMsE.SetText(strconv.FormatInt(snap.interval.Milliseconds(), 10))

	smoothE := widget.NewEntry()
	smoothE.SetText(strconv.FormatFloat(snap.smooth, 'f', -1, 64))

	warnCPUE := widget.NewEntry()
	warnCPUE.SetText(strconv.FormatFloat(snap.WarnCPU, 'f', -1, 64))
	warnRAME := widget.NewEntry()
	warnRAME.SetText(strconv.FormatFloat(snap.WarnRAM, 'f', -1, 64))
	warnDiskE := widget.NewEntry()
	warnDiskE.SetText(strconv.FormatFloat(snap.WarnDisk, 'f', -1, 64))
	warnTempE := widget.NewEntry()
	warnTempE.SetText(strconv.FormatFloat(snap.WarnTemp, 'f', -1, 64))
	netMaxE := widget.NewEntry()
	netMaxE.SetText(strconv.FormatFloat(snap.NetMaxMbps, 'f', -1, 64))
	hostE := widget.NewEntry()
	hostE.SetText(snap.HostLabel)
	hostE.SetPlaceHolder("пусто = имя ПК; «-» без подписи внизу экрана")

	startPageE := widget.NewEntry()
	if snap.StartPage == 0 {
		startPageE.SetText("0")
	} else {
		startPageE.SetText(strconv.Itoa(snap.StartPage))
	}
	startPageE.SetPlaceHolder("0 = не слать; 1…5 экран при подключении")

	hint := widget.NewLabel("USB CDC: 12 полей, H …, команды P n / R на плате. Экран при старте — из NVS (кнопки) или P с хоста. «Сохранить» при смене порта — переподключение.")
	hint.Wrapping = fyne.TextWrapWord

	form := widget.NewForm(
		widget.NewFormItem("Порт", portE),
		widget.NewFormItem("Скорость (baud)", baudE),
		widget.NewFormItem("Интервал (мс)", intervalMsE),
		widget.NewFormItem("Сглаживание smooth", smoothE),
		widget.NewFormItem("Порог CPU %", warnCPUE),
		widget.NewFormItem("Порог RAM %", warnRAME),
		widget.NewFormItem("Порог диск %", warnDiskE),
		widget.NewFormItem("Порог темп. °C", warnTempE),
		widget.NewFormItem("Шкала сети max (Мбит/с)", netMaxE),
		widget.NewFormItem("Подпись на плате", hostE),
		widget.NewFormItem("Экран при USB (1…5, 0=авто)", startPageE),
	)

	win := a.NewWindow("Параметры statsfeed")
	resetPeaks := widget.NewButton("Сброс пиков pk/rk", func() {
		rt.EnqueueBoardLine("R\n")
		dialog.ShowInformation("Команда", "Отправлена команда сброса пиков (R) на следующий тик USB.", win)
	})
	save := widget.NewButton("Сохранить", func() {
		baud, err := strconv.Atoi(strings.TrimSpace(baudE.Text))
		if err != nil || baud < 300 {
			dialog.ShowError(fmt.Errorf("некорректный baud"), win)
			return
		}
		ms, err := strconv.ParseInt(strings.TrimSpace(intervalMsE.Text), 10, 64)
		if err != nil || ms < 50 {
			dialog.ShowError(fmt.Errorf("интервал не меньше 50 мс"), win)
			return
		}
		sm, err := strconv.ParseFloat(strings.TrimSpace(smoothE.Text), 64)
		if err != nil || sm < 0 || sm > 1 {
			dialog.ShowError(fmt.Errorf("smooth: число от 0 до 1"), win)
			return
		}
		wc, err := strconv.ParseFloat(strings.TrimSpace(warnCPUE.Text), 64)
		if err != nil || wc < 1 || wc > 100 {
			dialog.ShowError(fmt.Errorf("порог CPU: 1…100"), win)
			return
		}
		wr, err := strconv.ParseFloat(strings.TrimSpace(warnRAME.Text), 64)
		if err != nil || wr < 1 || wr > 100 {
			dialog.ShowError(fmt.Errorf("порог RAM: 1…100"), win)
			return
		}
		wd, err := strconv.ParseFloat(strings.TrimSpace(warnDiskE.Text), 64)
		if err != nil || wd < 1 || wd > 100 {
			dialog.ShowError(fmt.Errorf("порог диск: 1…100"), win)
			return
		}
		wt, err := strconv.ParseFloat(strings.TrimSpace(warnTempE.Text), 64)
		if err != nil || wt < 1 || wt > 125 {
			dialog.ShowError(fmt.Errorf("порог температуры: 1…125"), win)
			return
		}
		nm, err := strconv.ParseFloat(strings.TrimSpace(netMaxE.Text), 64)
		if err != nil || nm < 1 || nm > 10000 {
			dialog.ShowError(fmt.Errorf("шкала сети: 1…10000 Мбит/с"), win)
			return
		}
		sp, err := strconv.Atoi(strings.TrimSpace(startPageE.Text))
		if err != nil || sp < 0 || sp > 5 {
			dialog.ShowError(fmt.Errorf("экран при USB: 0 или 1…5"), win)
			return
		}
		next := feedConfig{
			portHint:   strings.TrimSpace(portE.Text),
			baud:       baud,
			interval:   time.Duration(ms) * time.Millisecond,
			smooth:     sm,
			WarnCPU:    wc,
			WarnRAM:    wr,
			WarnDisk:   wd,
			WarnTemp:   wt,
			NetMaxMbps: nm,
			HostLabel:  strings.TrimSpace(hostE.Text),
			StartPage:  sp,
		}
		rt.apply(next)
		dialog.ShowInformation("Сохранено", "Настройки применены; при необходимости порт переподключится.", win)
	})
	closeBtn := widget.NewButton("Закрыть", func() { win.Close() })
	btns := container.NewHBox(save, resetPeaks, closeBtn)

	win.SetContent(container.NewVBox(hint, widget.NewSeparator(), form, btns))
	win.Resize(fyne.NewSize(520, 560))
	win.CenterOnScreen()
	win.Show()
}

func runDesktopBlocking(onQuit func(), rt *feedRuntime) {
	hideConsoleWindow()

	a := app.NewWithID("dev.tdisplay.statsfeed")
	icon := fyne.NewStaticResource("statsfeed.png", appIconPNG())
	a.SetIcon(icon)

	master := a.NewWindow("statsfeed")
	master.SetMaster()
	master.Resize(fyne.NewSize(1, 1))
	master.SetFixedSize(true)
	master.Show()
	master.Hide()

	desk, ok := a.(desktop.App)
	if !ok {
		onQuit()
		log.Fatal("statsfeed: драйвер Fyne без системного трея — запустите с -foreground")
	}
	menu := fyne.NewMenu("statsfeed",
		fyne.NewMenuItem("Параметры…", func() {
			openSettingsWindow(a, rt)
		}),
		fyne.NewMenuItemSeparator(),
		fyne.NewMenuItem("Выход", func() {
			onQuit()
			a.Quit()
		}),
	)
	desk.SetSystemTrayMenu(menu)
	desk.SetSystemTrayIcon(icon)

	master.SetOnClosed(func() {
		onQuit()
	})

	a.Run()
}
