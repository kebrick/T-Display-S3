#!/usr/bin/env python3
"""
Кроссплатформенная отправка метрик на T-Display-S3 (USB CDC).
Формат: 12 полей (см. statsfeed README) + при необходимости строка H …; rx/tx в Мбит/с.

При отключении USB скрипт не завершается: пауза и повторное открытие порта.

  pip install pyserial psutil
  python mac_cpu_to_serial.py
  python mac_cpu_to_serial.py /dev/cu.usbmodem21401
  python mac_cpu_to_serial.py --once
  python mac_cpu_to_serial.py --smooth 0.35
  python mac_cpu_to_serial.py --list-esp
"""

from __future__ import annotations

import argparse
import os
import sys
import time
from typing import Any, Dict, List, Optional, Tuple

try:
    import psutil
except ImportError:
    print("pip install psutil", file=sys.stderr)
    sys.exit(1)

try:
    import serial
    from serial.tools import list_ports
    from serial.tools.list_ports import ListPortInfo
except ImportError:
    print("pip install pyserial", file=sys.stderr)
    sys.exit(1)

RECONNECT_SEC = 1.5
NO_PORT_SEC = 2.0
VID_ESPRESSIF = 0x303A

_IGNORE_NAME = ("debug-console", "bluetooth", "bthmodem", "wlan")


def _ignored_device(name: str) -> bool:
    n = name.lower()
    return any(s in n for s in _IGNORE_NAME)


def _comports() -> List[ListPortInfo]:
    return list(list_ports.comports())


def list_ports_cmd(esp_only: bool = False) -> None:
    for p in _comports():
        if esp_only and (p.vid is None or p.vid != VID_ESPRESSIF):
            continue
        vid = f"{p.vid:04X}" if p.vid is not None else ""
        pid = f"{p.pid:04X}" if p.pid is not None else ""
        print(f"{p.device}\t{vid}\t{pid}")


def try_pick_port(hint: Optional[str]) -> Optional[str]:
    """Порт для T-Display-S3: приоритет Espressif 303A + usbmodem; без слепого первого tty."""
    if hint and str(hint).strip():
        return str(hint).strip()

    ports = [p for p in _comports() if not _ignored_device(p.device)]
    if not ports:
        return None

    def has_usbmodem(p: ListPortInfo) -> bool:
        return "usbmodem" in p.device.lower()

    tier1 = [p for p in ports if p.vid == VID_ESPRESSIF and has_usbmodem(p)]
    if tier1:
        return tier1[0].device
    tier2 = [p for p in ports if p.vid == VID_ESPRESSIF]
    if tier2:
        return tier2[0].device
    tier3 = [p for p in ports if has_usbmodem(p)]
    if tier3:
        return tier3[0].device
    prefer = ("usbserial", "ttyACM", "ttyUSB")
    for key in prefer:
        for p in ports:
            if key in p.device.lower():
                return p.device
    for p in ports:
        if p.device.upper().startswith("COM"):
            return p.device
    return None


def disk_root() -> str:
    if sys.platform == "win32":
        return os.environ.get("SystemDrive", "C:") + "\\"
    return "/"


def load_avg_1m() -> float:
    if hasattr(os, "getloadavg"):
        try:
            return float(os.getloadavg()[0])
        except OSError:
            pass
    try:
        return float(psutil.getloadavg()[0])  # type: ignore[attr-defined]
    except (AttributeError, OSError):
        return 0.0


def disk_used_percent() -> float:
    try:
        return float(psutil.disk_usage(disk_root()).percent)
    except Exception:
        return 0.0


def cpu_temp_c() -> float:
    try:
        t = psutil.sensors_temperatures()
        for name, entries in t.items():
            low = name.lower()
            if "cpu" in low or "core" in low or "package" in low or "k10temp" in low:
                for e in entries:
                    c = getattr(e, "current", None)
                    if c is not None and 1 < c < 125:
                        return float(c)
        for _name, entries in t.items():
            for e in entries:
                c = getattr(e, "current", None)
                if c is not None and 1 < c < 125:
                    return float(c)
    except Exception:
        pass
    return -1.0


def sample_raw() -> Tuple[float, float, float, float, float]:
    cpu = float(psutil.cpu_percent(interval=None))
    ram = float(psutil.virtual_memory().percent)
    load1 = load_avg_1m()
    disk = disk_used_percent()
    temp = cpu_temp_c()
    return cpu, ram, load1, disk, temp


def sum_non_loopback_bytes() -> Tuple[int, int]:
    try:
        counters = psutil.net_io_counters(pernic=True)
    except Exception:
        return 0, 0
    r = t = 0
    for name, io in counters.items():
        n = str(name).lower()
        if n == "lo" or n.startswith("loopback"):
            continue
        r += int(io.bytes_recv)
        t += int(io.bytes_sent)
    return r, t


def net_mbps_since(state: Dict[str, Any]) -> Tuple[float, float]:
    """Сумма не-loopback интерфейсов, Мбит/с по дельте между вызовами."""
    br, bt = sum_non_loopback_bytes()
    now = time.monotonic()
    if not state.get("init"):
        state.clear()
        state.update(init=True, prev_r=br, prev_t=bt, prev_mono=now)
        return 0.0, 0.0
    dt = now - float(state["prev_mono"])
    if dt < 0.05:
        return 0.0, 0.0
    pr, pt = int(state["prev_r"]), int(state["prev_t"])
    dr = float(br - pr) if br >= pr else 0.0
    ds = float(bt - pt) if bt >= pt else 0.0
    state.update(prev_r=br, prev_t=bt, prev_mono=now)
    rx = (dr * 8.0) / (dt * 1e6)
    tx = (ds * 8.0) / (dt * 1e6)
    return max(0.0, rx), max(0.0, tx)


def smooth_values(
    state: Dict[str, Any], alpha: float, cpu: float, ram: float, load1: float, disk: float
) -> Tuple[float, float, float, float]:
    if alpha <= 0:
        return cpu, ram, load1, disk
    if not state.get("ok"):
        state.clear()
        state.update(ok=True, c=cpu, r=ram, l=load1, d=disk)
        return cpu, ram, load1, disk
    state["c"] = alpha * cpu + (1 - alpha) * float(state["c"])
    state["r"] = alpha * ram + (1 - alpha) * float(state["r"])
    state["l"] = alpha * load1 + (1 - alpha) * float(state["l"])
    state["d"] = alpha * disk + (1 - alpha) * float(state["d"])
    return float(state["c"]), float(state["r"]), float(state["l"]), float(state["d"])


def format_line(
    cpu: float, ram: float, load1: float, disk: float, temp: float, rx_mbps: float, tx_mbps: float
) -> str:
    # хвост: wcpu, wram, wdisk, wtemp, net_max (как в statsfeed / прошивке v2)
    return (
        f"{cpu:.1f},{ram:.1f},{load1:.2f},{disk:.1f},{temp:.1f},"
        f"{rx_mbps:.2f},{tx_mbps:.2f},85,88,92,85,100\n"
    )


def port_still_listed(device: str) -> bool:
    try:
        return any(p.device == device for p in _comports())
    except Exception:
        return True


def main() -> None:
    ap = argparse.ArgumentParser(description="CPU, RAM, load, disk, temp -> serial (reconnect)")
    ap.add_argument("port", nargs="?", default=None, help="COM3, /dev/cu.usbmodem…; пусто = авто (Espressif)")
    ap.add_argument("-b", "--baud", type=int, default=115200)
    ap.add_argument("-i", "--interval", type=float, default=0.25)
    ap.add_argument("--list", action="store_true", help="список портов")
    ap.add_argument("--list-esp", action="store_true", help="только USB VID 303A (Espressif)")
    ap.add_argument("--once", action="store_true", help="одна строка в stdout (+ запись в порт если указан)")
    ap.add_argument("--smooth", type=float, default=0.0, help="EMA alpha для CPU/RAM/load/disk (0=выкл)")
    ap.add_argument("--quiet", action="store_true", help="меньше сообщений в stderr")
    args = ap.parse_args()

    def log(msg: str) -> None:
        if not args.quiet:
            print(msg, file=sys.stderr)

    if args.list or args.list_esp:
        list_ports_cmd(esp_only=bool(args.list_esp))
        return

    trend: Dict[str, Any] = {}
    net_bps: Dict[str, Any] = {}

    psutil.cpu_percent(interval=None)
    time.sleep(0.15)

    port_hint: Optional[str] = args.port

    if args.once:
        cpu, ram, load1, disk, temp = sample_raw()
        c, r, l, d = smooth_values(trend, float(args.smooth), cpu, ram, load1, disk)
        rx_m, tx_m = net_mbps_since(net_bps)
        line = format_line(c, r, l, d, temp, rx_m, tx_m)
        print(line, end="")
        port = try_pick_port(port_hint)
        if port:
            try:
                ser = serial.Serial(port, args.baud, timeout=0.5)
                ser.write(line.encode("ascii", errors="replace"))
                ser.flush()
                ser.close()
            except (serial.SerialException, OSError) as e:
                print(f"open/write {port}: {e}", file=sys.stderr)
                sys.exit(1)
        return

    log("Ctrl+C — выход. При отключении USB ждём и переподключаемся.")

    verify_every = 0.35
    while True:
        port = try_pick_port(port_hint)
        if not port:
            log(f"Нет подходящего порта, повтор через {NO_PORT_SEC:.0f} с…")
            try:
                time.sleep(NO_PORT_SEC)
            except KeyboardInterrupt:
                print("\nСтоп.", file=sys.stderr)
                return
            continue

        try:
            ser = serial.Serial(port, args.baud, timeout=0.5)
        except (serial.SerialException, OSError) as e:
            print(f"Не удалось открыть {port}: {e} — повтор через {RECONNECT_SEC:.0f} с", file=sys.stderr)
            try:
                time.sleep(RECONNECT_SEC)
            except KeyboardInterrupt:
                print("\nСтоп.", file=sys.stderr)
                return
            continue

        trend.clear()
        net_bps.clear()
        time.sleep(0.2)
        try:
            ser.reset_input_buffer()
        except (serial.SerialException, OSError):
            pass

        opened = port
        log(f"Подключено: {opened} @ {args.baud}")

        absent = 0
        last_verify = 0.0
        try:
            while True:
                now = time.monotonic()
                if now - last_verify >= verify_every:
                    last_verify = now
                    if not port_still_listed(opened):
                        absent += 1
                        if absent >= 2:
                            print(f"Порт {opened} пропал из списка — переподключение…", file=sys.stderr)
                            break
                    else:
                        absent = 0

                cpu, ram, load1, disk, temp = sample_raw()
                c, r, l, d = smooth_values(trend, float(args.smooth), cpu, ram, load1, disk)
                rx_m, tx_m = net_mbps_since(net_bps)
                line = format_line(c, r, l, d, temp, rx_m, tx_m)
                data = line.encode("ascii", errors="replace")
                try:
                    ser.write(data)
                    ser.flush()
                except (serial.SerialException, OSError) as e:
                    print(f"Потеря порта ({e}), переподключение…", file=sys.stderr)
                    break
                time.sleep(args.interval)
        except KeyboardInterrupt:
            print("\nСтоп.", file=sys.stderr)
            try:
                ser.close()
            except Exception:
                pass
            return
        finally:
            try:
                ser.close()
            except Exception:
                pass

        time.sleep(RECONNECT_SEC)


if __name__ == "__main__":
    main()
