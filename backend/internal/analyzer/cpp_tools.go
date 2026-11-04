package analyzer

import (
	"context"
	"encoding/xml"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// C/C++ 分析，包装 cppcheck，读它的 XML 输出
//
// 不需要编译数据库，也不要求项目能构建，指向源码目录即可

// CppcheckAnalyzer 运行 cppcheck
type CppcheckAnalyzer struct{ cppOnly }

// NewCppcheckAnalyzer 构造该分析器
func NewCppcheckAnalyzer() Analyzer { return &CppcheckAnalyzer{} }

func (a *CppcheckAnalyzer) Name() string { return "cppcheck" }

func (a *CppcheckAnalyzer) Description() string {
	return "Runs cppcheck: memory leaks, buffer overflows, dangling pointers and other defects in C and C++. Needs no build and no compilation database."
}

func (a *CppcheckAnalyzer) Category() string { return "bugrisk" }

func (a *CppcheckAnalyzer) Requires() []string { return []string{"cppcheck"} }

// InstallHint 给出可直接复制的下载地址，分步说明在前端 toolHints 词条里
func (a *CppcheckAnalyzer) InstallHint() string {
	return "https://github.com/cppcheck-opensource/cppcheck/releases"
}

// ProbeCommands 让探测认 PATH 上的 cppcheck
func (a *CppcheckAnalyzer) ProbeCommands() [][]string {
	return [][]string{{"cppcheck"}, {"cppcheck.exe"}}
}

// cppReport 对应 `cppcheck --xml --xml-version=2` 的输出
type cppReport struct {
	XMLName xml.Name   `xml:"results"`
	Errors  []cppError `xml:"errors>error"`
}

type cppError struct {
	ID        string        `xml:"id,attr"`
	Severity  string        `xml:"severity,attr"`
	Message   string        `xml:"msg,attr"`
	CWE       string        `xml:"cwe,attr"`
	Locations []cppLocation `xml:"location"`
}

type cppLocation struct {
	File   string `xml:"file,attr"`
	Line   int    `xml:"line,attr"`
	Column int    `xml:"column,attr"`
	Info   string `xml:"info,attr"`
}

// cppIgnoredIDs 是不指向代码缺陷的规则
//
// missingIncludeSystem 只是 cppcheck 找不到标准库头，它自己的提示里也写明不需要，
// checkersReport 是它统计自身耗时的信息，没有位置
var cppIgnoredIDs = map[string]bool{
	"missingIncludeSystem": true,
	"checkersReport":       true,
}

// cppIgnoredSeverities 是不当作发现汇报的级别
var cppIgnoredSeverities = map[string]bool{
	"information": true,
	"debug":       true,
}

func (a *CppcheckAnalyzer) Analyze(ctx context.Context, projectPath string) ([]Issue, error) {
	return nil, nil
}

func (a *CppcheckAnalyzer) AnalyzeWithContext(actx *Context) ([]Issue, error) {
	cmd, err := cppcheckCommand(actx)
	if err != nil {
		return nil, err
	}

	// XML 走 stderr，而 Runner 只返回 stdout，所以让它写文件
	out, err := os.CreateTemp("", "fizzra-cppcheck-*.xml")
	if err != nil {
		return nil, fmt.Errorf("create cppcheck report file: %w", err)
	}
	outPath := out.Name()
	out.Close()
	defer os.Remove(outPath)

	args := []string{
		"--enable=all",
		"--inconclusive",
		"--xml",
		"--xml-version=2",
		"--quiet",
		"--output-file=" + outPath,
		actx.ProjectPath,
	}

	_, runErr := actx.Runner.Run(actx.Context, actx.ProjectPath, cmd.name, args...)
	if IsTimeout(runErr) {
		return nil, runErr
	}

	data, err := os.ReadFile(outPath)
	if err != nil {
		if runErr != nil {
			return nil, fmt.Errorf("cppcheck failed: %w", runErr)
		}
		return nil, fmt.Errorf("read cppcheck report: %w", err)
	}

	var report cppReport
	if err := xml.Unmarshal(trimToXML(data), &report); err != nil {
		return nil, fmt.Errorf("parse cppcheck report: %w", err)
	}

	snippets := NewSnippetReader(actx.Files)
	var issues []Issue
	for _, e := range report.Errors {
		if cppIgnoredIDs[e.ID] || cppIgnoredSeverities[e.Severity] {
			continue
		}
		if len(e.Locations) == 0 {
			continue
		}
		loc := e.Locations[0]

		file := relToProject(loc.File, actx.ProjectPath)
		issues = append(issues, Issue{
			Analyzer: a.Name(),
			Rule:     e.ID,
			Severity: mapCppcheckSeverity(e.Severity),
			Message:  e.Message,
			File:     file,
			Line:     loc.Line,
			Column:   loc.Column,
			Code:     snippetAt(snippets, file, loc.Line),
			DocURL:   cppcheckDocURL(e.CWE),
		})
	}
	return issues, nil
}

// trimToXML 去掉 XML 声明之前的输出
//
// cppcheck 会把 "Checking file.c ..." 混进 XML 前面，直接 Unmarshal 会失败
func trimToXML(out []byte) []byte {
	if i := strings.Index(string(out), "<?xml"); i >= 0 {
		return out[i:]
	}
	return out
}

// cppcheckInvocation 是解析好的命令
type cppcheckInvocation struct {
	name string
}

// cppcheckCommand 定位 cppcheck，先 PATH，再看 FIZZRA_CPPCHECK_HOME
func cppcheckCommand(actx *Context) (cppcheckInvocation, error) {
	if path, err := LookCandidates(actx.Runner, "cppcheck", "cppcheck.exe"); err == nil {
		return cppcheckInvocation{name: path}, nil
	}
	if home := toolHome("FIZZRA_CPPCHECK_HOME"); home != "" {
		candidate := filepath.Join(home, "cppcheck.exe")
		if fileExists(candidate) {
			return cppcheckInvocation{name: candidate}, nil
		}
	}
	return cppcheckInvocation{}, ErrToolUnavailable("cppcheck")
}

// mapCppcheckSeverity 把 cppcheck 的 severity 映射到本项目的级别
func mapCppcheckSeverity(severity string) Severity {
	switch severity {
	case "error":
		return SeverityError
	case "warning":
		return SeverityWarning
	case "style", "performance", "portability":
		return SeverityInfo
	}
	return SeverityWarning
}

// cppcheckDocURL 按 CWE 编号给出说明地址，没有编号时返回空
func cppcheckDocURL(cwe string) string {
	if cwe == "" {
		return ""
	}
	return "https://cwe.mitre.org/data/definitions/" + cwe + ".html"
}

// 编译期断言
var _ Analyzer = (*CppcheckAnalyzer)(nil)
