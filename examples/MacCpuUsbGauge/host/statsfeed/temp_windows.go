//go:build windows

package main

import (
	"strings"
	"time"

	"github.com/yusufpapurcu/wmi"
)

const wmiSensorQueryTimeout = 2 * time.Second

type wmiSensorRow struct {
	Name       string
	Value      float32 // COM часто отдаёт VT_R4
	Identifier string
}

// readWindowsAuxiliaryCPUTemp — температура CPU через WMI LibreHardwareMonitor / OpenHardwareMonitor.
// gopsutil на Windows использует root\wmi\MSAcpi_ThermalZoneTemperature; на многих ПК класс пустой.
func readWindowsAuxiliaryCPUTemp() float64 {
	ch := make(chan float64, 1)
	go func() { ch <- queryOHMLikeSensors() }()
	select {
	case v := <-ch:
		return v
	case <-time.After(wmiSensorQueryTimeout):
		return -1
	}
}

func queryOHMLikeSensors() float64 {
	namespaces := []string{`ROOT\LibreHardwareMonitor`, `ROOT\OpenHardwareMonitor`}
	queries := []string{
		`SELECT Name, Value, Identifier FROM Sensor WHERE SensorType='Temperature'`,
		`SELECT Name, Value, Identifier FROM Sensor WHERE SensorType=2`,
		`SELECT Name, Value, Identifier FROM Sensor WHERE Type='Temperature'`,
		`SELECT Name, Value, Identifier FROM Sensor WHERE Type=2`,
	}
	for _, ns := range namespaces {
		for _, q := range queries {
			var rows []wmiSensorRow
			err := wmi.QueryNamespace(q, &rows, ns)
			if err != nil || len(rows) == 0 {
				continue
			}
			if v := pickCPUishTemp(rows); v > 0 {
				return v
			}
		}
	}
	return -1
}

func pickCPUishTemp(rows []wmiSensorRow) float64 {
	var best float64
	found := false
	for _, r := range rows {
		v := float64(r.Value)
		if v <= 1 || v >= 125 {
			continue
		}
		n := strings.ToLower(r.Name + " " + r.Identifier)
		if strings.Contains(n, "cpu") || strings.Contains(n, "core") || strings.Contains(n, "package") ||
			strings.Contains(n, "tctl") || strings.Contains(n, "tdie") || strings.Contains(n, "ccd") ||
			strings.Contains(n, "ryzen") || strings.Contains(n, "intel") {
			if !found || v > best {
				best, found = v, true
			}
		}
	}
	if found {
		return best
	}
	return -1
}
