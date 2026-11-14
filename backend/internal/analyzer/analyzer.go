// Package analyzer 定义 Fizzra 的分析器契约，以及所有内置和第三方分析器注册用的插件注册表
//
// 契约刻意做得很小，Name 加 Analyze。分析器需要宿主提供的一切（日志、超时、
// 规则引擎、仓库根目录）都挂在 Context 上，这样日后再加能力也不会破坏已有的分析器
package analyzer

import (
	"context"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"sync"
	"time"
)

// Analyzer 是每个插件都必须满足的契约
type Analyzer interface {
	// Name 是在问题、配置和 API 中使用的稳定标识，在同一个注册表内必须唯一且为小写（如 "golangci"）
	Name() string
	// Analyze 扫描 projectPath 并返回发现的问题
	// 返回非 nil 错误会把该分析器本次运行标记为失败，
	// 随错误一起返回的部分结果仍会被记录
	Analyze(ctx context.Context, projectPath string) ([]Issue, error)
}

// ContextAware 可选接口，供需要宿主服务（规则引擎、文件集合、命令执行器、按工具的超时）
type ContextAware interface {
	AnalyzeWithContext(ctx *Context) ([]Issue, error)
}

// Invoke 通过正确的入口运行分析器，调用方应该用它而不是直接调 Analyzer.Analyze
func Invoke(a Analyzer, ctx *Context) ([]Issue, error) {
	if ca, ok := a.(ContextAware); ok {
		return ca.AnalyzeWithContext(ctx)
	}
	return a.Analyze(ctx, ctx.ProjectPath)
}

// Describable 可选接口，分析器实现它以向界面暴露元数据（这个分析器检查什么），避免宿主写死一个按分析器名分支的 switch
type Describable interface {
	Description() string
	// Category 取值之一，lint、security、vulnerability、metrics、wails、custom
	Category() string
	// Requires 列出该分析器要调用的外部二进制
	Requires() []string
}

// Probeable 可选接口，供可以通过多个命令调到的工具实现
// 默认探测是对 Requires() 里的每个名字做 LookPath，对独立分发的二进制够用，
// 但对只能作为语言模块安装的工具不够，Windows 上 pip 安装的脚本目录常常不在 PATH 上，
// 而 `python -m ruff` 是可以用的。这种情况下报「缺失」是错的，
// 而且是更容易让人困惑的那种错，用户确实装了，界面却说他没装
type Probeable interface {
	// ProbeCommands 返回能让该分析器工作的备选命令，按优先级排列
	// 每项是命令名加上它的参数，返回空表示用默认的 Requires() 探测
	ProbeCommands() [][]string
}

// Installable 可选接口，供所需工具有固定安装命令的分析器实现
// 没有它时，命令行只根据工具名拼出 `go install <tool>@latest`，这只对 Go 工具成立，
// 问到 oxlint 这种 npm 包会立刻报 malformed module path，避免出现一条跑不通的提示
type Installable interface {
	// InstallHint 返回一行说明如何安装该工具，没什么可说时返回空串
	// 可以是多个备选方案，用换行分隔
	InstallHint() string
}

// analyzerInstallHint 返回分析器的安装提示，未声明时返回空串，由调用方回退到通用猜测
func analyzerInstallHint(a Analyzer) string {
	if a == nil {
		return ""
	}
	if in, ok := a.(Installable); ok {
		return in.InstallHint()
	}
	return ""
}

// ProjectScoped 可选接口，供「工具装在项目里、而不是 PATH 上」的分析器实现
//
// 背景，可用性探测（Availability.Probe）是全局的，它只看某个命令名在不在 PATH 上。
// 对 Go、Rust、Python 那类全局安装的工具这是准的，但对经 npx 调用的 npm 包就失效，
// 探测看到 npx 在，便判定分析器就绪，而真正要跑的那个包其实只存在于项目自己的
// node_modules 里。于是界面会声称一切就绪，扫描时才报「包缺失」
//
// 实现这个接口的分析器让可用性检查能落到项目维度，判断的是「这个项目里能不能跑」，
// 而不是「这台机器上有没有」
type ProjectScoped interface {
	// AvailableIn 报告该分析器在给定项目里是否具备运行条件
	//
	// 只做文件系统与 PATH 的检查，不要起子进程，探测会和扫描共用 --timeout，
	// 慢的探测会被杀掉，于是一个装好的工具被报成缺失。同样一段论证写在
	// hasLocalBin 上方
	AvailableIn(projectPath string) bool
}

// LanguageScoped 可选接口，供只处理特定语言的分析器实现
//
// 没有它时，每个注册的分析器都会对着每个项目运行，各自决定适不适用，Go 工具
// 的包装器就是这样最后变成手写 go.mod 判断的。这样能work，但它是约定而不是
// 契约，而且失败时悄无声息，无法区分「我不处理 Rust」和「我没发现问题」，
// 于是一个 Rust 项目会被评成干净的
//
// 不实现该接口的分析器对每个项目都运行，这对与语言无关的检查是正确的默认
// （行数统计、密钥扫描）
type LanguageScoped interface {
	// Languages 返回该分析器处理的语种标识，取值同 model.Language*
	// 空切片表示「所有语言」
	Languages() []string
}

// AnalyzerLanguages 报告分析器处理哪些语言
//
// 未实现 LanguageScoped 的分析器返回 nil，含义是「全部」，调用方不应把它读成「没有」
func AnalyzerLanguages(a Analyzer) []string {
	if ls, ok := a.(LanguageScoped); ok {
		return ls.Languages()
	}
	return nil
}

// AppliesToLanguage 报告某分析器是否应对某语言运行
//
// 语言列表为 nil 或空表示该分析器与语言无关。lang 为空（项目字段未设置）时
// 匹配一切，这样一个配置有误的项目会跑全套，而不是静默什么都不跑
func AppliesToLanguage(a Analyzer, lang string) bool {
	langs := AnalyzerLanguages(a)
	if len(langs) == 0 || lang == "" {
		return true
	}
	return slices.Contains(langs, lang)
}

// analyzerAvailableIn 是 ProjectScoped 的共用实现
// PATH 上有就算有，否则看项目本地的 node_modules/.bin
//
// 两个位置对应两种安装方式，全局装的在 PATH 上，装进项目的在 devDependencies 里。
// 前者自然可用，后者才是 ProjectScoped 要解决的场景
func analyzerAvailableIn(projectPath, name string) bool {
	if lookPathAny(name, name+".exe", name+".cmd") {
		return true
	}
	if projectPath == "" {
		return false
	}
	return hasLocalBinIn(projectPath, name)
}

// goOnly 供读取 Go 源码的分析器嵌入，避免重复写同一个方法
//
// 目前所有内置分析器都限定 Go，工具包装器判断 go.mod，规则引擎、指标和 Wails
// 检查也都遍历 Go 文件。新增语言靠在分析器里各自声明语言
type goOnly struct{}

func (goOnly) Languages() []string { return []string{modelLanguageGo} }

// pythonOnly 是 Python 分析器对应的标记
type pythonOnly struct{}

func (pythonOnly) Languages() []string { return []string{modelLanguagePython} }

// javascriptOnly 是 JavaScript/TypeScript 分析器的标记
type javascriptOnly struct{}

func (javascriptOnly) Languages() []string { return []string{modelLanguageJavaScript} }

// rustOnly 是 Rust 分析器的标记
type rustOnly struct{}

func (rustOnly) Languages() []string { return []string{modelLanguageRust} }

// javaOnly 是 Java 分析器的标记
type javaOnly struct{}

func (javaOnly) Languages() []string { return []string{modelLanguageJava} }

// csharpOnly 是 C# 分析器的标记
type csharpOnly struct{}

func (csharpOnly) Languages() []string { return []string{modelLanguageCSharp} }

// cppOnly 是 C/C++ 分析器的标记
type cppOnly struct{}

func (cppOnly) Languages() []string { return []string{modelLanguageCpp} }

// phpOnly 是 PHP 分析器的标记
type phpOnly struct{}

func (phpOnly) Languages() []string { return []string{modelLanguagePHP} }

// modelLanguageGo 对应 model.LanguageGo
//
// 这里是复制的常量而不是导入，分析器包刻意不导入 model，插件因此不依赖持久层。
// 见上方 Issue 类型的说明
const modelLanguageGo = "go"

// modelLanguagePython 对应 model.LanguagePython，原因同上
const modelLanguagePython = "python"

// modelLanguageJavaScript 对应 model.LanguageJavaScript，原因同上
const modelLanguageJavaScript = "javascript"

// modelLanguageRust 对应 model.LanguageRust，原因同上
const modelLanguageRust = "rust"

// modelLanguageJava 对应 model.LanguageJava，原因同上
const modelLanguageJava = "java"

// modelLanguageCSharp 对应 model.LanguageCSharp，原因同上
const modelLanguageCSharp = "csharp"

// modelLanguageCpp 对应 model.LanguageCpp，原因同上
const modelLanguageCpp = "cpp"

// modelLanguagePHP 对应 model.LanguagePHP，原因同上
const modelLanguagePHP = "php"

// Issue 是面向分析器的发现视图
//
// 与 model.Issue 保持一致，分析器因此不依赖持久层。
// service 层负责两者之间的映射，并补上 ID、项目与分析归属和指纹
type Issue struct {
	Analyzer string
	Rule     string
	Severity Severity

	Message string
	Advice  string
	// Fix 是该分析器提供的修改前后对照示例
	Fix FixExample

	File   string
	Line   int
	Column int
	Code   string
	DocURL string

	// Origin 标记该发现属于项目自身代码还是依赖，取值同 model.Origin*
	//
	// 声明为普通字符串而不是 model 的常量类型，原因与这个结构体上其他字段
	// 一样，分析器不导入持久层模型，由 service 层负责映射
	Origin string
	// DependencyPath 是引入第三方发现的那条链路，从最外层开始
	// Origin 不是 OriginTransitive 时为空
	DependencyPath []string
}

// FixExample 对应 model.FixExample
type FixExample struct {
	Before   string
	After    string
	Note     string
	Language string
}

// HasContent 报告该示例是否有值得渲染的内容
func (f FixExample) HasContent() bool {
	return f.Before != "" || f.After != ""
}

// Severity 对应 model.Severity，在这里取别名是为了让分析器与存储保持解耦
type Severity = string

// 分析器可返回的严重级别
const (
	SeverityCritical = "critical"
	SeverityError    = "error"
	SeverityWarning  = "warning"
	SeverityInfo     = "info"
)

// Context 把宿主服务传入 Analyze
type Context struct {
	// Context 是本次分析的取消与超时作用域
	context.Context //nolint:containedctx // 有意为之：整个结构体贯穿所有分析器

	// ProjectPath 是仓库根目录的绝对路径
	ProjectPath string
	// ProjectName 是用于消息中的人类可读名称
	ProjectName string
	// ProjectKind 是框架（"wails"），或空
	// 与 ProjectLanguage 正交，一个 Wails 应用的语言是 go，框架是 wails
	ProjectKind string
	// ProjectLanguage 是生态（"go"、"rust"、"python"、"javascript"）
	// 用来决定哪些分析器适用
	ProjectLanguage string
	// Root 是工作区根目录，用于定位共享缓存
	Root string

	// Log 是预先打好标签的日志器
	Log *slog.Logger
	// ToolTimeout 限制单次外部命令调用的时长
	ToolTimeout time.Duration
	// Runner 执行外部命令，抽象出来是为了测试可以替换
	Runner Runner
	// Files 提供安全且尊重忽略规则的目录遍历
	Files *FileSet
	// Rules 是编译好的自定义规则引擎，分析器支持时才有
	// 类型是 RuleEvaluator 而不是 rule 包，避免分析器包导入 rule
	//（后者又导入 model，会把存储类型拖进每个插件）
	Rules RuleEvaluator
	// Options 按分析器名报告配置里的对应设置
	//
	// 用带分析器名的函数而不是 map，有两个原因。一是分析器只能读到自己的键，
	// 不会意外依赖别人的。二是 Context 由并发运行分析器的多个 goroutine 共享，
	// 每个分析器一个闭包会有数据竞争，而按名查询是共享安全的
	//
	// 为 nil 表示没有配置，所有访问器都当作使用默认值处理
	Options func(analyzerName, key string) (string, bool)
	// Offline 表示是否应避免网络访问
	//
	// 与 Options 分开暴露，因为它是全局策略而不是某个分析器的开关，也因为它
	// 要防的失败是看不见的，扫描器在离网机器上去连网络不会报错，会一直卡到 socket 超时
	Offline bool
}

// Option 返回某分析器某个键的配置值
func (c *Context) Option(analyzerName, key string) (string, bool) {
	if c.Options == nil {
		return "", false
	}
	return c.Options(analyzerName, key)
}

// OptionBool 返回某分析器的布尔设置，未设置时返回 fallback
//
// 值解析不了时回退而不是报错，配置在加载时已经校验过，等分析器运行到这里，
// 坏值早就该报出来了。每次扫描再报一遍只会把真正的发现淹没掉
func (c *Context) OptionBool(analyzerName, key string, fallback bool) bool {
	raw, ok := c.Option(analyzerName, key)
	if !ok {
		return fallback
	}
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "true", "yes", "on", "1":
		return true
	case "false", "no", "off", "0":
		return false
	}
	return fallback
}

// RuleEvaluator 是自定义规则分析器所需的最小接口面
type RuleEvaluator interface {
	Evaluate(relPath string, content []byte) []RuleMatch
	Len() int
	Skipped() []error
	// AppliesTo 报告是否有已启用的规则可能匹配该路径
	//
	// 规则引擎用它避免读取不可能匹配的文件，自定义分析器用它区分「我查过这些
	// 文件，它们是干净的」和「没有规则作用于这门语言，我什么都没查」，避免语义问题
	AppliesTo(relPath string) bool
}

// RuleMatch 对应 rule.Match
type RuleMatch struct {
	Rule     string
	Severity string
	Message  string
	Advice   string
	Fix      FixExample
	File     string
	Line     int
	Column   int
	Code     string
}

// ---------------------------------------------------------------------------
// 注册表
// ---------------------------------------------------------------------------

// Registry 持有可用的分析器
// 插件式架构，新增分析器只需注册一个构造函数，代码库中其他地方都不用改
type Registry struct {
	mu        sync.RWMutex
	factories map[string]Factory
	order     []string
}

// Factory 构造分析器实例
// 用构造函数而不是注册实例，是为了每次分析都拿到一个全新、未被污染的分析器
type Factory func() Analyzer

// NewRegistry 返回空注册表
func NewRegistry() *Registry {
	return &Registry{factories: map[string]Factory{}}
}

// Register 添加一个构造函数。重名注册会在启动时 panic，这属于编程错误，值得
// 大声失败
func (r *Registry) Register(f Factory) {
	r.mu.Lock()
	defer r.mu.Unlock()

	probe := f()
	name := probe.Name()
	if name == "" {
		panic("analyzer: registered analyzer with empty name")
	}
	if _, dup := r.factories[name]; dup {
		panic(fmt.Sprintf("analyzer: duplicate registration for %q", name))
	}
	r.factories[name] = f

	// 按序插入而不是追加后再排序，避免循环内复制这一性能问题
	idx, _ := slices.BinarySearch(r.order, name)
	r.order = slices.Insert(r.order, idx, name)
}

// Names 列出所有已注册的分析器，按名称排序
func (r *Registry) Names() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]string, len(r.order))
	copy(out, r.order)
	return out
}

// New 实例化指定的分析器，未知名称会报错而不是静默忽略
func (r *Registry) New(names []string) ([]Analyzer, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	if len(names) == 0 {
		out := make([]Analyzer, 0, len(r.order))
		for _, n := range r.order {
			out = append(out, r.factories[n]())
		}
		return out, nil
	}

	var out []Analyzer
	var unknown []string
	for _, n := range names {
		f, ok := r.factories[n]
		if !ok {
			unknown = append(unknown, n)
			continue
		}
		out = append(out, f())
	}
	if len(unknown) > 0 {
		return out, fmt.Errorf("unknown analyzer(s): %v (available: %v)", unknown, r.order)
	}
	return out, nil
}

// NewForLanguage 实例化指定的分析器中适用于某语言的那些
//
// 这是引擎应该使用的入口。New 保留给没有项目上下文的调用方（可用性探测、测试），返回全部
//
// 过滤是必要的，否则分析器会被盲目地对着每个项目运行，一个 Go 工具包装器指向
// Rust 代码树，要么空转，要么更糟。在这之前，每个分析器需自行判断，忘了就静默返回零
//
// 未知名称即使会被过滤掉也仍然报错，配置里的拼写错误因此不会被语言检查吞掉
func (r *Registry) NewForLanguage(names []string, lang string) ([]Analyzer, error) {
	all, err := r.New(names)
	if err != nil {
		return all, err
	}

	out := make([]Analyzer, 0, len(all))
	for _, a := range all {
		if AppliesToLanguage(a, lang) {
			out = append(out, a)
		}
	}
	return out, nil
}

// SkippedForLanguage 列出对某语言不会运行的已注册分析器，调用方因此能解释空扫描
func (r *Registry) SkippedForLanguage(lang string) []string {
	r.mu.RLock()
	defer r.mu.RUnlock()

	var out []string
	for _, n := range r.order {
		if !AppliesToLanguage(r.factories[n](), lang) {
			out = append(out, n)
		}
	}
	return out
}

// Describe 返回所有已注册分析器的元数据，供 GET /api/v1/analyzers 使用
func (r *Registry) Describe() []Descriptor {
	r.mu.RLock()
	defer r.mu.RUnlock()

	out := make([]Descriptor, 0, len(r.order))
	for _, n := range r.order {
		a := r.factories[n]()
		d := Descriptor{
			Name:      a.Name(),
			Category:  "lint",
			Requires:  []string{},
			Languages: AnalyzerLanguages(a),
		}
		if desc, ok := a.(Describable); ok {
			d.Description = desc.Description()
			d.Category = desc.Category()
			d.Requires = desc.Requires()
		}
		if op, ok := a.(OptionProvider); ok {
			d.Options = op.Options()
		}
		out = append(out, d)
	}
	return out
}

// OptionProvider 可选接口，供向界面暴露可调设置的分析器实现
//
// 面板应该为每个设置生成一个控件，而不是给每个分析器写死一张表单
// 分析器在这里声明一个开关，界面就会渲染出来
type OptionProvider interface {
	// Options 列出该分析器的设置，每项含默认值和一段描述
	Options() []DescriptorOption
}

// DescriptorOption 是一个可配置的分析器设置
type DescriptorOption struct {
	// Key 是设置名，在配置里写作 analyzerOptions.<analyzer>.<key>，
	// 也是设置接口接受的 JSON 字段名
	Key string `json:"key"`
	// Default 是未配置时的取值
	Default bool `json:"default"`
	// Description 说明启用该选项会做什么
	Description string `json:"description"`
}

// Descriptor 是面向界面的分析器描述
type Descriptor struct {
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Category    string   `json:"category"`
	Requires    []string `json:"requires"`
	// Languages 列出该分析器处理的语种，为空表示全部
	Languages []string `json:"languages,omitempty"`
	// Options 列出可调设置，没有时为空
	Options []DescriptorOption `json:"options,omitempty"`
}

// ---------------------------------------------------------------------------
// 各具体分析器共用的辅助函数
// ---------------------------------------------------------------------------

// AnalysisResult 是一次分析器运行的产物，含那些不应中断整个扫描的分析器级错误
type AnalysisResult struct {
	Analyzer string
	Issues   []Issue
	Err      error
	Skipped  bool
	// SkipReason 说明该分析器为什么被跳过（如缺少二进制）
	SkipReason   string
	SkippedTools []string
}

// Runner 执行外部命令
type Runner interface {
	// Run 在 dir 下以 args 执行 name 并返回 stdout
	// 退出码非零会以 *ExitError 返回而不是普通错误，因为 linter 就是用这种方式
	// 表示「发现了问题」的
	Run(ctx context.Context, dir, name string, args ...string) (stdout []byte, err error)
	// LookPath 报告某个二进制是否可用
	LookPath(name string) (string, error)
}

// ExitError 描述外部工具的非零退出
type ExitError struct {
	Command  string
	Code     int
	Stderr   string
	Stdout   []byte
	TimedOut bool
	// Timeout 实际应用于该命令的预算，只在 TimedOut 为真时设置
	Timeout time.Duration
	// Cancelled 在本次运行的 context 被外部取消时设置，比如关闭扫描，或整个扫描层的超时
	Cancelled bool
}

// Error 渲染一条给读扫描摘要的人看的消息
//
// 完整命令行和全部 stderr 属于调试日志，不该放进一行摘要
// 命令名和退出码保留，它们能指出是什么失败了
func (e *ExitError) Error() string {
	if e.TimedOut {
		if e.Timeout > 0 {
			return fmt.Sprintf("%s timed out after %s", e.Command, e.Timeout)
		}
		return fmt.Sprintf("%s timed out", e.Command)
	}
	if e.Cancelled {
		return fmt.Sprintf("%s was cancelled before it finished", e.Command)
	}
	if e.Stderr == "" {
		return fmt.Sprintf("%s exited with status %d", e.Command, e.Code)
	}
	return fmt.Sprintf("%s exited with status %d: %s", e.Command, e.Code, firstLine(e.Stderr))
}

// firstLine 从 stderr 里挑一行最有代表性的提示
func firstLine(s string) string {
	// 这些前缀出现时那一行就是结论，优先于第一行
	markers := []string{"fatal:", "error:", "cannot ", "failed to ", "unable to "}

	var fallback string
	for _, line := range strings.Split(s, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if fallback == "" {
			fallback = line
		}
		lower := strings.ToLower(line)
		for _, m := range markers {
			if strings.HasPrefix(lower, m) {
				return truncateMessage(line)
			}
		}
	}
	if fallback == "" {
		return ""
	}
	return truncateMessage(fallback)
}

func truncateMessage(s string) string {
	if len(s) > 200 {
		return s[:200]
	}
	return s
}
