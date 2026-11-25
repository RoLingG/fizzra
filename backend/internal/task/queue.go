// Package task 提供在后台运行分析的异步任务队列
//
// 目标是有 Asynq 的语义而不依赖 Redis：一个有界的 worker 池，加上
// Enqueue / Running 这组小接口。日后换成 Asynq 只需改这一个文件
package task

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/rolingg/fizzra/backend/internal/model"
	"github.com/rolingg/fizzra/backend/internal/store"
)

// Runner 执行一次分析，由 service 层满足
type Runner interface {
	RunAnalysis(ctx context.Context, analysisID string) error
}

// Queue  API 层使用的队列契约
type Queue interface {
	// Enqueue 把一次分析排入执行队列
	Enqueue(analysisID string) error
	// Running 报告有多少任务在等待或执行中
	Running() int
	// Start 开始处理并立即返回
	Start(ctx context.Context)
	// Stop 中止执行中的任务，未完成的分析标成失败
	Stop(ctx context.Context) error
}

// MemoryQueue 进程内的有界 worker 池
//
// 任务放在带缓冲的 channel 里。缓冲满时 Enqueue 短暂阻塞后返回失败，接口据此回
// 503 把背压传出去，而不是静默丢任务
type MemoryQueue struct {
	cfg      Config
	log      *slog.Logger
	runner   Runner
	data     store.Data
	tasks    chan string
	running  atomic.Int64
	enqueued atomic.Int64
	done     atomic.Int64
	failed   atomic.Int64

	startOnce sync.Once
	stopOnce  sync.Once
	wg        sync.WaitGroup
	stopCh    chan struct{}
	// stopRun 停止时取消 worker 的运行 ctx，正在跑的分析随之退出
	stopRun context.CancelFunc
}

// Config 用于调整队列
type Config struct {
	// Workers 并发执行的分析数
	Workers int
	// BufferSize 允许排队的待处理分析数
	BufferSize int
	// EnqueueTimeout 限制 Enqueue 等待空位的时长
	EnqueueTimeout time.Duration
	// ClearOrphansOnStart 启动时把上一个进程遗留的 pending/running 标成失败
	ClearOrphansOnStart bool
}

// DefaultConfig 返回适合单台开发机规模的配置
func DefaultConfig() Config {
	return Config{
		Workers:             2,
		BufferSize:          32,
		EnqueueTimeout:      2 * time.Second,
		ClearOrphansOnStart: true,
	}
}

// NewMemoryQueue 构建队列
func NewMemoryQueue(cfg Config, runner Runner, data store.Data, log *slog.Logger) *MemoryQueue {
	if cfg.Workers < 1 {
		cfg.Workers = 1
	}
	if cfg.BufferSize < 1 {
		cfg.BufferSize = 16
	}
	return &MemoryQueue{
		cfg:    cfg,
		log:    log,
		runner: runner,
		data:   data,
		tasks:  make(chan string, cfg.BufferSize),
		stopCh: make(chan struct{}),
	}
}

// Start 启动 worker 池，并清理上一个进程遗留的未完成分析
func (q *MemoryQueue) Start(ctx context.Context) {
	q.startOnce.Do(func() {
		if q.cfg.ClearOrphansOnStart {
			q.clearOrphans()
		}

		q.log.Info("task queue started", "workers", q.cfg.Workers, "buffer", q.cfg.BufferSize)

		// worker 跑在派生出来的 ctx 上，Stop 取消它就能中断正在执行的分析。
		// 直接用调用方的 ctx 做不到这一点
		runCtx, cancel := context.WithCancel(ctx)
		q.stopRun = cancel

		for i := 0; i < q.cfg.Workers; i++ {
			q.wg.Add(1)
			go q.worker(runCtx, i)
		}
	})
}

// Enqueue 提交一个分析 ID
func (q *MemoryQueue) Enqueue(analysisID string) error {
	select {
	case <-q.stopCh:
		return errors.New("task queue is shutting down")
	default:
	}

	select {
	case q.tasks <- analysisID:
		q.enqueued.Add(1)
		q.log.Debug("task enqueued", "analysis", analysisID, "depth", len(q.tasks))
		return nil
	case <-time.After(q.cfg.EnqueueTimeout):
		return fmt.Errorf("task queue is full (%d pending); wait for running analyses to finish", len(q.tasks))
	case <-q.stopCh:
		return errors.New("task queue is shutting down")
	}
}

// Running 报告等待中加执行中的任务数
func (q *MemoryQueue) Running() int {
	return len(q.tasks) + int(q.running.Load())
}

// Stats 描述队列活动，供系统接口使用
type Stats struct {
	Pending  int    `json:"pending"`
	Active   int    `json:"active"`
	Enqueued uint64 `json:"enqueued"`
	Done     uint64 `json:"done"`
	Failed   uint64 `json:"failed"`
}

// Stats 返回队列计数器的一份快照
func (q *MemoryQueue) Stats() Stats {
	return Stats{
		Pending:  len(q.tasks),
		Active:   int(q.running.Load()),
		Enqueued: uint64(q.enqueued.Load()),
		Done:     uint64(q.done.Load()),
		Failed:   uint64(q.failed.Load()),
	}
}

// Stop 停止 worker 并清理未完成的分析
//
// ctx 决定等多久，退不出来的分析标成失败而不是留在 pending，后者在界面上
// 等于「扫描中」，详情页会一直轮询
func (q *MemoryQueue) Stop(ctx context.Context) error {
	var err error
	q.stopOnce.Do(func() {
		close(q.stopCh)
		// 先取消运行 ctx，正在跑的分析立刻收到 Done 并退出
		if q.stopRun != nil {
			q.stopRun()
		}
		pending := len(q.tasks)
		q.log.Info("task queue stopping", "pending", pending)

		finished := make(chan struct{})
		go func() {
			q.wg.Wait()
			close(finished)
		}()

		select {
		case <-finished:
			q.log.Info("task queue stopped cleanly")
		case <-ctx.Done():
			err = fmt.Errorf("task queue did not stop within the shutdown timeout")
			q.log.Warn("task queue shutdown timed out; in-flight analyses are abandoned")
		}

		// 队列里的任务不会再跑，标成失败
		q.abandonQueued()

		q.annotateInterrupted()
	})
	return err
}

// abandonQueued 把队列里还没开始的任务标成失败
//
// 不标回 pending，那在界面上等于「扫描中」，详情页会等一个永远不会开始的扫描
func (q *MemoryQueue) abandonQueued() {
	var stranded []string
	for drained := false; !drained; {
		select {
		case id := <-q.tasks:
			stranded = append(stranded, id)
		default:
			drained = true
		}
	}
	if len(stranded) == 0 {
		return
	}

	for _, id := range stranded {
		analysis, err := q.data.Analyses().Get(id)
		if err != nil {
			q.log.Warn("cannot mark queued analysis as abandoned", "analysis", id, "err", err)
			continue
		}
		// 排空与写入之间跑完的分析是 success，不能改回失败
		if analysis.Status != model.StatusPending && analysis.Status != model.StatusRunning {
			continue
		}
		analysis.Status = model.StatusFailed
		analysis.Error = "analysis was abandoned at shutdown; start a new scan to retry"
		now := time.Now()
		analysis.FinishedAt = &now
		if err := q.data.Analyses().Update(analysis); err != nil {
			q.log.Warn("cannot mark queued analysis as abandoned", "analysis", id, "err", err)
		}
	}
	q.log.Info("abandoned queued analyses at shutdown", "count", len(stranded))
}

// worker 消费分析 ID，直到队列停止
// 停止信号只在两次任务之间生效，持有任务时检查会让那次分析跑到一半被抛弃，
// 留下一行没有标注状态的数据，下次启动也无法把它与崩溃区分开
func (q *MemoryQueue) worker(ctx context.Context, id int) {
	defer q.wg.Done()

	for {
		select {
		case <-q.stopCh:
			return
		case analysisID := <-q.tasks:
			q.runOne(ctx, id, analysisID)

			// 刚跑完的这一轮结束后再看一次停止信号，避免关掉之后又取下一个
			select {
			case <-q.stopCh:
				return
			default:
			}
		}
	}
}

// runOne 执行单次分析并做 panic 隔离
// 一个坏掉的分析器不能把 worker（进而整个队列）带下去
func (q *MemoryQueue) runOne(ctx context.Context, workerID int, analysisID string) {
	q.running.Add(1)
	defer q.running.Add(-1)

	log := q.log.With("analysis", analysisID, "worker", workerID)
	start := time.Now()

	defer func() {
		if r := recover(); r != nil {
			q.failed.Add(1)
			log.Error("analysis panicked", "panic", fmt.Sprint(r), "duration", time.Since(start))
			q.markFailed(ctx, analysisID, fmt.Sprintf("internal panic: %v", r))
		}
	}()

	log.Info("analysis started")

	if err := q.runner.RunAnalysis(ctx, analysisID); err != nil {
		q.failed.Add(1)
		log.Error("analysis failed", "err", err, "duration", time.Since(start).Round(time.Millisecond))
		return
	}

	q.done.Add(1)
	log.Info("analysis finished", "duration", time.Since(start).Round(time.Millisecond))
}

// markFailed 记录一次崩溃的分析，免得它永远停在 running
func (q *MemoryQueue) markFailed(ctx context.Context, analysisID, reason string) {
	analysis, err := q.data.Analyses().Get(analysisID)
	if err != nil {
		q.log.Error("cannot mark analysis failed", "analysis", analysisID, "err", err)
		return
	}
	analysis.Status = model.StatusFailed
	analysis.Error = reason
	now := time.Now()
	analysis.FinishedAt = &now
	if err := q.data.Analyses().Update(analysis); err != nil {
		q.log.Error("cannot persist failed analysis", "analysis", analysisID, "err", err)
	}
}

// annotateInterrupted 把关闭时还没跑完的分析标成失败
//
// 留在 pending 会显示成「扫描中」，用户分不清是真在跑还是已经停了
func (q *MemoryQueue) annotateInterrupted() {
	recent, err := q.data.Analyses().ListRecent(50)
	if err != nil {
		return
	}
	for i := range recent {
		a := recent[i]
		if a.Status != model.StatusPending && a.Status != model.StatusRunning {
			continue
		}
		a.Status = model.StatusFailed
		a.Error = "analysis was interrupted by shutdown; start a new scan to retry"
		now := time.Now()
		a.FinishedAt = &now
		if err := q.data.Analyses().Update(&a); err != nil {
			q.log.Warn("cannot annotate interrupted analysis", "analysis", a.ID, "err", err)
		}
	}
}

// clearOrphans 把上一个进程遗留的未完成分析标成失败
func (q *MemoryQueue) clearOrphans() {
	recent, err := q.data.Analyses().ListRecent(100)
	if err != nil {
		q.log.Warn("cannot scan for orphaned analyses", "err", err)
		return
	}

	var cleared int
	for i := range recent {
		a := recent[i]
		if a.Status != model.StatusRunning && a.Status != model.StatusPending {
			continue
		}
		a.Status = model.StatusFailed
		a.Error = "analysis was interrupted by an unclean shutdown; start a new scan to retry"
		now := time.Now()
		a.FinishedAt = &now
		if err := q.data.Analyses().Update(&a); err != nil {
			q.log.Warn("cannot clear orphaned analysis", "analysis", a.ID, "err", err)
			continue
		}
		cleared++
	}
	if cleared > 0 {
		q.log.Info("cleared analyses left over from an unclean shutdown", "count", cleared)
	}
}
