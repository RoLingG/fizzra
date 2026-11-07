package analyzer

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Rust 分析，基于 clippy
//
// clippy 随 Rust 工具链分发，不是独立二进制，rustup component add clippy 之后即可用，
// 版本随编译器走，不必单独固定
//
// Rust 没有 golangci-lint 的对应物：clippy 负责 lint，依赖漏洞要靠 cargo-audit
// 作为第二个分析器

// ClippyAnalyzer 运行 cargo clippy
type ClippyAnalyzer struct{ rustOnly }

// NewClippyAnalyzer 构造该分析器
func NewClippyAnalyzer() Analyzer { return &ClippyAnalyzer{} }

func (a *ClippyAnalyzer) Name() string { return "clippy" }

func (a *ClippyAnalyzer) Description() string {
	return "Runs cargo clippy: correctness, suspicious patterns, performance and idiomatic-Rust lints from the official Rust toolchain."
}

func (a *ClippyAnalyzer) Category() string { return "lint" }

// Requires 列出两个二进制
// cargo 负责调用 clippy，真正产生诊断的是 clippy-driver
func (a *ClippyAnalyzer) Requires() []string { return []string{"cargo"} }

func (a *ClippyAnalyzer) Analyze(ctx context.Context, projectPath string) ([]Issue, error) {
	return nil, nil
}

// clippyRecord 是 cargo JSON Lines 流中的一行
//
// 诊断嵌在下一层，位于 message 之下，cargo 把 rustc 自己的记录包了一层，以便
// 附上 reason 和 package_id。从顶层读这些字段会让每一行都解出空结构体，这是
// 静默的零发现 bug 而不是解析错误，测试里的样本因此取自真实报文而不是手写
// 简化版
type clippyRecord struct {
	// Reason 用来区分诊断（compiler-message）和共用同一流的构建进度
	// （compiler-artifact、build-finished）
	Reason  string        `json:"reason"`
	Message clippyMessage `json:"message"`
}

// clippyMessage 是 rustc 的诊断载荷
//
// 格式见 doc.rust-lang.org/rustc/json.html，文档明确警告它可能新增字段和枚举
// 值。所以这里每个字段都是可选的，未知字段一律忽略，严格解码会在工具链升级时
// 直接坏掉，而对随工具链分发的 linter 来说，那就是所有用户同时坏掉
type clippyMessage struct {
	Message string `json:"message"`
	Level   string `json:"level"`
	Code    *struct {
		Code string `json:"code"`
	} `json:"code"`
	Spans []clippySpan `json:"spans"`
	// 某些 lint 更精确的位置在 Children 上
	// 父 span 往往是整个条目，而子 span 才指向真正出问题的表达式
	Children []struct {
		Spans []clippySpan `json:"spans"`
	} `json:"children"`
}

// clippySpan 是诊断内部的一个源码位置
type clippySpan struct {
	FileName    string `json:"file_name"`
	LineStart   int    `json:"line_start"`
	ColumnStart int    `json:"column_start"`
	IsPrimary   bool   `json:"is_primary"`

	// Text 是该 span 覆盖的源码，SuggestedReplacement 是 rustc 建议替换成的内容
	// 两者对修复示例都很关键，它们是直接来自工具的一对修改前后，都不必自己
	// 拼凑
	//
	// Text 是数组，因为 span 可能跨越宏展开，此时 rustc 会按展开层级各报一条，
	// 最外层在前。只有第一条是实际写下的代码，其余是宏内部的东西
	Text []struct {
		Text string `json:"text"`
	} `json:"text"`
	SuggestedReplacement *string `json:"suggested_replacement"`
	// SuggestionApplicability 取 MachineApplicable、MaybeIncorrect 或
	// HasPlaceholders。只有第一种适合作为修正后的代码展示
	SuggestionApplicability string `json:"suggestion_applicability"`
}

func (a *ClippyAnalyzer) AnalyzeWithContext(actx *Context) ([]Issue, error) {
	if _, err := actx.Runner.LookPath("cargo"); err != nil {
		return nil, ErrToolUnavailable("cargo (clippy)")
	}

	// 定位 crate，它不一定在仓库根目录
	//
	// Tauri 应用的 Cargo.toml 在 src-tauri/ 下，前端在根目录，所以要求根目录
	// 必须有清单文件会让 clippy 恰好跳过这类项目，而本分析器正是为它们而加的。
	// cargo 还必须在放着清单文件的目录里运行，不能在仓库根目录，否则它找不到
	// 任何包
	crateDir, ok := findRustCrate(actx)
	if !ok {
		return nil, ErrToolUnavailable("clippy (no Cargo.toml found)")
	}

	// --message-format=json 把 JSON Lines 写到 stderr 而不是 stdout，所以下面
	// 的 stdout 是空的，解析的是 stderr
	//
	// -- 之后的 -W clippy::all 会启用标准 lint 组，否则 cargo 只跑 clippy 的
	// 默认集合，报出来的比工具本身能做的少
	out, err := actx.Runner.Run(actx.Context, crateDir,
		"cargo", "clippy", "--message-format=json", "--", "-W", "clippy::all")
	if err != nil {
		if IsTimeout(err) {
			return nil, err
		}
		// 退出码非零是正常的：clippy 报出 error 级诊断时就会非零退出
		// 只有完全没有输出才说明工具本身没跑起来
		if len(out) == 0 && !stderrHasDiagnostics(err) {
			return nil, fmt.Errorf("clippy failed: %w", err)
		}
		actx.Log.Debug("clippy exited non-zero with diagnostics", "err", err)
	}

	issues, skipped := parseClippyJSONLines(out, NewSnippetReader(actx.Files))
	if skipped > 0 {
		actx.Log.Debug("clippy emitted non-diagnostic records", "count", skipped)
	}
	return issues, nil
}

// findRustCrate 返回放着 crate 的 Cargo.toml 的那个目录
//
// Rust 项目的清单文件不总在仓库根目录。Tauri 把它放在 src-tauri/，一个工作区
// 可能有多个 crate，仓库也可能把工具放在子目录里。只考虑前两层，更深的清单
// 属于 vendored 依赖而不是本项目，构建产物目录则跳过
//
// 返回的路径是绝对路径，因为 cargo 要拿它当工作目录，相对路径会被按进程自身
// 的目录解析
func findRustCrate(actx *Context) (string, bool) {
	if actx.Files.Exists("Cargo.toml") {
		return actx.ProjectPath, true
	}

	// 只看直接子目录，优先 src-tauri，这样即使旁边还放着别的 crate 也能认出
	// Tauri 的布局
	candidates := []string{"src-tauri"}
	if entries, err := os.ReadDir(actx.ProjectPath); err == nil {
		for _, e := range entries {
			if e.IsDir() && !skipRustSubdir(e.Name()) && e.Name() != "src-tauri" {
				candidates = append(candidates, e.Name())
			}
		}
	}

	for _, sub := range candidates {
		rel := filepath.ToSlash(filepath.Join(sub, "Cargo.toml"))
		if actx.Files.Exists(rel) {
			return filepath.Join(actx.ProjectPath, sub), true
		}
	}
	return "", false
}

// skipRustSubdir 列出永远不会放着本项目 crate 的目录
func skipRustSubdir(name string) bool {
	switch name {
	case "target", "node_modules", "vendor", "dist", "build", ".git":
		return true
	}
	return strings.HasPrefix(name, ".")
}

// crateManifest 把文件路径解析为相对项目根目录的形式
//
// Tauri 应用的 crate 在 src-tauri/ 下，所以它的 Cargo.lock 从项目角度看是
// src-tauri/Cargo.lock。只报文件名会指向一个根目录下并不存在的文件
func crateManifest(crateDir, projectRoot, name string) string {
	if crateDir == projectRoot {
		return name
	}
	rel, err := filepath.Rel(projectRoot, crateDir)
	if err != nil {
		return name
	}
	return filepath.ToSlash(filepath.Join(rel, name))
}

// stderrHasDiagnostics 报告某个 ExitError 是否带有可解析的输出
//
// clippy 把 JSON Lines 写到 stderr，所以诊断在被包装的错误里而不是返回的
// stdout 里。完全没有 stderr 的失败是真正的失败（cargo 缺失、没有清单文件），
// 不是发现了问题
func stderrHasDiagnostics(err error) bool {
	var e *ExitError
	if !errors.As(err, &e) {
		return false
	}
	return strings.TrimSpace(e.Stderr) != "" || len(e.Stdout) > 0
}

// parseClippyJSONLines 解析 rustc 的 JSON Lines 流，保留其中的诊断
//
// 非诊断记录（compiler-artifact、build-finished，以及 cargo 日后新增的任何
// 类型）按 reason 字段跳过，而不是靠解析失败来过滤
func parseClippyJSONLines(out []byte, snippets *SnippetReader) (issues []Issue, skipped int) {
	sc := bufio.NewScanner(bytes.NewReader(out))
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)

	for sc.Scan() {
		line := bytes.TrimSpace(sc.Bytes())
		if len(line) == 0 || line[0] != '{' {
			if len(line) > 0 {
				skipped++
			}
			continue
		}

		var rec clippyRecord
		if err := json.Unmarshal(line, &rec); err != nil {
			skipped++
			continue
		}
		// cargo 把每条 rustc 诊断包进 compiler-message 记录，并在同一条流上
		// 输出构建进度（compiler-artifact、build-finished）。按 reason 过滤
		// 才能把编译器的记账信息挡在问题列表之外
		if rec.Reason != "compiler-message" {
			skipped++
			continue
		}
		msg := rec.Message

		span, ok := primarySpan(msg)
		if !ok {
			// 没有源码位置的诊断（比如 crate 级错误）无处可报。跳过它比硬挂到
			// 一个不相干的文件上要诚实
			skipped++
			continue
		}

		rel := filepath.ToSlash(span.FileName)
		issues = append(issues, Issue{
			Analyzer: "clippy",
			Rule:     clippyRuleCode(msg),
			Severity: mapClippySeverity(msg.Level),
			Message:  strings.TrimSpace(msg.Message),
			Fix:      clippyFix(msg),
			File:     rel,
			Line:     span.LineStart,
			Column:   span.ColumnStart,
			Code:     snippets.Line(rel, span.LineStart),
			DocURL:   clippyDocURL(clippyRuleCode(msg)),
		})
	}
	return issues, skipped
}

// clippyFix 提取 rustc 提供的可直接套用建议，没有则返回零值
//
// 建议几乎不出现在主 span 上，而是在子诊断里，也就是 message 为建议的 help 项
// （"using `!is_empty` is clearer and more explicit" 对应 `!v.is_empty()`），
// 所以要遍历 children
//
// 示例的两半都直接来自 span 本身：text 是写下的代码，suggested_replacement 是替换
// 它的内容，不需要读文件
//
// 多数 lint 都没有建议，建议只是文字描述的 lint 返回零值
// （比如「考虑改用迭代器」）不能被包装成修改前后对照
func clippyFix(msg clippyMessage) FixExample {
	// 只有 MachineApplicable 的建议是编译器有把握的改写。MaybeIncorrect 和
	// HasPlaceholders 都还是草稿，把其中之一当成修正后的代码展示，等于断言
	// 一件 rustc 明确不肯断言的事
	for _, child := range msg.Children {
		for _, s := range child.Spans {
			if s.SuggestedReplacement == nil || s.SuggestionApplicability != "MachineApplicable" {
				continue
			}
			after := *s.SuggestedReplacement
			before := spanText(s)
			if before == "" && after == "" {
				continue
			}

			note := "Clippy can apply this automatically with `cargo clippy --fix`."
			return FixExample{
				Before:   before,
				After:    after,
				Note:     note,
				Language: "rust",
			}
		}
	}

	// 有些 lint 会把建议放在主 span 上，所以也检查那里，不要假定只有一种形状
	for _, s := range msg.Spans {
		if s.SuggestedReplacement == nil || s.SuggestionApplicability != "MachineApplicable" {
			continue
		}
		before := spanText(s)
		after := *s.SuggestedReplacement
		if before == "" && after == "" {
			continue
		}
		return FixExample{
			Before:   before,
			After:    after,
			Note:     "Clippy can apply this automatically with `cargo clippy --fix`.",
			Language: "rust",
		}
	}

	return FixExample{}
}

// spanText 返回该 span 覆盖的源码
//
// 跨越宏展开的 span 会按层级各报一条，最外层在前，只有第一条是实际写下的代码，
// 其余描述的是读者从未敲过的展开内容。工具完全不给 text 时（较老的工具链）
// 这里为空，调用方就退回到不做示例
func spanText(s clippySpan) string {
	if len(s.Text) == 0 {
		return ""
	}
	return s.Text[0].Text
}

// primarySpan 挑出要上报的 span
//
// 优先顺序，诊断自身的 primary span，然后是 children 里的 primary span。lint
// 的父 span 常常是整个外层条目，报它会把发现落在 fn 那一行，而不是真正出错
// 的表达式上
func primarySpan(msg clippyMessage) (clippySpan, bool) {
	for _, s := range msg.Spans {
		if s.IsPrimary && s.FileName != "" {
			return clippySpan{
				FileName:    s.FileName,
				LineStart:   s.LineStart,
				ColumnStart: s.ColumnStart,
				IsPrimary:   true,
			}, true
		}
	}
	for _, child := range msg.Children {
		for _, s := range child.Spans {
			if s.IsPrimary && s.FileName != "" {
				return s, true
			}
		}
	}
	// 退而取任意 span，总比丢掉这条发现好
	for _, s := range msg.Spans {
		if s.FileName != "" {
			return clippySpan{
				FileName:    s.FileName,
				LineStart:   s.LineStart,
				ColumnStart: s.ColumnStart,
			}, true
		}
	}
	return clippySpan{}, false
}

// clippyRuleCode 提取 lint 名
//
// 不同分组里可能有同名的 lint，所以取能保留下来的是标识符本身，没有 code 的
// 诊断（clippy 透出的普通编译错误）退回用它的 level，这样问题列表读起来仍然
// 合理
func clippyRuleCode(msg clippyMessage) string {
	if msg.Code != nil && msg.Code.Code != "" {
		return msg.Code.Code
	}
	return "compiler-" + msg.Level
}

// mapClippySeverity 把 rustc 的 level 映射到本项目的等级
//
// clippy 把多数 lint 报成 warning，所以退出码干净时仍可能带着几百条发现。
// note 和 help 是附在诊断上的补充记录，本身不算独立发现
func mapClippySeverity(level string) string {
	switch strings.ToLower(strings.TrimSpace(level)) {
	case "error", "failure-note", "error: internal compiler error":
		return SeverityError
	case "warning":
		return SeverityWarning
	case "note", "help":
		return SeverityInfo
	}
	return SeverityWarning
}

// clippyDocURL 给出某个 lint 的文档链接
//
// clippy 的 lint 索引里只有它自己的 lint，rustc 的 lint（clippy 既然跑编译器
// 也会带出来）不在里面，指向错误索引的链接比没有链接更糟。clippy:: 前缀就是
// 区分标志，像 unused_variables 这样的裸名属于 rustc
func clippyDocURL(code string) string {
	if !strings.HasPrefix(code, "clippy::") {
		return ""
	}
	return "https://rust-lang.github.io/rust-clippy/master/index.html#" + strings.TrimPrefix(code, "clippy::")
}

// RustfmtAnalyzer 用 cargo fmt 检查格式
//
// 它是 gofmt 分析器的直接对应物，也是 Rust 工具链自带的仅两个检查工具中的
// 第二个，生态里其余的（audit、deny、geiger、udeps）都要单独安装。值得接入的
// 原因是，在已经有工具链的机器上它不需要额外安装，而每个 Rust 开发者都是从那
// 里起步的
type RustfmtAnalyzer struct{ rustOnly }

// NewRustfmtAnalyzer 构造该分析器
func NewRustfmtAnalyzer() Analyzer { return &RustfmtAnalyzer{} }

func (a *RustfmtAnalyzer) Name() string { return "rustfmt" }

func (a *RustfmtAnalyzer) Description() string {
	return "Reports Rust files that are not rustfmt-formatted. Uses the local Rust toolchain."
}

func (a *RustfmtAnalyzer) Category() string   { return "lint" }
func (a *RustfmtAnalyzer) Requires() []string { return []string{"cargo"} }

func (a *RustfmtAnalyzer) Analyze(ctx context.Context, projectPath string) ([]Issue, error) {
	return nil, nil
}

func (a *RustfmtAnalyzer) AnalyzeWithContext(actx *Context) ([]Issue, error) {
	if _, err := actx.Runner.LookPath("cargo"); err != nil {
		return nil, ErrToolUnavailable("cargo (rustfmt)")
	}

	crateDir, ok := findRustCrate(actx)
	if !ok {
		return nil, ErrToolUnavailable("rustfmt (no Cargo.toml found)")
	}

	// --check 只报告不重写，扫描期间重排用户源码等于把一次破坏性操作包装成 lint
	//
	// 无论文件是否需要格式化，退出码都是 0，所以输出是唯一信号，总是要解析。
	// 这样退出码非零就意味着真正的失败，与 Go 侧分析器的约定一致
	out, err := actx.Runner.Run(actx.Context, crateDir, "cargo", "fmt", "--check")
	if err != nil {
		if IsTimeout(err) {
			return nil, err
		}
		if len(out) == 0 {
			return nil, fmt.Errorf("cargo fmt failed: %w", err)
		}
		actx.Log.Debug("cargo fmt exited non-zero with output", "err", err)
	}

	issues := parseRustfmtDiff(out, crateDir)
	logRustfmtResult(actx, issues)
	return issues, nil
}

// parseRustfmtDiff 提取 rustfmt 会改动的文件
//
// 输出是每个文件一段 unified diff，由 `Diff in <path>:<line>:` 一行引出。路径是
// 绝对路径，Windows 上还带 \?\ 前缀，上报前要转成相对 crate 的路径。只取这个头，
// diff 正文本身就是改法，重复到消息里只会淹没这条发现
func parseRustfmtDiff(out []byte, crateDir string) []Issue {
	var issues []Issue
	seen := map[string]bool{}

	for _, line := range splitLines(string(out)) {
		// rustfmt 会给 diff 上色，头部目前是纯文本，但仍然剥掉转义，以防日后
		// 的版本连头部也上色
		clean := stripANSI(strings.TrimSpace(line))
		if !strings.HasPrefix(clean, "Diff in ") {
			continue
		}

		path := strings.TrimSuffix(strings.TrimPrefix(clean, "Diff in "), ":")
		// 去掉末尾的 ":<行号>"。取最后一个冒号，因为路径本身就含冒号
		// （`C:\...`），按第一个冒号切会把盘符截掉
		if idx := strings.LastIndex(path, ":"); idx > 1 {
			path = path[:idx]
		}

		// relToProject 在归一化时会剥掉 Windows 的 verbatim 前缀
		rel := relToProject(path, crateDir)
		if rel == "" || seen[rel] {
			continue
		}
		seen[rel] = true

		issues = append(issues, Issue{
			Analyzer: "rustfmt",
			Rule:     "rustfmt",
			Severity: SeverityInfo,
			Message:  "file is not formatted with rustfmt",
			Advice:   "Run `cargo fmt` (or `rustfmt " + rel + "`) and commit the result.",
			File:     rel,
			Line:     1,
			// Code 刻意留空。rustfmt 只报告它会重写哪些文件，所以这里的行号是
			// 指向文件本身的占位符，而不是文件内的某个位置。读第 1 行会把一段
			// 不相干的源码挂到一条关于整个文件的发现下面，片段会和消息自相矛盾，
			// 而这正是本项目要避免的失败。Advice 里已经写明了该跑的命令
		})
	}
	return issues
}

// relToProject 把上报的路径转为相对项目根目录的形式
//
// rustfmt 打印的是绝对路径，涉及盘符时是 Windows verbatim 形式。问题列表显示的
// 是相对项目的位置，所以扫描 Tauri 应用会报出 src-tauri/src/lib.rs 而不是一台
// 机器特有的绝对路径，而 verbatim 前缀必须先去掉，filepath 才会把它当普通
// 路径处理
func relToProject(path, fromDir string) string {
	clean := normalizeVerbatim(path)
	base := normalizeVerbatim(fromDir)

	if rel, err := filepath.Rel(base, clean); err == nil {
		rel = filepath.ToSlash(rel)
		if !strings.HasPrefix(rel, "..") {
			return rel
		}
	}
	// 不在 crate 目录下，就按原样上报路径，而不是什么都不报
	return filepath.ToSlash(clean)
}

// normalizeVerbatim 剥掉 Windows 的 verbatim 路径前缀并统一分隔符
//
// 该前缀可能是 `\\?\`，也可能因为工具自己把斜杠归一化过而变成 `//?/`。只处理
// 反斜杠形式会让 `//?/C:/...` 留在上报的位置里，而那个路径没有编辑器打得开
func normalizeVerbatim(path string) string {
	p := strings.TrimSpace(path)
	p = strings.ReplaceAll(p, "\\", "/")
	for _, prefix := range []string{"//?/", "//./"} {
		p = strings.TrimPrefix(p, prefix)
	}
	return filepath.Clean(filepath.FromSlash(p))
}

// logRustfmtResult 把需要格式化的文件数记入扫描日志
func logRustfmtResult(actx *Context, issues []Issue) {
	if len(issues) > 0 {
		actx.Log.Debug("rustfmt found unformatted files", "count", len(issues))
	}
}

// stripANSI 去掉终端颜色转义
//
// rustfmt 会给 diff 输出上色，而文件路径里夹着原始转义序列会让上报的位置无法
// 使用
func stripANSI(s string) string {
	if !strings.Contains(s, "\x1b[") {
		return s
	}
	var b strings.Builder
	for i := 0; i < len(s); {
		if s[i] == '\x1b' && i+1 < len(s) && s[i+1] == '[' {
			i += 2
			for i < len(s) && !((s[i] >= 'a' && s[i] <= 'z') || (s[i] >= 'A' && s[i] <= 'Z')) {
				i++
			}
			i++ // consume the terminating letter
			continue
		}
		b.WriteByte(s[i])
		i++
	}
	return b.String()
}

// CargoAuditAnalyzer 扫描依赖中的已知漏洞
//
// 它是 govulncheck 在 Rust 侧的对应物，不读项目代码，而是读 Cargo.lock，把每个
// 固定住的依赖拿去比对 RustSec 通告库。这使它成为这里唯一能报出团队自己没写的
// 问题的分析器，也就是一个他们从没听说过的、有漏洞的传递依赖
type CargoAuditAnalyzer struct{ rustOnly }

// NewCargoAuditAnalyzer 构造该分析器
func NewCargoAuditAnalyzer() Analyzer { return &CargoAuditAnalyzer{} }

func (a *CargoAuditAnalyzer) Name() string { return "cargo-audit" }

func (a *CargoAuditAnalyzer) Description() string {
	return "Runs cargo audit: known vulnerabilities (RustSec advisories, CVEs) in Cargo.lock dependencies."
}

func (a *CargoAuditAnalyzer) Category() string   { return "vulnerability" }
func (a *CargoAuditAnalyzer) Requires() []string { return []string{"cargo-audit"} }

// Options 把该分析器的开关暴露给面板
//
// 只有一个，且默认关闭，读者没有选择的那个依赖里的漏洞，是否该计入他的评分。
// 默认关，因为答案通常是不该，见 engine.Run 里关于评分的说明。对想要严格口径、
// 不管该由谁修的项目可以打开
func (a *CargoAuditAnalyzer) Options() []DescriptorOption {
	return []DescriptorOption{
		{
			Key:     "offline",
			Default: false,
			Description: "Run cargo-audit with --no-fetch, using the advisory snapshot already on disk. " +
				"Unlike govulncheck this keeps working — it reports against a possibly stale database " +
				"rather than skipping. The global `offline` setting has the same effect on every analyzer.",
		},
		{
			Key:     "scoredTransitive",
			Default: false,
			Description: "Count vulnerabilities in transitive dependencies against the quality score. " +
				"Off by default: the project cannot upgrade those directly, so their advisories track " +
				"upstream release timing rather than the quality of this code. " +
				"They are always reported; this only controls whether they affect the score.",
		},
	}
}

func (a *CargoAuditAnalyzer) Analyze(ctx context.Context, projectPath string) ([]Issue, error) {
	return nil, nil
}

// auditReport 对应 `cargo audit --json`
//
// 只声明了发现问题所需的字段，真实文档里还带着完整的依赖图和通告库元数据，
// 那些既大又用不上
type auditReport struct {
	Vulnerabilities struct {
		Found bool         `json:"found"`
		Count int          `json:"count"`
		List  []auditEntry `json:"list"`
	} `json:"vulnerabilities"`
}

// auditEntry 是一条命中的通告
type auditEntry struct {
	Advisory struct {
		ID            string   `json:"id"`
		Package       string   `json:"package"`
		Title         string   `json:"title"`
		Description   string   `json:"description"`
		Date          string   `json:"date"`
		Aliases       []string `json:"aliases"`
		Categories    []string `json:"categories"`
		URL           string   `json:"url"`
		Informational *string  `json:"informational"`
	} `json:"advisory"`
	Versions struct {
		Patched    []string `json:"patched"`
		Unaffected []string `json:"unaffected"`
	} `json:"versions"`
	// Package 是通告命中的那个已解析依赖，它带着 Cargo.lock 里实际固定的版本
	Package struct {
		Name    string `json:"name"`
		Version string `json:"version"`
	} `json:"package"`
}

func (a *CargoAuditAnalyzer) AnalyzeWithContext(actx *Context) ([]Issue, error) {
	if _, err := actx.Runner.LookPath("cargo-audit"); err != nil {
		return nil, ErrToolUnavailable("cargo-audit")
	}

	crateDir, ok := findRustCrate(actx)
	if !ok {
		return nil, ErrToolUnavailable("cargo-audit (no Cargo.toml found)")
	}
	if !actx.Files.Exists(crateManifest(crateDir, actx.ProjectPath, "Cargo.lock")) {
		return nil, ErrNothingToCheck("no Cargo.lock - run `cargo build` first")
	}

	// --no-fetch 决定了一次扫描是很快还是卡住
	//
	// 不明确禁止的话，cargo-audit 每次运行都会克隆一遍 RustSec 通告库。在连不上
	// GitHub 的机器上，这个克隆不会快速失败，会一直阻塞到 socket 超时，于是本该
	// 两秒跑完的扫描会干等一分钟然后什么都不报。该开关以 offline 暴露在配置里，
	// 联网的机器仍然可以有意识地刷新数据库
	args := []string{"audit", "--json"}
	if actx.Offline || actx.OptionBool(a.Name(), "offline", false) {
		args = append(args, "--no-fetch")
	}

	out, err := actx.Runner.Run(actx.Context, crateDir, "cargo-audit", args...)
	if err != nil {
		if IsTimeout(err) {
			return nil, err
		}
		// 退出码非零是常态：cargo-audit 就是这样表示「发现了漏洞」的
		// 只有完全没有输出才是一次真正的失败
		if len(out) == 0 {
			return nil, fmt.Errorf("cargo-audit failed: %w", err)
		}
		actx.Log.Debug("cargo-audit exited non-zero with output", "err", err)
	}

	var report auditReport
	if err := json.Unmarshal(out, &report); err != nil {
		return nil, fmt.Errorf("parse cargo-audit output: %w", err)
	}

	// 依赖图的作用是把「rustls-webpki 有漏洞」变成「是 reqwest 把它引进来的」。
	// 每次扫描加载一次，锁文件缺失或解析不了时它为 nil，所有分类都报 unknown，
	// 这会退化成原有行为而不是直接失败
	graph := LoadCargoLock(crateDir)

	issues := make([]Issue, 0, len(report.Vulnerabilities.List))
	for _, v := range report.Vulnerabilities.List {
		issues = append(issues, a.auditIssue(crateDir, actx, v, graph))
	}
	return issues, nil
}

// auditIssue 根据一条命中的通告构造问题
//
// 位置落在 Cargo.lock 而不是某个源码文件。依赖漏洞在项目自己的代码里没有行号，
// 它是关于锁文件的一件事，指到某个源码文件只会把读者送到并不包含该问题的地方
func (a *CargoAuditAnalyzer) auditIssue(crateDir string, actx *Context, v auditEntry, graph *CargoLockGraph) Issue {
	ad := v.Advisory

	// 通告自己的标题比任何从中推导出来的说法都更准确地描述这个缺陷。前面加上固定
	// 住的版本，因为那才是读者要处理的部分，而且同一条通告可能命中多个版本
	message := ad.Title
	if message == "" {
		message = ad.ID
	}
	name := v.Package.Name
	if name == "" {
		name = ad.Package
	}
	if name != "" {
		message = fmt.Sprintf("%s %s: %s", name, v.Package.Version, message)
	}

	// 对照锁文件依赖图做分类：直接依赖、传递依赖，没有图可比对时为 unknown
	origin := ""
	var depPath []string
	if graph != nil {
		origin = graph.Origin(v.Package.Name, v.Package.Version)
		depPath = graph.Path(v.Package.Name, v.Package.Version)
	}

	return Issue{
		Analyzer:       a.Name(),
		Rule:           ad.ID,
		Severity:       mapAuditSeverity(ad.Categories, ad.Informational),
		Message:        message,
		Advice:         auditAdvice(ad.Informational, ad.Aliases, v.Versions.Patched, origin, depPath),
		File:           crateManifest(crateDir, actx.ProjectPath, "Cargo.lock"),
		DocURL:         ad.URL,
		Origin:         origin,
		DependencyPath: depPath,
	}
}

// auditAdvice 说明该做什么。对直接依赖的通告，就是给出修复它的那个版本，因为
// 升级本身就是全部解法
//
// 对传递依赖，建议必须不同，读者升级不了一个他并没直接依赖的包，只报版本号
// 是一条他没法照做的指示。改成给出依赖链，指向他真正能动的那一环
func auditAdvice(informational *string, aliases, patched []string, origin string, depPath []string) string {
	var parts []string

	if informational != nil && *informational != "" {
		// 信息类通告不是漏洞，数据库用它记录一个已无人维护或不健全的 crate。说明这点
		// 可以免得读者去找一个并不存在的利用方式
		parts = append(parts, fmt.Sprintf("This is an informational advisory (%s), not a known exploit.", *informational))
	}

	if origin == OriginTransitive && len(depPath) > 1 {
		// depPath 以受影响的包结尾，所以它前一项就是该动的那个依赖，那才是这条链里
		// 可操作的部分
		parts = append(
			parts,
			fmt.Sprintf(
				"Reached through %s; this package is not declared by the project, so upgrade %s rather than it directly.",
				strings.Join(depPath, " -> "),
				depPath[0],
			),
		)
	}

	if len(patched) > 0 {
		parts = append(parts, fmt.Sprintf("Fixed in %s.", strings.Join(patched, " or ")))
	} else {
		parts = append(parts, "No patched version is listed; consider replacing the dependency.")
	}

	if len(aliases) > 0 {
		parts = append(parts, "Aliases: "+strings.Join(aliases, ", ")+".")
	}

	return strings.Join(parts, " ")
}

// mapAuditSeverity 根据通告的类别推导等级
//
// RustSec 不给严重级别，所以类别是现有最接近的信号。它们不能混为一谈，网络库
// 里的内存破坏可能是远程代码执行，而「无人维护」的通知只是个维护问题。把所有
// 通告都当 critical 和都不当 critical 一样错，所以映射是显式的，只对可能意味着
// 代码执行的类别往上偏
func mapAuditSeverity(categories []string, informational *string) string {
	// 信息类通告无论还说了什么，都绝不是漏洞
	if informational != nil && *informational != "" {
		return SeverityInfo
	}

	for _, c := range categories {
		switch strings.ToLower(strings.TrimSpace(c)) {
		case "memory-corruption", "code-execution", "privilege-escalation":
			return SeverityCritical
		case "memory-exposure", "crypto-failure", "format-injection":
			return SeverityError
		case "denial-of-service", "file-disclosure", "sql-injection":
			return SeverityWarning
		}
	}
	// 没有类别的通告仍然是真正的漏洞
	return SeverityWarning
}
