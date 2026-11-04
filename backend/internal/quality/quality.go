// Package quality 把一组问题和指标转成评分与门禁结论
//
// 评分模型刻意保持简单可解释。给出无法解释的数字的审查工具会被直接忽略，所以每个
// 维度都会输出自己贡献的子分和扣分理由，界面也展示这份拆解
package quality

import (
	"fmt"
	"math"
	"strings"

	"github.com/rolingg/fizzra/backend/internal/model"
)

// Weights 控制每个维度对总分的贡献，它们的和必须为 1.0
type Weights struct {
	Security        float64
	Bugs            float64
	Maintainability float64
	Complexity      float64
	Coverage        float64
}

// DefaultWeights 是随发布提供的配比
// 正确性和安全占主导，覆盖率权重较小，因为它经常拿不到
func DefaultWeights() Weights {
	return Weights{
		Security:        0.30,
		Bugs:            0.30,
		Maintainability: 0.20,
		Complexity:      0.12,
		Coverage:        0.08,
	}
}

// Thresholds 控制各维度降到零的参考点
type Thresholds struct {
	// CriticalIssues 与 HighIssues 是安全和 bug 子分触底的计数
	CriticalIssues int
	HighIssues     int
	// MaxDuplicationPercent 是让可维护性归零的重复率
	MaxDuplicationPercent float64
	// MaxAvgComplexity 是让复杂度得分归零的平均复杂度
	MaxAvgComplexity float64
	// TargetCoverage 是能拿满分的覆盖率百分比
	TargetCoverage float64
}

// DefaultThresholds 返回对中等规模 Go 项目合理的参考点
func DefaultThresholds() Thresholds {
	return Thresholds{
		CriticalIssues:        5,
		HighIssues:            25,
		MaxDuplicationPercent: 25,
		MaxAvgComplexity:      15,
		TargetCoverage:        80,
	}
}

// Input 是给一次分析评分所需的全部输入
type Input struct {
	Issues  []model.Issue
	Metrics model.Metrics
	// Loc 是非测试代码总行数，用于归一化问题密度
	Loc int
}

// Dimension 是评分的一个组成部分
type Dimension struct {
	Name   string  `json:"name"`
	Score  float64 `json:"score"`
	Weight float64 `json:"weight"`
	// Detail 解释这个数字是怎么算出来的
	Detail string `json:"detail"`
}

// Result 是评分完成的这次分析
type Result struct {
	Score      float64     `json:"score"`
	Grade      string      `json:"grade"`
	Dimensions []Dimension `json:"dimensions"`
	// Summary 是一句人类可读的评分说明
	Summary string `json:"summary"`
}

// Grade 把分数映射成字母等级，便于快速扫读
// 六档 A–F，阈值与前端 api/format.ts 的 scoreGrade() 一致，改动时两边同步
func Grade(score float64) string {
	switch {
	case score >= 90:
		return "A"
	case score >= 80:
		return "B"
	case score >= 70:
		return "C"
	case score >= 60:
		return "D"
	case score >= 50:
		return "E"
	default:
		return "F"
	}
}

// Score 计算加权质量评分
func Score(in Input, w Weights, t Thresholds) Result {
	dims := []Dimension{
		securityDimension(in, t),
		bugDimension(in, t),
		maintainabilityDimension(in, t),
		complexityDimension(in, t),
		coverageDimension(in, t),
	}

	// 未测出的维度不占权重，其余维度按剩余权重重新归一
	//
	// 否则缺失的权重会直接变成减项，没测复杂度的项目会因为少了一项测量而受罚，
	// 而不是因为代码本身有问题，这与把未测维度当成零是同一类缺陷，只是方向相反。
	// 重新归一后，少几项测量的分数与五项齐全的分数仍然可比
	var weightSum float64
	for _, d := range dims {
		weightSum += d.Weight
	}

	var total float64
	if weightSum > 0 {
		for _, d := range dims {
			total += d.Score * (d.Weight / weightSum)
		}
	}
	total = math.Round(clamp(total, 0, 100)*10) / 10

	return Result{
		Score:      total,
		Grade:      Grade(total),
		Dimensions: dims,
		Summary:    summarize(total, dims, in),
	}
}

// securityDimension 对 critical 和 error 级发现评分
//
// 扣分由两项合成，让严重程度和数量都起作用
//
//   - 线性项，每个 critical 扣 0.10，让最初几条发现就有分量
//   - 对数项随数量继续增长但不超出预算，5 个 critical 与 500 个因此可以区分
//
// 纯饱和曲线会让两者同分，对排优先级没有用处，所以没有采用
func securityDimension(in Input, t Thresholds) Dimension {
	critical, high := 0, 0
	for _, i := range in.Issues {
		switch i.Severity {
		case model.SeverityCritical:
			critical++
		case model.SeverityError:
			high++
		}
	}

	criticalPenalty := 0.0
	if critical > 0 {
		criticalPenalty = 0.22*float64(critical) + 0.35*math.Log1p(float64(critical))/math.Log1p(50)
	}
	// error 级发现有分量，但比 critical 轻一个数量级
	highPenalty := 0.0
	if high > 0 {
		highPenalty = math.Min(0.45, 0.015*float64(high)+0.15*math.Log1p(float64(high))/math.Log1p(25))
	}

	score := clamp(100*(1-criticalPenalty-highPenalty), 0, 100)
	return Dimension{
		Name:   "Security",
		Score:  math.Round(score*10) / 10,
		Weight: DefaultWeights().Security,
		Detail: fmt.Sprintf("%d critical, %d high-severity findings", critical, high),
	}
}

// bugDimension 对正确性发现评分，涵盖 warning 及更严重的级别
//
// 只数 warning 会让有五十个 critical 的项目拿到满分的 bug 子分。critical 已在安全
// 维度重罚过，这里只让它贡献较小的权重，两个维度因此给出连贯的结论
func bugDimension(in Input, t Thresholds) Dimension {
	warnings, errors, critical := 0, 0, 0
	for _, i := range in.Issues {
		switch i.Severity {
		case model.SeverityWarning:
			warnings++
		case model.SeverityError:
			errors++
		case model.SeverityCritical:
			critical++
		}
	}

	// warning 密度按规模归一为每 1000 行的问题数，不足 1000 行时用绝对计数，
	// 避免小项目被放大。参考点给得宽松，每 1000 行 25 个已是相当嘈杂的代码库，
	// 而基于规则的 linter 总会报出些东西，按真实缺陷的力度扣分只会让人忽略工具
	density := float64(warnings)
	if in.Loc >= 1000 {
		density = float64(warnings) / float64(in.Loc) * 1000
	}
	densityPenalty := saturation(int(density), 25) * 0.45

	// 更严重的级别也计入 bug 风险，权重取安全维度的几分之一并设上限，
	// 无法单独把这一维度打到零
	severePenalty := math.Min(0.35, float64(critical)*0.02+float64(errors)*0.01)

	score := clamp(100*(1-densityPenalty-severePenalty), 0, 100)
	return Dimension{
		Name:   "Bug risk",
		Score:  math.Round(score*10) / 10,
		Weight: DefaultWeights().Bugs,
		Detail: fmt.Sprintf("%d warnings (%.1f per 1000 LOC), %d error, %d critical", warnings, density, errors, critical),
	}
}

// maintainabilityDimension 对重复率、注释比例和问题分布评分
func maintainabilityDimension(in Input, t Thresholds) Dimension {
	dup := in.Metrics.Duplication

	// 一点重复是正常的，得分在配置的天花板处线性降到零
	dupPenalty := 0.0
	if t.MaxDuplicationPercent > 0 {
		dupPenalty = clamp(dup/t.MaxDuplicationPercent, 0, 1)
	}

	// 奖励健康的注释比例，但不苛罚简洁的代码，
	// 没有注释但清晰的 API 和难以维护的 API 不是一回事
	commentRatio := 0.0
	if in.Metrics.CodeLines > 0 {
		commentRatio = float64(in.Metrics.CommentLines) / float64(in.Metrics.CodeLines)
	}
	commentPenalty := 0.0
	switch {
	case commentRatio < 0.03 && in.Metrics.CodeLines > 500:
		commentPenalty = 0.10
	case commentRatio < 0.08 && in.Metrics.CodeLines > 2000:
		commentPenalty = 0.05
	}

	score := clamp(100*(1-dupPenalty*0.75-commentPenalty), 0, 100)

	detail := fmt.Sprintf("%.1f%% duplicated", dup)
	if in.Metrics.CodeLines > 0 {
		detail += fmt.Sprintf(", %.0f%% comments", commentRatio*100)
	}
	return Dimension{
		Name:   "Maintainability",
		Score:  math.Round(score*10) / 10,
		Weight: DefaultWeights().Maintainability,
		Detail: detail,
	}
}

// complexityDimension 对平均和峰值圈复杂度评分
//
// 未测到复杂度的项目不能因此默认拿高分。没有 AST 遍历器时零是自然值，但它同时也是
// 最好的读数，当成零等于白送 100 分，信息更少的 Rust 项目会赢过同等的 Go 项目
//
// 是否测过从指标判断而不是从语言判断，所以确实没有函数的 Go 项目走同一条规则，
// 没数到函数就说明什么都没测，该维度如实说明而不是宣称完美
func complexityDimension(in Input, t Thresholds) Dimension {
	measured := in.Metrics.Functions > 0
	avg := in.Metrics.AvgComplexity
	worst := in.Metrics.MaxComplexity

	if !measured {
		return Dimension{
			Name:   "Complexity",
			Score:  0,
			Weight: 0, // 不参与加权总分，见 Score
			Detail: "not measured for this language",
		}
	}

	avgPenalty := 0.0
	if t.MaxAvgComplexity > 0 {
		avgPenalty = clamp(avg/t.MaxAvgComplexity, 0, 1)
	}

	// 最差的函数有分量但不该占主导，其余都干净时一个复杂的解析器 switch 可以接受
	worstPenalty := 0.0
	if worst > 20 {
		worstPenalty = clamp(float64(worst-20)/40, 0, 0.4)
	}

	score := clamp(100*(1-avgPenalty*0.7-worstPenalty), 0, 100)
	return Dimension{
		Name:   "Complexity",
		Score:  math.Round(score*10) / 10,
		Weight: DefaultWeights().Complexity,
		Detail: fmt.Sprintf("avg %.1f, worst %d", avg, worst),
	}
}

// coverageDimension 对测试覆盖率评分，没有覆盖率报告时回退到以有无测试来代理
func coverageDimension(in Input, t Thresholds) Dimension {
	cov := in.Metrics.Coverage
	if cov <= 0 {
		// 没有可用报告时以有无测试作代理，否则没有覆盖率工具的项目会和零测试的
		// 项目得到同样的分数
		if in.Metrics.TestFiles == 0 {
			return Dimension{
				Name:   "Coverage",
				Score:  30,
				Weight: DefaultWeights().Coverage,
				Detail: "no test files found",
			}
		}
		return Dimension{
			Name:   "Coverage",
			Score:  65,
			Weight: DefaultWeights().Coverage,
			Detail: fmt.Sprintf("%d test files (no coverage profile; run `go test -coverprofile=coverage.out ./...`)", in.Metrics.TestFiles),
		}
	}

	score := 100.0
	if t.TargetCoverage > 0 {
		score = clamp(cov/t.TargetCoverage*100, 0, 100)
	}
	return Dimension{
		Name:   "Coverage",
		Score:  math.Round(score*10) / 10,
		Weight: DefaultWeights().Coverage,
		Detail: fmt.Sprintf("%.1f%% statements", cov),
	}
}

// saturation 把计数映射到 0 到 1，在阈值处到达 1
//
// 曲线在低计数处刻意做得很陡，0 个与 1 个 critical 之间的差别远大于 9 个与 10 个
func saturation(count, threshold int) float64 {
	if threshold <= 0 {
		if count == 0 {
			return 0
		}
		return 1
	}
	ratio := float64(count) / float64(threshold)
	if ratio >= 1 {
		return 1
	}
	// 开方给出想要的先陡后平形状
	return math.Sqrt(ratio)
}

func clamp(v, lo, hi float64) float64 {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

// summarize 构造显示在评分旁边的那句说明
func summarize(score float64, dims []Dimension, in Input) string {
	// 点名最弱的维度，那才是可改进的部分
	weakest := dims[0]
	for _, d := range dims {
		if d.Score < weakest.Score {
			weakest = d
		}
	}

	var b strings.Builder
	fmt.Fprintf(&b, "Score %.1f (%s). ", score, Grade(score))
	if weakest.Score >= 85 {
		b.WriteString("All dimensions are healthy.")
		return b.String()
	}
	fmt.Fprintf(&b, "Weakest dimension: %s at %.0f/100 — %s.", weakest.Name, weakest.Score, weakest.Detail)
	return b.String()
}

// ---------------------------------------------------------------------------
// 质量门禁
// ---------------------------------------------------------------------------

// Gate 是通过与否的策略
type Gate struct {
	MinScore      float64
	MaxCritical   int
	MaxHigh       int
	MaxComplexity int
	MinCoverage   float64
	FailOnWarning bool
	Enabled       bool
}

// DefaultGate 从服务器配置推导出一个门禁
func DefaultGate() Gate {
	return Gate{
		MinScore:      70,
		MaxCritical:   0,
		MaxHigh:       0,
		MaxComplexity: 20,
		MinCoverage:   0,
		Enabled:       true,
	}
}

// GateResult 是对门禁求值的结果
type GateResult struct {
	Passed bool   `json:"passed"`
	Status string `json:"status"`
	Reason string `json:"reason"`
	// Violations 列出每一条未满足的条件，界面因此能全部展示而不只是第一条
	Violations []Violation `json:"violations"`
}

// Violation 是一条未满足的门禁条件
type Violation struct {
	Rule     string `json:"rule"`
	Expected string `json:"expected"`
	Actual   string `json:"actual"`
	Message  string `json:"message"`
}

// Evaluate 对照门禁检查一次分析
func Evaluate(g Gate, score Result, in Input) GateResult {
	if !g.Enabled {
		return GateResult{Passed: true, Status: model.GateSkipped, Reason: "quality gate is disabled"}
	}

	var violations []Violation

	if score.Score < g.MinScore {
		violations = append(violations, Violation{
			Rule:     "minScore",
			Expected: fmt.Sprintf(">= %.1f", g.MinScore),
			Actual:   fmt.Sprintf("%.1f", score.Score),
			Message:  fmt.Sprintf("quality score %.1f is below the required %.1f", score.Score, g.MinScore),
		})
	}

	critical, high, warnings := 0, 0, 0
	for _, i := range in.Issues {
		switch i.Severity {
		case model.SeverityCritical:
			critical++
		case model.SeverityError:
			high++
		case model.SeverityWarning:
			warnings++
		}
	}

	if critical > g.MaxCritical {
		violations = append(violations, Violation{
			Rule:     "maxCritical",
			Expected: fmt.Sprintf("<= %d", g.MaxCritical),
			Actual:   fmt.Sprintf("%d", critical),
			Message:  fmt.Sprintf("%d critical issue(s) found; the gate allows %d", critical, g.MaxCritical),
		})
	}
	if high > g.MaxHigh {
		violations = append(violations, Violation{
			Rule:     "maxHigh",
			Expected: fmt.Sprintf("<= %d", g.MaxHigh),
			Actual:   fmt.Sprintf("%d", high),
			Message:  fmt.Sprintf("%d high-severity issue(s) found; the gate allows %d", high, g.MaxHigh),
		})
	}
	if g.MaxComplexity > 0 && in.Metrics.MaxComplexity > g.MaxComplexity {
		violations = append(violations, Violation{
			Rule:     "maxComplexity",
			Expected: fmt.Sprintf("<= %d", g.MaxComplexity),
			Actual:   fmt.Sprintf("%d", in.Metrics.MaxComplexity),
			Message:  fmt.Sprintf("peak cyclomatic complexity %d exceeds the limit of %d", in.Metrics.MaxComplexity, g.MaxComplexity),
		})
	}
	if g.MinCoverage > 0 && in.Metrics.Coverage > 0 && in.Metrics.Coverage < g.MinCoverage {
		violations = append(violations, Violation{
			Rule:     "minCoverage",
			Expected: fmt.Sprintf(">= %.1f%%", g.MinCoverage),
			Actual:   fmt.Sprintf("%.1f%%", in.Metrics.Coverage),
			Message:  fmt.Sprintf("coverage %.1f%% is below the required %.1f%%", in.Metrics.Coverage, g.MinCoverage),
		})
	}
	if g.FailOnWarning && warnings > 0 {
		violations = append(violations, Violation{
			Rule:     "failOnWarning",
			Expected: "0 warnings",
			Actual:   fmt.Sprintf("%d", warnings),
			Message:  fmt.Sprintf("%d warning(s) found and the gate is configured to fail on warnings", warnings),
		})
	}

	if len(violations) == 0 {
		return GateResult{
			Passed:     true,
			Status:     model.GatePassed,
			Reason:     fmt.Sprintf("all conditions met (score %.1f)", score.Score),
			Violations: []Violation{},
		}
	}

	reasons := make([]string, 0, len(violations))
	for _, v := range violations {
		reasons = append(reasons, v.Message)
	}
	return GateResult{
		Passed:     false,
		Status:     model.GateFailed,
		Reason:     strings.Join(reasons, "; "),
		Violations: violations,
	}
}
