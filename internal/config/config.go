package config

import (
	"os"
	"time"

	"github.com/arvindell/glab-overseer/internal/actions"
)

type Config struct {
	Project       string
	Host          string
	Token         string
	Ref           string
	PollInterval  time.Duration
	TraceInterval time.Duration
	Action        actions.Action
	StateFile     string
}

func EnvOrDefault(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}

func DurationEnvOrDefault(key string, fallback time.Duration) time.Duration {
	if value := os.Getenv(key); value != "" {
		if parsed, err := time.ParseDuration(value); err == nil {
			return parsed
		}
	}
	return fallback
}
