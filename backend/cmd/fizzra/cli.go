package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/rolingg/fizzra/backend/internal/config"
	"github.com/rolingg/fizzra/backend/internal/model"
	"github.com/rolingg/fizzra/backend/internal/quality"
	"github.com/rolingg/fizzra/backend/internal/report"
	"github.com/rolingg/fizzra/backend/internal/service"
	"github.com/rolingg/fizzra/backend/internal/store"
	"gopkg.in/yaml.v3"
)

// cliOptions 持有 scan / ci / report 共用的参数
type cliOptions struct {
	Target      string
	Project     string
	Format      string
	Output      string
	Analyzers   []string
	MinScore    float64
	hasMinScore bool
	FailOn      []string
	Quiet       bool
	NoSave      bool
	Timeout     time.Duration
	// Lang 是报告框架文案的语言，取值 en / zh，空表示用默认的 en
	Lang string
	// Offline 阻止分析器访问网络
	// 在没有外网出口的机器上设置，两个漏洞扫描器会克隆通告库，
	// 而在被阻断的网络上这个克隆不会快速失败，会一直卡到 socket 超时
	Offline bool
}

// parseCommonFlags 解析 scan 系列共用的参数
// 手写而不是用 flag 包，因为这些命令接受一个与参数混在一起的位置目标（路径或项目名），
// 这是 flag.Parse 表达不了的
func parseCommonFlags(args []string) (cliOptions, error) {
	opts := cliOptions{Format: "text", Timeout: 15 * time.Minute}

	for i := 0; i < len(args); i++ {
		arg := args[i]
		next := func() (string, error) {
			if i+1 >= len(args) {
				return "", fmt.Errorf("flag %s requires a value", arg)
			}
			i++
			return args[i], nil
		}

		switch {
		case arg == "--project" || arg == "-p":
			v, err := next()
			if err != nil {
				return opts, err
			}
			opts.Project = v
		case arg == "--format" || arg == "-f":
			v, err := next()
			if err != nil {
				return opts, err
			}
			opts.Format = strings.ToLower(v)
		case arg == "--output" || arg == "-o":
			v, err := next()
			if err != nil {
				return opts, err
			}
			opts.Output = v
		case arg == "--lang":
			v, err := next()
			if err != nil {
				return opts, err
			}
			opts.Lang = strings.ToLower(v)
		case arg == "--analyzers":
			v, err := next()
			if err != nil {
				return opts, err
			}
			opts.Analyzers = splitCSV(v)
		case arg == "--min-score":
			v, err := next()
			if err != nil {
				return opts, err
			}
			if _, err := fmt.Sscanf(v, "%f", &opts.MinScore); err != nil {
				return opts, fmt.Errorf("invalid --min-score %q", v)
			}
			opts.hasMinScore = true
		case arg == "--fail-on":
			v, err := next()
			if err != nil {
				return opts, err
			}
			opts.FailOn = splitCSV(v)
		case arg == "--timeout":
			v, err := next()
			if err != nil {
				return opts, err
			}
			d, err := time.ParseDuration(v)
			if err != nil {
				return opts, fmt.Errorf("invalid --timeout %q: use forms like 5m or 90s", v)
			}
			opts.Timeout = d
		case arg == "--quiet" || arg == "-q":
			opts.Quiet = true
		case arg == "--offline":
			opts.Offline = true
		case arg == "--no-save":
			opts.NoSave = true
		case arg == "--help" || arg == "-h":
			return opts, errHelp
		case strings.HasPrefix(arg, "-"):
			return opts, fmt.Errorf("unknown flag %s", arg)
		default:
			if opts.Target != "" {
				return opts, fmt.Errorf("unexpected extra argument %q", arg)
			}
			opts.Target = arg
		}
	}
	return opts, nil
}

var errHelp = errors.New("help requested")

// resolveTarget 把命令行目标转成一条项目记录
// 接受三种形式，已有的项目（按 ID 或名字）、本地目录，或什么都不给（表示当前目录）
// 本地目录会即时注册，命令行因此不需要事先准备，CI 场景下每次运行的检出路径都会变
func resolveTarget(ctx context.Context, app *App, opts cliOptions) (*model.Project, error) {
	if opts.Project != "" {
		return app.Svc.ResolveProject(ctx, opts.Project)
	}

	target := opts.Target
	if target == "" {
		target = "."
	}

	// 与已注册项目同名的目标优先，但前提是它同时不是一个目录，否则
	// `fizzra scan ./src` 就有歧义
	if _, err := os.Stat(target); err != nil {
		if project, err := app.Svc.ResolveProject(ctx, target); err == nil {
			return project, nil
		}
		return nil, fmt.Errorf("no such project or directory: %s", target)
	}

	abs, err := filepath.Abs(target)
	if err != nil {
		return nil, fmt.Errorf("resolve path %s: %w", target, err)
	}

	// 复用已指向该目录的项目，反复的 CI 运行因此不会往数据库里塞一堆重复项
	//
	// 语言是重新检测的而不是直接信旧的。在语言检测存在之前注册的项目带着旧的硬编码
	// "go"，沿用它会让 Go 分析器永远对着 Rust 代码树跑，而那正是这套检测要防的静默零
	// 结果失败。重新检测也覆盖了仓库后来才加上清单文件这种常见情况
	language, kind := detectLanguageKind(abs)

	projects, err := app.Svc.ListProjects(ctx)
	if err == nil {
		for _, p := range projects {
			if p.LocalPath == "" || !samePath(p.LocalPath, abs) {
				continue
			}
			if p.Language != language {
				updated, err := app.Svc.SetLanguage(ctx, p.ID, language, kind)
				if err != nil {
					// 不致命：扫描仍可用已存的值继续，但要说明，而不是静默扫错语言
					return nil, fmt.Errorf("update language for %s: %w", p.Name, err)
				}
				return updated, nil
			}
			return &p.Project, nil
		}
	}

	name := filepath.Base(abs)
	project, err := app.Svc.CreateProject(ctx, service.CreateProjectInput{
		Name:        name,
		Description: "Registered automatically by the CLI",
		LocalPath:   abs,
		Language:    language,
		Kind:        kind,
	})
	if err != nil {
		// 重名不致命：回退到由路径派生的名字，不同检出的反复扫描因此仍可区分
		project, err = app.Svc.CreateProject(ctx, service.CreateProjectInput{
			Name:      name + "-" + shortHash(abs),
			LocalPath: abs,
			Language:  language,
			Kind:      kind,
		})
		if err != nil {
			return nil, err
		}
	}
	return project, nil
}

// detectLanguageKind 根据目录里包含的清单文件猜测其生态与框架
//
// 检测决定分析器的选择，所以猜错直接决定了这次扫描到底查不查东西。在这之前，它对
// 所有不是 Wails 的东西一律返回 "go"，于是一个通过 `fizzra scan <path>` 注册的
// Rust 项目会变成 Go 项目，每个分析器都空转，结果是干净的评分
//
// 根目录和一层子目录都会被搜索，因为框架并不总是把清单放在顶层。Tauri 应用就是迫使
// 这么做的例子，它的 Cargo.toml 在 src-tauri/ 下，而根目录只有一个给前端的
// package.json，所以只查根目录会把一个 Rust 后端归类成 JavaScript 项目，然后发现
// 没有东西可查
//
// 顺序很要紧：
//   - 先看框架标记，因为它们既标识语言也标识项目类型
//   - 再看语言清单，根目录先于子目录，这样一个根目录已说明自身身份的仓库不会被一个
//     vendored 的子项目覆盖
func detectLanguageKind(dir string) (language, kind string) {
	// frameworkAt 报告框架标记是否存在于 dir 或下一层，Tauri 和 Wails 都把标记放在
	// 那里
	exists := func(name string) bool {
		_, err := os.Stat(filepath.Join(dir, name))
		return err == nil
	}
	// 部分清单没有固定文件名（.csproj 随项目命名），按后缀匹配
	hasSuffix := func(suffix string) bool {
		entries, err := os.ReadDir(dir)
		if err != nil {
			return false
		}
		for _, e := range entries {
			if !e.IsDir() && strings.EqualFold(filepath.Ext(e.Name()), suffix) {
				return true
			}
		}
		return false
	}

	switch {
	case exists("wails.json"):
		return model.LanguageGo, model.ProjectKindWails
	case exists("tauri.conf.json") || exists(filepath.Join("src-tauri", "tauri.conf.json")):
		return model.LanguageRust, ""
	}

	// 语言清单，根目录优先
	manifests := []struct {
		names    []string
		language string
	}{
		{[]string{"go.mod"}, model.LanguageGo},
		{[]string{"Cargo.toml"}, model.LanguageRust},
		{[]string{"pyproject.toml", "setup.py", "requirements.txt"}, model.LanguagePython},
		// pom.xml 与 build.gradle 是 Java 的两类构建清单，排在 package.json 之前
		// 一个带前端子模块的 Java 仓库根目录往往两者都有，而源码主体在 Java 一侧
		{[]string{"pom.xml", "build.gradle", "build.gradle.kts"}, model.LanguageJava},
		{[]string{".csproj", ".sln"}, model.LanguageCSharp},
		{[]string{"package.json", "tsconfig.json"}, model.LanguageJavaScript},
		// C/C++ 和 PHP 没有通用的清单文件，只能看源码扩展名。放在最后：有清单的
		// 项目先按清单判定，否则一个带 .cpp 的 JS 仓库会被误认成 C++
		//
		// composer.json 排在 .php 之前，一个同时有前端构建的 PHP 仓库根目录往往
		// 两者都有，项目主体仍按 PHP 算
		{[]string{"composer.json"}, model.LanguagePHP},
		{[]string{".c", ".cc", ".cpp", ".cxx"}, model.LanguageCpp},
		{[]string{".php"}, model.LanguagePHP},
	}

	for _, m := range manifests {
		for _, name := range m.names {
			// 以点开头的项是后缀，见 hasSuffix
			if strings.HasPrefix(name, ".") {
				if hasSuffix(name) {
					return m.language, ""
				}
				continue
			}
			if exists(name) {
				return m.language, ""
			}
		}
	}

	// 根目录什么都没有时检查直接子目录，跳过那些放依赖或构建产物而非源码的
	for _, m := range manifests {
		for _, name := range m.names {
			if strings.HasPrefix(name, ".") {
				if subdirHasSuffix(dir, name) {
					return m.language, ""
				}
				continue
			}
			if subdirHasManifest(dir, name) {
				return m.language, ""
			}
		}
	}

	// 哪里都没有清单时默认成 Go
	// 那是未构建或只有文档的仓库常见的样子，扫描会报出什么都没跑，而不是报通过
	return model.LanguageGo, ""
}

// subdirHasManifest 报告该目录的直接子目录中是否恰好有一个包含所给文件
//
// 只考虑直接子目录。五层之下的 Cargo.toml 属于某个依赖或 vendored crate，不是项目
// 自己的清单，把它当成项目清单会让一个内含 Rust 工具的 Go 仓库被归错类
func subdirHasManifest(dir, name string) bool {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return false
	}
	for _, e := range entries {
		if !e.IsDir() || skipDetectDir(e.Name()) {
			continue
		}
		if _, err := os.Stat(filepath.Join(dir, e.Name(), name)); err == nil {
			return true
		}
	}
	return false
}

// subdirHasSuffix 与 subdirHasManifest 同理，按扩展名匹配
func subdirHasSuffix(dir, suffix string) bool {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return false
	}
	for _, e := range entries {
		if !e.IsDir() || skipDetectDir(e.Name()) {
			continue
		}
		inner, err := os.ReadDir(filepath.Join(dir, e.Name()))
		if err != nil {
			continue
		}
		for _, f := range inner {
			if !f.IsDir() && strings.EqualFold(filepath.Ext(f.Name()), suffix) {
				return true
			}
		}
	}
	return false
}

// skipDetectDir 列出永远不会放着项目自身清单的目录
func skipDetectDir(name string) bool {
	switch name {
	case "node_modules", "vendor", "target", "dist", "build", ".git", ".venv", "venv":
		return true
	}
	// 隐藏目录是工具链的东西，不是源码
	return strings.HasPrefix(name, ".")
}

func samePath(a, b string) bool {
	ca, errA := filepath.Abs(a)
	cb, errB := filepath.Abs(b)
	if errA != nil || errB != nil {
		return a == b
	}
	return strings.EqualFold(filepath.Clean(ca), filepath.Clean(cb))
}

// runScan 注册项目、创建一次分析并就地执行
//
// 命令行有意绕过队列，CI 任务要的是确定、同步的行为和有意义的退出码，不是发完就不管
func runScan(ctx context.Context, app *App, opts cliOptions) (*model.Project, *model.Analysis, error) {
	project, err := resolveTarget(ctx, app, opts)
	if err != nil {
		return nil, nil, err
	}

	// 这个参数只覆盖本次运行的配置。之所以改共享配置，是因为引擎从那里读离线行为，
	// 而为了传达同一件事再把一个参数穿过每一层，是比改那个唯一归属地更差的接缝
	if opts.Offline {
		app.Cfg.Scan.Offline = true
	}

	analysis := &model.Analysis{
		ID:        model.NewID("ana"),
		ProjectID: project.ID,
		Status:    model.StatusPending,
		Trigger:   triggerFor(opts),
		Branch:    project.Branch,
		Path:      project.LocalPath,
		Analyzers: opts.Analyzers,
		StartedAt: time.Now(),
		CreatedAt: time.Now(),
	}
	if err := app.Data.Analyses().Create(analysis); err != nil {
		return nil, nil, err
	}

	runCtx, cancel := context.WithTimeout(ctx, opts.Timeout)
	defer cancel()

	if err := app.Engine.Run(runCtx, analysis.ID); err != nil {
		return project, analysis, err
	}

	// 重新加载，让调用方看到已持久化的状态、评分和计数
	updated, err := app.Data.Analyses().Get(analysis.ID)
	if err != nil {
		return project, analysis, err
	}
	return project, updated, nil
}

func triggerFor(opts cliOptions) string {
	if os.Getenv("CI") != "" || os.Getenv("GITHUB_ACTIONS") != "" || os.Getenv("GITLAB_CI") != "" {
		return model.TriggerCI
	}
	return model.TriggerCLI
}

// ---------------------------------------------------------------------------
// fizzra scan
// ---------------------------------------------------------------------------

func cmdScan(app *App, cfg *config.Config, args []string) int {
	opts, err := parseCommonFlags(args)
	if err != nil {
		if errors.Is(err, errHelp) {
			fmt.Println(scanUsage)
			return 0
		}
		fmt.Fprintf(os.Stderr, "error: %v\n\n%s\n", err, scanUsage)
		return 2
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	project, analysis, runErr := runScan(ctx, app, opts)
	if runErr != nil {
		fmt.Fprintf(os.Stderr, "scan failed: %v\n", runErr)
		return 1
	}

	issues, err := loadIssues(app, analysis.ID)
	if err != nil {
		fmt.Fprintf(os.Stderr, "load issues: %v\n", err)
		return 1
	}

	switch opts.Format {
	case "json":
		return emitJSON(app, project, analysis, issues, opts)
	case "html":
		return emitHTML(app, project, analysis, issues, opts)
	case "sarif":
		return emitSARIF(app, project, analysis, issues, opts)
	case "text", "":
		printScanSummary(project, analysis, issues, opts.Quiet)
	default:
		fmt.Fprintf(os.Stderr, "unknown format %q (use text, json, html or sarif)\n", opts.Format)
		return 2
	}
	return 0
}

const scanUsage = `Usage: fizzra scan [path|project] [flags]

Scans a project and prints a summary. Exits 0 regardless of findings; use
"fizzra ci" for a gate-enforcing exit code.

Flags:
  --project <name|id>   Target a registered project
  --analyzers <list>    Comma separated analyzer names
  --format <fmt>        text (default), json, html, sarif
  --output <file>       Write to a file instead of stdout
  --timeout <dur>       Overall budget, e.g. 10m (default 15m)
  --offline             Do not let analyzers reach the network
  --quiet               Only print the summary line

Examples:
  fizzra scan
  fizzra scan ./my-app --analyzers custom,metrics,wails
  fizzra scan ./my-app --offline
  fizzra scan --project my-app --format json --output scan.json`

// printScanSummary 渲染人类可读的命令行输出
func printScanSummary(project *model.Project, analysis *model.Analysis, issues []model.Issue, quiet bool) {
	if !quiet {
		fmt.Printf("\n  %s\n", project.Name)
		fmt.Printf("  %s\n\n", strings.Repeat("─", len(project.Name)+2))
	}

	// 不完整的扫描在数字之前告知，而不是之后
	//
	// 这里防的失败模式是，一次大多数分析器都死掉的运行会比健康的运行报出更高的
	// 分数（发现的少），而且门禁通过。一个看到「94.4 / A / 通过」就停下的读者已经被
	// 主动误导了，所以警告必须排在前面
	//
	// 「失败」和「跳过」是两件不同的事，措辞也不同。失败的分析器坏了，跳过的分析器
	// 正确地拒绝了，多数是因为工具没装。对着一个缺失的 linter 说「失败」，会把读者
	// 引去找一场根本没发生的崩溃
	if analysis.Incomplete {
		if len(analysis.FailedAnalyzers) > 0 {
			verb := "failed"
			if analysis.NoAnalyzerRan {
				verb = "skipped"
			}
			fmt.Printf("  !! INCOMPLETE — %d analyzer(s) %s: %s\n",
				len(analysis.FailedAnalyzers), verb, strings.Join(analysis.FailedAnalyzers, ", "))
			for i, reason := range analysis.FailureReasons {
				if i >= 2 {
					fmt.Printf("     … and %d more\n", len(analysis.FailureReasons)-2)
					break
				}
				// 对跳过标记来说，原因里已经带了分析器名，所以只在不会重复时才
				// 加前缀
				if strings.HasPrefix(reason, analysis.FailedAnalyzers[i]+":") {
					fmt.Printf("     %s\n", reason)
					continue
				}
				fmt.Printf("     %s: %s\n", analysis.FailedAnalyzers[i], reason)
			}
		}
		fmt.Printf("     The score below is computed from partial results and understates\n")
		fmt.Printf("     the real issue count. Fix the failure and re-run before trusting it.\n\n")
	}

	score := analysis.Score
	fmt.Printf("  Score          %.1f/100  (%s)\n", score, quality.Grade(score))
	fmt.Printf("  Gate           %s\n", analysis.GateStatus)
	if analysis.GateReason != "" && analysis.GateStatus == model.GateFailed {
		fmt.Printf("                 %s\n", analysis.GateReason)
	}
	fmt.Printf("  Issues         %d total\n", analysis.TotalIssues)
	fmt.Printf("                 %d critical · %d error · %d warning · %d info\n",
		analysis.CriticalIssues, analysis.ErrorIssues, analysis.WarningIssues, analysis.InfoIssues)

	m := analysis.Metrics
	if m.Files > 0 {
		fmt.Printf("  Code           %d files · %d lines (%d code)\n", m.Files, m.Lines, m.CodeLines)
		// 复杂度来自 AST 遍历，而那只对 Go 存在。对一个从未测量过的项目打印
		// 「avg 0.0」，读起来像「这段代码完美简单」，恰恰与已知的情况相反
		if m.Functions > 0 {
			fmt.Printf("  Complexity     avg %.1f · worst %d\n", m.AvgComplexity, m.MaxComplexity)
		} else {
			fmt.Printf("  Complexity     not measured (no AST walker for this language)\n")
		}
		fmt.Printf("  Duplication    %.1f%%\n", m.Duplication)
		if m.Coverage > 0 {
			fmt.Printf("  Coverage       %.1f%%\n", m.Coverage)
		}
	}
	fmt.Printf("  Duration       %s\n", time.Duration(analysis.DurationMS)*time.Millisecond)

	if analysis.Error != "" {
		fmt.Printf("  Note           %s\n", analysis.Error)
	}

	if quiet || len(issues) == 0 {
		if len(issues) == 0 {
			fmt.Printf("\n  No issues found.\n\n")
		} else {
			fmt.Println()
		}
		return
	}

	// 按文件分组，输出读起来像一条审查意见
	byFile := map[string][]model.Issue{}
	for _, issue := range issues {
		key := issue.File
		if key == "" {
			key = "(project-wide)"
		}
		byFile[key] = append(byFile[key], issue)
	}

	files := make([]string, 0, len(byFile))
	for f := range byFile {
		files = append(files, f)
	}
	slices.Sort(files)

	const maxFiles = 15
	shown := 0
	for _, file := range files {
		if shown >= maxFiles {
			fmt.Printf("\n  … and %d more file(s). Use --format json for the full list.\n\n", len(files)-shown)
			break
		}
		shown++

		fileIssues := byFile[file]
		fmt.Printf("\n  %s\n", file)
		const maxPerFile = 8
		for i, issue := range fileIssues {
			if i >= maxPerFile {
				fmt.Printf("      … %d more in this file\n", len(fileIssues)-maxPerFile)
				break
			}
			loc := ""
			if issue.Line > 0 {
				loc = fmt.Sprintf(":%d", issue.Line)
			}
			fmt.Printf("      %-8s %s%s  %s\n", severityTag(issue.Severity), file, loc, truncate(issue.Message, 78))
			fmt.Printf("               %s · %s\n", issue.Analyzer, issue.Rule)
		}
	}
	fmt.Println()
}

func severityTag(s model.Severity) string {
	switch s {
	case model.SeverityCritical:
		return "CRITICAL"
	case model.SeverityError:
		return "ERROR"
	case model.SeverityWarning:
		return "WARNING"
	default:
		return "INFO"
	}
}

func truncate(s string, n int) string {
	s = strings.ReplaceAll(s, "\n", " ")
	if len(s) <= n {
		return s
	}
	return s[:n-1] + "…"
}

func loadIssues(app *App, analysisID string) ([]model.Issue, error) {
	return app.Data.Issues().ListByAnalysis(analysisID, store.IssueFilter{})
}

// ---------------------------------------------------------------------------
// fizzra ci
// ---------------------------------------------------------------------------

func cmdCI(app *App, cfg *config.Config, args []string) int {
	// 在通用解析器看到之前，先把与门禁相关的参数摘出来
	var ciArgs []string
	ci := ciConfig{}
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--fail-on-critical":
			ci.FailOnCritical = true
		case "--allow-critical":
			ci.FailOnCritical = false
			ci.explicitAllowCritical = true
		case "--no-gate":
			ci.SkipGate = true
		case "--allow-skipped":
			ci.AllowSkipped = true
		case "--print-project":
			ci.PrintProject = true
		default:
			ciArgs = append(ciArgs, args[i])
		}
	}

	opts, err := parseCommonFlags(ciArgs)
	if err != nil {
		if errors.Is(err, errHelp) {
			fmt.Println(ciUsage)
			return 0
		}
		fmt.Fprintf(os.Stderr, "error: %v\n\n%s\n", err, ciUsage)
		return 2
	}
	if opts.Format == "text" {
		opts.Format = "text"
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	project, analysis, runErr := runScan(ctx, app, opts)
	if runErr != nil {
		fmt.Fprintf(os.Stderr, "scan failed: %v\n", runErr)
		return 1
	}

	issues, err := loadIssues(app, analysis.ID)
	if err != nil {
		fmt.Fprintf(os.Stderr, "load issues: %v\n", err)
		return 1
	}

	// 走 stderr，和报告正文分开，调用方 grep 这一行前缀即可
	if ci.PrintProject {
		fmt.Fprintf(os.Stderr, "project: %s\n", project.Name)
	}

	// 先输出机器可读的产物，这样只看文件的 CI 系统即使下面门禁失败也仍能拿到它
	switch opts.Format {
	case "json":
		if code := emitJSON(app, project, analysis, issues, opts); code != 0 {
			return code
		}
	case "html":
		if code := emitHTML(app, project, analysis, issues, opts); code != 0 {
			return code
		}
	case "sarif":
		if code := emitSARIF(app, project, analysis, issues, opts); code != 0 {
			return code
		}
	}

	printScanSummary(project, analysis, issues, opts.Quiet)

	if ci.SkipGate {
		fmt.Println("  Gate disabled (--no-gate); exiting 0.")
		return 0
	}

	// 在存储的门禁之上应用命令行覆盖
	gateResult := evaluateCIGate(app, project, analysis, issues, opts, ci)

	// 不完整的扫描能否决通过，无论重算后的门禁怎么说
	//
	// evaluateCIGate 会从持久化的指标重新求值门禁，好让 `--min-score` 和
	// `--fail-on` 的覆盖生效。这个重算看不到引擎记录的分析器失败，于是一次大多数
	// 分析器都死掉的运行会在这里得更高的分（发现的少）并以 0 退出。在这一点上
	// 以引擎的判定为准
	//
	// --allow-skipped 只对「因为工具没装而跳过」放行，且只在没有任何分析器真正
	// 失败时才生效。崩溃和超时属于另一类问题：重跑或修好后结论会变，不能当作
	// 环境没配好放过
	if analysis.Incomplete && gateResult.Passed {
		switch {
		case len(analysis.FailedAnalyzers) > 0:
			gateResult.Passed = false
			gateResult.Status = model.GateFailed
			gateResult.Reason = incompleteReason(analysis)
		case ci.AllowSkipped && len(analysis.SkippedAnalyzers) > 0 && analysis.AnalyzersCompleted > 0:
			// 用户显式放宽了「工具没装」这一种缺口。放行但必须说清楚覆盖不完整
			fmt.Printf("  Skipped        %d analyzer(s) had no tool installed and were not run: %s\n",
				len(analysis.SkippedAnalyzers), strings.Join(analysis.SkippedAnalyzers, ", "))
			if len(analysis.SkippedTools) > 0 {
				fmt.Printf("                 Missing tools: %s\n", strings.Join(analysis.SkippedTools, ", "))
			}
			fmt.Printf("                 Allowed by --allow-skipped; the score covers only the analyzers that ran.\n")
			gateResult.Reason = fmt.Sprintf(
				"gate passed with %d analyzer(s) skipped for missing tools", len(analysis.SkippedAnalyzers))
		default:
			// 没有放宽，或压根没有任何分析器检查过代码：否决
			gateResult.Passed = false
			gateResult.Status = model.GateFailed
			if ci.AllowSkipped && analysis.AnalyzersCompleted == 0 {
				gateResult.Reason = "no analyzer completed, so nothing was checked and --allow-skipped cannot pass this run"
			} else {
				gateResult.Reason = incompleteReason(analysis)
			}
		}
	}

	fmt.Printf("  Result         %s\n", strings.ToUpper(gateResult.Status))
	fmt.Printf("                 %s\n\n", gateResult.Reason)

	if gateResult.Passed {
		return 0
	}
	return 1
}

// incompleteReason 说明一次不完整的扫描为什么不通过
//
// 引擎侧的原因已经区分了「分析器失败」和「分析器跳过」，但命令行在这里用的是
// 重新求值的门禁，看不到引擎写的那段文字，只能自己拼。失败和跳过要分开说：
// 前者是项目或工具坏了，后者是这台机器上没装对应的检查工具，读者要做的准备不同
func incompleteReason(analysis *model.Analysis) string {
	failed := analysis.FailedAnalyzers
	skipped := analysis.SkippedAnalyzers

	switch {
	case len(failed) > 0:
		return fmt.Sprintf(
			"%d analyzer(s) failed (%s), so the scan is incomplete and cannot be trusted to pass",
			len(failed), strings.Join(failed, ", "))
	case len(skipped) > 0:
		return fmt.Sprintf(
			"no analyzer inspected the code (%d skipped: %s), so the scan is incomplete and cannot be trusted to pass",
			len(skipped), strings.Join(skipped, ", "))
	default:
		return "the scan is incomplete and cannot be trusted to pass"
	}
}

type ciConfig struct {
	FailOnCritical        bool
	explicitAllowCritical bool
	SkipGate              bool
	// AllowSkipped 只把「工具未安装」这一种不完整降级为通过
	//
	// 分析器崩溃或超时仍然失败：那是这台机器或这个项目坏了，重跑或修好后
	// 结论会变；而缺工具是环境准备问题，CI 模板里常常是用户漏了一步 setup。
	// 这个区分让前者的信号不被后者淹没
	AllowSkipped bool
	// PrintProject 把本次扫描注册的项目名输出到 stdout
	//
	// 项目名不一定等于目录名，重名时会被追加哈希。调用方想对同一次扫描再取报告，
	// 只能靠这个名字，猜目录名会在目录同名时取到另一个项目
	PrintProject bool
}

// evaluateCIGate 把存储的门禁与命令行覆盖合到一起
func evaluateCIGate(app *App, project *model.Project, analysis *model.Analysis, issues []model.Issue, opts cliOptions, ci ciConfig) quality.GateResult {
	gate, err := app.Svc.GetGate(context.Background(), project.ID)
	if err != nil {
		def := quality.DefaultGate()
		gate = &model.QualityGate{
			MinScore:      def.MinScore,
			MaxCritical:   def.MaxCritical,
			MaxHigh:       def.MaxHigh,
			MaxComplexity: def.MaxComplexity,
			Enabled:       true,
		}
	}

	qGate := service.GateFromModel(gate)

	// 命令行覆盖优先于存储的配置
	// CI 常常想要比交互式默认更严格的规则，又不想去改共享设置
	if opts.hasMinScore {
		qGate.MinScore = opts.MinScore
	}
	if len(opts.FailOn) > 0 {
		applied, err := applyFailOn(qGate, opts.FailOn)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			return quality.GateResult{
				Passed: false,
				Status: model.GateFailed,
				Reason: err.Error(),
			}
		}
		qGate = applied
	}

	input := quality.Input{
		Issues:  issues,
		Metrics: analysis.Metrics,
		Loc:     analysis.Metrics.CodeLines,
	}
	score := quality.Score(input, quality.DefaultWeights(), quality.DefaultThresholds())
	return quality.Evaluate(qGate, score, input)
}

// applyFailOn 把 "critical,error" 变成显式的门禁阈值
//
// 未知级别会报错而不是强行归成 warning。把 `--fail-on errors`（复数，或任何拼写
// 错误）静默理解成「按警告失败」，会让一个本意放宽门禁的笔误变成最严格的设置，与用户
// 的本意相反，而且没有任何迹象表明这发生了
func applyFailOn(g quality.Gate, levels []string) (quality.Gate, error) {
	for _, level := range levels {
		sev, err := model.ParseSeverityStrict(level)
		if err != nil {
			return g, err
		}
		switch sev {
		case model.SeverityCritical:
			g.MaxCritical = 0
		case model.SeverityError:
			g.MaxHigh = 0
		case model.SeverityWarning:
			g.FailOnWarning = true
		case model.SeverityInfo:
			// info 类发现按定义就是提示性的，不存在让它失败还有意义的阈值。拒绝
			// 而不是忽略，用户因此能知道他的参数没有作用
			return g, fmt.Errorf("--fail-on info is not supported: info findings are advisory and cannot fail a gate")
		}
	}
	return g, nil
}

const ciUsage = `Usage: fizzra ci [path|project] [flags]

Scans a project and exits non-zero when the quality gate fails. Intended for
CI pipelines.

Exit codes:
  0  gate passed
  1  gate failed, or the scan could not complete
  2  invalid usage

Flags:
  --project <name|id>    Target a registered project
  --format <fmt>         text (default), json, html, sarif
  --output <file>        Write the report to a file (for CI artefacts)
  --lang <code>          Language for the HTML report chrome (en, zh)
  --fail-on <levels>     Comma separated levels that fail the build
                         (critical, error, warning)
  --min-score <n>        Override the minimum score
  --no-gate              Always exit 0; useful for a first, informational run
  --allow-skipped        Exit 0 when the only gap is analyzers skipped for a
                         missing tool. Crashes and timeouts still fail, and a
                         scan where nothing ran never passes
  --print-project        Print the registered project name to stderr, for
                         callers that need to address the same scan again
  --timeout <dur>        Overall budget (default 15m)

Examples:
  fizzra ci
  fizzra ci --format sarif --output fizzra.sarif
  fizzra ci --project my-app --fail-on critical,error --min-score 80`

// ---------------------------------------------------------------------------
// fizzra report
// ---------------------------------------------------------------------------

func cmdReport(app *App, cfg *config.Config, args []string) int {
	opts, err := parseCommonFlags(args)
	if err != nil {
		if errors.Is(err, errHelp) {
			fmt.Println(reportUsage)
			return 0
		}
		fmt.Fprintf(os.Stderr, "error: %v\n\n%s\n", err, reportUsage)
		return 2
	}

	if opts.Format == "text" {
		opts.Format = "html"
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	project, analysis, issues, err := resolveReportTarget(ctx, app, opts)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}

	switch opts.Format {
	case "json":
		return emitJSON(app, project, analysis, issues, opts)
	case "html":
		return emitHTML(app, project, analysis, issues, opts)
	case "sarif":
		return emitSARIF(app, project, analysis, issues, opts)
	default:
		fmt.Fprintf(os.Stderr, "unknown format %q (use html, json or sarif)\n", opts.Format)
		return 2
	}
}

const reportUsage = `Usage: fizzra report [path|project] [flags]

Renders a report for an existing project, or scans one first when given a path.

Flags:
  --project <name|id>   Target a registered project
  --format <fmt>        html (default), json, sarif
  --output <file>       Write to a file instead of stdout
  --analyzers <list>    Analyzer names (only when scanning a new path)
  --lang <code>         Language for the HTML report chrome (en, zh)

Examples:
  fizzra report --project my-app --format html --output report.html
  fizzra report --project my-app --lang zh --output 报告.html
  fizzra report ./my-app --format json`

// resolveReportTarget 找到最近一次分析，需要时跑一次
func resolveReportTarget(ctx context.Context, app *App, opts cliOptions) (*model.Project, *model.Analysis, []model.Issue, error) {
	// 显式给出路径一律表示「去扫它」
	if opts.Project == "" && opts.Target != "" {
		if _, err := os.Stat(opts.Target); err == nil {
			project, analysis, err := runScan(ctx, app, opts)
			if err != nil {
				return nil, nil, nil, err
			}
			issues, err := loadIssues(app, analysis.ID)
			return project, analysis, issues, err
		}
	}

	project, err := resolveTarget(ctx, app, opts)
	if err != nil {
		return nil, nil, nil, err
	}

	analysis, err := app.Data.Analyses().LatestForProject(project.ID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return nil, nil, nil, fmt.Errorf("project %q has no completed analysis yet; run `fizzra scan` first", project.Name)
		}
		return nil, nil, nil, err
	}

	issues, err := loadIssues(app, analysis.ID)
	return project, analysis, issues, err
}

// ---------------------------------------------------------------------------
// 输出器
// ---------------------------------------------------------------------------

// buildReport 组装报告的载荷
//
// 组装逻辑在 service 里，因为面板也要导出同一份东西。这里只负责把已经取到手的
// issues 交给它，省掉一次重复查询
func buildReport(app *App, project *model.Project, analysis *model.Analysis, issues []model.Issue) (report.Report, error) {
	rep, err := app.Svc.BuildReport(context.Background(), project.ID, analysis.ID)
	if err != nil {
		return report.Report{}, err
	}
	// 命令行已经用 loadIssues 取过一遍，并可能套了 --severity 之类的过滤
	// 以调用方手上的那份为准，否则过滤参数会被静默忽略
	rep.Issues = issues
	rep.Stats = report.BuildStats(issues)
	return *rep, nil
}

// outputWriter 返回报告的目标位置，会遵循 --output
func outputWriter(opts cliOptions) (interface{ Write([]byte) (int, error) }, func() error, error) {
	if opts.Output == "" {
		return os.Stdout, func() error { return nil }, nil
	}
	if dir := filepath.Dir(opts.Output); dir != "." && dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, nil, fmt.Errorf("create output directory: %w", err)
		}
	}
	f, err := os.Create(opts.Output)
	if err != nil {
		return nil, nil, fmt.Errorf("create %s: %w", opts.Output, err)
	}
	return f, f.Close, nil
}

func emitJSON(app *App, project *model.Project, analysis *model.Analysis, issues []model.Issue, opts cliOptions) int {
	w, closeFn, err := outputWriter(opts)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}
	defer closeFn()

	rep, err := buildReport(app, project, analysis, issues)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}
	if err := report.WriteJSON(w, rep); err != nil {
		fmt.Fprintf(os.Stderr, "write json report: %v\n", err)
		return 1
	}
	if opts.Output != "" && !opts.Quiet {
		fmt.Fprintf(os.Stderr, "JSON report written to %s\n", opts.Output)
	}
	return 0
}

func emitHTML(app *App, project *model.Project, analysis *model.Analysis, issues []model.Issue, opts cliOptions) int {
	w, closeFn, err := outputWriter(opts)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}
	defer closeFn()

	rep, err := buildReport(app, project, analysis, issues)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}
	if err := report.WriteHTML(w, rep, opts.Lang); err != nil {
		fmt.Fprintf(os.Stderr, "write html report: %v\n", err)
		return 1
	}
	if opts.Output != "" && !opts.Quiet {
		fmt.Fprintf(os.Stderr, "HTML report written to %s\n", opts.Output)
	}
	return 0
}

func emitSARIF(app *App, project *model.Project, analysis *model.Analysis, issues []model.Issue, opts cliOptions) int {
	w, closeFn, err := outputWriter(opts)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}
	defer closeFn()

	rep, err := buildReport(app, project, analysis, issues)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}
	if err := report.WriteSARIF(w, rep); err != nil {
		fmt.Fprintf(os.Stderr, "write sarif report: %v\n", err)
		return 1
	}
	if opts.Output != "" && !opts.Quiet {
		fmt.Fprintf(os.Stderr, "SARIF report written to %s\n", opts.Output)
	}
	return 0
}

// ---------------------------------------------------------------------------
// fizzra projects / analyzers / rules
// ---------------------------------------------------------------------------

func cmdProjects(app *App, cfg *config.Config, args []string) int {
	if hasHelp(args) {
		fmt.Println(`Usage: fizzra projects

Lists every registered project with its latest score.`)
		return 0
	}

	ctx := context.Background()
	summaries, err := app.Svc.ListProjects(ctx)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}
	if len(summaries) == 0 {
		fmt.Println("No projects registered. Run `fizzra scan <path>` to add one.")
		return 0
	}

	fmt.Printf("\n  %-24s %-8s %-10s %-8s %s\n", "NAME", "SCORE", "GATE", "ISSUES", "LAST SCAN")
	fmt.Printf("  %s\n", strings.Repeat("─", 76))
	for _, s := range summaries {
		score, gate, count, last := "—", "—", "—", "never"
		if s.Latest != nil {
			score = fmt.Sprintf("%.1f", s.Latest.Score)
			gate = s.Latest.GateStatus
			count = fmt.Sprint(s.Latest.TotalIssues)
			last = s.Latest.CreatedAt.Format("2006-01-02 15:04")
		}
		fmt.Printf("  %-24s %-8s %-10s %-8s %s\n",
			truncate(s.Name, 24), score, gate, count, last)
	}
	fmt.Printf("\n  %d project(s)\n\n", len(summaries))
	return 0
}

func cmdAnalyzers(app *App, cfg *config.Config, args []string) int {
	if hasHelp(args) {
		fmt.Println(`Usage: fizzra analyzers

Shows every registered analyzer and whether its external tools are installed.`)
		return 0
	}

	ctx := context.Background()
	info := app.Svc.Info(ctx)

	fmt.Printf("\n  %-14s %-10s %-8s %s\n", "ANALYZER", "CATEGORY", "STATUS", "REQUIRES")
	fmt.Printf("  %s\n", strings.Repeat("─", 72))
	for _, a := range info.Analyzers {
		status := "ready"
		requires := "—"
		if !a.Ready {
			status = "missing"
			requires = strings.Join(a.MissingTools, ", ")
		} else if len(a.Requires) > 0 {
			requires = strings.Join(a.Requires, ", ")
		}
		fmt.Printf("  %-14s %-10s %-8s %s\n", a.Name, a.Category, status, requires)
	}

	fmt.Println()
	for _, a := range info.Analyzers {
		fmt.Printf("  %s\n      %s\n", a.Name, wrapText(a.Description, 74, "      "))
	}

	missing := missingTools(info)
	installHints := collectInstallHints(info)
	if len(missing) > 0 {
		fmt.Printf("\n  Install the missing tools to enable everything:\n")
		for _, m := range missing {
			// 优先用分析器声明的提示
			//
			// 光有名字不足以拼出命令，它对工具属于哪个生态只字未提，所以这个循环
			// 过去会打印 `go install oxlint@latest`，一个立刻失败的畸形模块路径。
			// 知道自己的工具怎么安装的分析器现在会说明，下面的回退只针对 Go 工具，
			// 那是从名字推导命令唯一正确的情况
			if hint := installHints[m]; hint != "" {
				fmt.Printf("      %s\n", hint)
				continue
			}
			fmt.Printf("      go install %s@latest\n", installPath(m))
		}
	}
	fmt.Println()
	return 0
}

// missingTools 返回不可用的依赖名，已排序
//
// 工具名可能带一个括号标注，比如 "ruff (not probed)"，所以调用方按前缀而不是按
// 原始字符串去查提示
func missingTools(info service.SystemInfo) []string {
	var out []string
	seen := map[string]bool{}
	for _, a := range info.Analyzers {
		for _, m := range a.MissingTools {
			if !seen[m] {
				seen[m] = true
				out = append(out, m)
			}
		}
	}
	slices.Sort(out)
	return out
}

// collectInstallHints 把每个不可用的工具名映射到其分析器声明的安装提示
//
// 按工具名而不是分析器名索引，因为列表打印的粒度就是工具名，两个分析器共用一个
// 工具，不该把同一条指示产出两遍
func collectInstallHints(info service.SystemInfo) map[string]string {
	hints := map[string]string{}
	for _, a := range info.Analyzers {
		if a.Ready || a.InstallHint == "" {
			continue
		}
		for _, tool := range a.MissingTools {
			hints[toolName(tool)] = a.InstallHint
		}
	}
	return hints
}

// toolName 去掉 Registry.Readiness 给缺失工具附加的标注
func toolName(missing string) string {
	if i := strings.Index(missing, " ("); i > 0 {
		return missing[:i]
	}
	return missing
}

func installPath(tool string) string {
	switch tool {
	case "golangci-lint":
		return "github.com/golangci/golangci-lint/v2/cmd/golangci-lint"
	case "staticcheck":
		return "honnef.co/go/tools/cmd/staticcheck"
	case "gosec":
		return "github.com/securego/gosec/v2/cmd/gosec"
	case "govulncheck":
		return "golang.org/x/vuln/cmd/govulncheck"
	default:
		return tool
	}
}

func cmdRules(app *App, cfg *config.Config, args []string) int {
	// `--help` 必须在解析子命令之前就回应。它以横杠开头，所以下面那个「这是子命令名
	// 吗」的判断会跳过它，转而跑默认的 list，结果要帮助却静默产出了一份规则列表
	if hasHelp(args) {
		fmt.Println(rulesUsage)
		return 0
	}

	sub := "list"
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		sub = args[0]
	}

	ctx := context.Background()

	switch sub {
	case "list":
		rules, err := app.Svc.ListRules(ctx)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			return 1
		}
		if len(rules) == 0 {
			fmt.Println("No rules defined.")
			return 0
		}
		fmt.Printf("\n  %-32s %-10s %-8s %s\n", "RULE", "SEVERITY", "ENABLED", "SOURCE")
		fmt.Printf("  %s\n", strings.Repeat("─", 72))
		for _, r := range rules {
			source := "custom"
			if r.Builtin {
				source = "built-in"
			}
			enabled := "yes"
			if !r.Enabled {
				enabled = "no"
			}
			fmt.Printf("  %-32s %-10s %-8s %s\n", truncate(r.Name, 32), r.Severity, enabled, source)
		}
		fmt.Printf("\n  %d rule(s)\n\n", len(rules))
		return 0

	case "export":
		// 输出一段与 fizzra.yaml 兼容的规则块
		rules, err := app.Svc.ListRules(ctx)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			return 1
		}
		out := rulesFile{Rules: make([]ruleYAML, 0, len(rules))}
		for _, r := range rules {
			out.Rules = append(out.Rules, ruleYAML{
				Name:     r.Name,
				Analyzer: r.Analyzer,
				Severity: string(r.Severity),
				Message:  r.Message,
				Advice:   r.Advice,
				Patterns: r.Patterns,
				Paths:    r.Paths,
				Exclude:  r.Exclude,
				Enabled:  &r.Enabled,
				Builtin:  r.Builtin,
			})
		}
		enc := yaml.NewEncoder(os.Stdout)
		enc.SetIndent(2)
		defer enc.Close()
		if err := enc.Encode(out); err != nil {
			fmt.Fprintf(os.Stderr, "encode rules: %v\n", err)
			return 1
		}
		return 0

	case "check":
		// 校验每条模式都能编译。对一个把规则纳入版本控制的仓库，这在 CI 里很有用
		rules, err := app.Svc.ListRules(ctx)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			return 1
		}
		bad := 0
		for _, r := range rules {
			if _, err := app.Svc.ValidateRule(r); err != nil {
				fmt.Printf("  INVALID  %s: %v\n", r.Name, err)
				bad++
			}
		}
		if bad == 0 {
			fmt.Printf("  All %d rule(s) compile cleanly.\n", len(rules))
			return 0
		}
		fmt.Printf("\n  %d invalid rule(s)\n", bad)
		return 1

	case "help", "--help", "-h":
		fmt.Println(rulesUsage)
		return 0

	default:
		fmt.Fprintf(os.Stderr, "unknown rules subcommand %q\n\n%s\n", sub, rulesUsage)
		return 2
	}
}

const rulesUsage = `Usage: fizzra rules <subcommand>

Subcommands:
  list      List every rule with severity and state (default)
  export    Print the rule set as YAML for fizzra.yaml
  check     Validate that every rule pattern compiles

Examples:
  fizzra rules list
  fizzra rules export > fizzra-rules.yaml
  fizzra rules check`

// ruleYAML 是导出的形状，与 config.RuleConfig 一致
type ruleYAML struct {
	Name     string   `yaml:"name"`
	Analyzer string   `yaml:"analyzer,omitempty"`
	Severity string   `yaml:"severity"`
	Message  string   `yaml:"message,omitempty"`
	Advice   string   `yaml:"advice,omitempty"`
	Patterns []string `yaml:"patterns"`
	Paths    []string `yaml:"paths,omitempty"`
	Exclude  []string `yaml:"exclude,omitempty"`
	Enabled  *bool    `yaml:"enabled,omitempty"`
	Builtin  bool     `yaml:"builtin,omitempty"`
}

type rulesFile struct {
	Rules []ruleYAML `yaml:"rules"`
}

// ---------------------------------------------------------------------------
// 辅助函数
// ---------------------------------------------------------------------------

func hasHelp(args []string) bool {
	for _, a := range args {
		if a == "--help" || a == "-h" || a == "help" {
			return true
		}
	}
	return false
}

// shortHash 从路径推导出一个短的稳定后缀，用于区分自动注册的项目
func shortHash(s string) string {
	sum := sha256.Sum256([]byte(strings.ToLower(filepath.ToSlash(s))))
	return hex.EncodeToString(sum[:])[:6]
}

func splitCSV(s string) []string {
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// wrapText 按宽度折行，续行带缩进
func wrapText(s string, width int, indent string) string {
	words := strings.Fields(s)
	if len(words) == 0 {
		return ""
	}
	var b strings.Builder
	lineLen := 0
	for i, w := range words {
		if i > 0 {
			if lineLen+1+len(w) > width {
				b.WriteString("\n")
				b.WriteString(indent)
				lineLen = 0
			} else {
				b.WriteString(" ")
				lineLen++
			}
		}
		b.WriteString(w)
		lineLen += len(w)
	}
	return b.String()
}
