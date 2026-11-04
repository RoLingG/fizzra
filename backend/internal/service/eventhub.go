package service

import (
	"sync"
)

// ScanEvent 是一次扫描状态变化，推给前端的 SSE 事件
type ScanEvent struct {
	// Type 是 created / progress / finished，前端据此决定怎么用
	Type       string `json:"type"`
	AnalysisID string `json:"analysisId"`
	ProjectID  string `json:"projectId"`
	Status     string `json:"status"`
	Progress   int    `json:"progress"`
	Stage      string `json:"stage"`
}

// EventHub 在进程内广播扫描事件
//
// 只用于界面告知，不承载数据，订阅者断了就断了，事件不重放，前端重连后以
// 自己的首次加载为准
type EventHub struct {
	mu     sync.RWMutex
	subs   map[int]chan ScanEvent
	nextID int
}

// NewEventHub 构建一个事件中心
func NewEventHub() *EventHub {
	return &EventHub{subs: map[int]chan ScanEvent{}}
}

// Subscribe 注册一个订阅者，返回频道和取消函数
func (h *EventHub) Subscribe() (<-chan ScanEvent, func()) {
	h.mu.Lock()
	defer h.mu.Unlock()

	id := h.nextID
	h.nextID++

	// 缓冲避免订阅者慢时阻塞广播方；满了就丢弃事件，界面下次刷新会补上
	ch := make(chan ScanEvent, 32)
	h.subs[id] = ch

	return ch, func() {
		h.mu.Lock()
		defer h.mu.Unlock()
		if c, ok := h.subs[id]; ok {
			delete(h.subs, id)
			close(c)
		}
	}
}

// Publish 广播一个事件，没有订阅者时立即返回
func (h *EventHub) Publish(ev ScanEvent) {
	h.mu.RLock()
	defer h.mu.RUnlock()

	for _, ch := range h.subs {
		select {
		case ch <- ev:
		default:
			// 订阅者来不及消费，丢掉这一条而不是阻塞扫描
		}
	}
}
