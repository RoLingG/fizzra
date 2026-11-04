// Package task 提供在后台运行分析的异步任务队列
//
// 目标是有 Asynq 的语义而不依赖 Redis，一个有界的 worker 池、一个够用的持久队列
// （靠从数据库恢复孤儿记录扛过重启），以及 Enqueue / Running 这组小接口。日后换成
// Asynq 只需改这一个文件
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

// Queue 是 API 层使用的队列契约
type Queue interface {
	// Enqueue 把一次分析排入执行队列
	Enqueue(analysisID string) error
	// Running 报告有多少任务在等待或执行中
	Running() int
	// Start 开始处理并立即返回
	Start(ctx context.Context)
	// Stop 排空队列，最多等待到关闭超时
	Stop(ctx context.Context) error
}

// MemoryQueue 是进程内的有界 worker 池
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
	// pending 统计关闭时交还给数据库的工作，系统接口据此能给出真实数字而不是零
	pending atomic.Int64

	startOnce sync.Once
	stopOnce  sync.Once
	wg        sync.WaitGroup
	stopCh    chan struct{}
}

// Config 用于调整队列
type Config struct {
	// Workers 是并发执行的分析数
	Workers int
	// BufferSize 是允许排队的待处理分析数
	BufferSize int
	// EnqueueTimeout 限制 Enqueue 等待空位的时长
	EnqueueTimeout time.Duration
	// ShutdownTimeout 是 Stop 等待进行中分析的时长
	ShutdownTimeout time.Duration
	// RecoverOnStart 是否重新排队上一个进程遗留在 running 状态的分析
	RecoverOnStart bool
}

// DefaultConfig 返回适合单台开发机规模的配置
func DefaultConfig() Config {
	return Config{
		Workers:         2,
		BufferSize:      32,
		EnqueueTimeout:  2 * time.Second,
		ShutdownTimeout: 60 * time.Second,
		RecoverOnStart:  true,
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

// Start 启动 worker 池并恢复任何孤儿分析
func (q *MemoryQueue) Start(ctx context.Context) {
	q.startOnce.Do(func() {
		if q.cfg.RecoverOnStart {
			q.recoverOrphans(ctx)
		}

		q.log.Info("task queue started", "workers", q.cfg.Workers, "buffer", q.cfg.BufferSize)

		for i := 0; i < q.cfg.Workers; i++ {
			q.wg.Add(1)
			go q.worker(ctx, i)
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
	// Requeued 统计关闭时交还给数据库的分析
	Requeued uint64 `json:"requeued"`
}

// Stats 返回队列计数器的一份快照
func (q *MemoryQueue) Stats() Stats {
	return Stats{
		Pending:  len(q.tasks),
		Active:   int(q.running.Load()),
		Enqueued: uint64(q.enqueued.Load()),
		Done:     uint64(q.done.Load()),
		Failed:   uint64(q.failed.Load()),
		Requeued: uint64(q.pending.Load()),
	}
}

// Stop 等待进行中的分析结束，并把仍在排队的重新入队
// 缓冲里的分析会被重置为 pending，下次启动的孤儿恢复会接上，静默丢弃会让数据行
// 永远卡在 pending，界面显示一个永不结束的转圈
func (q *MemoryQueue) Stop(ctx context.Context) error {
	var err error
	q.stopOnce.Do(func() {
		close(q.stopCh)
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
			err = fmt.Errorf("task queue did not drain within the shutdown timeout")
			q.log.Warn("task queue shutdown timed out; in-flight analyses are abandoned")
		}

		// 缓冲里剩余的一律回到 pending，后续启动能恢复它们而不是丢弃
		q.requeuePending()

		// worker 停止时仍在运行的分析也会留在 pending 且没有解释，
		// 一并标注，界面因此能显示「被关闭中断」而非未知状态
		q.annotateInterrupted()
	})
	return err
}

// requeuePending 把缓冲中的分析重置回 pending
//
// 先排空 channel 再写状态，这个顺序最多只会丢掉从未被确认过的工作，
// 反过来则可能把同一条记录恢复两次
func (q *MemoryQueue) requeuePending() {
	var stranded []string
	for {
		select {
		case id := <-q.tasks:
			stranded = append(stranded, id)
		default:
			goto done
		}
	}
done:
	if len(stranded) == 0 {
		return
	}

	for _, id := range stranded {
		analysis, err := q.data.Analyses().Get(id)
		if err != nil {
			q.log.Warn("cannot recover queued analysis", "analysis", id, "err", err)
			continue
		}
		// 已经跑完的不动，只重置从未开始过的工作
		//
		// 排空与写入之间跑完的分析是 success，推回 pending 会让一次已完成的扫描
		// 看起来从没跑过
		if analysis.Status != model.StatusPending && analysis.Status != model.StatusRunning {
			continue
		}
		analysis.Status = model.StatusPending
		analysis.Error = "queued at shutdown; resumed on next start"
		if err := q.data.Analyses().Update(analysis); err != nil {
			q.log.Warn("cannot reset queued analysis", "analysis", id, "err", err)
			continue
		}
		q.pending.Add(1)
	}
	q.log.Info("requeued analyses left in the queue at shutdown", "count", len(stranded))
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

			// 检查停止信号前先把缓冲排空，关闭应当把已接受的工作做完而不是截断
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

// annotateInterrupted 为关闭时留在 pending 的分析记下原因
//
// 否则一次跑到一半被中断的运行，与一次只是排了队还没跑的运行无法区分，两者都停在
// pending 且 Error 为空
func (q *MemoryQueue) annotateInterrupted() {
	recent, err := q.data.Analyses().ListRecent(50)
	if err != nil {
		return
	}
	for i := range recent {
		a := recent[i]
		if a.Status != model.StatusPending || a.Error != "" {
			continue
		}
		a.Error = "analysis was interrupted by shutdown; it will run again on the next start"
		if err := q.data.Analyses().Update(&a); err != nil {
			q.log.Warn("cannot annotate interrupted analysis", "analysis", a.ID, "err", err)
		}
	}
}

// recoverOrphans 把进程崩溃时仍在运行的分析重新排队
//
// 否则数据行会卡在 running，界面永远转圈。重新排队是安全的，分析是幂等的，
// 每次运行都会重建自己的问题记录
func (q *MemoryQueue) recoverOrphans(ctx context.Context) {
	recent, err := q.data.Analyses().ListRecent(100)
	if err != nil {
		q.log.Warn("cannot scan for orphaned analyses", "err", err)
		return
	}

	var recovered int
	for i := range recent {
		a := recent[i]
		if a.Status != model.StatusRunning && a.Status != model.StatusPending {
			continue
		}
		// 只回收本进程启动之前遗留、且尚未排队的工作
		a.Status = model.StatusPending
		a.Error = "requeued after an unclean shutdown"
		if err := q.data.Analyses().Update(&a); err != nil {
			q.log.Warn("cannot reset orphaned analysis", "analysis", a.ID, "err", err)
			continue
		}
		select {
		case q.tasks <- a.ID:
			recovered++
		default:
			q.log.Warn("queue full while recovering orphans, remaining analyses stay pending", "analysis", a.ID)
		}
	}
	if recovered > 0 {
		q.log.Info("recovered orphaned analyses", "count", recovered)
	}
}
