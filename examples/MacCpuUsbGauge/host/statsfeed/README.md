Каталог: examples/MacCpuUsbGauge/host/statsfeed/

go.mod / go.sum — go.bug.st/serial, gopsutil/v4 (температура: `sensors`; на Mac нужен **gopsutil v4.26.4+**, иначе на Apple Silicon возможен краш рантайма при частых вызовах), **fyne.io/fyne/v2** (трей и окно **«Параметры…»** на **macOS** и **Windows**). Сборка: **Go 1.24+**, для трея на Mac/Win нужен **CGO**.
main.go — раз в -i (по умолчанию 250 ms) шлёт строку
cpu%,ram%,load1m,disk%,cpuTempC,rx_mbps,tx_mbps\n
(последние два поля — суммарная скорость приёма/передачи по всем интерфейсам кроме loopback, в Мбит/с по дельте счётчиков.)
На прошивке экраны: 0 CPU+RAM, 1 LOAD+DISK, 2 CPU+TEMP, 3 пики сессии (pk/rk), 4 сеть (DN/UP).
Диск: на Windows — %SystemDrive%\ (часто C:\), иначе /.
Load: через gopsutil (на Windows часто 0 — это нормально).
Температура CPU на **Windows**: встроенный WMI `root\wmi` часто без данных; statsfeed дополнительно читает **LibreHardwareMonitor** или **Open Hardware Monitor**, если они запущены с включённым WMI (`ROOT\LibreHardwareMonitor` / `ROOT\OpenHardwareMonitor`). Иначе в строке протокола будет **-1** (на дисплее как «нет данных»).
На дисплее цвет цифр при высоких значениях задаётся в прошивке (`CPU_WARN_PCT`, `RAM_WARN_PCT`, `DISK_WARN_PCT`, `TEMP_WARN_C` в `MacCpuUsbGauge.ino`).
Команды:

```
cd examples/MacCpuUsbGauge/host/statsfeed
go build -o statsfeed .
./statsfeed -list
./statsfeed -port COM5
./statsfeed -once
./statsfeed -quiet
./statsfeed -smooth 0.35
./statsfeed -list-esp
./statsfeed -foreground
```

На **macOS** и **Windows** при обычном запуске (без **`-foreground`**) сразу **трей** (иконка шкалы/USB) и меню: **«Параметры…»** — окно с полями порт, baud, интервал (мс), smooth (пока без файла на диске; сохранение меняет работу потока и при необходимости переподключает порт), **«Выход»** — завершение. На Windows консоль после старта **скрывается**. **`-foreground`** — только терминал (Ctrl+C, логи). На **Linux** трея нет.

- **macOS**: **CGO** / Xcode CLT. Запуск из **Terminal** оставляет окно терминала; полностью без окон — **.app** с `LSUIElement=true` или **LaunchAgent**.