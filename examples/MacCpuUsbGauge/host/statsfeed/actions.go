package main

// Действия над хостом по запросу с часов (POST /action). Включается флагом
// -actions и требует токен. Команды подобраны так, чтобы по возможности
// работать без sudo в активной сессии пользователя.

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os/exec"
	"runtime"
	"strings"
)

func writeActionResult(w http.ResponseWriter, ok bool, msg string) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	b, _ := json.Marshal(map[string]any{"ok": ok, "msg": msg})
	_, _ = w.Write(b)
}

// runAction выполняет действие для текущей ОС. Возвращает сообщение и ошибку.
func runAction(action, custom string) (string, error) {
	action = strings.ToLower(strings.TrimSpace(action))

	var name string
	var args []string

	switch runtime.GOOS {
	case "darwin":
		switch action {
		case "sleep":    name, args = "pmset", []string{"sleepnow"}
		case "lock":     name, args = "/System/Library/CoreServices/Menu Extras/User.menu/Contents/Resources/CGSession", []string{"-suspend"}
		case "reboot":   name, args = "osascript", []string{"-e", `tell app "System Events" to restart`}
		case "shutdown": name, args = "osascript", []string{"-e", `tell app "System Events" to shut down`}
		}
	case "windows":
		switch action {
		case "sleep":    name, args = "rundll32.exe", []string{"powrprof.dll,SetSuspendState", "0,1,0"}
		case "lock":     name, args = "rundll32.exe", []string{"user32.dll,LockWorkStation"}
		case "reboot":   name, args = "shutdown", []string{"/r", "/t", "0"}
		case "shutdown": name, args = "shutdown", []string{"/s", "/t", "0"}
		}
	default: // linux
		switch action {
		case "sleep":    name, args = "systemctl", []string{"suspend"}
		case "lock":     name, args = "loginctl", []string{"lock-session"}
		case "reboot":   name, args = "systemctl", []string{"reboot"}
		case "shutdown": name, args = "systemctl", []string{"poweroff"}
		}
	}

	if action == "cmd" {
		if strings.TrimSpace(custom) == "" {
			return "empty command", fmt.Errorf("empty")
		}
		if runtime.GOOS == "windows" {
			name, args = "cmd", []string{"/c", custom}
		} else {
			name, args = "sh", []string{"-c", custom}
		}
	}

	if name == "" {
		return "unknown action: " + action, fmt.Errorf("unknown action")
	}

	out, err := exec.Command(name, args...).CombinedOutput()
	if err != nil {
		return fmt.Sprintf("%s: %v %s", action, err, strings.TrimSpace(string(out))), err
	}
	if action == "cmd" {
		return strings.TrimSpace(string(out)), nil
	}
	return action + " ok", nil
}
