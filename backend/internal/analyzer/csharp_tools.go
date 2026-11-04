package analyzer

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// C# 分析，包装 Roslynator CLI，读它的 SARIF 输出
//
// Roslynator 不需要往用户项目里加任何 NuGet 包，一条命令就能分析 csproj，这是选它
// 而不是 StyleCop 的原因，后者要改用户的工程文件

// RoslynatorAnalyzer 运行 Roslynator
type RoslynatorAnalyzer struct{ csharpOnly }

// NewRoslynatorAnalyzer 构造该分析器
func NewRoslynatorAnalyzer() Analyzer { return &RoslynatorAnalyzer{} }

func (a *RoslynatorAnalyzer) Name() string { return "roslynator" }

func (a *RoslynatorAnalyzer) Description() string {
	return "Runs Roslynator: compiler diagnostics, .NET built-in analyzers and code-quality rules for C#."
}

func (a *RoslynatorAnalyzer) Category() string { return "lint" }

func (a *RoslynatorAnalyzer) Requires() []string { return []string{"roslynator"} }

// InstallHint 给出可直接复制的安装命令，分步说明在前端 toolHints 词条里
func (a *RoslynatorAnalyzer) InstallHint() string {
	return "dotnet tool install --global Roslynator.DotNet.Cli"
}

// ProbeCommands 让探测认 PATH 上的 roslynator，以及 dotnet 全局工具
func (a *RoslynatorAnalyzer) ProbeCommands() [][]string {
	return [][]string{{"roslynator"}, {"roslynator.exe"}}
}

// roslynatorExtraArgs 是默认参数之外要加的开关
//
// AnalysisMode=All 打开默认关闭的分析器集合，其中 CA2000 是资源未释放，默认参数下
// 完全查不到这一类。代价是规则面变宽，由 roslynatorNoiseRules 压制
var roslynatorExtraArgs = []string{"-p", "AnalysisMode=All"}

// roslynatorNoiseRules 是报了对整个文件重复且不指向具体缺陷的规则
//
// CA1822 会把同一类里的多个成员各报一次，CA1823 与 CS0169 说的是同一件事。
// 留着它们会让真实缺陷被淹掉
var roslynatorNoiseRules = map[string]bool{
	"CA1822": true,
	"CA1823": true,
}

// sarifLog 对应 SARIF 2.1.0，只声明用得到的字段
type sarifLog struct {
	Runs []struct {
		Results []sarifResult `json:"results"`
	} `json:"runs"`
}

type sarifResult struct {
	RuleID  string `json:"ruleId"`
	Level   string `json:"level"`
	Message struct {
		Text string `json:"text"`
	} `json:"message"`
	Locations []struct {
		PhysicalLocation struct {
			ArtifactLocation struct {
				URI string `json:"uri"`
			} `json:"artifactLocation"`
			Region struct {
				StartLine   int `json:"startLine"`
				StartColumn int `json:"startColumn"`
			} `json:"region"`
		} `json:"physicalLocation"`
	} `json:"locations"`
}

func (a *RoslynatorAnalyzer) Analyze(ctx context.Context, projectPath string) ([]Issue, error) {
	return nil, nil
}

func (a *RoslynatorAnalyzer) AnalyzeWithContext(actx *Context) ([]Issue, error) {
	project, err := roslynatorProject(actx.ProjectPath)
	if err != nil {
		return nil, err
	}
	if project == "" {
		return nil, ErrNothingToCheck("no .csproj found")
	}

	cmd, err := roslynatorCommand(actx)
	if err != nil {
		return nil, err
	}

	out, err := os.CreateTemp("", "fizzra-roslynator-*.sarif")
	if err != nil {
		return nil, fmt.Errorf("create roslynator report file: %w", err)
	}
	outPath := out.Name()
	out.Close()
	defer os.Remove(outPath)

	args := append([]string{
		"analyze", project,
		"--output-format", "sarif",
		"-o", outPath,
		// 退出码不该由「有没有发现问题」决定，否则有发现也走错误分支
		"--return-success-on-diagnostics",
	}, roslynatorExtraArgs...)

	if _, runErr := actx.Runner.Run(actx.Context, actx.ProjectPath, cmd.name, args...); runErr != nil {
		if IsTimeout(runErr) {
			return nil, runErr
		}
		return nil, fmt.Errorf("roslynator failed: %w", runErr)
	}

	data, err := os.ReadFile(outPath)
	if err != nil {
		return nil, fmt.Errorf("read roslynator report: %w", err)
	}

	data = trimBOM(data)

	var log sarifLog
	if err := parseSARIF(data, &log); err != nil {
		return nil, fmt.Errorf("parse roslynator report: %w", err)
	}

	snippets := NewSnippetReader(actx.Files)
	var issues []Issue
	for _, run := range log.Runs {
		for _, r := range run.Results {
			if roslynatorNoiseRules[r.RuleID] {
				continue
			}

			file, line, col := "", 0, 0
			if len(r.Locations) > 0 {
				loc := r.Locations[0].PhysicalLocation
				file = fileURIToRel(loc.ArtifactLocation.URI, actx.ProjectPath)
				line = loc.Region.StartLine
				col = loc.Region.StartColumn
			}

			issues = append(issues, Issue{
				Analyzer: a.Name(),
				Rule:     r.RuleID,
				Severity: mapSarifLevel(r.Level),
				Message:  r.Message.Text,
				File:     file,
				Line:     line,
				Column:   col,
				Code:     snippetAt(snippets, file, line),
				DocURL:   roslynatorDocURL(r.RuleID),
			})
		}
	}
	return issues, nil
}

// roslynatorProject 找到要分析的工程文件，多个 csproj 时取最浅的那个
func roslynatorProject(projectPath string) (string, error) {
	var found []string
	err := filepath.Walk(projectPath, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		if info.IsDir() {
			// 构建产物目录里不会有 csproj，但遍历进去很慢
			name := info.Name()
			if name == "bin" || name == "obj" || name == ".git" || name == "node_modules" {
				return filepath.SkipDir
			}
			return nil
		}
		if strings.EqualFold(filepath.Ext(path), ".csproj") {
			found = append(found, path)
		}
		return nil
	})
	if err != nil {
		return "", err
	}
	if len(found) == 0 {
		return "", nil
	}

	shallowest := found[0]
	for _, p := range found[1:] {
		if len(p) < len(shallowest) {
			shallowest = p
		}
	}
	return shallowest, nil
}

// roslynatorInvocation 是解析好的命令
type roslynatorInvocation struct {
	name string
}

// roslynatorCommand 定位 Roslynator：先 PATH，再看 dotnet 全局工具目录
func roslynatorCommand(actx *Context) (roslynatorInvocation, error) {
	if path, err := LookCandidates(actx.Runner, "roslynator", "roslynator.exe"); err == nil {
		return roslynatorInvocation{name: path}, nil
	}
	// dotnet tool 的默认安装位置，常常不在 PATH 上
	if home, err := os.UserHomeDir(); err == nil {
		for _, name := range []string{"roslynator.exe", "roslynator"} {
			candidate := filepath.Join(home, ".dotnet", "tools", name)
			if fileExists(candidate) {
				return roslynatorInvocation{name: candidate}, nil
			}
		}
	}
	return roslynatorInvocation{}, ErrToolUnavailable("roslynator")
}

// mapSarifLevel 把 SARIF 的 level 映射到本项目的级别
//
// note 会对应大量「成员可以改为 static」这类提醒，所以压到 info
func mapSarifLevel(level string) Severity {
	switch level {
	case "error":
		return SeverityError
	case "warning":
		return SeverityWarning
	case "note", "none":
		return SeverityInfo
	}
	return SeverityWarning
}

// trimBOM 去掉 UTF-8 BOM
//
// Roslynator 输出的 SARIF 带 BOM，json.Unmarshal 会在第 0 字节报错。浏览器和 jq 都
// 能正常解析，所以这个问题只在集成里出现
func trimBOM(data []byte) []byte {
	return bytes.TrimPrefix(data, []byte("\xef\xbb\xbf"))
}

// parseSARIF 解析去掉 BOM 后的 SARIF
func parseSARIF(data []byte, out *sarifLog) error {
	return json.Unmarshal(data, out)
}

// fileURIToRel 把 SARIF 里的 file:/// URI 还原成项目内的相对路径
func fileURIToRel(uri, projectPath string) string {
	if !strings.HasPrefix(uri, "file:///") {
		return uri
	}
	path := filepath.ToSlash(strings.TrimPrefix(uri, "file:///"))
	return relToProject(path, filepath.ToSlash(projectPath))
}

// roslynatorDocURL 给出规则说明地址
//
// 编译器与 .NET 内置规则（CS/CA）在微软文档里，Roslynator 自己的规则（RCS）在它
// 的仓库里，两套前缀对应两个空间
func roslynatorDocURL(rule string) string {
	switch {
	case strings.HasPrefix(rule, "CS"):
		return "https://learn.microsoft.com/dotnet/csharp/language-reference/compiler-messages/" + strings.ToLower(rule)
	case strings.HasPrefix(rule, "CA"):
		return "https://learn.microsoft.com/dotnet/fundamentals/code-analysis/quality-rules/" + strings.ToLower(rule)
	case strings.HasPrefix(rule, "RCS"):
		return "https://josefpihrt.github.io/docs/roslynator/analyzers/" + rule
	}
	return ""
}

// 编译期断言
var _ Analyzer = (*RoslynatorAnalyzer)(nil)
