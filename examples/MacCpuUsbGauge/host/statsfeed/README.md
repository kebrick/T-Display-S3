Каталог: examples/MacCpuUsbGauge/host/statsfeed/

Повторный **долгий** запуск (трей, foreground, Linux) блокируется: один экземпляр на пользователя (`UserCacheDir()/statsfeed/instance.lock`). **`-list`**, **`-list-esp`**, **`-once`** не учитываются — можно вызывать параллельно со службой.

go.mod / go.sum — go.bug.st/serial, gopsutil/v4 (температура: `sensors`; на Mac нужен **gopsutil v4.26.4+**, иначе на Apple Silicon возможен краш рантайма при частых вызовах), **fyne.io/fyne/v2** (трей и окно **«Параметры…»** на **macOS** и **Windows**). Сборка: **Go 1.24+**, для трея на Mac/Win нужен **CGO**.
main.go — раз в -i (по умолчанию 250 ms) шлёт:
- строку `H …\n` (подпись на дисплее, до 32 символов; пусто в настройках = **имя ПК**; **`-`** = не слать);
- при подключении USB (если в «Параметрах» задано **1…5**): `P n\n` — тот же экран, что кнопки на плате; на прошивке сохраняется в **NVS** вместе с переключением кнопками;
- по кнопке в приложении или вручную в UART: `R\n` / `RESET_PEAKS` — сброс пиков **pk/rk**;
- строку метрик (12 полей):
`cpu%,ram%,load1m,disk%,cpuTempC,rx_Mb/s,tx_Mb/s,wcpu,wram,wdisk,wtemp,net_max_Mb/s\n`
  — `rx/tx` в **мегабит/с**; пять хвостовых полей — пороги и максимум шкалы сети (как в **«Параметры…»**). Прошивка **v2** при 7 полях ведёт себя по-старому.
На прошивке экраны: 0 CPU+RAM, 1 LOAD+DISK, 2 CPU+TEMP, 3 пики (pk/rk), 4 сеть — цифры **с подписью** `Mb/s` или `kb/s` (килобит/с).
Диск: на Windows — %SystemDrive%\ (часто C:\), иначе /.
Load: через gopsutil (на Windows часто 0 — это нормально).
Температура CPU на **Windows**: встроенный WMI `root\wmi` часто без данных; statsfeed дополнительно читает **LibreHardwareMonitor** или **Open Hardware Monitor**, если они запущены с включённым WMI (`ROOT\LibreHardwareMonitor` / `ROOT\OpenHardwareMonitor`). Иначе в строке протокола будет **-1** (на дисплее как «нет данных»).
Пороги цвета на дисплее приходят с хоста (по умолчанию как раньше 85/88/92/85); шкала дуг сети — поле **net_max** (Мбит/с).
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

На **macOS** и **Windows** при обычном запуске (без **`-foreground`**) сразу **трей** и меню **«Параметры…»** — порт, baud, интервал, smooth, пороги CPU/RAM/диск/температура, шкала сети (Мбит/с), подпись на плате; **«Выход»** — завершение. На Windows консоль после старта **скрывается**. Служебные **логи** в режиме трея: **macOS** — `~/Library/Logs/statsfeed.log`, **Windows** — `%LOCALAPPDATA%\statsfeed\statsfeed.log`. **`-foreground`** — только терминал (Ctrl+C, логи в консоль). На **Linux** трея нет.

- **macOS**: **CGO** / Xcode CLT. В режиме трея **без fork из Go**: **`syscall.Exec("/bin/sh", …)`** подставляет скрипт `nohup statsfeed … & exit` — fork делается только внутри shell (обход **`fork/exec … operation not permitted`** у процессов Go на части систем). Первые строки лога могут попасть в тот же **`~/Library/Logs/statsfeed.log`** (дозапись из `nohup`). Если окно Terminal **не закрывается** — в профиле оболочки включите **«При выходе из оболочки»: закрыть окно**, либо **.app** / Automator.