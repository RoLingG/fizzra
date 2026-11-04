// Package api 暴露 Fizzra 的 HTTP 接口面
//
// handler 只负责解析并校验输入、调用 service 层、把领域错误映射到状态码，
// 业务规则全部留在 service 里
package api

import (
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/rolingg/fizzra/backend/internal/service"
)

// Handler 持有每条路由都需要的依赖
type Handler struct {
	Svc *service.Service
	Log *slog.Logger

	// Shutdown 关闭时被关闭，SSE 这类不会自行结束的流要盯着它，否则
	// server.Shutdown 会一直等到超时。测试或命令行场景可为 nil，此时流只靠
	// 请求上下文结束
	Shutdown <-chan struct{}
}

// NewHandler 构建 handler
func NewHandler(svc *service.Service, log *slog.Logger) *Handler {
	return &Handler{Svc: svc, Log: log}
}

// ---------------------------------------------------------------------------
// 响应信封
// ---------------------------------------------------------------------------

// Response 是统一的接口信封
//
// 每个载荷都包一层，前端只需处理一种形状，错误除消息外还带机器可读的码
type Response struct {
	Data  any        `json:"data,omitempty"`
	Meta  *Meta      `json:"meta,omitempty"`
	Error *ErrorBody `json:"error,omitempty"`
}

// Meta 携带分页和耗时信息
type Meta struct {
	Total  int64 `json:"total,omitempty"`
	Limit  int   `json:"limit,omitempty"`
	Offset int   `json:"offset,omitempty"`
}

// ErrorBody 是信封中错误的那一半
type ErrorBody struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func ok(c *gin.Context, data any) {
	c.JSON(http.StatusOK, Response{Data: data})
}

func created(c *gin.Context, data any) {
	c.JSON(http.StatusCreated, Response{Data: data})
}

func okList(c *gin.Context, data any, total int64, limit, offset int) {
	c.JSON(http.StatusOK, Response{
		Data: data,
		Meta: &Meta{Total: total, Limit: limit, Offset: offset},
	})
}

// fail 把领域错误映射成 HTTP 状态码和稳定的错误码
//
// 映射集中在一处，handler 不会自行发明状态码，前端也能按 error.code 分支，
// 而不必解析消息文本
func (h *Handler) fail(c *gin.Context, err error) {
	status, code := classify(err)
	if status >= http.StatusInternalServerError {
		// 完整原因只进日志，不泄露给客户端
		h.Log.Error("request failed",
			"path", c.Request.URL.Path,
			"method", c.Request.Method,
			"status", status,
			"err", err,
		)
	}

	c.JSON(status, Response{Error: &ErrorBody{Code: code, Message: err.Error()}})
}

func classify(err error) (int, string) {
	switch {
	case errors.Is(err, service.ErrNotFound):
		return http.StatusNotFound, "not_found"
	case errors.Is(err, service.ErrConflict):
		return http.StatusConflict, "conflict"
	case errors.Is(err, service.ErrInvalid):
		return http.StatusBadRequest, "invalid_input"
	}
	return http.StatusInternalServerError, "internal_error"
}

// badRequest 是 handler 里检出输入问题时的快捷方式
func (h *Handler) badRequest(c *gin.Context, msg string) {
	c.JSON(http.StatusBadRequest, Response{
		Error: &ErrorBody{Code: "invalid_input", Message: msg},
	})
}

// ---------------------------------------------------------------------------
// 查询辅助函数
// ---------------------------------------------------------------------------

func queryInt(c *gin.Context, key string, fallback int) int {
	raw := c.Query(key)
	if raw == "" {
		return fallback
	}
	n, err := strconv.Atoi(raw)
	if err != nil {
		return fallback
	}
	return n
}

// queryList 拆分一个逗号分隔的查询参数
func queryList(c *gin.Context, key string) []string {
	raw := strings.TrimSpace(c.Query(key))
	if raw == "" {
		return nil
	}
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

func queryBool(c *gin.Context, key string) bool {
	raw := strings.ToLower(strings.TrimSpace(c.Query(key)))
	return raw == "1" || raw == "true" || raw == "yes"
}
