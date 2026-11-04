package analyzer

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
)

// ---------------------------------------------------------------------------
// gofmt
// ---------------------------------------------------------------------------

// GofmtAnalyzer 用标准工具链检查格式，不需要任何第三方二进制，所以始终可用
type GofmtAnalyzer struct{ goOnly }

// NewGofmtAnalyzer 构造该分析器
func NewGofmtAnalyzer() Analyzer { return &GofmtAnalyzer{} }

func (a *GofmtAnalyzer) Name() string { return "gofmt" }

func (a *GofmtAnalyzer) Description() string {
	return "Reports Go files that are not gofmt-formatted. Uses the local Go toolchain."
}

func (a *GofmtAnalyzer) Category() string { return "lint" }
func (a *GofmtAnalyzer) Requires() []string {
	return []string{"gofmt"}
}

func (a *GofmtAnalyzer) Analyze(ctx context.Context, projectPath string) ([]Issue, error) {
	return nil, nil
}

func (a *GofmtAnalyzer) AnalyzeWithContext(actx *Context) ([]Issue, error) {
	if _, err := exec.LookPath("gofmt"); err != nil {
		// 与 go vet 同理：返回空结果会被当成「检查过且干净」
		return nil, ErrToolUnavailable("gofmt")
	}

	// gofmt 配 -l 时从 stdin 读取文件列表
	paths := make([]string, 0, len(actx.Files.GoFiles()))
	for _, fi := range actx.Files.GoFiles() {
		paths = append(paths, fi.Abs)
	}
	if len(paths) == 0 {
		return nil, ErrNothingToCheck("gofmt (no Go files)")
	}

	// 对目录树跑 gofmt -l，而不是塞上千个 argv 参数，遍历目录也能保持文件顺序
	out, err := actx.Runner.Run(actx.Context, actx.ProjectPath, "gofmt", "-l", ".")
	if err != nil {
		// 即使有文件需要格式化，gofmt 也退出 0，所以这里的报错是真正的失败
		// （比如某个文件有语法错误）
		if IsToolMissing(err) {
			return nil, nil
		}
		actx.Log.Debug("gofmt returned an error", "err", err)
	}

	var issues []Issue
	for _, line := range splitLines(string(out)) {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		rel := filepath.ToSlash(strings.TrimPrefix(line, "./"))
		issues = append(issues, Issue{
			Analyzer: a.Name(),
			Rule:     "gofmt",
			Severity: SeverityWarning,
			Message:  "file is not formatted with gofmt",
			Advice:   "Run `gofmt -w " + rel + "` (or `go fmt ./...`) and commit the result.",
			File:     rel,
			Line:     1,
		})
	}
	return issues, nil
}

// ---------------------------------------------------------------------------
// go vet
// ---------------------------------------------------------------------------

// GoVetAnalyzer 运行标准的 vet 工具
type GoVetAnalyzer struct{ goOnly }

// NewGoVetAnalyzer 构造该分析器
func NewGoVetAnalyzer() Analyzer { return &GoVetAnalyzer{} }

func (a *GoVetAnalyzer) Name() string { return "go-vet" }

func (a *GoVetAnalyzer) Description() string {
	return "Runs go vet: suspicious constructs the compiler accepts, such as bad Printf verbs, lost cancel funcs and unreachable code."
}

func (a *GoVetAnalyzer) Category() string   { return "lint" }
func (a *GoVetAnalyzer) Requires() []string { return []string{"go"} }

// vetDiag 匹配 go vet 的 `file.go:12:34: message` 输出
var vetDiag = regexp.MustCompile(`^(.+?\.go):(\d+):(?:(\d+):)?\s*(.*)$`)

func (a *GoVetAnalyzer) Analyze(ctx context.Context, projectPath string) ([]Issue, error) {
	return nil, nil
}

func (a *GoVetAnalyzer) AnalyzeWithContext(actx *Context) ([]Issue, error) {
	if _, err := actx.Runner.LookPath("go"); err != nil {
		// 必须报「工具缺失」而不是返回空结果。返回 nil 等于告诉引擎「我跑完了，
		// 没发现问题」，而引擎据此不计入跳过，于是一个没装 Go 的机器上扫描会
		// 报出通过的高分
		return nil, ErrToolUnavailable("go (go vet)")
	}
	if !actx.Files.Exists("go.mod") {
		// go vet 需要模块上下文，对裸目录运行会大声失败
		return nil, ErrNothingToCheck("go vet (no go.mod)")
	}

	out, err := actx.Runner.Run(actx.Context, actx.ProjectPath, "go", "vet", "./...")
	if err != nil {
		if ee := asExitError(err); ee != nil && ee.TimedOut {
			return nil, fmt.Errorf("go vet timed out")
		}
		// vet 发现问题时退出码非零是正常的，stderr 里带着的是构建失败，不该
		// 被当成问题报出来
		actx.Log.Debug("go vet exited non-zero", "err", err)
	}

	var issues []Issue
	for _, line := range splitLines(string(out)) {
		m := vetDiag.FindStringSubmatch(strings.TrimSpace(line))
		if m == nil {
			continue
		}
		lineNo, _ := strconv.Atoi(m[2])
		col := 0
		if m[3] != "" {
			col, _ = strconv.Atoi(m[3])
		}
		issues = append(issues, Issue{
			Analyzer: a.Name(),
			Rule:     "govet",
			Severity: classifyVetMessage(m[4]),
			Message:  strings.TrimSpace(m[4]),
			Advice:   "go vet findings are almost always real bugs. Fix the construct rather than silencing the check.",
			File:     filepath.ToSlash(m[1]),
			Line:     lineNo,
			Column:   col,
			DocURL:   "https://pkg.go.dev/cmd/vet",
		})
	}
	return issues, nil
}

// classifyVetMessage 把指向真实运行时故障的发现提级
func classifyVetMessage(msg string) string {
	lower := strings.ToLower(msg)
	switch {
	case strings.Contains(lower, "printf"), strings.Contains(lower, "lostcancel"),
		strings.Contains(lower, "unreachable"), strings.Contains(lower, "nilness"):
		return SeverityError
	case strings.Contains(lower, "copylocks"), strings.Contains(lower, "atomic"):
		return SeverityError
	default:
		return SeverityWarning
	}
}

// ---------------------------------------------------------------------------
// golangci-lint
// ---------------------------------------------------------------------------

// GolangciAnalyzer 接入聚合式 linter golangci-lint
type GolangciAnalyzer struct {
	goOnly
	// probeOnce 保护版本探测，jsonFlag 缓存它的结果
	probeOnce sync.Once
	jsonFlag  string
}

// NewGolangciAnalyzer 构造该分析器
func NewGolangciAnalyzer() Analyzer { return &GolangciAnalyzer{} }

func (a *GolangciAnalyzer) Name() string { return "golangci" }

func (a *GolangciAnalyzer) Description() string {
	return "Runs golangci-lint: unused code, error handling, style and a broad set of static analysis linters in one pass."
}

func (a *GolangciAnalyzer) Category() string   { return "lint" }
func (a *GolangciAnalyzer) Requires() []string { return []string{"golangci-lint"} }

// golangciJSON 是我们用到的 `golangci-lint run --out-format json` 输出的子集
type golangciJSON struct {
	Issues []struct {
		FromLinter  string   `json:"FromLinter"`
		Text        string   `json:"Text"`
		Severity    string   `json:"Severity"`
		SourceLines []string `json:"SourceLines"`
		Pos         struct {
			Filename string `json:"Filename"`
			Line     int    `json:"Line"`
			Column   int    `json:"Column"`
		} `json:"Pos"`
	} `json:"Issues"`
	Report struct {
		Linters []struct {
			Name string `json:"Name"`
		} `json:"Linters"`
	} `json:"Report"`
}

func (a *GolangciAnalyzer) Analyze(ctx context.Context, projectPath string) ([]Issue, error) {
	return nil, nil
}

func (a *GolangciAnalyzer) AnalyzeWithContext(actx *Context) ([]Issue, error) {
	if _, err := actx.Runner.LookPath("golangci-lint"); err != nil {
		return nil, ErrToolUnavailable("golangci-lint")
	}
	if !actx.Files.Exists("go.mod") {
		return nil, ErrToolUnavailable("golangci-lint (no go.mod in project root)")
	}

	// golangci-lint 在 v2 改了 JSON 输出参数，而且两种拼写互斥
	// v1 拒绝 --output.json.path，v2 拒绝 --out-format，两者输出同一份文档。
	// 所以从 --version 探测一次版本，之后一直用匹配的那个参数
	//
	// go install ...@latest 装出来的是 v2，很多机器上还留着旧 SDK 带来的 v1，
	// 写死一个参数会静默弄坏其中一半
	jsonFlag := a.jsonOutputFlag(actx)

	out, runErr := actx.Runner.Run(actx.Context, actx.ProjectPath,
		"golangci-lint", "run", jsonFlag, "--issues-exit-code", "0", "./...")
	if runErr != nil && len(out) == 0 {
		if IsTimeout(runErr) {
			return nil, runErr
		}
		// 诊断在 stderr 上，两路输出都过一遍修复建议匹配器，这样版本不匹配会被
		// 解释清楚，而不是把原文直接贴出来
		stderr := ""
		if ee := asExitError(runErr); ee != nil {
			stderr = ee.Stderr
		}
		if remedy := golangciRemedy(stderr); remedy != "" {
			return nil, fmt.Errorf("golangci-lint could not analyze the project: %s", remedy)
		}
		return nil, fmt.Errorf("golangci-lint could not analyze the project: %s", toolFailureText(runErr))
	}

	// 编译不过的项目会让 golangci-lint 报出一个类型检查错误和零个问题。
	// 这时报「干净」完全是反的，什么都没检查过，所以要把失败暴露出来
	//
	// stderr 也要看，golangci-lint 把这些诊断写在 stderr 上，stdout 留空，所以
	// 检查必须同时看两路
	stderr := ""
	if ee := asExitError(runErr); ee != nil {
		stderr = ee.Stderr
	}
	// 只有运行失败才值得诊断。golangci-lint 产出了有效的问题文档就说明它成功
	// 了，而在那份文档里搜「typechecking error」这类字样会命中普通的问题文本，
	// 把一次健康的扫描报成失败
	//
	// 剩下的检查很窄，只认版本不匹配的特征串，并且要在完整的 stderr 上匹配，
	// 因为它位于单行摘要保留的 200 字符之外
	if looksLikeGolangciRunFailure(out) {
		if remedy := golangciRemedy(stderr + string(out)); remedy != "" {
			return nil, fmt.Errorf("golangci-lint could not analyze the project: %s", remedy)
		}
		return nil, fmt.Errorf("golangci-lint could not analyze the project: %s", toolFailureText(runErr))
	}

	payload := jsonPayload(out)
	if payload == nil {
		return nil, fmt.Errorf("golangci-lint produced no parsable report: %s", collapseDiagnostic(string(out)))
	}

	var report golangciJSON
	if err := json.Unmarshal(payload, &report); err != nil {
		return nil, fmt.Errorf("parse golangci-lint output: %w", err)
	}

	issues := make([]Issue, 0, len(report.Issues))
	for _, it := range report.Issues {
		code := ""
		if len(it.SourceLines) > 0 {
			code = strings.TrimSpace(it.SourceLines[0])
		}
		issues = append(issues, Issue{
			Analyzer: a.Name(),
			Rule:     it.FromLinter,
			Severity: mapGolangciSeverity(it.FromLinter, it.Severity),
			Message:  it.Text,
			Advice:   adviceForLinter(it.FromLinter),
			File:     filepath.ToSlash(it.Pos.Filename),
			Line:     it.Pos.Line,
			Column:   it.Pos.Column,
			Code:     code,
			DocURL:   "https://golangci-lint.run/usage/linters/#" + it.FromLinter,
		})
	}
	return issues, nil
}

// mapGolangciSeverity 把 linter 名映射到本项目的严重级别
//
// golangci-lint 自己的 Severity 字段通常是空的，默认值也不一致，所以按名字把
// 面向正确性的 linter 提级
func mapGolangciSeverity(linter, raw string) string {
	if raw != "" {
		return normalizeSeverity(raw)
	}
	switch linter {
	case "errcheck", "govet", "staticcheck", "ineffassign", "typecheck":
		return SeverityError
	case "unused", "gosimple", "bodyclose", "rowserrcheck", "sqlclosecheck":
		return SeverityWarning
	case "gocritic", "revive", "stylecheck", "misspell":
		return SeverityInfo
	default:
		return SeverityWarning
	}
}

func adviceForLinter(linter string) string {
	switch linter {
	case "errcheck":
		return "Handle the returned error, or explicitly assign it to _ with a comment explaining why it is safe to ignore."
	case "unused":
		return "Remove the dead code. If it is part of a public API, add a test or a reference so the linter can see the use."
	case "ineffassign":
		return "The assignment is overwritten before it is read. Delete it."
	case "staticcheck":
		return "See the SA/S/ST/QF check documentation for the specific fix."
	case "gocritic":
		return "gocritic suggests an idiomatic rewrite; the diagnostic names the check."
	case "misspell":
		return "Correct the spelling."
	}
	return ""
}

// ---------------------------------------------------------------------------
// staticcheck
// ---------------------------------------------------------------------------

// StaticcheckAnalyzer 在 staticcheck 单独安装时接入它
//
// golangci-lint 内置了 staticcheck，所以这个分析器主要对固定了比 golangci-lint
// 自带版本更新的 staticcheck 的项目有用
type StaticcheckAnalyzer struct{ goOnly }

// NewStaticcheckAnalyzer 构造该分析器
func NewStaticcheckAnalyzer() Analyzer { return &StaticcheckAnalyzer{} }

func (a *StaticcheckAnalyzer) Name() string { return "staticcheck" }

func (a *StaticcheckAnalyzer) Description() string {
	return "Runs staticcheck standalone: deep static analysis for bugs, simplifications and questionable code."
}

func (a *StaticcheckAnalyzer) Category() string   { return "lint" }
func (a *StaticcheckAnalyzer) Requires() []string { return []string{"staticcheck"} }

// staticcheck 的 `file:line:col: message` 布局与多数 Go 工具相同
var scDiag = regexp.MustCompile(`^(.+?\.go):(\d+):(\d+):\s*(.*)$`)

func (a *StaticcheckAnalyzer) Analyze(ctx context.Context, projectPath string) ([]Issue, error) {
	return nil, nil
}

func (a *StaticcheckAnalyzer) AnalyzeWithContext(actx *Context) ([]Issue, error) {
	if _, err := actx.Runner.LookPath("staticcheck"); err != nil {
		return nil, ErrToolUnavailable("staticcheck")
	}
	if !actx.Files.Exists("go.mod") {
		return nil, ErrToolUnavailable("staticcheck (no go.mod in project root)")
	}

	out, err := actx.Runner.Run(actx.Context, actx.ProjectPath, "staticcheck", "-f", "text", "./...")
	if err != nil && len(out) == 0 {
		if IsTimeout(err) {
			return nil, fmt.Errorf("staticcheck timed out")
		}
		return nil, fmt.Errorf("staticcheck failed: %w", err)
	}

	var issues []Issue
	for _, line := range splitLines(string(out)) {
		m := scDiag.FindStringSubmatch(strings.TrimSpace(line))
		if m == nil {
			continue
		}
		lineNo, _ := strconv.Atoi(m[2])
		col, _ := strconv.Atoi(m[3])
		msg := strings.TrimSpace(m[4])
		code := staticcheckCode(msg)

		issues = append(issues, Issue{
			Analyzer: a.Name(),
			Rule:     code,
			Severity: staticcheckSeverity(code),
			Message:  msg,
			Advice:   adviceForStaticcheck(code),
			File:     filepath.ToSlash(m[1]),
			Line:     lineNo,
			Column:   col,
			DocURL:   "https://staticcheck.dev/docs/checks/#" + code,
		})
	}
	return issues, nil
}

func staticcheckCode(msg string) string {
	i := strings.Index(msg, "(")
	if i <= 0 {
		return "staticcheck"
	}
	end := strings.Index(msg[i:], ")")
	if end <= 0 {
		return "staticcheck"
	}
	code := msg[i+1 : i+end]
	if len(code) < 2 || len(code) > 6 {
		return "staticcheck"
	}
	return code
}

// staticcheckSeverity 沿用上游的分组
// SA 是 bug，S 是简化，ST 是风格，QF 是快速修复
func staticcheckSeverity(code string) string {
	switch {
	case strings.HasPrefix(code, "SA"):
		return SeverityError
	case strings.HasPrefix(code, "S1"), strings.HasPrefix(code, "QF"):
		return SeverityWarning
	default:
		return SeverityInfo
	}
}

func adviceForStaticcheck(code string) string {
	switch {
	case strings.HasPrefix(code, "SA"):
		return "SA checks flag real bugs. Treat this as a defect, not a style preference."
	case strings.HasPrefix(code, "ST"):
		return "Style check: align with the project's conventions."
	}
	return ""
}

// ---------------------------------------------------------------------------
// 共用辅助函数
// ---------------------------------------------------------------------------

// ToolUnavailableError 表示「工具没装」，框架把它记为跳过而不是失败
type ToolUnavailableError struct{ Tool string }

func (e *ToolUnavailableError) Error() string {
	return e.Tool + " is not installed or not applicable"
}

// ErrToolUnavailable 构造一个跳过标记
func ErrToolUnavailable(tool string) error { return &ToolUnavailableError{Tool: tool} }

// IsToolUnavailable 报告 err 是否为跳过标记
func IsToolUnavailable(err error) bool {
	var e *ToolUnavailableError
	return errors.As(err, &e)
}

// ErrNothingToCheck 标记运行了但没有可检查输入的分析器
//
// 它与 ErrToolUnavailable 不同，工具装了也适用，只是没有东西可处理。规则引擎在
// 没有启用任何规则时就是典型情形，它遍历零个文件、不报任何发现，而引擎否则会
// 把它算成「跑过了，代码是干净的」。在一个只有它和另一个因缺 clippy 而被跳过的
// 分析器的 Rust 项目上，这会把一次什么都没查的扫描变成一次通过的扫描
//
// 把它当跳过处理，全部跳过时的否决规则才会生效
func ErrNothingToCheck(reason string) error {
	return &NothingToCheckError{Reason: reason}
}

// NothingToCheckError 是「适用但没有输入」的跳过标记
type NothingToCheckError struct{ Reason string }

func (e *NothingToCheckError) Error() string {
	if e.Reason == "" {
		return "nothing to check"
	}
	return "nothing to check: " + e.Reason
}

// IsNothingToCheck 报告 err 是否表示分析器没有输入
func IsNothingToCheck(err error) bool {
	var e *NothingToCheckError
	return errors.As(err, &e)
}

func asExitError(err error) *ExitError {
	var ee *ExitError
	if errors.As(err, &ee) {
		return ee
	}
	return nil
}

func splitLines(s string) []string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	return strings.Split(s, "\n")
}

// golangciFailure 从 golangci-lint 的日志输出里提取致命错误
//
// 该工具把结构化问题作为单个 JSON 对象写到 stdout，但把自己的失败以 logrus 行
// （`level=error msg="..."`）写到 stderr。这些行不属于问题文档，所以没有
// 这个检查的话，一次彻底失败的扫描仍然看起来像什么都没发现，而那是相反的结论
func golangciFailure(out []byte) string {
	var messages []string

	for _, line := range splitLines(string(out)) {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "level=error") {
			continue
		}
		msg := extractLogrusMessage(line)
		if msg == "" {
			continue
		}
		// golangci-lint 给每个上下文错误加的那串噪音前缀
		msg = strings.TrimPrefix(msg, "[linters_context] ")
		messages = append(messages, msg)
	}

	if len(messages) == 0 {
		return ""
	}

	// 一两条就足以说明问题，构建坏掉的项目会产生几十条一模一样的类型检查错误
	const keep = 2
	if len(messages) > keep {
		return strings.Join(messages[:keep], "; ") +
			fmt.Sprintf(" (and %d more)", len(messages)-keep)
	}
	return strings.Join(messages, "; ")
}

// extractLogrusMessage 从 logrus 行里取出 msg="..." 的值
func extractLogrusMessage(line string) string {
	const key = `msg="`
	start := strings.Index(line, key)
	if start < 0 {
		return ""
	}
	rest := line[start+len(key):]

	// 扫描到收尾引号，同时处理反斜杠转义
	var b strings.Builder
	escaped := false
	for i := 0; i < len(rest); i++ {
		c := rest[i]
		if escaped {
			// logrus 会把值里的换行和引号转义
			switch c {
			case 'n':
				b.WriteByte('\n')
			case 't':
				b.WriteByte('\t')
			default:
				b.WriteByte(c)
			}
			escaped = false
			continue
		}
		if c == '\\' {
			escaped = true
			continue
		}
		if c == '"' {
			break
		}
		b.WriteByte(c)
	}

	// 把换行折叠掉，让消息能放进命令行的一行摘要
	msg := strings.TrimSpace(b.String())
	return strings.Join(strings.Fields(msg), " ")
}

// toolFailureText 把外部工具的失败渲染成一行摘要
//
// 优先用抓到的 stderr，因为 golangci-lint 和 gosec 把诊断写在那里，并把它折叠
// 到能放进一行的长度，而不是把整份类型检查报告贴进扫描摘要
func toolFailureText(err error) string {
	if err == nil {
		return ""
	}

	var exitErr *ExitError
	if errors.As(err, &exitErr) {
		if exitErr.Stderr != "" {
			return collapseDiagnostic(exitErr.Stderr)
		}
		if exitErr.Code > 0 {
			return fmt.Sprintf("exit status %d (no diagnostic output)", exitErr.Code)
		}
		// 根本不是退出：进程没能启动
		return collapseDiagnostic(err.Error())
	}
	return firstLine(err.Error())
}

// collapseDiagnostic 把多行的工具报告压成一行可读内容
//
// 构建和类型检查的失败有好几段，而摘要只有一行的位置。第一行有意义的文字会
// 给出文件名和成因，那才是用户要处理的
func collapseDiagnostic(s string) string {
	var parts []string
	for _, line := range splitLines(s) {
		// 如果存在 golangci-lint 的 logrus 外壳就去掉
		line = strings.TrimSpace(line)
		if msg := extractLogrusMessage(line); msg != "" {
			line = msg
		}
		line = strings.TrimPrefix(line, "[linters_context] ")
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		parts = append(parts, line)
		if len(parts) == 2 {
			break
		}
	}
	if len(parts) == 0 {
		return "the tool produced no diagnostic output"
	}
	joined := strings.Join(parts, " ")
	if len(joined) > 200 {
		joined = joined[:200] + "…"
	}
	return joined
}

// golangciRemedy 把 golangci-lint 的原始诊断变成可操作的消息
//
// 到目前为止最常见的失败是版本不匹配，golangci-lint 是针对某个 Go 版本编译
// 的，读不了更新工具链写出的导出数据。它表现为一句晦涩的 "export data version
// N is greater than maximum supported version M"，没说该怎么办，而解决办法其实
// 就是一次 go install，用户却无从猜到
func golangciRemedy(raw string) string {
	lower := strings.ToLower(raw)

	// 版本不匹配是环境问题且一行就能修好，它的原始形式（"cannot decode
	// internal/goarch, export data version 4 is greater than maximum supported
	// version 2"）对用户没有任何可操作的信息
	if strings.Contains(lower, "export data version") ||
		strings.Contains(lower, "internal error in importing") {
		return "the installed golangci-lint was built against an older Go release and cannot read " +
			"data produced by your current toolchain. Upgrade it with " +
			"`go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@latest`, then make sure " +
			"the Go bin directory comes first on PATH — a copy installed elsewhere " +
			"(a previous SDK, an IDE bundle) will otherwise keep shadowing the new one"
	}

	// 构建失败是项目自己的问题，编译器自己的消息才是有用的部分，保留它，只做
	// 整理
	if strings.Contains(lower, "typechecking error") || strings.Contains(lower, "redeclared") ||
		strings.Contains(lower, "malformed import path") {
		if detail := collapseDiagnostic(raw); detail != "" {
			return "the project does not compile, so nothing could be checked — fix the build errors first. " + detail
		}
		return "the project does not compile, so nothing could be checked — fix the build errors first"
	}

	return collapseDiagnostic(raw)
}

// jsonOutputFlag 返回已安装的 golangci-lint 所需的 JSON 输出参数
//
// 每个分析器实例只探测一次并缓存，因为探测要启动一个进程，而一次扫描只会运行
// 该 linter 一次
func (a *GolangciAnalyzer) jsonOutputFlag(actx *Context) string {
	a.probeOnce.Do(func() {
		a.jsonFlag = "--out-format=json" // v1 default

		out, err := actx.Runner.Run(actx.Context, actx.ProjectPath, "golangci-lint", "--version")
		if err != nil && len(out) == 0 {
			return
		}
		// v2 会打印 "golangci-lint has version 2.x.y"。读不到版本时退回 v1 的
		// 参数，保持原有行为
		if strings.Contains(string(out), "version 2.") {
			a.jsonFlag = "--output.json.path=stdout"
		} else if strings.Contains(string(out), "version 1.") {
			a.jsonFlag = "--out-format=json"
		}
	})
	return a.jsonFlag
}

// looksLikeGolangciRunFailure 报告该输出是失败报告而不是问题文档
//
// 成功时 golangci-lint 会输出形如 `{"Issues":[...],"Report":{...}}` 的问题文档，
// 即使什么都没发现也如此。不是这份文档的任何东西（空输出，或 logrus 诊断）都
// 说明运行本身失败了，问题的缺席说明不了代码的任何情况
func looksLikeGolangciRunFailure(out []byte) bool {
	return len(jsonPayload(out)) == 0
}

// jsonPayload 从 golangci-lint 的输出里提取 JSON 文档
//
// 该文档前后都有任意文本，v2 在它前面打印版本头，在它后面打印按 linter 的汇总
// （"* govet: 1"、"* unused: 6"）。把整条流交给 json.Unmarshal 只会得到一句
// 没什么用的 "invalid character after top-level value"，所以这里靠括号配对来划定
// 对象边界，而不是信任第一个字节或行结构
//
// 括号深度计数要考虑字符串和转义，因为问题文本里经常出现大括号，
// map[string]struct{}、格式化动词、消息里引用的 JSON 片段
func jsonPayload(out []byte) []byte {
	trimmed := bytes.TrimSpace(out)
	if len(trimmed) == 0 {
		return nil
	}

	start := bytes.IndexByte(trimmed, '{')
	if start < 0 {
		return nil
	}

	depth := 0
	inString := false
	escaped := false

	for i := start; i < len(trimmed); i++ {
		c := trimmed[i]

		if escaped {
			escaped = false
			continue
		}
		if inString {
			switch c {
			case '\\':
				escaped = true
			case '"':
				inString = false
			}
			continue
		}

		switch c {
		case '"':
			inString = true
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return trimmed[start : i+1]
			}
		}
	}

	// 括号不配对说明文档被截断了，绝不能把它当成一份合法的空报告
	return nil
}
