package main

// Конфигурация через переменные окружения (приоритетный способ для сервиса).
// Флаги остаются как переопределение.
//
//   STATSFEED_SERVE       адрес HTTP (":47800"; "" — выключить)
//   STATSFEED_TOKEN       токен метрик (/stats); пусто — открыто
//   STATSFEED_SECURE      1/true — авто-токен метрик
//   STATSFEED_ACTIONS     1/true — включить /action (для сопряжённых часов)
//   STATSFEED_AUTHORIZED  список токенов часов через пробел/запятую (allowlist)

import (
	"os"
	"strings"
)

func envOr(key, def string) string {
	if v, ok := os.LookupEnv(key); ok {
		return v
	}
	return def
}

func envBool(key string) bool {
	v := strings.ToLower(strings.TrimSpace(os.Getenv(key)))
	return v == "1" || v == "true" || v == "yes" || v == "on"
}

// токены часов из STATSFEED_AUTHORIZED (разделители: пробел, запятая, перевод строки)
func envAuthorizedTokens() []string {
	v := os.Getenv("STATSFEED_AUTHORIZED")
	if strings.TrimSpace(v) == "" {
		return nil
	}
	f := strings.FieldsFunc(v, func(r rune) bool { return r == ' ' || r == ',' || r == '\n' || r == '\t' || r == ';' })
	return f
}
