// Кроссплатформенная отправка метрик на T-Display-S3 по USB CDC.
// Формат: cpu%,ram%,load1m,disk%,cpuTempC,rx_mbps,tx_mbps\n
//
// При отключении USB не выходим: закрываем порт, ждём, снова открываем.
//
// Сборка: go build -o statsfeed .
// Запуск: ./statsfeed  или  statsfeed.exe -port COM5
// Протокол: см. README — 12 полей метрик + строка H …; rx/tx в мегабит/с.
// Авто-порт: только USB CDC с признаками платы (VID Espressif 303A и т.д.), не первый /dev из списка.
// -once: одна строка в stdout (+ запись в -port если задан). -quiet: меньше служебных логов.
// -smooth: EMA для CPU/RAM/load/disk (0=выкл). -list-esp: только VID 303A.
// macOS/Windows: по умолчанию трей (Fyne) + окно «Параметры…»; -foreground — только терминал.
// macOS: при трее процесс перезапускается в фоне — окно Terminal после старта закрывается; логи в ~/Library/Logs/statsfeed.log.

package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/shirou/gopsutil/v4/cpu"
	"github.com/shirou/gopsutil/v4/disk"
	"github.com/shirou/gopsutil/v4/load"
	"github.com/shirou/gopsutil/v4/mem"
	netutil "github.com/shirou/gopsutil/v4/net"
	"github.com/shirou/gopsutil/v4/sensors"
	serial "go.bug.st/serial"
	"go.bug.st/serial/enumerator"
)

const (
	reconnectDelay = 1500 * time.Millisecond
	noPortDelay    = 2 * time.Second
	// На macOS Write в отключённый USB CDC часто не даёт ошибку — смотрим список портов и короткий Read.
	portVerifyInterval = 350 * time.Millisecond
	portAbsentTicks    = 2
	readProbeTimeout   = 25 * time.Millisecond
	// На darwin/arm64 частые вызовы sensors + старый gopsutil давали SIGSEGV; v4.26+ чинит IOKit, кэш снижает нагрузку.
	tempPollMin = 2 * time.Second
)

// trendEMA — экспоненциальное сглаживание строки на хосте (меньше дёргания дуг на дисплее).
type trendEMA struct {
	ok                   bool
	cpu, ram, load, disk float64
}

func (t *trendEMA) smooth(alpha float64, cpu, ram, load1, diskPct float64) (float64, float64, float64, float64) {
	if alpha <= 0 {
		return cpu, ram, load1, diskPct
	}
	if !t.ok {
		t.cpu, t.ram, t.load, t.disk = cpu, ram, load1, diskPct
		t.ok = true
		return cpu, ram, load1, diskPct
	}
	t.cpu = alpha*cpu + (1-alpha)*t.cpu
	t.ram = alpha*ram + (1-alpha)*t.ram
	t.load = alpha*load1 + (1-alpha)*t.load
	t.disk = alpha*diskPct + (1-alpha)*t.disk
	return t.cpu, t.ram, t.load, t.disk
}

var (
	tempMu     sync.Mutex
	tempCached float64 = -1
	tempAt     time.Time
	quiet      bool
	trend      trendEMA

	netBpsPrevR, netBpsPrevT uint64
	netBpsAt               time.Time
	netBpsInit             bool
)

func vlogf(format string, args ...interface{}) {
	if !quiet {
		log.Printf(format, args...)
	}
}

// sleepOrCtx: true — ctx отменён, пора выйти из цикла.
func sleepOrCtx(ctx context.Context, d time.Duration) bool {
	select {
	case <-ctx.Done():
		return true
	case <-time.After(d):
		return false
	}
}

type feedConfig struct {
	portHint string
	baud     int
	interval time.Duration
	smooth   float64
	// Пороги и шкала сети для дисплея (12-е поле протокола).
	WarnCPU, WarnRAM, WarnDisk, WarnTemp float64
	NetMaxMbps                         float64
	// HostLabel: пусто — os.Hostname(); "-" — не слать строку H … на плату.
	HostLabel string
	// StartPage: 1…5 — после подключения USB отправить "P n\n"; 0 — не слать (экран из NVS на плате).
	StartPage int
}

func defaultBoardConfig() feedConfig {
	return feedConfig{
		WarnCPU: 85, WarnRAM: 88, WarnDisk: 92, WarnTemp: 85,
		NetMaxMbps: 100,
		HostLabel:  "",
		StartPage:  0,
	}
}

func serialHostPrefix(cfg feedConfig) []byte {
	if strings.TrimSpace(cfg.HostLabel) == "-" {
		return nil
	}
	name := strings.TrimSpace(cfg.HostLabel)
	if name == "" {
		h, err := os.Hostname()
		if err != nil || h == "" {
			return nil
		}
		name = h
	}
	name = strings.ReplaceAll(name, "\n", " ")
	name = strings.ReplaceAll(name, ",", " ")
	rn := []rune(name)
	if len(rn) > 32 {
		name = string(rn[:32])
	}
	return []byte("H " + name + "\n")
}

func sumNonLoopbackBytes() (recv uint64, sent uint64, err error) {
	stats, err := netutil.IOCounters(true)
	if err != nil {
		return 0, 0, err
	}
	for _, s := range stats {
		n := strings.ToLower(s.Name)
		if n == "lo" || strings.HasPrefix(n, "loopback") {
			continue
		}
		recv += s.BytesRecv
		sent += s.BytesSent
	}
	return recv, sent, nil
}

// netMbpsSince: сумма не-loopback интерфейсов, Мбит/с по дельте между вызовами.
func netMbpsSince() (rx float64, tx float64) {
	r, t, err := sumNonLoopbackBytes()
	now := time.Now()
	if err != nil {
		return 0, 0
	}
	if !netBpsInit {
		netBpsPrevR, netBpsPrevT = r, t
		netBpsAt = now
		netBpsInit = true
		return 0, 0
	}
	dt := now.Sub(netBpsAt).Seconds()
	if dt < 0.05 {
		return 0, 0
	}
	var dr, ds float64
	if r >= netBpsPrevR {
		dr = float64(r - netBpsPrevR)
	}
	if t >= netBpsPrevT {
		ds = float64(t - netBpsPrevT)
	}
	netBpsPrevR, netBpsPrevT, netBpsAt = r, t, now
	rx = (dr * 8.0) / (dt * 1e6)
	tx = (ds * 8.0) / (dt * 1e6)
	if rx < 0 {
		rx = 0
	}
	if tx < 0 {
		tx = 0
	}
	return rx, tx
}

// collectSampleLine одна строка протокола; primeCPU — первый раз вызвать с паузой для cpu.Percent.
// emaAlpha: 0 = без сглаживания; 0.2–0.45 обычно достаточно (см. -smooth).
// board задаёт 5 дополнительных полей (пороги + net_max Мбит/с) для прошивки с 12 полями.
func collectSampleLine(primeCPU *bool, emaAlpha float64, board feedConfig) (string, error) {
	if !*primeCPU {
		_, _ = cpu.Percent(150*time.Millisecond, false)
		*primeCPU = true
	}
	cpuPct, err := cpu.Percent(0, false)
	if err != nil || len(cpuPct) == 0 {
		return "", err
	}
	var sum float64
	for _, v := range cpuPct {
		sum += v
	}
	cpuVal := sum / float64(len(cpuPct))

	vm, err := mem.VirtualMemory()
	if err != nil {
		return "", err
	}

	avg, err := load.Avg()
	load1 := 0.0
	if err == nil && avg != nil {
		load1 = avg.Load1
	}

	path := "/"
	if runtime.GOOS == "windows" {
		path = `C:\`
		if s := os.Getenv("SystemDrive"); s != "" {
			path = s + `\`
		}
	}
	du, err := disk.Usage(path)
	diskPct := 0.0
	if err == nil && du != nil {
		diskPct = du.UsedPercent
	}

	tempC := pickCPUTempC()
	cOut, rOut, lOut, dOut := trend.smooth(emaAlpha, cpuVal, vm.UsedPercent, load1, diskPct)
	rxM, txM := netMbpsSince()
	return fmt.Sprintf("%.1f,%.1f,%.2f,%.1f,%.1f,%.2f,%.2f,%.1f,%.1f,%.1f,%.1f,%.1f\n",
		cOut, rOut, lOut, dOut, tempC, rxM, txM,
		board.WarnCPU, board.WarnRAM, board.WarnDisk, board.WarnTemp, board.NetMaxMbps), nil
}

func main() {
	list := flag.Bool("list", false, "list serial ports and exit")
	listEsp := flag.Bool("list-esp", false, "list serial ports with USB VID 303A (Espressif) only")
	portFlag := flag.String("port", "", "serial device; empty = auto (Espressif VID 303A / usbmodem, not arbitrary tty)")
	baud := flag.Int("baud", 115200, "baud rate")
	interval := flag.Duration("i", 250*time.Millisecond, "update interval")
	once := flag.Bool("once", false, "print one CSV line to stdout and exit (if -port set, also write to serial once)")
	smooth := flag.Float64("smooth", 0, "EMA alpha for CPU/RAM/load/disk on wire (0=off, try 0.25-0.4)")
	foreground := flag.Bool("foreground", false, "macOS/Windows: stay in terminal/console (no tray); Linux: no effect")
	flag.BoolVar(&quiet, "quiet", false, "fewer log lines (errors and reconnect still logged)")
	flag.Parse()

	if *list || *listEsp {
		ports, err := enumerator.GetDetailedPortsList()
		if err != nil {
			log.Fatal(err)
		}
		for _, p := range ports {
			if *listEsp && vidUpper(p) != "303A" {
				continue
			}
			fmt.Printf("%s\t%s\t%s\n", p.Name, p.VID, p.PID)
		}
		return
	}

	cfg := defaultBoardConfig()
	cfg.portHint = strings.TrimSpace(*portFlag)
	cfg.baud = *baud
	cfg.interval = *interval
	cfg.smooth = *smooth
	rt := newFeedRuntime(cfg)

	mode := &serial.Mode{BaudRate: cfg.baud, DataBits: 8, Parity: serial.NoParity, StopBits: serial.OneStopBit}
	cpuPrimed := false

	if *once {
		line, err := collectSampleLine(&cpuPrimed, cfg.smooth, cfg)
		if err != nil {
			log.Fatal(err)
		}
		fmt.Print(line)
		port := cfg.portHint
		if port == "" {
			port = pickSerialPort()
		}
		if port != "" {
			p, err := serial.Open(port, mode)
			if err != nil {
				log.Fatalf("open %s: %v", port, err)
			}
			_, _ = p.Write([]byte(line))
			_ = p.Close()
		}
		return
	}

	useTray := (runtime.GOOS == "darwin" || runtime.GOOS == "windows") && !*foreground
	if useTray {
		maybeDetachTrayDarwin()
		if isTrayDetachedChild() {
			signal.Ignore(syscall.SIGHUP)
		}
	}
	if !ensureSingleRunningInstance() {
		exitIfAlreadyRunning()
	}
	defer releaseInstanceLock()

	if useTray {
		setupTrayLogForGUI()
		vlogf("режим трея: иконка statsfeed; «Параметры…» — настройки; «Выход» — завершение.")
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		go runSerialFeed(ctx, rt)
		runDesktopBlocking(cancel, rt)
		return
	}

	vlogf("Ctrl+C to exit. On USB disconnect we wait and reconnect.")
	runSerialFeed(context.Background(), rt)
}

func runSerialFeed(ctx context.Context, rt *feedRuntime) {
	cpuPrimed := false

	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		cfg := rt.snapshot()
		mode := &serial.Mode{BaudRate: cfg.baud, DataBits: 8, Parity: serial.NoParity, StopBits: serial.OneStopBit}
		port := cfg.portHint
		if port == "" {
			port = pickSerialPort()
		}
		if port == "" {
			vlogf("no serial port, retry in %v", noPortDelay)
			if sleepOrCtx(ctx, noPortDelay) {
				return
			}
			continue
		}

		p, err := serial.Open(port, mode)
		if err != nil {
			log.Printf("open %s: %v — retry in %v", port, err, reconnectDelay)
			if sleepOrCtx(ctx, reconnectDelay) {
				return
			}
			continue
		}

		openRev := rt.revSnapshot()

		trend = trendEMA{}
		netBpsInit = false
		primeCfg := rt.snapshot()
		_, _ = collectSampleLine(&cpuPrimed, primeCfg.smooth, primeCfg)
		_ = p.ResetInputBuffer()
		if sp := primeCfg.StartPage; sp >= 1 && sp <= 5 {
			_, _ = p.Write([]byte(fmt.Sprintf("P %d\n", sp)))
		}
		openedName := port
		_ = p.SetReadTimeout(readProbeTimeout)
		vlogf("connected %s @ %d", openedName, cfg.baud)

		disconnected := false
		lastPortVerify := time.Now()
		portAbsent := 0
		for !disconnected {
			select {
			case <-ctx.Done():
				disconnected = true
			default:
			}
			if disconnected {
				break
			}

			if rt.revSnapshot() != openRev {
				vlogf("настройки изменены — переподключение")
				disconnected = true
				break
			}

			if time.Since(lastPortVerify) >= portVerifyInterval {
				lastPortVerify = time.Now()
				if serialReadSaysClosed(p) || serialPortProbeHWDead(p) {
					log.Printf("serial: link lost (read/HW probe) — reconnect")
					disconnected = true
					break
				}
				if !portInEnumerator(openedName) {
					portAbsent++
					if portAbsent >= portAbsentTicks {
						log.Printf("port %s not in list — reconnect", openedName)
						disconnected = true
						break
					}
				} else {
					portAbsent = 0
				}
			}

			cfgLive := rt.snapshot()
			line, err := collectSampleLine(&cpuPrimed, cfgLive.smooth, cfgLive)
			if err != nil {
				vlogf("sample: %v", err)
				if sleepOrCtx(ctx, cfgLive.interval) {
					disconnected = true
					break
				}
				continue
			}
			if extra := rt.takePendingBoard(); len(extra) > 0 {
				_, _ = p.Write(extra)
			}
			if px := serialHostPrefix(cfgLive); len(px) > 0 {
				_, _ = p.Write(px)
			}
			if _, err := p.Write([]byte(line)); err != nil {
				log.Printf("write: %v — reconnect", err)
				disconnected = true
				break
			}
			if sleepOrCtx(ctx, cfgLive.interval) {
				disconnected = true
				break
			}
		}

		_ = p.Close()
		if sleepOrCtx(ctx, reconnectDelay) {
			return
		}
	}
}

func pickCPUTempC() float64 {
	tempMu.Lock()
	defer tempMu.Unlock()
	if time.Since(tempAt) < tempPollMin {
		return tempCached
	}
	tempAt = time.Now()
	tempCached = readCPUTempCSensors()
	return tempCached
}

func readCPUTempCSensors() (v float64) {
	v = -1
	defer func() {
		if recover() != nil {
			v = -1
		}
	}()
	// v4/sensors: darwin/arm64 без CGO (IOKit). Нужен gopsutil >= v4.26.4 — иначе dlopen/dlclose на каждом тике и краш рантайма.
	temps, err := sensors.SensorsTemperatures()
	if err != nil || len(temps) == 0 {
		return -1
	}
	for _, t := range temps {
		k := strings.ToLower(t.SensorKey)
		if strings.Contains(k, "cpu") || strings.Contains(k, "core") || strings.Contains(k, "package") ||
			strings.Contains(k, "k10temp") || strings.Contains(k, "acpitz") ||
			(runtime.GOOS == "windows" && (strings.Contains(k, "acpi") || strings.Contains(k, "thermal") || strings.Contains(k, "tz."))) {
			if t.Temperature > 1 && t.Temperature < 125 {
				return t.Temperature
			}
		}
	}
	for _, t := range temps {
		if t.Temperature > 1 && t.Temperature < 125 {
			return t.Temperature
		}
	}
	if t := readWindowsAuxiliaryCPUTemp(); t > 0 {
		return t
	}
	return -1
}

// normalizeSerialPortName убирает пробелы и префикс \\.\ (Windows, COM10+ и ручной ввод).
func normalizeSerialPortName(name string) string {
	s := strings.TrimSpace(name)
	if runtime.GOOS == "windows" {
		s = strings.TrimPrefix(s, `\\.\`)
	}
	return s
}

// serialPortNamesMatch сравнивает имя открытого порта с записью из enumerator (Windows: COM без учёта регистра).
func serialPortNamesMatch(a, b string) bool {
	na := normalizeSerialPortName(a)
	nb := normalizeSerialPortName(b)
	if runtime.GOOS == "windows" {
		return strings.EqualFold(na, nb)
	}
	return na == nb
}

// portInEnumerator: при отключении USB узел /dev/cu… исчезает, а Write может не вернуть ошибку.
func portInEnumerator(name string) bool {
	ports, err := enumerator.GetDetailedPortsList()
	if err != nil {
		return true
	}
	for _, p := range ports {
		if p != nil && serialPortNamesMatch(name, p.Name) {
			return true
		}
	}
	return false
}

// serialReadSaysClosed: короткий Read с таймаутом; при отвале USB на Windows драйвер часто даёт ошибку без PortClosed.
func serialReadSaysClosed(p serial.Port) bool {
	buf := make([]byte, 64)
	n, err := p.Read(buf)
	if err != nil {
		return true
	}
	_ = n
	return false
}

func isIgnoredSerialPort(name string) bool {
	n := strings.ToLower(name)
	// Не системные / виртуальные TTY (раньше при отсутствии платы брался ports[0] → debug-console).
	ignore := []string{
		"debug-console",
		"bluetooth",
		"bthmodem",
		"wlan",
	}
	for _, s := range ignore {
		if strings.Contains(n, s) {
			return true
		}
	}
	return false
}

func vidUpper(p *enumerator.PortDetails) string {
	return strings.ToUpper(strings.TrimSpace(p.VID))
}

// pickSerialPort выбирает порт для T-Display-S3 (ESP32-S3 USB CDC). Без слепого ports[0].
func pickSerialPort() string {
	ports, err := enumerator.GetDetailedPortsList()
	if err != nil || len(ports) == 0 {
		return ""
	}
	var espUSBModem []string
	var espOther []string
	var usbModem []string
	var usbClassic []string
	for _, p := range ports {
		if p == nil || isIgnoredSerialPort(p.Name) {
			continue
		}
		nameL := strings.ToLower(p.Name)
		vid := vidUpper(p)
		if p.IsUSB && vid == "303A" {
			if strings.Contains(nameL, "usbmodem") {
				espUSBModem = append(espUSBModem, p.Name)
			} else {
				espOther = append(espOther, p.Name)
			}
			continue
		}
		if strings.Contains(nameL, "usbmodem") {
			usbModem = append(usbModem, p.Name)
			continue
		}
		if strings.Contains(nameL, "usbserial") || strings.Contains(nameL, "ttyacm") || strings.Contains(nameL, "ttyusb") {
			usbClassic = append(usbClassic, p.Name)
		}
	}
	if len(espUSBModem) > 0 {
		return espUSBModem[0]
	}
	if len(espOther) > 0 {
		return espOther[0]
	}
	if len(usbModem) > 0 {
		return usbModem[0]
	}
	if len(usbClassic) > 0 {
		return usbClassic[0]
	}
	for _, p := range ports {
		if p == nil || isIgnoredSerialPort(p.Name) {
			continue
		}
		u := strings.ToUpper(strings.TrimSpace(p.Name))
		if strings.HasPrefix(u, "COM") {
			return p.Name
		}
	}
	return ""
}
