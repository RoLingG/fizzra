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

// Java 分析包装 PMD，读取它的 JSON 输出

// PmdAnalyzer 运行 PMD
type PmdAnalyzer struct{ javaOnly }

// NewPmdAnalyzer 构造该分析器
func NewPmdAnalyzer() Analyzer { return &PmdAnalyzer{} }

func (a *PmdAnalyzer) Name() string { return "pmd" }

func (a *PmdAnalyzer) Description() string {
	return "Runs PMD: error-prone patterns, best practices, performance, concurrency, design and security rules for Java."
}

func (a *PmdAnalyzer) Category() string { return "lint" }

func (a *PmdAnalyzer) Requires() []string { return []string{"pmd"} }

// InstallHint 给出可直接复制的下载地址，分步说明在前端 toolHints 词条里
func (a *PmdAnalyzer) InstallHint() string {
	return "https://github.com/pmd/pmd/releases"
}

// ProbeCommands 让探测认 PATH 上的 pmd 和 Windows 发行版里的 pmd.bat
func (a *PmdAnalyzer) ProbeCommands() [][]string {
	return [][]string{{"pmd"}, {"pmd.bat"}}
}

// pmdRulesets 是交给 PMD 的规则类别
//
// 不用默认规则集，Maven 插件那份只有 41 条，且不含 security。codestyle 也不整块
// 引入，它会把「参数可以加 final」这类提醒淹掉真实缺陷，个别有价值的规则单独加
var pmdRulesets = []string{
	"category/java/errorprone.xml",
	"category/java/bestpractices.xml",
	"category/java/performance.xml",
	"category/java/multithreading.xml",
	"category/java/security.xml",
	"category/java/design.xml",
}

// pmdExtraRules 是从 codestyle 里单独捞回来的规则。UnnecessaryBoxing 是循环里的
// 装箱，性能缺陷，按类别整块排除会把它一起丢掉
var pmdExtraRules = []string{
	"category/java/codestyle.xml/UnnecessaryBoxing",
}

// pmdReport 对应 `pmd check -f json` 的输出，只声明用得到的字段
type pmdReport struct {
	FormatVersion int       `json:"formatVersion"`
	PMDVersion    string    `json:"pmdVersion"`
	Files         []pmdFile `json:"files"`
	Errors        []pmdErr  `json:"processingErrors"`
	ConfigErrors  []pmdErr  `json:"configurationErrors"`
}

type pmdFile struct {
	Filename   string         `json:"filename"`
	Violations []pmdViolation `json:"violations"`
}

type pmdViolation struct {
	BeginLine   int    `json:"beginline"`
	BeginColumn int    `json:"begincolumn"`
	EndLine     int    `json:"endline"`
	Description string `json:"description"`
	Rule        string `json:"rule"`
	Ruleset     string `json:"ruleset"`
	// Priority 是 PMD 的 1-5 刻度，表达规则置信度而非缺陷严重度，所以不用，见 mapPmdSeverity
	Priority int    `json:"priority"`
	URL      string `json:"externalInfoUrl"`
}

type pmdErr struct {
	Filename string `json:"filename"`
	Rule     string `json:"rule"`
	Message  string `json:"message"`
	Msg      string `json:"msg"`
}

// text 给出这条错误的可读描述。两个字段名都要认，processingErrors 用 msg，
// configurationErrors 用 message
func (e pmdErr) text() string {
	if e.Message != "" {
		return e.Message
	}
	if e.Msg != "" {
		return e.Msg
	}
	if e.Rule != "" {
		return e.Rule
	}
	return "unknown PMD error"
}

func (a *PmdAnalyzer) Analyze(ctx context.Context, projectPath string) ([]Issue, error) {
	return nil, nil
}

func (a *PmdAnalyzer) AnalyzeWithContext(actx *Context) ([]Issue, error) {
	cmd, err := pmdCommand(actx)
	if err != nil {
		return nil, err
	}

	rulesets := append(append([]string{}, pmdRulesets...), pmdExtraRules...)

	args := []string{
		"check",
		"-d", actx.ProjectPath,
		"-R", strings.Join(rulesets, ","),
		"-f", "json",
		// 不写缓存文件：扫描不该往用户的项目里留东西
		"--no-cache",
		"--no-progress",
	}

	// PMD 发现违规时退出码也是非零，所以以输出能否解析为准，不能才当失败
	out, runErr := actx.Runner.Run(actx.Context, actx.ProjectPath, cmd.name, args...)
	if IsTimeout(runErr) {
		return nil, runErr
	}

	var report pmdReport
	if jsonErr := json.Unmarshal(bytes.TrimSpace(out), &report); jsonErr != nil {
		// 解析不了：可能是工具报错，也可能是版本不认识，把两者都带上
		if runErr != nil {
			return nil, fmt.Errorf("pmd failed: %w", runErr)
		}
		return nil, fmt.Errorf("pmd produced unparseable output: %w", jsonErr)
	}

	// configurationErrors 不致命
	// PMD 会因参数不全移除个别规则，扫描照常完成并报出全部违规，当失败会丢掉整份结果
	for _, e := range report.ConfigErrors {
		actx.Log.Debug("pmd dropped a misconfigured rule", "rule", e.Rule, "detail", e.text())
	}

	issues := make([]Issue, 0, len(report.Files))
	for _, f := range report.Files {
		rel := relToProject(f.Filename, actx.ProjectPath)
		for _, v := range f.Violations {
			issues = append(issues, Issue{
				Analyzer: a.Name(),
				Rule:     v.Rule,
				Severity: mapPmdSeverity(v.Ruleset, v.Rule),
				Message:  v.Description,
				File:     rel,
				Line:     v.BeginLine,
				Column:   v.BeginColumn,
				DocURL:   v.URL,
			})
		}
	}
	return issues, nil
}

// pmdInvocation 是解析好的命令
type pmdInvocation struct {
	name string
}

// pmdCommand 定位 PMD，先 PATH，再看 FIZZRA_PMD_HOME 指向的发行版目录
func pmdCommand(actx *Context) (pmdInvocation, error) {
	if path, err := LookCandidates(actx.Runner, "pmd", "pmd.bat"); err == nil {
		return pmdInvocation{name: path}, nil
	}
	if home := toolHome("FIZZRA_PMD_HOME"); home != "" {
		for _, name := range []string{filepath.Join(home, "bin", "pmd.bat"), filepath.Join(home, "bin", "pmd")} {
			if fileExists(name) {
				return pmdInvocation{name: name}, nil
			}
		}
	}
	return pmdInvocation{}, ErrToolUnavailable("pmd")
}

// toolHome 返回环境变量指向的工具目录，没设时返回空串
func toolHome(envVar string) string {
	return strings.TrimSpace(os.Getenv(envVar))
}

// fileExists 报告路径是否是一个存在的普通文件
func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

// mapPmdSeverity 按规则类别定级
//
// 不能用 priority，它表达「这条规则有多确信」，不是「这个缺陷有多严重」。
// MethodNamingConventions 是 p1（命名规范），CloseResource 是 p3（资源泄漏），
// 按 priority 排会把命名排在泄漏前面。类别对应缺陷类型，和严重度一致
func mapPmdSeverity(ruleset, rule string) Severity {
	switch ruleset {
	case "Error Prone", "Security":
		return SeverityError
	case "Performance", "Multithreading", "Best Practices":
		return SeverityWarning
	case "Design", "Documentation":
		return SeverityInfo
	case "Code Style":
		// 少数从风格类别里捞回来的规则实际是缺陷，见 pmdExtraRules
		if pmdStyleRulesThatMatter[rule] {
			return SeverityWarning
		}
		return SeverityInfo
	}
	return SeverityWarning
}

// pmdStyleRulesThatMatter 是 codestyle 里实际会改变运行时行为的规则。名单刻意
// 短，那个类别其余的（UnnecessarySemicolon、UselessParentheses）确实是风格
var pmdStyleRulesThatMatter = map[string]bool{
	"UnnecessaryBoxing":     true,
	"EmptyControlStatement": true,
	"AvoidUsingNativeCode":  true,
}
