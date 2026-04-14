package config

import "strings"

// Config captures LiveKit turn detector settings for Engram.spec.with.
type Config struct {
	FrameDurationMs int    `json:"frameDurationMs" mapstructure:"frameDurationMs"`
	SilenceDuration int    `json:"silenceDurationMs" mapstructure:"silenceDurationMs"`
	MinDurationMs   int    `json:"minDurationMs" mapstructure:"minDurationMs"`
	MaxDurationMs   int    `json:"maxDurationMs" mapstructure:"maxDurationMs"`
	Aggressiveness  int    `json:"aggressiveness" mapstructure:"aggressiveness"`
	OutputEncoding  string `json:"outputEncoding" mapstructure:"outputEncoding"`
}

// Normalize applies defaults and clamps tuning parameters.
func Normalize(cfg Config) Config {
	if cfg.FrameDurationMs <= 0 {
		cfg.FrameDurationMs = 20
	}
	if cfg.SilenceDuration <= 0 {
		cfg.SilenceDuration = 400
	}
	if cfg.MinDurationMs <= 0 {
		cfg.MinDurationMs = 200
	}
	if cfg.MaxDurationMs <= 0 {
		cfg.MaxDurationMs = 10000
	}
	if cfg.Aggressiveness < 0 || cfg.Aggressiveness > 3 {
		cfg.Aggressiveness = 2
	}
	if strings.TrimSpace(cfg.OutputEncoding) == "" {
		cfg.OutputEncoding = "wav"
	}
	return cfg
}
