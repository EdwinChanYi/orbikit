// Package log provides the process-wide structured logger shared by all services.
package log

import (
	"os"
	"sync/atomic"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"gopkg.in/natefinch/lumberjack.v2"
)

// Config controls where and how logs are written.
type Config struct {
	Level string `yaml:"level"`
	// File is the log file path. Logs go to stdout when empty.
	File       string `yaml:"file"`
	MaxSizeMB  int    `yaml:"max_size_mb"`
	MaxAgeDays int    `yaml:"max_age_days"`
	// Console switches to the human-readable console encoder.
	Console bool `yaml:"console"`
}

var (
	logger atomic.Pointer[zap.Logger]
	level  = zap.NewAtomicLevelAt(zapcore.InfoLevel)
)

func init() {
	logger.Store(zap.NewNop())
}

// Init builds the global logger. service is attached to every entry.
func Init(cfg Config, service string) error {
	if cfg.Level != "" {
		if err := SetLevel(cfg.Level); err != nil {
			return err
		}
	}

	var ws zapcore.WriteSyncer
	if cfg.File == "" {
		ws = zapcore.AddSync(os.Stdout)
	} else {
		ws = zapcore.AddSync(&lumberjack.Logger{
			Filename: cfg.File,
			MaxSize:  orDefault(cfg.MaxSizeMB, 1024),
			MaxAge:   orDefault(cfg.MaxAgeDays, 30),
			Compress: true,
		})
	}

	enc := zap.NewProductionEncoderConfig()
	enc.EncodeTime = zapcore.ISO8601TimeEncoder
	var encoder zapcore.Encoder
	if cfg.Console {
		enc.EncodeLevel = zapcore.CapitalColorLevelEncoder
		encoder = zapcore.NewConsoleEncoder(enc)
	} else {
		encoder = zapcore.NewJSONEncoder(enc)
	}

	l := zap.New(zapcore.NewCore(encoder, ws, level),
		zap.AddCaller(),
		zap.AddStacktrace(zapcore.ErrorLevel),
		zap.Fields(zap.String("service", service)),
	)
	logger.Store(l)
	return nil
}

// L returns the global logger.
func L() *zap.Logger { return logger.Load() }

// SetLevel changes the log level at runtime.
func SetLevel(s string) error {
	var lv zapcore.Level
	if err := lv.Set(s); err != nil {
		return err
	}
	level.SetLevel(lv)
	return nil
}

// Sync flushes buffered entries.
func Sync() { _ = L().Sync() }

func orDefault(v, d int) int {
	if v <= 0 {
		return d
	}
	return v
}
