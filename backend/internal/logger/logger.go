package logger

import (
	"io"
	"log/slog"
	"os"
	"strings"
)

// New 构建进程级的结构化日志器
// 刻意使用标准库的 log/slog 而不是引入 zap 或 logrus，依赖更少，
// 输出格式本来就是对 CI 日志收集器友好的 JSON
func New(level string, pretty bool) *slog.Logger {
	return NewTo(os.Stderr, level, pretty)
}

// NewTo 把日志写到显式指定的目标
// 命令行把日志送到 stderr，`fizzra scan --format json > report.json` 因此产出
// 一份干净的文档，只有报告本身进 stdout
func NewTo(w io.Writer, level string, pretty bool) *slog.Logger {
	var lv slog.Level
	switch strings.ToLower(level) {
	case "debug":
		lv = slog.LevelDebug
	case "warn", "warning":
		lv = slog.LevelWarn
	case "error":
		lv = slog.LevelError
	default:
		lv = slog.LevelInfo
	}

	opts := &slog.HandlerOptions{Level: lv}

	var handler slog.Handler
	if pretty {
		handler = slog.NewTextHandler(w, opts)
	} else {
		handler = slog.NewJSONHandler(w, opts)
	}
	return slog.New(handler)
}
