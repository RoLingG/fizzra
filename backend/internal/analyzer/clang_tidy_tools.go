package analyzer

import (
	"context"
	"regexp"
	"strconv"
	"strings"
)

// C/C++ 分析，包装 clang-tidy
//
// 与 cppcheck 互补，cppcheck 不需要任何环境，clang-tidy 要 Python 和标准库头，
// 但能查出命令注入、栈地址逃逸这类 cppcheck 看不到的问题

// ClangTidyAnalyzer 运行 clang-tidy
type ClangTidyAnalyzer struct{ cppOnly }

// NewClangTidyAnalyzer 构造该分析器
func NewClangTidyAnalyzer() Analyzer { return &ClangTidyAnalyzer{} }

func (a *ClangTidyAnalyzer) Name() string { return "clang-tidy" }

func (a *ClangTidyAnalyzer) Description() string {
	return "Runs clang-tidy: command injection, dangling pointers, memory leaks and CERT C rules. Needs Python and a C/C++ standard library on the machine."
}

func (a *ClangTidyAnalyzer) Category() string { return "bugrisk" }

func (a *ClangTidyAnalyzer) Requires() []string { return []string{"clang-tidy"} }

// InstallHint 给出可直接复制的安装命令，分步说明在前端 toolHints 词条里
func (a *ClangTidyAnalyzer) InstallHint() string {
	return "pip install clang-tidy"
}

// ProbeCommands 让探测认 PATH 上的 clang-tidy
func (a *ClangTidyAnalyzer) ProbeCommands() [][]string {
	return [][]string{{"clang-tidy"}, {"clang-tidy.exe"}}
}

// clangTidyChecks 是交给 clang-tidy 的检查集
//
// 默认一条都不开，必须显式指定。security-* 在探针上没产出，但保留它不增加成本
var clangTidyChecks = "clang-analyzer-*,bugprone-*,cert-*,security-*"

// clangTidySources 是参与分析的文件扩展名
var clangTidySources = []string{".c", ".cc", ".cpp", ".cxx", ".h", ".hh", ".hpp", ".hxx"}

// clangTidyLine 匹配一条诊断
//
//	path:line:col: warning: message [rule]  或  [rule1,rule2]
var clangTidyLine = regexp.MustCompile(`^(.+?):(\d+):(\d+): (warning|error|note): (.+?)(?: \[([^\]]+)\])?$`)

// clangTidyIssue 是一条解析出来的诊断
type clangTidyIssue struct {
	file     string
	line     int
	column   int
	severity string
	message  string
	rules    []string
}

func (a *ClangTidyAnalyzer) Analyze(ctx context.Context, projectPath string) ([]Issue, error) {
	return nil, nil
}

func (a *ClangTidyAnalyzer) AnalyzeWithContext(actx *Context) ([]Issue, error) {
	cmd, err := clangTidyCommand(actx)
	if err != nil {
		return nil, err
	}

	files := actx.Files.WithExt(clangTidySources...)
	if len(files) == 0 {
		return nil, ErrNothingToCheck("no C or C++ source files found")
	}

	snippets := NewSnippetReader(actx.Files)
	var issues []Issue
	for _, fi := range files {
		// 显式给出 -- 之后的参数，clang-tidy 就不会去找 compile_commands.json
		args := []string{fi.Abs, "--checks=" + clangTidyChecks, "--", "-std=" + clangTidyStandard(fi.Ext)}

		out, runErr := actx.Runner.Run(actx.Context, actx.ProjectPath, cmd.name, args...)
		if IsTimeout(runErr) {
			return nil, runErr
		}
		// 退出码只反映能否运行，有发现时也是 0

		issues = append(issues, clangTidyIssues(parseClangTidyOutput(out), actx, snippets)...)
	}
	return issues, nil
}

// clangTidyIssues 把诊断转成发现，同一位置只留最严重的一条
//
// 一个 bugprone 常常同时挂着 cert 编号，不同检查也会在同一行报同一件事，
// 原样保留会让读者以为问题有两条
func clangTidyIssues(diags []clangTidyIssue, actx *Context, snippets *SnippetReader) []Issue {
	best := map[string]Issue{}
	for _, d := range diags {
		if len(d.rules) == 0 || d.severity == "note" {
			continue
		}
		rel := relToProject(d.file, actx.ProjectPath)
		rule := d.rules[0]
		issue := Issue{
			Analyzer: "clang-tidy",
			Rule:     rule,
			Severity: mapClangTidySeverity(rule, d.severity),
			Message:  d.message,
			File:     rel,
			Line:     d.line,
			Column:   d.column,
			Code:     snippetAt(snippets, rel, d.line),
		}

		key := rel + ":" + strconv.Itoa(d.line)
		// Rank() 越小越严重
		if prev, ok := best[key]; ok && severityRank(prev.Severity) <= severityRank(issue.Severity) {
			continue
		}
		best[key] = issue
	}

	out := make([]Issue, 0, len(best))
	for _, is := range best {
		out = append(out, is)
	}
	return out
}

// clangTidyStandard 按扩展名给出合适的语言标准
func clangTidyStandard(ext string) string {
	switch ext {
	case ".c", ".h":
		return "c11"
	}
	return "c++17"
}

// parseClangTidyOutput 解析 clang-tidy 的文本输出
func parseClangTidyOutput(out []byte) []clangTidyIssue {
	var issues []clangTidyIssue
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		m := clangTidyLine.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		lineNo, _ := strconv.Atoi(m[2])
		col, _ := strconv.Atoi(m[3])

		var rules []string
		if m[6] != "" {
			for _, r := range strings.Split(m[6], ",") {
				if r = strings.TrimSpace(r); r != "" {
					rules = append(rules, r)
				}
			}
		}
		issues = append(issues, clangTidyIssue{
			file:     m[1],
			line:     lineNo,
			column:   col,
			severity: m[4],
			message:  m[5],
			rules:    rules,
		})
	}
	return issues
}

// clangTidyInvocation 是解析好的命令
type clangTidyInvocation struct {
	name string
}

// clangTidyCommand 定位 clang-tidy，先 PATH，再看 pip 的脚本目录
func clangTidyCommand(actx *Context) (clangTidyInvocation, error) {
	if path, err := LookCandidates(actx.Runner, "clang-tidy", "clang-tidy.exe"); err == nil {
		return clangTidyInvocation{name: path}, nil
	}
	return clangTidyInvocation{}, ErrToolUnavailable("clang-tidy")
}

// mapClangTidySeverity 映射严重度
//
// insecureAPI 与 deprecated 只是「建议换成带边界检查的函数」，不指向确定的缺陷，
// 降为提示，编译器给出的 always-overflow、返回栈地址则是确定错误
func mapClangTidySeverity(rule, level string) Severity {
	if strings.Contains(rule, "insecureAPI") {
		return SeverityInfo
	}
	if level == "error" {
		return SeverityError
	}
	switch rule {
	case "clang-diagnostic-deprecated-declarations", "clang-diagnostic-deprecated":
		return SeverityInfo
	case "clang-diagnostic-return-stack-address", "clang-diagnostic-fortify-source":
		return SeverityError
	}
	switch {
	case strings.HasPrefix(rule, "clang-analyzer"),
		strings.HasPrefix(rule, "bugprone-"),
		strings.HasPrefix(rule, "cert-"),
		strings.HasPrefix(rule, "clang-diagnostic"):
		return SeverityWarning
	}
	return SeverityInfo
}

// 编译期断言
var _ Analyzer = (*ClangTidyAnalyzer)(nil)
