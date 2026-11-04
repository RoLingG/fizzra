package analyzer

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// JavaScript 与 TypeScript 分析，基于 Biome
//
// Biome 是与 oxlint 并存的第二个 linter，不是替代品。两者都是无需项目配置的
// 原生二进制，所以接入前拿同一个文件对比过，结论是互补而非重复
//
//	                           oxlint   biome
//	if (x = 1)  noAssignInExpressions     -      yes
//	constant condition                    -      yes
//	assign to const                       -      yes
//	no-explicit-any                      yes      yes
//
// 前三项是正确性错误，oxlint 的默认规则集不启用，Biome 的默认集启用
//
// 下面的字段都是对照真实的 `biome check --reporter=json` 输出确认的，不是照
// 文档写的

// BiomeAnalyzer 运行 Biome 的 linter
type BiomeAnalyzer struct{ javascriptOnly }

// NewBiomeAnalyzer 构造该分析器
func NewBiomeAnalyzer() Analyzer { return &BiomeAnalyzer{} }

func (a *BiomeAnalyzer) Name() string { return "biome" }

func (a *BiomeAnalyzer) Description() string {
	return "Runs Biome's linter: correctness, suspicious patterns and style for JavaScript, TypeScript, JSX/TSX, JSON and CSS. Catches several correctness errors oxlint's default set does not."
}

func (a *BiomeAnalyzer) Category() string   { return "lint" }
func (a *BiomeAnalyzer) Requires() []string { return []string{"biome"} }

// Options 实现 OptionProvider，把开关暴露给面板
func (a *BiomeAnalyzer) Options() []DescriptorOption {
	return []DescriptorOption{{
		Key:     "css",
		Default: true,
		Description: "Lint CSS (.css) in addition to JavaScript and TypeScript: unknown " +
			"properties, duplicate declarations, empty blocks, !important. " +
			"Note that Biome does not read .scss or .less.",
	}}
}

// InstallHint 只给一条可直接复制的命令，并点出容易搞错的包名
//
// npm 上不带作用域的 biome 是一个毫不相干、早已废弃的 0.3.x 版本
//
// 装成全局，因为 Fizzra 是单个可执行文件，没有「每个项目装一份」的说法。项目里
// 若恰好也装了 biome，扫描时仍然优先用项目自己那份，版本才不会和它的配置错配。
// 其他装法与验证步骤在 toolHints 词条里
func (a *BiomeAnalyzer) InstallHint() string {
	return "npm install -g @biomejs/biome"
}

// ProbeCommands 让探测同时认独立二进制和经 npx 调用的项目本地安装
//
// Biome 以 npm 包 @biomejs/biome 分发，二进制名叫 biome。把它当 devDependency
// 安装的项目只在 node_modules/.bin 里有它，PATH 上没有，这和 ruff 的控制台脚本
// 问题形状相同，扫描能跑，界面却报它缺失
func (a *BiomeAnalyzer) ProbeCommands() [][]string {
	return [][]string{{"biome"}, {"npx", "--no-install", "@biomejs/biome"}}
}

// AvailableIn 报告这个项目里能不能跑 biome
//
// 覆盖 ProjectScoped，走 npx 的分析器不能只看 PATH，理由见接口定义处
func (a *BiomeAnalyzer) AvailableIn(projectPath string) bool {
	return analyzerAvailableIn(projectPath, "biome")
}

// biomeReport 对应 `biome check --reporter=json` 的输出
//
// JSON 报告器文档上标为实验性，它输出里有两件事必须处理
//
//   - 一行警告（"The `json` and `json-pretty` reporters are experimental"）打在
//     JSON 对象之前，之后还有一段人类可读的汇总。jsonPayload() 从第一个
//     左花括号开始做括号配对，两种都能处理
//   - 诊断位置嵌在 location.start 之下，line 和 column 从 1 开始，路径是
//     Windows 形式的绝对路径
type biomeReport struct {
	Summary struct {
		Errors    int `json:"errors"`
		Warnings  int `json:"warnings"`
		Infos     int `json:"infos"`
		Changed   int `json:"changed"`
		Unchanged int `json:"unchanged"`
		Matches   int `json:"matches"`
		Skipped   int `json:"skipped"`
	} `json:"summary"`
	Diagnostics []biomeDiagnostic `json:"diagnostics"`
}

// biomeDiagnostic 是 diagnostics 数组中的一项
type biomeDiagnostic struct {
	Severity string `json:"severity"`
	Message  string `json:"message"`
	Category string `json:"category"`
	Location struct {
		Path  string `json:"path"`
		Start struct {
			Line   int `json:"line"`
			Column int `json:"column"`
		} `json:"start"`
	} `json:"location"`
	Advices []struct {
		Start struct {
			Line   int `json:"line"`
			Column int `json:"column"`
		} `json:"start"`
		End struct {
			Line   int `json:"line"`
			Column int `json:"column"`
		} `json:"end"`
		Text string `json:"text"`
	} `json:"advices"`
}

func (a *BiomeAnalyzer) Analyze(ctx context.Context, projectPath string) ([]Issue, error) {
	return nil, nil
}

func (a *BiomeAnalyzer) AnalyzeWithContext(actx *Context) ([]Issue, error) {
	invocation, err := biomeCommand(actx)
	if err != nil {
		return nil, err
	}

	// check 会同时跑 linter 和 formatter，而 formatter 给每个需要重排的文件报一条
	// 没有位置、没有可操作内容的 `"category":"format"` 诊断，会把真正的缺陷淹没
	// 格式不在本工具关心的范围内，所以在命令里直接关掉，而不是事后过滤
	args := append([]string{}, invocation.args...)
	args = append(args,
		"check",
		"--reporter=json",
		"--formatter-enabled=false",
		// assist 动作是给编辑器用的小便利（排序导入、应用安全修复），也不算
		// 发现
		"--assist-enabled=false",
		".",
	)

	out, err := actx.Runner.Run(actx.Context, actx.ProjectPath, invocation.name, args...)
	if err != nil {
		if IsTimeout(err) {
			return nil, err
		}
		// Biome 报出错误时退出码非零，所以非零是预期内的，重要的是下面的载荷
		var exit *ExitError
		if !errors.As(err, &exit) || jsonPayload(out) == nil {
			return nil, fmt.Errorf("biome failed: %w", err)
		}
		actx.Log.Debug("biome exited non-zero with diagnostics", "err", err)
	}

	payload := jsonPayload(out)
	if payload == nil {
		return nil, nil
	}

	var report biomeReport
	if err := json.Unmarshal(payload, &report); err != nil {
		return nil, fmt.Errorf("parse biome output: %w", err)
	}

	issues, skippedVue, skippedCSS := biomeFindings(report, actx, a.Name())
	if skippedVue > 0 {
		actx.Log.Debug("biome: skipped .vue findings (no Vue parser; template usage is invisible)",
			"skipped", skippedVue)
	}
	if skippedCSS > 0 {
		actx.Log.Debug("biome: skipped CSS findings (css option disabled)", "skipped", skippedCSS)
	}
	return issues, nil
}

// biomeFindings 把解析好的报告转成问题，同时应用那两道过滤，并从磁盘读回每条
// 发现的源码行
//
// 从 AnalyzeWithContext 里拆出来，是为了能对着样本测试过滤和片段接线而不必真的
// 调起 biome。那一处报告已经解析完了，所以这个接缝没有成本
func biomeFindings(report biomeReport, actx *Context, analyzerName string) (issues []Issue, skippedVue, skippedCSS int) {
	snippets := NewSnippetReader(actx.Files)
	lintCSS := actx.OptionBool(analyzerName, "css", true)

	issues = make([]Issue, 0, len(report.Diagnostics))
	for _, d := range report.Diagnostics {
		// 格式诊断没有位置也没有定位点。因为已经关了 formatter，它本不该走到
		// 这里，但将来的 Biome 版本仍可能发出来。跳过是对的，没有内容可报，也
		// 没有地方可指
		if d.Category == "format" || d.Location.Path == "" {
			continue
		}

		// Biome 没有 Vue 解析器，`.vue` 文件里的发现会错得最要命
		// `<script setup>` 里声明的符号只要只在 `<template>` 中被用到，就会被报成未使用，
		// 因为那个块它根本没解析。报出去是一份和代码自相矛盾的结果，
		// 而这正是本工具要避免的。没有排除扩展名的参数，所以过滤放在这里
		if isVuePath(d.Location.Path) {
			skippedVue++
			continue
		}

		// CSS 走同一趟 lint，css 开关可以关掉它
		//
		// 在这里过滤而不是在命令里，因为没有 --css-linter-enabled=false，
		// Biome 的 --formatter-enabled 和 --assist-enabled 没有对应的 linter 语言
		// 开关。这种过滤是安全的，不像 .vue 那种情况，因为这些诊断说的是它们所指
		// 的那个文件本身，而不是某个未被解析的块
		if !lintCSS && isStylePath(d.Location.Path) {
			skippedCSS++
			continue
		}

		rel := relToProject(d.Location.Path, actx.ProjectPath)
		issues = append(issues, Issue{
			Analyzer: analyzerName,
			Rule:     d.Category,
			Severity: mapBiomeSeverity(d.Severity, d.Category),
			Message:  strings.TrimSpace(d.Message),
			Advice:   biomeAdvice(d),
			// Fix 刻意留空。Biome 只在人类可读报告器里打印修改前后的 diff，
			// JSON 报告器的 advices 是文字说明（如 "Safe fix: Use const instead."）
			// 或指向另一个 span 的引用，没有可用的替换文本
			File:   rel,
			Line:   d.Location.Start.Line,
			Column: d.Location.Start.Column,
			Code:   snippets.Line(rel, d.Location.Start.Line),
			DocURL: biomeDocURL(d.Category),
		})
	}
	return issues, skippedVue, skippedCSS
}

// isStylePath 报告某个路径是不是 Biome 会 lint 的样式表
//
// 只有 .css。Biome 解析 .css，完全忽略 .scss、.less 及其他预处理器方言，这是
// 拿每种各一个文件跑出来的，三个文件，一条诊断，而那条诊断指的是 .css 文件。
// 一个声称覆盖 SCSS 的分析器，就是在声称一个它并不执行的检查
func isStylePath(path string) bool {
	return strings.HasSuffix(strings.ToLower(path), ".css")
}

// isVuePath 报告某个路径是否为 Vue 单文件组件
//
// 比较前先转小写，因为 Biome 原样回显路径，在不区分大小写的文件系统上 .VUE
// 否则会漏过去
func isVuePath(path string) bool {
	return strings.HasSuffix(strings.ToLower(path), ".vue")
}

// biomeInvocation 是解析好的命令加上它的前置参数
type biomeInvocation struct {
	name string
	args []string
}

// biomeCommand 定位 Biome，优先用 PATH 上的安装而不是 npx
//
// 用 npx --no-install 而不是裸 npx，因为 npm 上不带作用域的 biome 包是一个毫不
// 相干、早已废弃的 0.3.x 版本，npx biome 装的是那个而不是 Biome。--no-install
// 还能防止运行中静默下载一个项目并未选择的 linter
//
// 两处都找不到时报工具缺失，而不是把命令交给 npx。这是这个函数里唯一非显而易见
// 的一点，npx 在包不存在时会输出一坨 npm 的错误并返回非零，调用方无从把它和
// 「biome 自己崩了」区分开，于是「没装 biome」被报成了扫描失败，那要求读者从
// npm 的报错里自己推断该装什么。而这个项目里，「工具没装」和「项目里有问题」是
// 两类不同的提示，前者要装东西，后者要改代码
func biomeCommand(actx *Context) (biomeInvocation, error) {
	if path, err := LookCandidates(actx.Runner, "biome", "biome.cmd"); err == nil {
		return biomeInvocation{name: path}, nil
	}
	// 项目本地安装。包装后的 argv 是 `npx --no-install @biomejs/biome`，它会解析
	// 到 node_modules/.bin/biome
	if hasLocalBin(actx, "biome") {
		return biomeInvocation{name: "npx", args: []string{"--no-install", "@biomejs/biome"}}, nil
	}
	return biomeInvocation{}, ErrToolUnavailable("biome (@biomejs/biome)")
}

// mapBiomeSeverity 把 Biome 自己的严重级别映射到本项目的
//
// Biome 的 severity 是有意义的（error / warning / info），所以直接采用。类别
// 只用来打破平局，一条被报成 warning 的正确性规则仍然是缺陷，把它当缺陷对待
// 才能让两个 linter 把同一类问题报在同一级别上
func mapBiomeSeverity(severity, category string) string {
	switch strings.ToLower(strings.TrimSpace(severity)) {
	case "error":
		return SeverityError
	case "warning":
		// correctness/* 的定义是「完全错误或毫无用处的代码」，标准与 error 相同，
		// 无论 Biome 给它什么级别
		if strings.HasPrefix(category, "lint/correctness/") {
			return SeverityError
		}
		return SeverityWarning
	case "information", "info":
		return SeverityInfo
	}
	return SeverityWarning
}

// biomeAdvice 在 Biome 给出修复建议时把它呈现出来
//
// Biome 把建议的修复作为 advices 项附上，带着替换文本（"Unsafe fix: Replace
// const with let..."）。那是真正的改法而不是问题的复述，所以原样作为建议传出，
// 而不是替换成泛泛的文字
func biomeAdvice(d biomeDiagnostic) string {
	for _, a := range d.Advices {
		if t := strings.TrimSpace(a.Text); t != "" {
			return t
		}
	}
	return ""
}

// biomeDocURL 指向该规则的文档页
//
// 类别本身就是一个路径（lint/correctness/noUnusedVariables），文档站在
// /linter/rules/ 下照着它对应
func biomeDocURL(category string) string {
	parts := strings.Split(category, "/")
	if len(parts) != 3 || parts[0] != "lint" {
		return ""
	}
	return fmt.Sprintf("https://biomejs.dev/linter/rules/%s/", toKebabCase(parts[2]))
}

// toKebabCase 把 camelCase 规则名转成文档所用的 kebab-case 短名
//
// noUnusedVariables 变成 no-unused-variables。连续的大写字母算作一整段，免得
// 一个缩写被逐字母拆开
func toKebabCase(s string) string {
	var b strings.Builder
	b.Grow(len(s) + 4)
	for i, r := range s {
		if r >= 'A' && r <= 'Z' {
			prevLower := i > 0 && s[i-1] >= 'a' && s[i-1] <= 'z'
			nextLower := i+1 < len(s) && s[i+1] >= 'a' && s[i+1] <= 'z'
			if prevLower || nextLower {
				b.WriteByte('-')
			}
			b.WriteRune(r + ('a' - 'A'))
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}
