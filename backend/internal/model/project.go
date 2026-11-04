package model

import (
	"time"
)

// Project 是一个注册用于分析的仓库
type Project struct {
	ID          string `json:"id" gorm:"primaryKey;size:64"`
	Name        string `json:"name" gorm:"uniqueIndex;size:128"`
	Description string `json:"description" gorm:"type:text"`
	RepoURL     string `json:"repoUrl" gorm:"size:512"`
	Branch      string `json:"branch" gorm:"size:128"`
	LocalPath   string `json:"localPath" gorm:"size:512"`
	Language    string `json:"language" gorm:"size:32;default:go"`
	// Kind 是框架名，决定运行哪些框架专项检查
	// 它与 Language 正交，一个 Wails 项目的 Language 是 go，Kind 是 wails，
	// 分开才能避免枚举那些根本不可能存在的「框架-语言」组合，比如 rust-wails
	Kind string `json:"kind" gorm:"size:32"`
	// Color 是面板卡片用的十六进制强调色
	Color string `json:"color" gorm:"size:16"`

	// DefaultAnalyzers 固定该项目的分析器集合，为空表示用注册表的默认值
	DefaultAnalyzers MultiString `json:"defaultAnalyzers" gorm:"type:text"`

	LastAnalysisID string    `json:"lastAnalysisId" gorm:"size:64"`
	CreatedAt      time.Time `json:"createdAt"`
	UpdatedAt      time.Time `json:"updatedAt"`
}

// 框架。Kind 与 Language 正交，见字段处的说明
const (
	ProjectKindGo    = "go"
	ProjectKindWails = "wails"
)

// 语言。Language 决定运行哪些分析器，它是生态而不是框架
// go 相当于零值（字段默认就是它），所以已有数据行无需迁移即视为 Go 项目
const (
	LanguageGo         = "go"
	LanguageRust       = "rust"
	LanguagePython     = "python"
	LanguageJavaScript = "javascript"
	LanguageJava       = "java"
	LanguageCSharp     = "csharp"
	LanguageCpp        = "cpp"
	LanguagePHP        = "php"
)

// AllLanguages 列出支持的语言，最成熟的在前
var AllLanguages = []string{LanguageGo, LanguageRust, LanguagePython, LanguageJavaScript, LanguageJava, LanguageCSharp, LanguageCpp, LanguagePHP}

// ValidLanguage 报告 lang 是否为 Fizzra 能分析的语言
func ValidLanguage(lang string) bool {
	for _, l := range AllLanguages {
		if l == lang {
			return true
		}
	}
	return false
}

// KindIsValidFor 报告 kind 是否为 lang 下可识别的框架
// 空 kind 始终合法，大多数项目不使用任何框架专项检查
// go 对 Go 项目也当成「没有框架」接受，已有数据行和命令行调用都带着这个值
// wails 仅限 Go，因为 Wails 是 Go 的框架
func KindIsValidFor(lang, kind string) bool {
	if kind == "" || kind == lang {
		return true
	}
	return lang == LanguageGo && kind == ProjectKindWails
}

// Analysis 是对项目的一次扫描运行
type Analysis struct {
	ID        string `json:"id" gorm:"primaryKey;size:64"`
	ProjectID string `json:"projectId" gorm:"index;size:64"`
	// Status 取值之一：pending、running、success、failed、canceled
	Status string `json:"status" gorm:"index;size:24"`
	// Trigger 记录这次运行的触发方式：manual、cli、ci、schedule
	Trigger string `json:"trigger" gorm:"size:24"`
	// Branch 与 Commit 标出被扫描的确切版本
	Branch string `json:"branch" gorm:"size:128"`
	Commit string `json:"commit" gorm:"size:64"`

	Path string `json:"path" gorm:"size:512"`

	Analyzers StringSlice `json:"analyzers" gorm:"type:text"`

	// Progress 是任务执行器报告的 0 到 100
	Progress int `json:"progress"`

	StartedAt  time.Time  `json:"startedAt"`
	FinishedAt *time.Time `json:"finishedAt"`
	DurationMS int64      `json:"durationMs"`

	// Error 保存运行失败的原因
	Error string `json:"error" gorm:"type:text"`

	// 计数做了反规范化，面板因此不需要聚合查询
	TotalIssues    int `json:"totalIssues"`
	CriticalIssues int `json:"criticalIssues"`
	ErrorIssues    int `json:"errorIssues"`
	WarningIssues  int `json:"warningIssues"`
	InfoIssues     int `json:"infoIssues"`

	Score      float64 `json:"score"`
	GateStatus string  `json:"gateStatus" gorm:"size:16"`
	GateReason string  `json:"gateReason" gorm:"type:text"`

	// Incomplete 在一个或多个分析器未能跑完时为 true
	// 丢了半数分析器的扫描和一次干净的扫描不是一回事，两者都报 success 时，
	// 前者发现的少、得分反而更高，还能通过 CI 门禁。这个字段让那种状态显形
	Incomplete bool `json:"incomplete"`
	// FailedAnalyzers 按名字列出出错的分析器，供界面使用
	FailedAnalyzers StringSlice `json:"failedAnalyzers" gorm:"type:text"`
	// FailureReasons 解释每个分析器为什么失败，与 FailedAnalyzers 一一对应
	// 光有名字无法据以行动，「golangci failed」只让用户去猜
	// 取值来自分析器返回的原始错误，逐字展示，不翻译
	FailureReasons StringSlice `json:"failureReasons" gorm:"type:text"`

	// SkippedAnalyzers 按名字列出拒绝运行的分析器
	// 与 FailedAnalyzers 分开，因为两者要求读者做的事不同，失败是项目里要修的东西，
	// 跳过通常是装什么或配什么。并在一起会让一次只跑了一半的扫描看起来像跑全了
	// 部分跳过才是常态，每次扫描都记录
	SkippedAnalyzers StringSlice `json:"skippedAnalyzers" gorm:"type:text"`
	// SkipReasons 解释每个分析器为什么跳过，与 SkippedAnalyzers 一一对应且顺序一致
	// 原因才是可行动的部分，光有名字只说明这次检查没发生
	SkipReasons StringSlice `json:"skipReasons" gorm:"type:text"`
	// SkippedTools 列出跳过背后的缺失二进制，已去重
	// 多个分析器可能想要同一个工具，读者的下一步是装一次它，而不是数有几项检查想要它
	SkippedTools StringSlice `json:"skippedTools" gorm:"type:text"`

	// NoAnalyzerRan 记录所有选中的分析器都跳过了，也就是什么都没真正检查
	// 它与 Incomplete 不同，后者在部分分析器失败时也会置位，这决定用户看到的措辞，
	// 跳过意味着工具缺失或不适用，失败意味着有东西坏了
	NoAnalyzerRan bool `json:"noAnalyzerRan" gorm:"default:false"`

	// AnalyzersCompleted 记录成功跑完的分析器数量，含度量类
	//
	// NoAnalyzerRan 只说「没有东西检查代码」，这正是它比不分场合都否决更严格的地方。
	// 但 CI 的 --allow-skipped 需要区分「一个都没跑」和「跑了度量、只是缺工具的检查被
	// 跳过」：前者放行等于什么都验过还报通过，后者在有度量兜底时是可以接受的过渡
	AnalyzersCompleted int `json:"analyzersCompleted" gorm:"default:0"`

	// Metrics 是 JSON 编码的 model.Metrics 快照
	Metrics Metrics `json:"metrics" gorm:"type:text"`

	CreatedAt time.Time `json:"createdAt"`
}

// 分析状态取值
const (
	StatusPending  = "pending"
	StatusRunning  = "running"
	StatusSuccess  = "success"
	StatusFailed   = "failed"
	StatusCanceled = "canceled"
)

// 分析触发方式取值
const (
	TriggerManual = "manual"
	TriggerCLI    = "cli"
	TriggerCI     = "ci"
)

// 质量门禁结果
const (
	GatePassed  = "passed"
	GateFailed  = "failed"
	GateSkipped = "skipped"
)

// Rule 是用户自定义或内置的模式规则
type Rule struct {
	ID       string `json:"id" gorm:"primaryKey;size:64"`
	Name     string `json:"name" gorm:"uniqueIndex;size:128"`
	Analyzer string `json:"analyzer" gorm:"size:64;default:custom"`

	Severity Severity `json:"severity" gorm:"size:16"`
	Message  string   `json:"message" gorm:"type:text"`

	// Patterns 是对源码行做匹配的 Go 正则表达式
	Patterns MultiString `json:"patterns" gorm:"type:text"`
	// Paths 是限定该规则适用于哪些文件的 glob
	Paths MultiString `json:"paths" gorm:"type:text"`
	// Exclude 是让规则在匹配到的文件上失效的 glob
	Exclude MultiString `json:"exclude" gorm:"type:text"`

	// Advice 是在界面和命令行报告里展示的修复提示
	Advice string `json:"advice" gorm:"type:text"`

	// Fix 是修改前后对照示例，可选，改法完全取决于上下文的规则只带建议
	Fix FixExample `json:"fix" gorm:"type:text"`

	// RequireLoop 把规则限定在循环体内的匹配上。关于延迟执行或每轮迭代工作的
	// 规则在别处没有意义，而且只会制造噪音
	RequireLoop bool `json:"requireLoop"`
	// ForbidInLoop 让规则在循环体内失效。用于那些只在**未**批量处理时才是问题的
	// 模式
	ForbidInLoop bool `json:"forbidInLoop"`

	Enabled bool `json:"enabled" gorm:"default:true"`
	Builtin bool `json:"builtin"`

	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"`
}

// QualityGate 是持久化的通过与否策略
type QualityGate struct {
	ID string `json:"id" gorm:"primaryKey;size:64"`
	// 必须有 uniqueIndex：门禁是用 ON CONFLICT (project_id) 做 upsert 的
	ProjectID     string  `json:"projectId" gorm:"uniqueIndex;size:64"`
	MinScore      float64 `json:"minScore"`
	MaxCritical   int     `json:"maxCritical"`
	MaxHigh       int     `json:"maxHigh"`
	MaxComplexity int     `json:"maxComplexity"`
	MinCoverage   float64 `json:"minCoverage"`
	// FailOnWarning 让 warning 级问题也能让门禁不通过
	FailOnWarning bool      `json:"failOnWarning"`
	Enabled       bool      `json:"enabled" gorm:"default:true"`
	UpdatedAt     time.Time `json:"updatedAt"`
}
