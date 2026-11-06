// Package report 把分析结果渲染成 JSON 和独立的 HTML
// HTML 报告是单个自包含文件，没有外部资源也没有 CDN 依赖，
// 可以作为构建产物提交，也能在无网络的机器上从 file:// 打开
package report

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"html/template"
	"io"
	"sort"
	"strings"
	"time"

	"github.com/rolingg/fizzra/backend/internal/model"
)

// reportTemplate 用嵌入而不是从磁盘读取，二进制因此保持自包含
//
//go:embed template.html
var reportTemplate string

// Report 打包一份渲染后的报告所需的全部内容
type Report struct {
	Project  model.Project  `json:"project"`
	Analysis model.Analysis `json:"analysis"`
	Issues   []model.Issue  `json:"issues"`
	Stats    Stats          `json:"stats"`
	// Trend 是评分历史，最早的在前
	Trend []TrendPoint `json:"trend"`
	// GeneratedAt 在报告构建时写入
	GeneratedAt time.Time `json:"generatedAt"`
	// Version 记录生成该报告的 Fizzra 构建版本
	Version string `json:"version"`
}

// TrendPoint 是历史图上的一个评分采样点
type TrendPoint struct {
	At         string  `json:"at"`
	Score      float64 `json:"score"`
	Total      int     `json:"total"`
	GateStatus string  `json:"gateStatus"`
}

// Stats 是问题汇总
type Stats struct {
	Total      int            `json:"total"`
	BySeverity map[string]int `json:"bySeverity"`
	ByAnalyzer map[string]int `json:"byAnalyzer"`
	ByFile     []FileStat     `json:"byFile"`
	TopRules   []RuleStat     `json:"topRules"`
}

// FileStat 是按文件统计的问题数
type FileStat struct {
	File  string `json:"file"`
	Count int    `json:"count"`
	// Critical 与 MaxSeverity 决定 HTML 报告里的热力着色
	Critical int `json:"critical"`
	Errors   int `json:"errors"`
}

// RuleStat 是按规则统计的问题数
type RuleStat struct {
	Rule     string `json:"rule"`
	Analyzer string `json:"analyzer"`
	Severity string `json:"severity"`
	Count    int    `json:"count"`
}

// BuildStats 计算报告所需的各项汇总
func BuildStats(issues []model.Issue) Stats {
	stats := Stats{
		Total:      len(issues),
		BySeverity: map[string]int{},
		ByAnalyzer: map[string]int{},
	}

	for _, sev := range model.AllSeverities {
		stats.BySeverity[string(sev)] = 0
	}

	perFile := map[string]*FileStat{}
	perRule := map[string]*RuleStat{}

	for _, issue := range issues {
		stats.BySeverity[string(issue.Severity)]++
		stats.ByAnalyzer[issue.Analyzer]++

		if issue.File != "" {
			fs, ok := perFile[issue.File]
			if !ok {
				fs = &FileStat{File: issue.File}
				perFile[issue.File] = fs
			}
			fs.Count++
			switch issue.Severity {
			case model.SeverityCritical:
				fs.Critical++
			case model.SeverityError:
				fs.Errors++
			}
		}

		key := issue.Analyzer + "\x00" + issue.Rule
		rs, ok := perRule[key]
		if !ok {
			rs = &RuleStat{Rule: issue.Rule, Analyzer: issue.Analyzer, Severity: string(issue.Severity)}
			perRule[key] = rs
		}
		rs.Count++
		if model.ParseSeverity(string(issue.Severity)).Rank() < model.ParseSeverity(rs.Severity).Rank() {
			rs.Severity = string(issue.Severity)
		}
	}

	for _, fs := range perFile {
		stats.ByFile = append(stats.ByFile, *fs)
	}
	sort.Slice(stats.ByFile, func(i, j int) bool {
		if stats.ByFile[i].Count != stats.ByFile[j].Count {
			return stats.ByFile[i].Count > stats.ByFile[j].Count
		}
		return stats.ByFile[i].File < stats.ByFile[j].File
	})
	if len(stats.ByFile) > 25 {
		stats.ByFile = stats.ByFile[:25]
	}

	for _, rs := range perRule {
		stats.TopRules = append(stats.TopRules, *rs)
	}
	sort.Slice(stats.TopRules, func(i, j int) bool {
		return stats.TopRules[i].Count > stats.TopRules[j].Count
	})
	if len(stats.TopRules) > 20 {
		stats.TopRules = stats.TopRules[:20]
	}

	return stats
}

// ---------------------------------------------------------------------------
// JSON
// ---------------------------------------------------------------------------

// WriteJSON 把报告渲染成带缩进的 JSON
func WriteJSON(w io.Writer, r Report) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false)
	return enc.Encode(r)
}

// ---------------------------------------------------------------------------
// SARIF
// ---------------------------------------------------------------------------

// sarifLog 是 GitHub 代码扫描接受的最小 SARIF 2.1.0 结构
type sarifLog struct {
	Schema  string     `json:"$schema"`
	Version string     `json:"version"`
	Runs    []sarifRun `json:"runs"`
}

type sarifRun struct {
	Tool    sarifTool     `json:"tool"`
	Results []sarifResult `json:"results"`
	// Invocations 记录产生该结果的那个工具的退出状态
	Invocations []sarifInvocation `json:"invocations,omitempty"`
}

type sarifTool struct {
	Driver sarifDriver `json:"driver"`
}

type sarifDriver struct {
	Name           string      `json:"name"`
	InformationURI string      `json:"informationUri"`
	Version        string      `json:"version"`
	Rules          []sarifRule `json:"rules"`
}

type sarifRule struct {
	ID               string    `json:"id"`
	Name             string    `json:"name"`
	ShortDescription sarifText `json:"shortDescription"`
	// FullDescription 是 GitHub 标为必需的字段，缺了规则的详情页会空一块
	FullDescription sarifText     `json:"fullDescription"`
	Help            *sarifText    `json:"help,omitempty"`
	DefaultConfig   sarifRuleConf `json:"defaultConfiguration"`
	// Properties 里的 tags 让 GitHub 能按类别筛结果
	Properties sarifRuleProps `json:"properties"`
}

type sarifRuleProps struct {
	Tags []string `json:"tags,omitempty"`
}

type sarifRuleConf struct {
	Level string `json:"level"`
}

type sarifText struct {
	Text string `json:"text"`
}

type sarifResult struct {
	RuleID    string          `json:"ruleId"`
	Level     string          `json:"level"`
	Message   sarifText       `json:"message"`
	Locations []sarifLocation `json:"locations,omitempty"`
	// PartialFingerprints 让 GitHub 跨提交认出同一个问题
	//
	// GitHub 只读 primaryLocationLineHash。缺了它 upload-sarif 会自己补，补出来的值
	// 依赖仓库里同时有源码。补不上时同一个问题每次扫描都变成新告警，旧的自动关闭、新的重新出现
	PartialFingerprints map[string]string `json:"partialFingerprints,omitempty"`
}

type sarifLocation struct {
	PhysicalLocation sarifPhysical `json:"physicalLocation"`
}

type sarifPhysical struct {
	ArtifactLocation sarifArtifact `json:"artifactLocation"`
	Region           *sarifRegion  `json:"region,omitempty"`
}

type sarifArtifact struct {
	URI string `json:"uri"`
}

type sarifRegion struct {
	StartLine   int `json:"startLine"`
	StartColumn int `json:"startColumn,omitempty"`
}

type sarifInvocation struct {
	ExecutionSuccessful bool `json:"executionSuccessful"`
}

// WriteSARIF 以 SARIF 2.1.0 渲染报告，供 GitHub / GitLab 代码扫描使用
//
// SARIF 让发现以行内标注出现在拉取请求的差异里，CI 接入的价值因此超出一个
// 通过与否的比特位
func WriteSARIF(w io.Writer, r Report) error {
	ruleIndex := map[string]int{}
	var rules []sarifRule

	for _, issue := range r.Issues {
		key := issue.Analyzer + ":" + issue.Rule
		if _, ok := ruleIndex[key]; ok {
			continue
		}
		ruleIndex[key] = len(rules)
		rules = append(rules, sarifRule{
			ID:   key,
			Name: issue.Rule,
			// shortDescription 和 fullDescription 在 GitHub 那边都是必需字段
			// 前者用规则自身的描述，后者用 advice，那是发现里最接近完整说明的一段
			ShortDescription: sarifText{Text: firstNonEmpty(issue.Message, issue.Rule)},
			FullDescription:  sarifText{Text: firstNonEmpty(issue.Advice, issue.Message, issue.Rule)},
			Help:             helpFor(issue),
			DefaultConfig:    sarifRuleConf{Level: sarifLevel(issue.Severity)},
			Properties:       sarifRuleProps{Tags: tagsFor(issue)},
		})
	}

	results := make([]sarifResult, 0, len(r.Issues))
	for _, issue := range r.Issues {
		result := sarifResult{
			RuleID:  issue.Analyzer + ":" + issue.Rule,
			Level:   sarifLevel(issue.Severity),
			Message: sarifText{Text: issue.Message},
		}
		// 适配 GitHub 存下来的指纹跨扫描稳定，也不含行号
		// 即便发现往下挪了几行仍然是同一个，也不会变成新告警
		if issue.Fingerprint != "" {
			result.PartialFingerprints = map[string]string{
				"primaryLocationLineHash": issue.Fingerprint,
			}
		}
		if issue.File != "" {
			region := &sarifRegion{StartLine: max(issue.Line, 1)}
			if issue.Column > 0 {
				region.StartColumn = issue.Column
			}
			result.Locations = []sarifLocation{{
				PhysicalLocation: sarifPhysical{
					ArtifactLocation: sarifArtifact{URI: issue.File},
					Region:           region,
				},
			}}
		}
		results = append(results, result)
	}

	log := sarifLog{
		Schema:  "https://json.schemastore.org/sarif-2.1.0.json",
		Version: "2.1.0",
		Runs: []sarifRun{{
			Tool: sarifTool{Driver: sarifDriver{
				Name:           "Fizzra",
				InformationURI: "https://github.com/RoLingG/fizzra",
				Version:        r.Version,
				Rules:          rules,
			}},
			Results:     results,
			Invocations: []sarifInvocation{{ExecutionSuccessful: r.Analysis.GateStatus != model.GateFailed}},
		}},
	}

	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false)
	return enc.Encode(log)
}

// tagsFor 给出 GitHub 用来筛选结果的标签
// 取值沿用 CodeQL 的惯例，用户从它迁过来时筛选习惯不用改，critical 额外标 security
// 是因为 GitHub 的安全告警视图按这个标签归类
func tagsFor(issue model.Issue) []string {
	tags := []string{"maintainability"}
	switch {
	case issue.Severity == model.SeverityCritical:
		tags = []string{"security", "correctness"}
	case issue.Severity == model.SeverityError:
		tags = []string{"correctness"}
	}
	return tags
}

func helpFor(issue model.Issue) *sarifText {
	if issue.Advice == "" && issue.DocURL == "" {
		return nil
	}
	text := issue.Advice
	if issue.DocURL != "" {
		if text != "" {
			text += "\n\n"
		}
		text += "Documentation: " + issue.DocURL
	}
	return &sarifText{Text: text}
}

// sarifLevel 把本项目的严重级别映射到 SARIF 的四档
func sarifLevel(sev model.Severity) string {
	switch sev {
	case model.SeverityCritical, model.SeverityError:
		return "error"
	case model.SeverityWarning:
		return "warning"
	default:
		return "note"
	}
}

// ---------------------------------------------------------------------------
// HTML
// ---------------------------------------------------------------------------

// WriteHTML 渲染一份自包含的 HTML 报告
//
// lang 决定报告框架自身的文案，章节名和表头。分析器的产出，问题描述、规则名、
// 修复建议，一律不翻译，那是工具的输出而不是本项目的文案
func WriteHTML(w io.Writer, r Report, lang string) error {
	s := stringsFor(lang)

	funcs := templateFuncs
	// severityLabel 依赖语言，所以叠在共用 FuncMap 之外，不把语言塞进共用表，
	// JSON 和 SARIF 用的是原始取值，不该受它影响
	localized := template.FuncMap{
		"severityLabel": func(sev model.Severity) string { return s.severityWord(sev) },
		// 聚合出来的 RuleStat.Severity 是普通字符串，模板里同样要翻
		"severityWord": s.wordFor,
	}
	for k, v := range localized {
		funcs[k] = v
	}

	tmpl, err := template.New("report").Funcs(funcs).Parse(reportTemplate)
	if err != nil {
		return fmt.Errorf("parse report template: %w", err)
	}
	return tmpl.Execute(w, struct {
		Report
		S Strings
	}{r, s})
}

var templateFuncs = template.FuncMap{
	"severityClass": func(s model.Severity) string {
		return "sev-" + strings.ToLower(string(s))
	},
	"severityLabel": func(s model.Severity) string {
		return strings.ToUpper(string(s))
	},
	"scoreClass": func(score float64) string {
		switch {
		case score >= 85:
			return "good"
		case score >= 70:
			return "ok"
		case score >= 50:
			return "warn"
		default:
			return "bad"
		}
	},
	"formatTime": func(t time.Time) string {
		if t.IsZero() {
			return "—"
		}
		return t.Format("2006-01-02 15:04:05")
	},
	"formatDuration": func(ms int64) string {
		d := time.Duration(ms) * time.Millisecond
		if d < time.Second {
			return d.Round(time.Millisecond).String()
		}
		return d.Round(time.Second).String()
	},
	// replace 供模板里做占位替换用，「显示 N / M 条」这句话的计数由脚本填
	"replace": func(s, old, new string) string {
		return strings.ReplaceAll(s, old, new)
	},
	"pct": func(part, total int) float64 {
		if total == 0 {
			return 0
		}
		return float64(part) / float64(total) * 100
	},
	// scorePath 构造迷你折线图的折线点
	"scorePath": func(points []TrendPoint) string {
		if len(points) < 2 {
			return ""
		}
		const w, h = 600.0, 80.0
		var b strings.Builder
		for i, p := range points {
			x := float64(i) / float64(len(points)-1) * w
			// 0 分对应框的底部，100 分对应顶部
			y := h - (p.Score/100)*h
			if i > 0 {
				b.WriteByte(' ')
			}
			fmt.Fprintf(&b, "%.1f,%.1f", x, y)
		}
		return b.String()
	},
	"truncate": func(s string, n int) string {
		if len(s) <= n {
			return s
		}
		return s[:n] + "…"
	},
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}
