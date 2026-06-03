package main

// Лёгкое управление токеном доступа к /stats:
//   - токен сохраняется между перезапусками в файле конфигурации;
//   - просмотр без GUI: флаг -show-token (печатает токен и выходит);
//   - при старте токен печатается в лог/stdout (для headless-серверов);
//   - -token X задаёт/перезаписывает; -open запускает без токена (открыто в LAN).

import (
	"crypto/rand"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
)

func tokenPath() string {
	base, err := os.UserConfigDir()
	if err != nil || base == "" {
		base, _ = os.UserCacheDir()
	}
	dir := filepath.Join(base, "statsfeed")
	_ = os.MkdirAll(dir, 0o700)
	return filepath.Join(dir, "token")
}

func loadToken() string {
	b, err := os.ReadFile(tokenPath())
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}

func saveToken(t string) {
	_ = os.WriteFile(tokenPath(), []byte(t+"\n"), 0o600)
}

func genToken() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "statsfeed"
	}
	return hex.EncodeToString(b[:])
}

// resolveToken возвращает действующий токен с учётом флагов и сохраняет его.
//   open=true            -> "" (без токена)
//   flagTok != ""        -> использовать и сохранить
//   иначе                -> загрузить сохранённый; если нет — сгенерировать и сохранить
func resolveToken(flagTok string, open bool) string {
	if open {
		return ""
	}
	if flagTok != "" {
		saveToken(flagTok)
		return flagTok
	}
	t := loadToken()
	if t == "" {
		t = genToken()
		saveToken(t)
	}
	return t
}
