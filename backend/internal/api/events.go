package api

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
)

// heartbeatEvery 是 SSE 保活间隔
//
// 没有它，连接在服务端看来永远是活跃的空转连接。server.Shutdown 会先关空闲连接再等
// 活跃连接自己结束，而 SSE 永不自行结束，Ctrl+C 后要等 shutdownCtx 超时才能退出。
// 定时写一行注释（SSE 中以 ':' 开头的行客户端会忽略）让写超时和关闭检查有机会收尾
const heartbeatEvery = 15 * time.Second

// streamEvents 把扫描状态变化推给界面
//
// 用 SSE 而不是轮询，扫描大部分时间空闲，轮询会在没有变化时反复请求。也不用
// WebSocket，这里只有服务端到客户端一个方向
func (h *Handler) streamEvents(c *gin.Context) {
	engine := h.Svc.Engine
	if engine == nil || engine.Events == nil {
		c.Status(http.StatusServiceUnavailable)
		return
	}

	c.Writer.Header().Set("Content-Type", "text/event-stream")
	c.Writer.Header().Set("Cache-Control", "no-cache")
	c.Writer.Header().Set("Connection", "keep-alive")
	// 关闭代理缓冲，否则事件会攒到连接结束才一起送出
	c.Writer.Header().Set("X-Accel-Buffering", "no")
	c.Writer.WriteHeader(http.StatusOK)

	events, cancel := engine.Events.Subscribe()
	defer cancel()

	// 先告知连接已建立，前端据此区分「已连上」和「尚未连上」
	c.SSEvent("ready", gin.H{"ok": true})
	c.Writer.Flush()

	ticker := time.NewTicker(heartbeatEvery)
	defer ticker.Stop()

	for {
		select {
		case <-c.Request.Context().Done():
			return
		case <-h.Shutdown:
			// 主动结束这条流，server.Shutdown 才能继续，不用等它超时。
			// h.Shutdown 为 nil 时这个分支永远不会就绪
			return
		case <-ticker.C:
			// 这行注释既是保活，也让 goroutine 每 15s 醒一次。连接已关闭时写入
			// 会失败，下一轮 select 就能从 Done 退出
			if _, err := c.Writer.WriteString(": ping\n\n"); err != nil {
				return
			}
			c.Writer.Flush()
		case ev, ok := <-events:
			if !ok {
				return
			}
			payload, err := json.Marshal(ev)
			if err != nil {
				continue
			}
			c.Writer.WriteString("event: scan\n")
			c.Writer.WriteString("data: ")
			c.Writer.Write(payload)
			c.Writer.WriteString("\n\n")
			c.Writer.Flush()
		}
	}
}
