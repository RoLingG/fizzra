package model

import (
	"fmt"
	"strings"
	"time"
)

// Severity 是所有分析器共用的统一问题等级
// 外部工具用词各异，由各分析器负责映射到这四个等级，
// 规则引擎、评分和门禁因此只需处理一套刻度
type Severity string

const (
	SeverityCritical Severity = "critical"
	SeverityError    Severity = "error"
	SeverityWarning  Severity = "warning"
	SeverityInfo     Severity = "info"
)

// AllSeverities 是规范顺序，最严重的在前
var AllSeverities = []Severity{SeverityCritical, SeverityError, SeverityWarning, SeverityInfo}

// Valid 报告 s 是否为已知等级
func (s Severity) Valid() bool {
	switch s {
	case SeverityCritical, SeverityError, SeverityWarning, SeverityInfo:
		return true
	}
	return false
}

// Rank 返回可排序的权重，critical 为 0，info 为 3，未知取 4
func (s Severity) Rank() int {
	for i, v := range AllSeverities {
		if v == s {
			return i
		}
	}
	return len(AllSeverities)
}

// ParseSeverity 把任意工具输出归一成 Severity，未知取值回退到 warning 而不是丢弃
// 这种宽容适用于工具输出，每个 linter 都自造一套用词
// 用户输入要用 ParseSeverityStrict，`--fail-on errors`（带 s）会被静默变成按警告失败，
// 存成 `severity: banana` 的规则也会悄悄变成 warning
func ParseSeverity(raw string) Severity {
	switch normalizeSeverity(raw) {
	case "critical", "fatal", "blocker":
		return SeverityCritical
	case "error", "high", "err":
		return SeverityError
	case "warning", "warn", "medium", "moderate":
		return SeverityWarning
	case "info", "note", "low", "hint", "style", "minor":
		return SeverityInfo
	}
	return SeverityWarning
}

// ParseSeverityStrict 解析用户提供的等级，其余一律报错，让拼写错误立刻暴露
// 静默归成 warning 会随使用场景朝相反方向出错，用在 --fail-on 上收紧门禁，
// 用在规则定义里则放松规则
func ParseSeverityStrict(raw string) (Severity, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return "", fmt.Errorf("severity is required")
	}

	s := normalizeSeverity(trimmed)
	// 接受带尾部的 s，让 `--fail-on errors` 按原样可用。
	// 把等级名写成复数是很自然的笔误，而且没有歧义，不像自造的单词
	s = strings.TrimSuffix(s, "s")

	switch s {
	case "critical", "fatal", "blocker", "crit":
		return SeverityCritical, nil
	case "error", "err", "high":
		return SeverityError, nil
	case "warning", "warn", "medium", "moderate":
		return SeverityWarning, nil
	case "info", "note", "low", "hint", "style", "minor":
		return SeverityInfo, nil
	}

	return "", fmt.Errorf("unknown severity %q (valid: critical, error, warning, info)", trimmed)
}

func normalizeSeverity(raw string) string {
	return strings.ToLower(raw)
}

// Issue 是每个分析器都必须产出的唯一输出格式
type Issue struct {
	ID       string   `json:"id" gorm:"primaryKey;size:64"`
	Analysis string   `json:"analysisId" gorm:"index;size:64"`
	Project  string   `json:"projectId" gorm:"index;size:64"`
	Analyzer string   `json:"analyzer" gorm:"index;size:64"`
	Rule     string   `json:"rule" gorm:"index;size:128"`
	Severity Severity `json:"severity" gorm:"index;size:16"`
	Message  string   `json:"message" gorm:"type:text"`
	// Advice 是可选的修复提示，来自工具或匹配到的自定义规则
	Advice string `json:"advice" gorm:"type:text"`
	// Fix 是修改前后对照示例，从产生该问题时的规则复制而来
	// 存在这里而不是关联到规则上，历史发现因此保留它当时被报出来时的指导，
	// 即便规则之后被改过或删掉
	Fix    FixExample `json:"fix" gorm:"type:text"`
	File   string     `json:"file" gorm:"size:512"`
	Line   int        `json:"line"`
	Column int        `json:"column"`
	// Code 是出问题的源码片段，分析器能提供时才有
	Code string `json:"code" gorm:"type:text"`
	// DocURL 在已知时指向上游规则文档
	DocURL string `json:"docUrl" gorm:"size:512"`
	// Fingerprint 是跨分析匹配问题时用的稳定哈希，界面因此能区分新发现和已有的
	Fingerprint string `json:"fingerprint" gorm:"index;size:64"`
	// IsNew 表示该项目最近的成功分析里都没有同一指纹
	// 是「最近」而不是「上一次」，基线覆盖项目近期的分析历史，所以一个修好后又复现的问题
	// 会被告知为已知而不是新增。落库是为了让「只看新增」成为一次带索引的比较
	IsNew bool `json:"isNew" gorm:"index"`
	// Status 默认为 open，用户可以把问题标为已忽略或误报
	Status string `json:"status" gorm:"index;size:24;default:open"`

	// Origin 说明问题在哪，项目自己的代码里，还是它没写过的依赖里
	// 两者可采取的行动不同，源码里的缺陷该由作者修，传递依赖的通告通常只能靠升级引入
	// 它的那个东西消化。一视同仁会让一长串 CVE 通告看起来像读者做错的事
	// 为空表示项目自己的代码
	Origin string `json:"origin" gorm:"index;size:24"`
	// DependencyPath 给出引入第三方发现的那条依赖链，最外层在前
	// 对 OriginSource 和直接依赖里的发现都为空，此时链路就是那个包本身
	DependencyPath MultiString `json:"dependencyPath" gorm:"type:text"`

	CreatedAt time.Time `json:"createdAt"`
}

// 问题来源取值
const (
	// OriginSource 是项目自己代码里的发现，即分析器读的是用户写的文件，这是默认值
	OriginSource = ""
	// OriginDirect 是项目自己声明的依赖，读者改动自己的清单文件就能处理
	OriginDirect = "direct"
	// OriginTransitive 是只能经由另一个依赖到达的依赖
	// 读者无法直接修好它，解决办法在上游维护者手里
	OriginTransitive = "transitive"
)

// 问题状态取值
const (
	IssueStatusOpen          = "open"
	IssueStatusIgnored       = "ignored"
	IssueStatusFalsePositive = "false_positive"
)

// Normalized 返回一份副本，把等级和状态收敛到合法取值范围内
func (i Issue) Normalized() Issue {
	if !i.Severity.Valid() {
		i.Severity = SeverityWarning
	}
	if i.Status == "" {
		i.Status = IssueStatusOpen
	}
	if i.Line < 0 {
		i.Line = 0
	}
	if i.Column < 0 {
		i.Column = 0
	}
	return i
}
