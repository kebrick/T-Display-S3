Каталог: examples/MacCpuUsbGauge/host/statsfeed/

go.mod / go.sum — go.bug.st/serial, gopsutil/v4 (температура: `sensors`; на Mac нужен **gopsutil v4.26.4+**, иначе на Apple Silicon возможен краш рантайма при частых вызовах). Сборка: **Go 1.24+**.
main.go — раз в -i (по умолчанию 250 ms) шлёт строку
cpu%,ram%,load1m,disk%,cpuTempC,rx_mbps,tx_mbps\n
(последние два поля — суммарная скорость приёма/передачи по всем интерфейсам кроме loopback, в Мбит/с по дельте счётчиков.)
На прошивке экраны: 0 CPU+RAM, 1 LOAD+DISK, 2 CPU+TEMP, 3 пики сессии (pk/rk), 4 сеть (DN/UP).
Диск: на Windows — %SystemDrive%\ (часто C:\), иначе /.
Load: через gopsutil (на Windows часто 0 — это нормально).
На дисплее цвет цифр при высоких значениях задаётся в прошивке (`CPU_WARN_PCT`, `RAM_WARN_PCT`, `DISK_WARN_PCT`, `TEMP_WARN_C` в `MacCpuUsbGauge.ino`).
Команды:

cd examples/MacCpuUsbGauge/host/statsfeed
go build -o statsfeed .
./statsfeed -list
./statsfeed -port COM5
./statsfeed -once
./statsfeed -quiet
./statsfeed -smooth 0.35
./statsfeed -list-esp