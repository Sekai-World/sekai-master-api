package logging

import (
	"os"
	"strings"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

func Setup(level string, development bool, lokiPushURL string) (func(), error) {
	cfg := zap.NewProductionConfig()
	cfg.EncoderConfig.EncodeTime = zapcore.ISO8601TimeEncoder
	cfg.EncoderConfig.EncodeLevel = zapcore.LowercaseLevelEncoder
	if development {
		cfg = zap.NewDevelopmentConfig()
		cfg.EncoderConfig.EncodeTime = zapcore.ISO8601TimeEncoder
		cfg.EncoderConfig.EncodeLevel = zapcore.LowercaseLevelEncoder
	}

	parsedLevel := zap.InfoLevel
	if err := parsedLevel.UnmarshalText([]byte(strings.ToLower(strings.TrimSpace(level)))); err == nil {
		cfg.Level = zap.NewAtomicLevelAt(parsedLevel)
	} else {
		cfg.Level = zap.NewAtomicLevelAt(zap.InfoLevel)
	}

	encoder := zapcore.NewJSONEncoder(cfg.EncoderConfig)
	stdoutCore := zapcore.NewCore(encoder, zapcore.AddSync(os.Stdout), cfg.Level)
	core := stdoutCore

	lokiURL := strings.TrimSpace(lokiPushURL)
	if lokiURL != "" {
		lokiCore := zapcore.NewCore(
			encoder,
			zapcore.AddSync(newLokiWriteSyncer(lokiURL, map[string]string{
				"job":     "sekai-dev-watch",
				"service": "sekai-master-api",
				"source":  "app",
			})),
			cfg.Level,
		)
		core = zapcore.NewTee(stdoutCore, lokiCore)
	}

	logger := zap.New(core, zap.AddCaller())

	undoGlobals := zap.ReplaceGlobals(logger)
	undoStdLog, err := zap.RedirectStdLogAt(logger, zapcore.InfoLevel)
	if err != nil {
		undoGlobals()
		_ = logger.Sync()
		return nil, err
	}

	cleanup := func() {
		undoStdLog()
		undoGlobals()
		_ = logger.Sync()
	}

	return cleanup, nil
}
