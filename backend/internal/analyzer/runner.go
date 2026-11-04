package analyzer

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os/exec"
	"strings"
	"sync"
	"time"
)

// ExecRunner 是生产用的 Runner：它真的去调用外部二进制
type ExecRunner struct {
	log *slog.Logger
	// timeout 限制单条命令的时长。为零表示除 ctx 之外不加额外限制
	timeout time.Duration
	// env 保存附加到每条命令上的额外环境变量
	env []string

	mu    sync.Mutex
	cache map[string]toolProbe
}

type toolProbe struct {
	path string
	err  error
}

// NewExecRunner 构造一个 runner
func NewExecRunner(log *slog.Logger, timeout time.Duration) *ExecRunner {
	return &ExecRunner{
		log:     log,
		timeout: timeout,
		cache:   map[string]toolProbe{},
	}
}

// WithEnv 给每条命令加上环境变量
func (r *ExecRunner) WithEnv(kv ...string) *ExecRunner {
	r.env = append(r.env, kv...)
	return r
}

// LookPath 检查某个二进制是否存在，并把结果缓存下来
// 一次运行里分析器会反复探测同一批工具
func (r *ExecRunner) LookPath(name string) (string, error) {
	r.mu.Lock()
	if p, ok := r.cache[name]; ok {
		r.mu.Unlock()
		return p.path, p.err
	}
	r.mu.Unlock()

	path, err := exec.LookPath(name)

	r.mu.Lock()
	r.cache[name] = toolProbe{path: path, err: err}
	r.mu.Unlock()

	if err != nil {
		return "", fmt.Errorf("%s not found in PATH: %w", name, err)
	}
	return path, nil
}

// LookCandidates 依次尝试候选名，返回第一个能解析的，全都不行时返回错误
//
// 候选名里可以混有平台变体（.exe、.bat）。LookPath 在哪个平台失败就是失败，
// 所以列全了是无害的，而每次只能写对一种平台才是这里要消除的隐患
func LookCandidates(r Runner, names ...string) (string, error) {
	if len(names) == 0 {
		return "", fmt.Errorf("no candidate names given")
	}
	var lastErr error
	for _, name := range names {
		path, err := r.LookPath(name)
		if err == nil {
			return path, nil
		}
		lastErr = err
	}
	return "", lastErr
}

// Run 执行命令并返回它的 stdout
//
// 退出状态非零会以携带 stdout 的 *ExitError 返回，因为
// 我们接入的每个 linter 都用退出码表示「存在问题」，而调用方仍然需要那份报告
func (r *ExecRunner) Run(ctx context.Context, dir, name string, args ...string) ([]byte, error) {
	// applied 是该命令实际生效的截止时间，它不总是 r.timeout。
	// 按工具的预算各自限制单个工具，而整个扫描还带着第二个更短的预算，
	// 后者触发时按工具的预算根本没被碰到。
	// 取两者中较小的那个，消息里给出的时长才是命令真正撞到的
	applied := r.timeout
	if dl, ok := ctx.Deadline(); ok {
		if left := time.Until(dl); applied == 0 || left < applied {
			applied = left
		}
	}

	if r.timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, r.timeout)
		defer cancel()
	}

	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = dir
	if len(r.env) > 0 {
		cmd.Env = append(cmd.Environ(), r.env...)
	}

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if r.log != nil {
		r.log.Debug("exec", "dir", dir, "cmd", name, "args", strings.Join(args, " "))
	}

	err := cmd.Run()
	out := stdout.Bytes()

	if err == nil {
		return out, nil
	}

	exitErr := &ExitError{
		Command: name + " " + strings.Join(args, " "),
		Stdout:  out,
		Stderr:  strings.TrimSpace(stderr.String()),
	}

	var ee *exec.ExitError
	switch {
	// 截止时间检查必须放在最前
	//
	// context 的截止时间杀掉进程时，Go 既把它报成 *exec.ExitError（被信号杀死），
	// 又设置 ctx.Err()。所以先测退出错误会把每一次超时都吞掉，TimedOut 保持
	// false，IsTimeout 返回 false，每个分析器都把一个其实是被我们自己的预算杀掉
	// 的进程报成「工具失败」。下面那个 DeadlineExceeded 分支，对它本来要处理的
	// 情况实际上不可达
	//
	// 这件事的影响不止于措辞，没被识别成超时的超时就是一个普通失败，而正是普通
	// 失败让分析器此前能从报告里消失且毫无解释
	case errors.Is(ctx.Err(), context.DeadlineExceeded):
		exitErr.TimedOut = true
		exitErr.Code = -1
		exitErr.Timeout = applied
		if errors.As(err, &ee) {
			// 有真实退出状态就保留，供日志使用
			exitErr.Code = ee.ExitCode()
		}
	case errors.As(err, &ee):
		exitErr.Code = ee.ExitCode()
		exitErr.Cancelled = ctx.Err() != nil
	default:
		// 进程完全没能启动（缺二进制、权限不对）
		exitErr.Code = -1
		exitErr.Stderr = err.Error()
	}

	return out, exitErr
}

// IsToolMissing 报告某个错误是否表示「二进制没装」，那是跳过条件而不是失败
func IsToolMissing(err error) bool {
	if err == nil {
		return false
	}
	var ee *ExitError
	if errors.As(err, &ee) {
		return false // the tool ran but failed
	}
	return errors.Is(err, exec.ErrNotFound) ||
		strings.Contains(err.Error(), "executable file not found") ||
		strings.Contains(err.Error(), "not found in PATH")
}

// IsTimeout 报告某个错误是否由超出超时引起
func IsTimeout(err error) bool {
	var ee *ExitError
	if errors.As(err, &ee) {
		return ee.TimedOut
	}
	return errors.Is(err, context.DeadlineExceeded)
}
