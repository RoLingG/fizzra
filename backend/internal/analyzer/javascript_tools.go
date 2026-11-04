package analyzer

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
)

// JavaScript 与 TypeScript 分析，基于 oxlint
//
// oxlint 是原生二进制而不是 npm 包，这对本工具很关键，它可以直接在 PATH 上
// 探测、无需项目安装即可运行。eslint 则不然，自 v9 起项目里没有
// eslint.config.js 就完全拒绝运行，所以一个没有任何 eslint 配置的仓库从 eslint
// 包装器那里什么都得不到，而 oxlint 开箱即可 lint

// OxlintAnalyzer 运行 oxlint
type OxlintAnalyzer struct{ javascriptOnly }

// NewOxlintAnalyzer 构造该分析器
func NewOxlintAnalyzer() Analyzer { return &OxlintAnalyzer{} }

func (a *OxlintAnalyzer) Name() string { return "oxlint" }

func (a *OxlintAnalyzer) Description() string {
	return "Runs oxlint: correctness, suspicious patterns, TypeScript, React, accessibility and import checks for JavaScript and TypeScript."
}

func (a *OxlintAnalyzer) Category() string   { return "lint" }
func (a *OxlintAnalyzer) Requires() []string { return []string{"oxlint"} }

// InstallHint 只给一条可直接复制的命令
//
// oxlint 通过 npm 分发，不在 Go 模块生态里。通用回退逻辑会把名字拼成
// `go install oxlint@latest`，它直接报 "malformed module path: missing dot in
// first path element"，于是用户本该照着执行的那一行反而给出了错误
//
// 装成全局，Fizzra 是单个可执行文件，没有「每个项目装一份」的说法。
// 其他装法与验证步骤在 toolHints 词条里
func (a *OxlintAnalyzer) InstallHint() string {
	return "npm install -g oxlint"
}

// ProbeCommands 让探测同时认独立二进制和经 npx 调用的项目本地安装
//
// oxlint 以 npm 包分发，二进制名叫 oxlint。把它当 devDependency 安装的项目只在
// node_modules/.bin 里有它，PATH 上没有，只查 PATH 会让扫描照常跑、界面却报缺失
func (a *OxlintAnalyzer) ProbeCommands() [][]string {
	return [][]string{{"oxlint"}, {"npx", "--no-install", "oxlint"}}
}

// AvailableIn 报告这个项目里能不能跑 oxlint
//
// 覆盖 ProjectScoped，走 npx 的分析器不能只看 PATH，理由见接口定义处
func (a *OxlintAnalyzer) AvailableIn(projectPath string) bool {
	return analyzerAvailableIn(projectPath, "oxlint")
}

// Options 实现 OptionProvider，把开关暴露给面板
func (a *OxlintAnalyzer) Options() []DescriptorOption {
	return []DescriptorOption{{
		Key:     "vuePlugin",
		Default: true,
		Description: "Enable oxlint's Vue plugin (--vue-plugin) for .vue template checks. " +
			"On by default; the script block of a .vue file is linted either way, so this " +
			"only adds the template-specific rules.",
	}}
}

// oxlintReport 对应 `oxlint -f json` 的输出
//
// 有两处形态与 eslint 不同，容易搞错
//
//   - 发现嵌在 diagnostics 键之下，不是裸数组
//   - 规则 id 是单个 code 字符串，格式为 plugin(rule-name)，而不是 eslint 那样
//     单独的 ruleId 字段
type oxlintReport struct {
	Diagnostics []struct {
		Message  string `json:"message"`
		Code     string `json:"code"`
		Severity string `json:"severity"`
		URL      string `json:"url"`
		Help     string `json:"help"`
		Filename string `json:"filename"`
		Labels   []struct {
			Label string `json:"label"`
			Span  struct {
				Line   int `json:"line"`
				Column int `json:"column"`
			} `json:"span"`
		} `json:"labels"`
	} `json:"diagnostics"`
	NumberOfFiles int `json:"number_of_files"`
	NumberOfRules int `json:"number_of_rules"`
}

// oxlintInvocation 是解析好的命令加上它的前置参数
type oxlintInvocation struct {
	name string
	args []string
}

// oxlintCommand 定位 oxlint，优先用 PATH 上的安装而不是 npx
//
// 用 npx --no-install 而不是裸 npx，因为裸 npx 在包不存在时会去下载一个，把一个
// 未安装的项目变成一次网络请求。--no-install 只解析项目自己的 node_modules/.bin
func oxlintCommand(actx *Context) (oxlintInvocation, error) {
	if path, err := LookCandidates(actx.Runner, "oxlint", "oxlint.cmd"); err == nil {
		return oxlintInvocation{name: path}, nil
	}
	// 项目本地安装。包装后的 argv 是 `npx --no-install oxlint`，它会解析到
	// node_modules/.bin/oxlint
	if hasLocalBin(actx, "oxlint") {
		return oxlintInvocation{name: "npx", args: []string{"--no-install", "oxlint"}}, nil
	}
	return oxlintInvocation{}, ErrToolUnavailable("oxlint")
}

// oxlintArgs 构造一次 oxlint 运行的参数列表
//
// 从分析器里抽出来，测试因此可以在没装 oxlint 的情况下断言它，Vue 开关有没有
// 真正传到命令行上是值得钉住的决定，而从外部看不出来
//
// Vue 插件会加上感知模板的规则，默认开启。留出关闭选项是给不用 Vue 的项目，
// 同时也让这个设置显示在面板上而不是隐含的
//
// 不传路径参数，也不传忽略规则：调用方在后面追加显式文件列表
func oxlintArgs(base []string, vuePlugin bool) []string {
	args := append(append([]string{}, base...), "-f", "json")
	if vuePlugin {
		args = append(args, "--vue-plugin")
	}
	return args
}

// oxlintFiles 返回要交给 oxlint 的源文件，路径相对项目根目录
//
// 依赖目录和忽略规则由 Fizzra 的遍历器处理，这里只按扩展名挑
func oxlintFiles(actx *Context) []string {
	var out []string
	for _, fi := range actx.Files.WithExt(".js", ".jsx", ".ts", ".tsx", ".mjs", ".cjs", ".vue") {
		out = append(out, fi.Rel)
	}
	return out
}

func (a *OxlintAnalyzer) Analyze(ctx context.Context, projectPath string) ([]Issue, error) {
	return nil, nil
}

func (a *OxlintAnalyzer) AnalyzeWithContext(actx *Context) ([]Issue, error) {
	invocation, err := oxlintCommand(actx)
	if err != nil {
		return nil, err
	}

	// -f json 输出到 stdout。注意 oxlint 即使报出警告也退出 0，所以退出码不带
	// 信号，输出必须始终解析。这也是刻意不传 --deny-warnings 的原因，把发现排除
	// 在退出码之外，非零退出才能明确表示「oxlint 失败了」
	//
	// 传显式文件列表而不是目录。oxlint 只在目录是 git 仓库时才递归遍历，没有 .git
	// 的目录（解压的源码包、CI 里的临时检出）会直接报「No files found to lint」并
	// 返回成功，看起来就是一次干净的扫描
	//
	// 文件列表来自 Fizzra 自己的遍历器，依赖目录和忽略规则已经处理过，因此不需要
	// 再传 --ignore-pattern
	files := oxlintFiles(actx)
	if len(files) == 0 {
		return nil, ErrNothingToCheck("no JavaScript or TypeScript source files")
	}

	args := append(invocation.args, oxlintArgs(nil, actx.OptionBool(a.Name(), "vuePlugin", true))...)
	args = append(args, files...)

	out, err := actx.Runner.Run(actx.Context, actx.ProjectPath, invocation.name, args...)
	if err != nil {
		if IsTimeout(err) {
			return nil, err
		}
		if len(out) == 0 {
			return nil, fmt.Errorf("oxlint failed: %w", err)
		}
		actx.Log.Debug("oxlint exited non-zero with output", "err", err)
	}

	payload := jsonPayload(out)
	if payload == nil {
		// 空运行什么都不打印，这不是错误
		return nil, nil
	}

	var report oxlintReport
	if err := json.Unmarshal(payload, &report); err != nil {
		return nil, fmt.Errorf("parse oxlint output: %w", err)
	}

	issues := make([]Issue, 0, len(report.Diagnostics))
	snippets := NewSnippetReader(actx.Files)
	for _, d := range report.Diagnostics {
		plugin, rule := splitOxlintCode(d.Code)

		// 文件级诊断的 labels 可能为空，此时没有位置可报，该发现属于整个文件
		line, col := 0, 0
		if len(d.Labels) > 0 {
			line = d.Labels[0].Span.Line
			col = d.Labels[0].Span.Column
		}

		rel := filepath.ToSlash(d.Filename)
		issues = append(issues, Issue{
			Analyzer: a.Name(),
			Rule:     d.Code,
			Severity: mapOxlintSeverity(d.Severity, plugin, rule),
			Message:  strings.TrimSpace(d.Message),
			Advice:   strings.TrimSpace(d.Help),
			// oxlint 不提供可直接套用的修复，所以 Fix 留空。源码片段从文件里读回，
			// 因为该工具的 JSON 也不带源码文本
			File:   rel,
			Line:   line,
			Column: col,
			Code:   snippets.Line(rel, line),
			DocURL: d.URL,
		})
	}
	return issues, nil
}

// splitOxlintCode 把 "eslint(no-unused-vars)" 拆成 ("eslint", "no-unused-vars")
//
// 没有插件前缀的 code 会作为规则名返回，插件部分为空
func splitOxlintCode(code string) (plugin, rule string) {
	open := strings.IndexByte(code, '(')
	if open < 0 || !strings.HasSuffix(code, ")") {
		return "", code
	}
	return code[:open], code[open+1 : len(code)-1]
}

// mapOxlintSeverity 映射 oxlint 自己的严重级别，回退时才看规则族
//
// 与 ruff 不同，oxlint 的 severity 字段是有意义的（warning、error、advice），
// 所以直接采用。规则族只用来打破平局，针对的是 oxlint 默认报成 error 但并不涉及
// 安全的那些规则
func mapOxlintSeverity(severity, plugin, rule string) string {
	switch strings.ToLower(strings.TrimSpace(severity)) {
	case "error":
		return SeverityError
	case "warning", "warn":
		return SeverityWarning
	case "advice", "info":
		return SeverityInfo
	}

	// 没有可用的严重级别，改看规则名
	lower := strings.ToLower(rule)
	switch {
	case strings.Contains(lower, "injection"),
		strings.Contains(lower, "eval"),
		strings.Contains(lower, "unsafe"),
		strings.Contains(lower, "security"):
		return SeverityError
	case plugin == "jsx-a11y", plugin == "import", plugin == "unicorn":
		return SeverityInfo
	}
	return SeverityWarning
}
