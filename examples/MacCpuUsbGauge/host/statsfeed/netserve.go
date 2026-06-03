package main

// Сетевой сервер метрик для KebrickSW (часы): отдаёт те же показатели, что и
// USB-протокол, в виде JSON по HTTP. Часы сканируют /24 подсеть по этому порту,
// затем GET /stats у откликнувшихся хостов.
//
//   GET /         -> "statsfeed" (быстрый health/discovery, 200)
//   GET /stats    -> JSON со всеми метриками и именем хоста
//
// Если задан -token, требуется заголовок X-Token: <token>.

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
)

// gSample — последняя строка метрик (CSV, как в USB-протоколе). Заполняется
// serial-петлёй (когда USB подключён) либо fallback-сэмплером.
type sampleCache struct {
	mu   sync.RWMutex
	line string
	at   time.Time
	ok   bool
}

var gSample sampleCache

func (s *sampleCache) set(line string) {
	s.mu.Lock()
	s.line, s.at, s.ok = line, time.Now(), true
	s.mu.Unlock()
}
func (s *sampleCache) get() (string, time.Time, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.line, s.at, s.ok
}

// runFallbackSampler — обновляет кэш, только когда serial-петля давно ничего не
// публиковала (USB не подключён). Так избегаем гонок на общих счётчиках сети.
func runFallbackSampler(ctx context.Context, rt *feedRuntime) {
	primed := false
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}
		_, at, ok := gSample.get()
		if !ok || time.Since(at) > 1200*time.Millisecond {
			cfg := rt.snapshot()
			if line, err := collectSampleLine(&primed, cfg.smooth, cfg); err == nil && line != "" {
				gSample.set(line)
			}
		}
		if sleepOrCtx(ctx, time.Second) {
			return
		}
	}
}

type statsJSON struct {
	Host     string  `json:"host"`
	CPU      float64 `json:"cpu"`
	RAM      float64 `json:"ram"`
	Load1    float64 `json:"load1"`
	Disk     float64 `json:"disk"`
	Temp     float64 `json:"temp"`
	Rx       float64 `json:"rx"`
	Tx       float64 `json:"tx"`
	WarnCPU  float64 `json:"wcpu"`
	WarnRAM  float64 `json:"wram"`
	WarnDisk float64 `json:"wdisk"`
	WarnTemp float64 `json:"wtemp"`
	NetMax   float64 `json:"net_max"`
	Age      int64   `json:"age_ms"`
	TS       int64   `json:"ts"`
}

func atof(s string) float64 { v, _ := strconv.ParseFloat(strings.TrimSpace(s), 64); return v }

func sampleToJSON() ([]byte, bool) {
	line, at, ok := gSample.get()
	if !ok {
		return nil, false
	}
	f := strings.Split(strings.TrimSpace(line), ",")
	if len(f) < 7 {
		return nil, false
	}
	host, _ := os.Hostname()
	st := statsJSON{
		Host: host,
		CPU:  atof(f[0]), RAM: atof(f[1]), Load1: atof(f[2]), Disk: atof(f[3]),
		Temp: atof(f[4]), Rx: atof(f[5]), Tx: atof(f[6]),
		Age: time.Since(at).Milliseconds(), TS: time.Now().Unix(),
	}
	if len(f) >= 12 { // расширенные поля (пороги + max сети)
		st.WarnCPU, st.WarnRAM, st.WarnDisk, st.WarnTemp, st.NetMax =
			atof(f[7]), atof(f[8]), atof(f[9]), atof(f[10]), atof(f[11])
	}
	b, err := json.Marshal(st)
	if err != nil {
		return nil, false
	}
	return b, true
}

func startStatsServer(addr, token string) {
	mux := http.NewServeMux()

	checkToken := func(w http.ResponseWriter, r *http.Request) bool {
		if token != "" && r.Header.Get("X-Token") != token {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return false
		}
		return true
	}

	// discovery/health: дёшево и без токена, чтобы скан /24 был быстрым
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		host, _ := os.Hostname()
		_, _ = w.Write([]byte("statsfeed " + host + "\n"))
	})

	mux.HandleFunc("/stats", func(w http.ResponseWriter, r *http.Request) {
		if !checkToken(w, r) {
			return
		}
		b, ok := sampleToJSON()
		if !ok {
			http.Error(w, "no sample yet", http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.Header().Set("Access-Control-Allow-Origin", "*")
		_, _ = w.Write(b)
	})

	srv := &http.Server{
		Addr:              addr,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}
	go func() {
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Printf("stats server (%s): %v", addr, err)
		}
	}()
}
