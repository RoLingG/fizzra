package analyzer

import (
	"context"

	"github.com/rolingg/fizzra/backend/internal/model"
)

// CustomAnalyzer 对项目源码执行用户的规则集（内置规则，加上在 fizzra.yaml 或
// 界面里定义的规则）
//
// 它是唯一没有外部依赖的分析器，全新安装、任何 linter 都没装时仍能给出有用的结果
//
// 它不限定语言。规则是带 glob 作用域的正则表达式，引擎的三道过滤器按
// 语言做语法感知，所以同一套规则适用于每个项目，变的只是它跑在哪些文件上、以及
// 一行如何被分类。若限定成 Go，非 Go 项目上的每一条自定义规则都会静默失效
type CustomAnalyzer struct{}

// NewCustomAnalyzer 构造该分析器
func NewCustomAnalyzer() Analyzer { return &CustomAnalyzer{} }

func (a *CustomAnalyzer) Name() string { return "custom" }

func (a *CustomAnalyzer) Description() string {
	return "Evaluates Fizzra's own rule set: correctness, security hygiene and Go/Wails specific patterns defined in fizzra.yaml or the web UI."
}

func (a *CustomAnalyzer) Category() string   { return "custom" }
func (a *CustomAnalyzer) Requires() []string { return nil }

func (a *CustomAnalyzer) Analyze(ctx context.Context, projectPath string) ([]Issue, error) {
	return nil, nil // replaced by AnalyzeWithContext
}

// AnalyzeWithContext 是真正的入口，分析器实现 ContextAware 时框架会调用它
func (a *CustomAnalyzer) AnalyzeWithContext(actx *Context) ([]Issue, error) {
	// 没有已启用的规则就是没有东西可查，这不能被报成「跑过了但没发现」。否则
	// 一个 clippy 缺失的 Rust 项目会让门禁因为该分析器什么都没做而通过
	if actx.Rules == nil || actx.Rules.Len() == 0 {
		return nil, ErrNothingToCheck("no rules are enabled")
	}

	var issues []Issue
	files := rulesFiles(actx)
	if len(files) == 0 {
		return nil, ErrNothingToCheck("no source files for this language")
	}

	// 读取那些没有已启用规则能匹配的文件，算不上一次检查
	//
	// 内置规则的作用域都是 `**/*.go`，所以在一个 Rust 或 Python 项目上，这个
	// 分析器会遍历整棵树、找不到任何它被允许报告的东西，然后返回干净，读起来像
	// 「没发现问题」，而真相是「这里没有规则适用」。如实说明才能让引擎启用它
	// 全部跳过时的否决，而不是直接通过门禁
	matched := 0
	for _, fi := range files {
		if actx.Rules.AppliesTo(fi.Rel) {
			matched++
		}
	}
	if matched == 0 {
		return nil, ErrNothingToCheck("no enabled rule is scoped to this language")
	}

	for _, fi := range files {
		if err := actx.Context.Err(); err != nil {
			return issues, err
		}
		content, err := actx.Files.Read(fi, 2<<20)
		if err != nil {
			actx.Log.Debug("rule scan: read failed", "file", fi.Rel, "err", err)
			continue
		}
		for _, m := range actx.Rules.Evaluate(fi.Rel, content) {
			issues = append(issues, Issue{
				Analyzer: a.Name(),
				Rule:     m.Rule,
				Severity: normalizeSeverity(m.Severity),
				Message:  m.Message,
				Advice:   m.Advice,
				Fix: FixExample{
					Before:   m.Fix.Before,
					After:    m.Fix.After,
					Note:     m.Fix.Note,
					Language: m.Fix.Language,
				},
				File:   m.File,
				Line:   m.Line,
				Column: m.Column,
				Code:   m.Code,
			})
		}
	}
	return issues, nil
}

// WailsAnalyzer 执行 Go/Wails 项目结构和运行时 API 检查
//
// 与上面基于规则的扫描不同，这些检查需要全项目上下文，wails.json 存不存在会
// 影响一条运行时 API 发现的严重级别，而绑定分析需要解析 main 包
type WailsAnalyzer struct{ goOnly }

// NewWailsAnalyzer 构造该分析器
func NewWailsAnalyzer() Analyzer { return &WailsAnalyzer{} }

func (a *WailsAnalyzer) Name() string { return "wails" }

func (a *WailsAnalyzer) Description() string {
	return "Wails project structure, runtime API usage (EventsEmit, Window, Dialog, BrowserOpenURL) and binding surface review."
}

func (a *WailsAnalyzer) Category() string   { return "wails" }
func (a *WailsAnalyzer) Requires() []string { return nil }

func (a *WailsAnalyzer) Analyze(ctx context.Context, projectPath string) ([]Issue, error) {
	return nil, nil
}

// AnalyzeWithContext 运行 Wails 专项检查
func (a *WailsAnalyzer) AnalyzeWithContext(actx *Context) ([]Issue, error) {
	chk := newWailsChecker(actx, a.Name())
	return chk.Run()
}

// normalizeSeverity 把任意字符串映射到规范的四个等级
func normalizeSeverity(s string) string {
	sev := model.ParseSeverity(s)
	return string(sev)
}

// rulesFiles 返回规则引擎应该跑在哪些文件上
//
// 引擎自己的作用域过滤与语言无关（规则声明 `paths: ['**/*.py']` 就是这个意思），
// 但这里的文件选择写死成了 .go，所以一条 Python 规则永远看不到 Python 文件。
// 那是规则引擎唯一还在假定宿主语言的地方，它让自定义规则在每个非 Go 项目上静默
// 失效
//
// 无法识别的语言回退到 Go，与工具其余部分一致
func rulesFiles(actx *Context) []FileInfo {
	switch actx.ProjectLanguage {
	case modelLanguagePython:
		return actx.Files.WithExt(".py", ".pyi")
	case modelLanguageRust:
		return actx.Files.WithExt(".rs")
	case modelLanguageJavaScript:
		return actx.Files.WithExt(".js", ".jsx", ".ts", ".tsx", ".mjs", ".cjs")
	default:
		return actx.Files.GoFiles()
	}
}
