package analyzer

import (
	"cmp"
	"maps"
	"slices"
	"strconv"
	"strings"
	"sync"

	"github.com/rolingg/fizzra/backend/internal/model"
)

// DefaultRegistry 构建含全部内置分析器的注册表
//
// 显式注册而不是用 init()，依赖关系集中在一处可见，测试也能只取一部分构建
// 注册表
func DefaultRegistry() *Registry {
	r := NewRegistry()
	r.Register(func() Analyzer { return NewCustomAnalyzer() })
	r.Register(func() Analyzer { return NewWailsAnalyzer() })
	r.Register(func() Analyzer { return NewMetricsAnalyzer() })
	r.Register(func() Analyzer { return NewGofmtAnalyzer() })
	r.Register(func() Analyzer { return NewGoVetAnalyzer() })
	r.Register(func() Analyzer { return NewGolangciAnalyzer() })
	r.Register(func() Analyzer { return NewStaticcheckAnalyzer() })
	r.Register(func() Analyzer { return NewGosecAnalyzer() })
	r.Register(func() Analyzer { return NewVulnerabilityAnalyzer() })
	r.Register(func() Analyzer { return NewRuffAnalyzer() })
	r.Register(func() Analyzer { return NewOxlintAnalyzer() })
	r.Register(func() Analyzer { return NewTscAnalyzer() })
	r.Register(func() Analyzer { return NewBiomeAnalyzer() })
	r.Register(func() Analyzer { return NewClippyAnalyzer() })
	r.Register(func() Analyzer { return NewRustfmtAnalyzer() })
	r.Register(func() Analyzer { return NewCargoAuditAnalyzer() })
	r.Register(func() Analyzer { return NewPmdAnalyzer() })
	r.Register(func() Analyzer { return NewSpotBugsAnalyzer() })
	r.Register(func() Analyzer { return NewRoslynatorAnalyzer() })
	r.Register(func() Analyzer { return NewCppcheckAnalyzer() })
	r.Register(func() Analyzer { return NewClangTidyAnalyzer() })
	r.Register(func() Analyzer { return NewPhpstanAnalyzer() })
	// 注册顺序不保留；每个分析器通过 Requires() 各自声明依赖
	return r
}

// Availability 探测装了哪些外部工具
//
// 界面用它解释某个分析器为什么没有结果，而不是让用户自己去猜是工具没装还是代码干净
type Availability struct {
	mu     sync.RWMutex
	runner Runner
	tools  map[string]ToolStatus
}

// ToolStatus 描述一个外部二进制
type ToolStatus struct {
	Name      string `json:"name"`
	Available bool   `json:"available"`
	Path      string `json:"path,omitempty"`
	Error     string `json:"error,omitempty"`
}

// NewAvailability 构建绑定到某个 runner 的探测器
func NewAvailability(runner Runner) *Availability {
	return &Availability{runner: runner, tools: map[string]ToolStatus{}}
}

// Probe 检查所有已注册分析器声明的每一个工具
func (a *Availability) Probe(registry *Registry) map[string]ToolStatus {
	a.mu.Lock()
	defer a.mu.Unlock()

	// 能通过模块运行器调到的分析器会自报命令，这样探测反映的是扫描实际能做到
	// 什么，而不只是 PATH 上直接有什么
	seen := map[string]bool{}
	for _, factory := range registry.factoriesSnapshot() {
		an := factory()
		if p, ok := an.(Probeable); ok {
			for _, cmd := range p.ProbeCommands() {
				if len(cmd) == 0 || seen[cmd[0]] {
					continue
				}
				seen[cmd[0]] = true
				a.tools[cmd[0]] = a.probeOne(cmd[0])
			}
		}
	}

	for _, desc := range registry.Describe() {
		for _, tool := range desc.Requires {
			if seen[tool] {
				continue
			}
			seen[tool] = true
			a.tools[tool] = a.probeOne(tool)
		}
	}
	return a.snapshot()
}

// factoriesSnapshot 返回已注册的构造函数，在锁内复制一份，这样探测器构建分析器时不必持锁
func (r *Registry) factoriesSnapshot() []Factory {
	r.mu.RLock()
	defer r.mu.RUnlock()

	out := make([]Factory, 0, len(r.order))
	for _, n := range r.order {
		out = append(out, r.factories[n])
	}
	return out
}

func (a *Availability) probeOne(tool string) ToolStatus {
	path, err := a.runner.LookPath(tool)
	if err != nil {
		return ToolStatus{Name: tool, Available: false, Error: err.Error()}
	}
	return ToolStatus{Name: tool, Available: true, Path: path}
}

// Snapshot 返回缓存的探测结果
func (a *Availability) Snapshot() map[string]ToolStatus {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.snapshot()
}

func (a *Availability) snapshot() map[string]ToolStatus {
	out := make(map[string]ToolStatus, len(a.tools))
	maps.Copy(out, a.tools)
	return out
}

// probeAlternatives 返回分析器声明的备选命令，用默认的按名探测时返回 nil
func (r *Registry) probeAlternatives(name string) [][]string {
	r.mu.RLock()
	factory, ok := r.factories[name]
	r.mu.RUnlock()
	if !ok {
		return nil
	}
	if p, ok := factory().(Probeable); ok {
		return p.ProbeCommands()
	}
	return nil
}

// byName 返回以某名字注册的分析器，没有则返回 nil
//
// Readiness 用它去够那些不属于 Descriptor 的可选接口（Installable），Descriptor
// 是个纯数据结构，表达不了接口。返回 nil 是安全的，每个可选接口的辅助函数都
// 容忍它
func (r *Registry) byName(name string) Analyzer {
	r.mu.RLock()
	factory, ok := r.factories[name]
	r.mu.RUnlock()
	if !ok {
		return nil
	}
	return factory()
}

// anyProbeAvailable 报告是否至少有一个备选能解析
func anyProbeAvailable(alternatives [][]string, statuses map[string]ToolStatus) bool {
	for _, cmd := range alternatives {
		if len(cmd) == 0 {
			continue
		}
		if st, ok := statuses[cmd[0]]; ok && st.Available {
			return true
		}
	}
	return false
}

// allUnprobed 报告是否每个备选都从未被探测过，那是接线错误而不是工具缺失
func allUnprobed(alternatives [][]string, statuses map[string]ToolStatus) bool {
	for _, cmd := range alternatives {
		if len(cmd) == 0 {
			continue
		}
		if _, ok := statuses[cmd[0]]; ok {
			return false
		}
	}
	return true
}

// missingLabel 给出缺失项的名字，优先用用户会去搜的工具名，而不是碰巧运行它的
// 解释器
func missingLabel(desc Descriptor, alternatives [][]string, statuses map[string]ToolStatus) string {
	if len(desc.Requires) > 0 {
		return strings.Join(desc.Requires, ", ")
	}
	for _, cmd := range alternatives {
		if len(cmd) > 0 {
			return cmd[0]
		}
	}
	return desc.Name
}

// AnalyzerReadiness 描述某个分析器当前能不能跑
type AnalyzerReadiness struct {
	Descriptor
	// 所需工具缺失时 Ready 为 false
	Ready bool `json:"ready"`
	// MissingTools 列出不可用的依赖项
	MissingTools []string `json:"missingTools"`
	// ProbeMissing 记录可用性从未被检查过，那是接线错误而不是工具缺失。界面
	// 会区别显示
	ProbeMissing bool `json:"probeMissing,omitempty"`
	// InstallHint 是缺失工具的安装方法，分析器知道的话
	//
	// 与 MissingTools 放在一起，调用方因此不必从工具名反推命令，正是那样做的
	// 结果，让命令行给一个 npm 包建议了 go install oxlint@latest
	InstallHint string `json:"installHint,omitempty"`
	// Languages 列出该分析器适用的语言，空表示与语言无关
	Languages []string `json:"languages"`
}

// Readiness 把注册表的描述信息与可用性探测结果合到一起
//
// 没有探测结果的工具报告为不可用而不是可用。默认当成「就绪」时，探测缓存初始
// 为空，忘了先调 Probe 的调用点会让界面和 `fizzra analyzers` 声称所有分析器
// 就绪，而扫描静默跳过了其中四个。把「未知」报成「缺失」反而能让这个疏漏显形
//
// 声明了备选探测命令的分析器，只要任一备选能解析就算就绪，工具可能是作为
// 语言模块装的而不是独立二进制，报它缺失会与实际扫描行为相矛盾
func Readiness(registry *Registry, avail *Availability) []AnalyzerReadiness {
	statuses := avail.Snapshot()
	out := make([]AnalyzerReadiness, 0)

	for _, desc := range registry.Describe() {
		item := AnalyzerReadiness{Descriptor: desc, Ready: true, MissingTools: []string{}}

		// 界面据此只列出适用于当前语言的分析器
		if an := registry.byName(desc.Name); an != nil {
			item.Languages = AnalyzerLanguages(an)
			if item.Languages == nil {
				item.Languages = []string{}
			}
		}

		// 一旦确定分析器不可用就置位，末尾针对未探测的情况再置一次，因为从用户
		// 角度看那样同样是不可用，尽管并没有发现什么东西缺失
		hint := analyzerInstallHint(registry.byName(desc.Name))

		if alternatives := registry.probeAlternatives(desc.Name); len(alternatives) > 0 {
			if !anyProbeAvailable(alternatives, statuses) {
				item.Ready = false
				item.MissingTools = append(item.MissingTools, missingLabel(desc, alternatives, statuses))
				item.ProbeMissing = allUnprobed(alternatives, statuses)
				item.InstallHint = hint
			}
			out = append(out, item)
			continue
		}

		for _, tool := range desc.Requires {
			st, probed := statuses[tool]
			switch {
			case !probed:
				// 从未探测过：如实说明而不是猜
				// 调用方本该先调 Probe，这里让那个疏漏显形
				item.Ready = false
				item.MissingTools = append(item.MissingTools, tool+" (not probed)")
				item.ProbeMissing = true
			case !st.Available:
				item.Ready = false
				item.MissingTools = append(item.MissingTools, tool)
			}
		}
		if !item.Ready {
			item.InstallHint = hint
		}
		out = append(out, item)
	}

	// 不需要任何外部依赖的分析器在任何机器上都能跑，把它们排在前面，让这种
	// 始终可用的能力显眼一些
	slices.SortStableFunc(out, func(a, b AnalyzerReadiness) int {
		// 先按是否就绪，再按名字
		// cmp.Compare 没有 bool 重载，所以显式比较，让 true（就绪）排在前面
		if a.Ready != b.Ready {
			if a.Ready {
				return -1
			}
			return 1
		}
		return cmp.Compare(a.Name, b.Name)
	})
	return out
}

// ReadinessForLanguage 只返回对该语言有效的分析器的就绪情况
//
// Readiness 报的是「这台机器上装了哪些工具」，与项目无关。而一次扫描真正要跑的
// 只是其中一部分，Go 工具在 JavaScript 项目上会被正常跳过，那不是缺失。
// 把两者混在一起，界面就会在一个 TS 项目上列出十一个「缺失」的 Go / Rust / Python
// 工具，它们本来就不该跑，列出来只会让真正缺的那一个淹掉
//
// 所以调用方在「扫描前这个项目缺什么」这个问题上应该用这个函数，而不是 Readiness
func ReadinessForLanguage(registry *Registry, avail *Availability, lang string) []AnalyzerReadiness {
	all := Readiness(registry, avail)
	out := make([]AnalyzerReadiness, 0, len(all))
	for _, item := range all {
		if AppliesToLanguage(registry.byName(item.Name), lang) {
			out = append(out, item)
		}
	}
	return out
}

// ReadinessInProject 在按语言过滤之外，再让实现了 ProjectScoped 的分析器用项目
// 自身的条件重新判定一次可用性
//
// 这一层是必要的，因为 Availability 的探测是全局的，它看到 npx 在 PATH 上，便认为
// biome 与 tsc 就绪，而这两个真正要跑的东西只存在于项目自己的 node_modules 里。
// 不修正的话，预检会对一个没装 biome 的项目报告「一切就绪」，用户点下扫描才撞上
// 「包缺失」
//
// 判定是双向的，ProjectScoped 说不可用就一定不可用，即使全局探测说就绪，这正是
// 要修的那种误报
func ReadinessInProject(registry *Registry, avail *Availability, lang, projectPath string) []AnalyzerReadiness {
	out := ReadinessForLanguage(registry, avail, lang)
	for i := range out {
		an := registry.byName(out[i].Name)
		ps, ok := an.(ProjectScoped)
		if !ok {
			continue
		}
		if ps.AvailableIn(projectPath) {
			// 项目里有它能用的东西，就覆盖全局探测的否定结论。PHPStan 就靠这
			// 一条，PATH 上没有 phpstan，但项目根目录放了 phpstan.phar，扫描
			// 实际能跑，界面不该说不可用
			out[i].Ready = true
			out[i].MissingTools = []string{}
			out[i].ProbeMissing = false
			out[i].InstallHint = ""
			continue
		}
		// 项目的判断更具体，所以它说了算，工具没装就不可用，不管 PATH 上有什么
		out[i].Ready = false
		out[i].ProbeMissing = false
		if len(out[i].MissingTools) == 0 {
			out[i].MissingTools = out[i].Requires
		}
		if out[i].InstallHint == "" {
			out[i].InstallHint = analyzerInstallHint(an)
		}
	}
	return out
}

// ---------------------------------------------------------------------------
// 聚合
// ---------------------------------------------------------------------------

// Aggregate 合并各分析器的问题，做归一化和去重
//
// 去重很要紧，因为内置规则集与外部 linter 是有意重叠的（见 rule/builtin.go）。
// 同一行以两个名字报两遍会让问题列表没法用，所以按（文件、行、规则族）取最高
// 严重级别，并记下重叠的分析器
func Aggregate(results []AnalysisResult) []Issue {
	byFingerprint := map[string]int{} // key -> index into out
	var out []Issue

	for _, result := range results {
		for _, issue := range result.Issues {
			issue.Analyzer = firstNonEmptyStr(issue.Analyzer, result.Analyzer)
			if issue.Severity == "" {
				issue.Severity = SeverityWarning
			}
			issue.Message = trimMessage(issue.Message)

			key := dedupeKey(issue)
			if idx, seen := byFingerprint[key]; seen {
				// 取两者中更严重的，消息取更长的那个（通常也更具体）
				if severityRank(issue.Severity) < severityRank(out[idx].Severity) {
					out[idx].Severity = issue.Severity
				}
				if len(issue.Message) > len(out[idx].Message) && issue.Analyzer == "custom" {
					out[idx].Message = issue.Message
				}
				if out[idx].Advice == "" {
					out[idx].Advice = issue.Advice
				}
				continue
			}
			byFingerprint[key] = len(out)
			out = append(out, issue)
		}
	}

	// 先严重级别，再文件，再行号。cmp.Or 在第一个非零比较处就停，正好等价于
	// 它替代的那串嵌套 if
	slices.SortStableFunc(out, func(a, b Issue) int {
		return cmp.Or(
			cmp.Compare(severityRank(a.Severity), severityRank(b.Severity)),
			cmp.Compare(a.File, b.File),
			cmp.Compare(a.Line, b.Line),
		)
	})
	return out
}

// dedupeKey 把指向同一行、同一规则的问题合并到一起
//
// 规则名要做归一化，因为不同工具对同一个检查的写法不同（比如 "errcheck" 和
// "G104" 描述的都是未处理的错误）
func dedupeKey(i Issue) string {
	return i.File + ":" + itoa(i.Line) + ":" + normalizeRuleFamily(i.Rule)
}

// ruleFamilies 把各工具特有的规则标识映射到共享的家族，重叠的发现因此能合并
var ruleFamilies = map[string]string{
	// 未处理的错误
	"errcheck":      "unchecked-error",
	"g104":          "unchecked-error",
	"ignored-error": "unchecked-error",
	// 硬编码凭据
	"g101":                 "hardcoded-credential",
	"hardcoded-credential": "hardcoded-credential",
	// 弱加密
	"g401":        "weak-crypto",
	"g501":        "weak-crypto",
	"g502":        "weak-crypto",
	"g505":        "weak-crypto",
	"weak-crypto": "weak-crypto",
	// 注入
	"g201":                   "sql-injection",
	"g202":                   "sql-injection",
	"sql-string-concat":      "sql-injection",
	"g204":                   "command-injection",
	"exec-command-injection": "command-injection",
	// 路径处理
	"g304":           "path-traversal",
	"path-traversal": "path-traversal",
	// TLS
	"g402":         "insecure-tls",
	"insecure-tls": "insecure-tls",
	// 格式
	"gofmt": "formatting",
}

func normalizeRuleFamily(rule string) string {
	lower := lowerASCII(rule)
	if family, ok := ruleFamilies[lower]; ok {
		return family
	}
	return lower
}

func severityRank(s string) int {
	return model.ParseSeverity(s).Rank()
}

func trimMessage(s string) string {
	// 工具会在消息里塞换行和文件前缀，只保留第一行
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' {
			return trimSpace(s[:i])
		}
	}
	if len(s) > 500 {
		return trimSpace(s[:500]) + "..."
	}
	return trimSpace(s)
}

// 小工具函数保持本地，本文件因此除 model 和 sort 之外没有别的依赖

func firstNonEmptyStr(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

func lowerASCII(s string) string {
	return strings.ToLower(s)
}

func trimSpace(s string) string {
	return strings.TrimSpace(s)
}

func itoa(n int) string {
	return strconv.Itoa(n)
}
