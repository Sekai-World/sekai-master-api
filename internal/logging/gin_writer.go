package logging

import (
	"bytes"
	"io"
	"strings"
	"sync"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

type ginZapWriter struct {
	component string
	level     zapcore.Level
	buffer    bytes.Buffer
	mu        sync.Mutex
}

func ConfigureGinWriters() {
	gin.DefaultWriter = io.MultiWriter(&ginZapWriter{component: "gin-access", level: zapcore.InfoLevel})
	gin.DefaultErrorWriter = io.MultiWriter(&ginZapWriter{component: "gin-error", level: zapcore.ErrorLevel})
}

func (writer *ginZapWriter) Write(payload []byte) (int, error) {
	writer.mu.Lock()
	defer writer.mu.Unlock()

	_, _ = writer.buffer.Write(payload)

	for {
		line, err := writer.buffer.ReadString('\n')
		if err != nil {
			writer.buffer.WriteString(line)
			break
		}
		writer.logLine(line)
	}

	return len(payload), nil
}

func (writer *ginZapWriter) logLine(line string) {
	trimmed := strings.TrimSpace(line)
	if trimmed == "" {
		return
	}

	level := writer.level
	lowerLine := strings.ToLower(trimmed)
	switch {
	case strings.Contains(lowerLine, "[gin-debug]"):
		level = zapcore.DebugLevel
	case strings.Contains(lowerLine, "[gin-warning]") || strings.Contains(lowerLine, "[gin-warn]"):
		level = zapcore.WarnLevel
	case strings.Contains(lowerLine, "[gin-error]"):
		level = zapcore.ErrorLevel
	}

	sugar := zap.S()
	switch level {
	case zapcore.ErrorLevel:
		sugar.Errorw("gin log", "component", writer.component, "line", trimmed)
	case zapcore.WarnLevel:
		sugar.Warnw("gin log", "component", writer.component, "line", trimmed)
	case zapcore.DebugLevel:
		sugar.Debugw("gin log", "component", writer.component, "line", trimmed)
	default:
		sugar.Infow("gin log", "component", writer.component, "line", trimmed)
	}
}
