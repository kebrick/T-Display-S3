package main

// Учёт часов (устройств) и журнал запросов (см. architecture/AUTH.md, v2).
// Единый токен часов — их идентификатор. Статусы: pending / accepted / blocked.
//   - /stats: разрешено всем, кроме blocked (чтобы скан/мониторинг работал);
//   - /action: только accepted (и не blocked).
//
// Источник правды для accepted/blocked — файл devices.json (перечитывается на
// каждую проверку), поэтому CLI, GUI и работающий сервер согласованы между
// процессами. pending-устройства и журнал — в памяти.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"
)

func cfgFile(name string) string {
	base, err := os.UserConfigDir()
	if err != nil || base == "" {
		base, _ = os.UserCacheDir()
	}
	dir := filepath.Join(base, "statsfeed")
	_ = os.MkdirAll(dir, 0o700)
	return filepath.Join(dir, name)
}

func devicesPath() string { return cfgFile("devices.json") }

type devStatus string

const (
	devPending  devStatus = "pending"
	devAccepted devStatus = "accepted"
	devBlocked  devStatus = "blocked"
)

type device struct {
	Token  string    `json:"token"`
	Id     string    `json:"id"`
	Status devStatus `json:"status"`
	Seen   int       `json:"-"`
	Last   time.Time `json:"-"`
}

type logEntry struct{ t time.Time; id, token, path, result string }

type seenDev struct {
	id   string
	seen int
	last time.Time
}

var (
	devMu  sync.Mutex
	seen   = map[string]*seenDev{} // ephemeral: all tokens that contacted us
	reqLog []logEntry
	logCap = 120
)

// ---- file (accepted/blocked) — источник правды ----------------------------
func loadFileDevices() []device {
	b, err := os.ReadFile(devicesPath())
	if err != nil {
		return nil
	}
	var list []device
	if json.Unmarshal(b, &list) != nil {
		return nil
	}
	return list
}
func saveFileDevices(list []device) {
	b, _ := json.MarshalIndent(list, "", "  ")
	_ = os.WriteFile(devicesPath(), b, 0o600)
}
func devLoad() {} // совместимость; правда читается из файла на лету

func statusOf(token string) devStatus {
	if token == "" {
		return devPending
	}
	for _, d := range loadFileDevices() {
		if d.Token == token {
			return d.Status
		}
	}
	return devPending
}

func setStatus(token, id string, s devStatus) {
	list := loadFileDevices()
	found := false
	for i := range list {
		if list[i].Token == token {
			list[i].Status = s
			if id != "" {
				list[i].Id = id
			}
			found = true
		}
	}
	if !found {
		list = append(list, device{Token: token, Id: id, Status: s})
	}
	saveFileDevices(list)
}

func devAccept(token string) { setStatus(token, "", devAccepted) }
func devBlock(token string)  { setStatus(token, "", devBlocked) }
func devUnblock(token string) { // снять статус -> снова pending
	var keep []device
	for _, d := range loadFileDevices() {
		if d.Token != token {
			keep = append(keep, d)
		}
	}
	saveFileDevices(keep)
}

func devIsAccepted(token string) bool { return statusOf(token) == devAccepted }
func devIsBlocked(token string) bool  { return statusOf(token) == devBlocked }

// ---- журнал + список -------------------------------------------------------
func devNote(id, token, path, result string) devStatus {
	devMu.Lock()
	if token != "" {
		d := seen[token]
		if d == nil {
			d = &seenDev{}
			seen[token] = d
		}
		if id != "" {
			d.id = id
		}
		d.seen++
		d.last = time.Now()
	}
	reqLog = append(reqLog, logEntry{time.Now(), id, token, path, result})
	if len(reqLog) > logCap {
		reqLog = reqLog[len(reqLog)-logCap:]
	}
	devMu.Unlock()
	return statusOf(token)
}

// объединение виденных устройств и файла статусов
func devList() []device {
	statuses := map[string]device{}
	for _, d := range loadFileDevices() {
		statuses[d.Token] = d
	}
	devMu.Lock()
	out := []device{}
	used := map[string]bool{}
	for tok, sd := range seen {
		st := devPending
		id := sd.id
		if fd, ok := statuses[tok]; ok {
			st = fd.Status
			if id == "" {
				id = fd.Id
			}
		}
		out = append(out, device{Token: tok, Id: id, Status: st, Seen: sd.seen, Last: sd.last})
		used[tok] = true
	}
	devMu.Unlock()
	for tok, fd := range statuses { // accepted/blocked, ещё не выходившие на связь
		if !used[tok] {
			out = append(out, fd)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Last.After(out[j].Last) })
	return out
}

func devLogLines(n int) []string {
	devMu.Lock()
	defer devMu.Unlock()
	if n <= 0 || n > len(reqLog) {
		n = len(reqLog)
	}
	out := make([]string, 0, n)
	for i := len(reqLog) - n; i < len(reqLog); i++ {
		e := reqLog[i]
		tk := e.token
		if len(tk) > 8 {
			tk = tk[:8]
		}
		out = append(out, e.t.Format("15:04:05")+"  "+e.id+"  "+tk+"  "+e.path+"  "+e.result)
	}
	return out
}
