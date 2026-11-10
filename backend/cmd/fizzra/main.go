// Command fizzra 是 Fizzra 的服务端兼命令行
//
// 单个二进制服务三类用途
//
//	fizzra serve    网页面板和 JSON 接口
//	fizzra scan     一次性检查，适合 CI，带退出码
//	fizzra rules    规则集的查看与导出
//
// 放在同一个二进制里是有意的简化，没有单独的命令行需要安装和定版本，
// 也不会与服务端版本不同步
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"slices"
	"strings"
	"syscall"
	"time"

	"github.com/rolingg/fizzra/backend/internal/analyzer"
	"github.com/rolingg/fizzra/backend/internal/api"
	"github.com/rolingg/fizzra/backend/internal/config"
	"github.com/rolingg/fizzra/backend/internal/logger"
	"github.com/rolingg/fizzra/backend/internal/model"
	"github.com/rolingg/fizzra/backend/internal/rule"
	"github.com/rolingg/fizzra/backend/internal/service"
	"github.com/rolingg/fizzra/backend/internal/store"
	"github.com/rolingg/fizzra/backend/internal/task"
	"github.com/rolingg/fizzra/backend/internal/web"
)

func main() {
	os.Exit(run())
}

func run() int {
	if len(os.Args) < 2 {
		printUsage()
		return 2
	}

	// 全局标志只认命令之前的位置
	flags, command, commandArgs := parseGlobalFlags(os.Args[1:])

	if flags.help || command == "help" {
		printUsage()
		return 0
	}

	cmd, ok := lookupCommand(command)
	if !ok {
		fmt.Fprintf(os.Stderr, "unknown command %q\n\n", command)
		printUsage()
		return 2
	}

	// 不走 bootstrap 触发的命令直接执行
	if cmd.run == nil {
		return cmd.standalone()
	}

	cfg, err := config.Load(flags.configPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "configuration error: %v\n", err)
		return 1
	}

	logLevel := "info"
	if flags.verbose {
		logLevel = "debug"
	}
	log := logger.New(logLevel, true)

	// 被修正而没有拒绝的配置问题每次都打印出来。静默纠正一个值只有在用户知情时
	// 才有意义，否则他以为生效的门禁并不是实际在跑的那个
	for _, w := range cfg.Warnings() {
		log.Warn("configuration adjusted", "field", w.Field, "detail", w.Message)
	}

	app, err := bootstrap(cfg, log)
	if err != nil {
		log.Error("startup failed", "err", err)
		fmt.Fprintf(os.Stderr, "startup failed: %v\n", err)
		return 1
	}
	defer app.Close()

	return cmd.run(app, cfg, commandArgs)
}

// command 描述一条子命令
type command struct {
	// names 是它接受的写法，第一个是规范名
	names []string
	// run 需要已接好线的 App；为 nil 表示该命令不需要数据库
	run func(app *App, cfg *config.Config, args []string) int
	// standalone 在 run 为 nil 时执行
	standalone func() int
}

var commands = []command{
	{names: []string{"serve", "server", "web"}, run: cmdServe},
	{names: []string{"scan"}, run: cmdScan},
	{names: []string{"ci"}, run: cmdCI},
	{names: []string{"report"}, run: cmdReport},
	{names: []string{"rules"}, run: cmdRules},
	{names: []string{"projects", "project"}, run: cmdProjects},
	{names: []string{"analyzers"}, run: cmdAnalyzers},
	{names: []string{"version"}, standalone: cmdVersion},
}

// cmdVersion 打印构建时通过 -ldflags -X 注入的版本号
func cmdVersion() int {
	fmt.Printf("fizzra %s\n", service.Version)
	return 0
}

// lookupCommand 按任意别名查找子命令
func lookupCommand(name string) (command, bool) {
	for _, c := range commands {
		if slices.Contains(c.names, name) {
			return c, true
		}
	}
	return command{}, false
}

// globalFlags 收集子命令之前的全局标志
type globalFlags struct {
	configPath string
	verbose    bool
	help       bool
}

// flagSpec 描述一个全局标志
type flagSpec struct {
	// names 是它接受的写法，第一个是规范名
	names []string
	// takesValue 表示它后面要跟一个值
	takesValue bool
	// valueName 是帮助里值的占位名，如 file、level
	valueName string
	// usage 是帮助里显示的一行说明
	usage string
	// set 把值写进 globalFlags
	set func(f *globalFlags, value string)
}

var globalFlagSpecs = []flagSpec{
	{
		names:      []string{"-c", "--config"},
		takesValue: true,
		valueName:  "file",
		usage:      "Configuration file (default: ./fizzra.yaml, ~/.fizzra/config.yaml)",
		set:        func(f *globalFlags, v string) { f.configPath = v },
	},
	{
		names: []string{"-v", "--verbose"},
		usage: "Enable debug logging",
		set:   func(f *globalFlags, _ string) { f.verbose = true },
	},
	{
		names: []string{"-h", "--help"},
		usage: "Show help",
		set:   func(f *globalFlags, _ string) { f.help = true },
	},
}

// lookupFlagSpec 按任意别名查找全局标志
func lookupFlagSpec(name string) (flagSpec, bool) {
	for _, s := range globalFlagSpecs {
		if slices.Contains(s.names, name) {
			return s, true
		}
	}
	return flagSpec{}, false
}

// parseGlobalFlags 取开头的全局标志，返回它们、命令名和命令自己的参数
//
// 遇到第一个不以 - 开头、或不认识的参数就停，那之后的都归子命令处理
func parseGlobalFlags(args []string) (globalFlags, string, []string) {
	var flags globalFlags
	i := 0
	for i < len(args) {
		arg := args[i]
		if arg == "" || arg[0] != '-' {
			break
		}
		spec, ok := lookupFlagSpec(arg)
		if !ok {
			// 不认识的标志留给子命令，`fizzra serve --port 9090` 里的 --port
			// 属于 serve 而不是全局
			break
		}
		value := ""
		if spec.takesValue {
			if i+1 >= len(args) {
				fmt.Fprintf(os.Stderr, "flag %s requires a value\n", arg)
				os.Exit(2)
			}
			value = args[i+1]
			i++
		}
		spec.set(&flags, value)
		i++
	}

	var command string
	var rest []string
	if i < len(args) {
		command = args[i]
		rest = args[i+1:]
	}
	return flags, command, rest
}

// App 打包每条命令共用的、已接好线的依赖
type App struct {
	Cfg      *config.Config
	Log      *slog.Logger
	Data     *store.SQLData
	DB       *store.DB
	Registry *analyzer.Registry
	Avail    *analyzer.Availability
	Engine   *service.Engine
	Queue    *task.MemoryQueue
	Svc      *service.Service
}

// Close 释放资源
func (a *App) Close() {
	if a.DB != nil {
		if err := a.DB.Close(); err != nil {
			a.Log.Warn("database close failed", "err", err)
		}
	}
}

// bootstrap 接好依赖图
// 所有依赖都显式构造在这里，不依赖 DI 容器或 init() 的副作用，
// 追溯依赖关系只需看这一处
func bootstrap(cfg *config.Config, log *slog.Logger) (*App, error) {
	db, err := store.Open(cfg, log)
	if err != nil {
		return nil, err
	}

	data := store.NewData(db)
	if cfg.Database.AutoMigrate {
		if err := data.Migrate(); err != nil {
			return nil, fmt.Errorf("migrate database: %w", err)
		}
	}

	registry := analyzer.DefaultRegistry()
	runner := analyzer.NewExecRunner(log, cfg.Scan.PerToolTimeout)
	availability := analyzer.NewAvailability(runner)

	// 启动时探测一次，就绪报告才反映真实情况。不探测的话缓存一直为空，
	// analyzer.Readiness 会把缺项当成可用，界面和 `fizzra analyzers` 声称全部就绪，
	// 实际扫描却跳过了其中几个
	if statuses := availability.Probe(registry); len(statuses) > 0 {
		var missing []string
		for name, st := range statuses {
			if !st.Available {
				missing = append(missing, name)
			}
		}
		slices.Sort(missing)
		if len(missing) > 0 {
			log.Info("some analyzer tools are unavailable; their analyzers will be skipped",
				"missing", strings.Join(missing, ", "))
		}
	}

	engine := service.NewEngine(data, cfg, log, registry)
	if err := seedRules(data, cfg, log); err != nil {
		return nil, err
	}
	if err := engine.ReloadRules(); err != nil {
		return nil, err
	}

	app := &App{
		Cfg:      cfg,
		Log:      log,
		Data:     data,
		DB:       db,
		Registry: registry,
		Avail:    availability,
		Engine:   engine,
	}

	queueCfg := task.DefaultConfig()
	queueCfg.Workers = cfg.Scan.Concurrency
	queueCfg.ShutdownTimeout = cfg.Scan.Timeout + 15*time.Second
	queue := task.NewMemoryQueue(queueCfg, engine, data, log)
	app.Queue = queue

	svc := &service.Service{
		Data:     data,
		Cfg:      cfg,
		Log:      log,
		Registry: registry,
		Avail:    availability,
		Enqueuer: queue,
		Engine:   engine,
	}
	app.Svc = svc

	return app, nil
}

// seedRules 把内置规则库同步进数据库，再应用 fizzra.yaml 里声明的规则覆盖
//
// 优先级从低到高
//
//   - 内置定义，模式、路径、排除项和严重级别
//   - fizzra.yaml，可覆盖上述任一字段
//   - 网页界面，可切换启用状态并调整严重级别
//
// 中间那层是关键，没有它仓库就无法收窄内置规则的作用域，比如把 no-panic 排除掉
// 一个确实会在启动时 panic 的文件
func seedRules(data store.Data, cfg *config.Config, log *slog.Logger) error {
	builtins := rule.BuiltinRulesAsModels()

	// 给配置覆盖建索引，内置同步和下面的创建路径因此共用同一份规则清单
	overrides := make(map[string]config.RuleConfig, len(cfg.Rules))
	for _, rc := range cfg.Rules {
		if rc.Name != "" {
			overrides[rc.Name] = rc
		}
	}

	for i := range builtins {
		b := builtins[i]

		// 被配置定制的内置规则按配置值创建，不走内置默认值的 upsert，
		// 下面的 upsert 有意不碰排除列表
		if rc, ok := overrides[b.Name]; ok {
			if err := applyConfigRule(data, rc, log); err != nil {
				return err
			}
			continue
		}

		if err := data.Rules().UpsertByName(&b); err != nil {
			return fmt.Errorf("seed built-in rule %s: %w", b.Name, err)
		}
	}

	// 只存在于配置里的规则
	for _, rc := range cfg.Rules {
		if rc.Name == "" {
			continue
		}
		if _, isBuiltin := builtinNames[rc.Name]; isBuiltin {
			continue // already handled above
		}
		if err := applyConfigRule(data, rc, log); err != nil {
			return err
		}
	}
	return nil
}

// builtinNames 是内置规则库所拥有的规则名集合
var builtinNames = func() map[string]bool {
	out := map[string]bool{}
	for _, r := range rule.BuiltinRulesAsModels() {
		out[r.Name] = true
	}
	return out
}()

// applyConfigRule 根据配置条目创建或更新一条规则
func applyConfigRule(data store.Data, rc config.RuleConfig, log *slog.Logger) error {
	enabled := true
	if rc.Enabled != nil {
		enabled = *rc.Enabled
	}

	existing, err := data.Rules().GetByName(rc.Name)
	if err == nil {
		// 这些字段归配置所有，显式的值会替换已存内容，包括空列表，空列表表示不设限制
		existing.Severity = model.ParseSeverity(stringOr(rc.Severity, string(existing.Severity)))
		existing.Enabled = enabled
		if rc.Message != "" {
			existing.Message = rc.Message
		}
		if rc.Advice != "" {
			existing.Advice = rc.Advice
		}
		if len(rc.Patterns) > 0 {
			existing.Patterns = rc.Patterns
		}
		if len(rc.Paths) > 0 {
			existing.Paths = rc.Paths
		}
		if len(rc.Exclude) > 0 {
			existing.Exclude = rc.Exclude
		}
		if err := data.Rules().Update(existing); err != nil {
			return fmt.Errorf("apply config for rule %s: %w", rc.Name, err)
		}
		return nil
	}
	if err != store.ErrNotFound {
		return fmt.Errorf("look up rule %s: %w", rc.Name, err)
	}

	newRule := &model.Rule{
		ID:        model.NewID("rul"),
		Name:      rc.Name,
		Analyzer:  stringOr(rc.Analyzer, "custom"),
		Severity:  model.ParseSeverity(stringOr(rc.Severity, string(model.SeverityWarning))),
		Message:   rc.Message,
		Advice:    rc.Advice,
		Patterns:  rc.Patterns,
		Paths:     rc.Paths,
		Exclude:   rc.Exclude,
		Enabled:   enabled,
		Builtin:   rc.Builtin,
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}
	if err := validateConfigRule(newRule); err != nil {
		// 配置里的坏模式是硬错误，静默丢弃意味着你配的规则没在跑且毫无提示
		return err
	}
	if err := data.Rules().Create(newRule); err != nil {
		return fmt.Errorf("create rule %s from config: %w", rc.Name, err)
	}
	log.Debug("created rule from config", "rule", rc.Name)
	return nil
}

// ---------------------------------------------------------------------------
// serve
// ---------------------------------------------------------------------------

func cmdServe(app *App, cfg *config.Config, args []string) int {
	// 其他子命令都回应 --help，serve 也必须如此，否则索要用法帮助会转而启动
	// 服务器，在默认端口上留下一个长期运行的进程
	if hasHelp(args) {
		fmt.Println(serveUsage)
		return 0
	}

	addr := cfg.Addr()
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--addr", "--host":
			if i+1 < len(args) {
				i++
				// 同时接受 "host:port" 和裸端口
				if isPort(args[i]) {
					addr = fmt.Sprintf("%s:%s", cfg.Server.Host, args[i])
				} else {
					addr = args[i]
				}
			}
		case "--port", "-p":
			if i+1 < len(args) {
				i++
				addr = fmt.Sprintf("%s:%s", cfg.Server.Host, args[i])
			}
		}
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// worker 池先于 HTTP 监听启动，服务起来后立刻到来的请求即可入队
	app.Queue.Start(ctx)

	handler := api.NewHandler(app.Svc, app.Log)
	// SSE 连接不会自行结束，server.Shutdown 会一直等它们。把关闭信号交给 handler
	// 主动收尾，Ctrl+C 才能立即退出而不是等到超时
	handler.Shutdown = ctx.Done()
	router := api.NewRouter(handler, api.RouterOptions{
		Config:   cfg,
		StaticFS: web.Dist(),
	})

	server := &http.Server{
		Addr:              addr,
		Handler:           router,
		ReadHeaderTimeout: 10 * time.Second,
		// 写超时给得宽松，大项目的报告生成要花些时间，而客户端通常是本地浏览器
		WriteTimeout: 5 * time.Minute,
		IdleTimeout:  120 * time.Second,
	}

	errCh := make(chan error, 1)
	go func() {
		app.Log.Info("fizzra listening", "addr", addr, "mode", cfg.Server.Mode, "version", service.Version)
		app.Log.Info("open the dashboard", "url", "http://"+addr)
		if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
	}()

	select {
	case err := <-errCh:
		app.Log.Error("http server failed", "err", err)
		return 1
	case <-ctx.Done():
		app.Log.Info("shutdown signal received")
	}

	// 优雅关闭，先停止接受请求，再排空队列
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if err := server.Shutdown(shutdownCtx); err != nil {
		app.Log.Warn("http shutdown was not clean", "err", err)
	}
	if err := app.Queue.Stop(shutdownCtx); err != nil {
		app.Log.Warn("queue shutdown was not clean", "err", err)
		return 1
	}
	return 0
}

// ---------------------------------------------------------------------------
// 各命令行命令共用的辅助函数
// ---------------------------------------------------------------------------

const serveUsage = `Usage: fizzra serve [flags]

Starts the web dashboard and the JSON API, plus the background worker pool that
runs queued analyses. Runs until interrupted (Ctrl-C), then shuts down
gracefully: in-flight analyses finish, and anything still queued is returned to
the database so the next start resumes it.

Flags:
  --addr, --host <h:p>  Bind address (default 127.0.0.1:8080)
  --port, -p <n>        Port only, keeping the configured host

There is no authentication. The default loopback address is deliberate: bind to
0.0.0.0 only on a trusted network.

Examples:
  fizzra serve
  fizzra serve --port 9090
  fizzra --config ./fizzra.yaml serve`

func isPort(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}

func stringOr(v, fallback string) string {
	if v == "" {
		return fallback
	}
	return v
}

func printUsage() {
	fmt.Fprint(os.Stderr, `Fizzra — lightweight code quality review for Go and Wails projects.

Usage:
  fizzra [global flags] <command> [flags]

Commands:
  serve                 Start the web dashboard and API (default port `+fmt.Sprint(config.Default().Server.Port)+`)
  scan [path|project]   Scan a project and print a summary
  ci [path|project]     Scan and exit non-zero when the quality gate fails
  report [path|project] Render a JSON, HTML or SARIF report
  rules                 List, export or check the rule set
  projects              List registered projects
  analyzers             Show analyzer availability
  version               Print the version
  help                  Show this message

Global flags:
`)
	fmt.Fprint(os.Stderr, formatFlagSpecs(globalFlagSpecs))
	fmt.Fprint(os.Stderr, `
Common flags:
  --project <name|id>   Target a registered project instead of a local path
  --format <fmt>        Output format: text, json, html, sarif
  --output <file>       Write output to a file instead of stdout
  --analyzers <list>    Comma separated analyzer names to run
  --min-score <n>       Override the gate's minimum score
  --fail-on <levels>    Comma separated severities that fail the gate (e.g. critical,error)

Examples:
  fizzra serve --port 9090
  fizzra scan ./my-wails-app
  fizzra ci --project my-app --format sarif --output fizzra.sarif
  fizzra report --project my-app --format html --output report.html
  fizzra rules export > fizzra-rules.yaml
`)
}

// formatFlagSpecs 渲染全局标志，短名在前，带值的补上占位符
func formatFlagSpecs(specs []flagSpec) string {
	var b strings.Builder
	for _, s := range specs {
		names := strings.Join(s.names, ", ")
		if s.takesValue {
			name := s.valueName
			if name == "" {
				name = "value"
			}
			names += " <" + name + ">"
		}
		fmt.Fprintf(&b, "  %-20s %s\n", names, s.usage)
	}
	return b.String()
}

// validateConfigRule 检查来自配置的规则是否真能跑起来
func validateConfigRule(r *model.Rule) error {
	engine := rule.New([]model.Rule{*r})
	if skipped := engine.Skipped(); len(skipped) > 0 {
		return fmt.Errorf("invalid rule %q in configuration: %w", r.Name, skipped[0])
	}
	return nil
}
