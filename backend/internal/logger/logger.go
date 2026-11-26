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
//
// w 为 nil 时写入 io.Discard。传 nil 通常是想在测试里静默日志，而 slog 直到首次
// 写入才解引用它，构造阶段一切正常、日志一打就 panic
func NewTo(w io.Writer, level string, pretty bool) *slog.Logger {
	if w == nil {
		w = io.Discard
	}

	opts := &slog.HandlerOptions{Level: parseLevel(level)}

	var handler slog.Handler
	if pretty {
		handler = slog.NewTextHandler(w, opts)
	} else {
		handler = slog.NewJSONHandler(w, opts)
	}
	return slog.New(handler)
}

// parseLevel 把配置里的级别名映射成 slog 级别
//
// 忽略前后空白与大小写，未识别的值回退到 Info。裁剪空白让 YAML 里写成
// `" debug "` 或从环境变量读来带换行的值也能正确识别
func parseLevel(level string) slog.Level {
	switch strings.ToLower(strings.TrimSpace(level)) {
	case "debug":
		return slog.LevelDebug
	case "warn", "warning":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}
