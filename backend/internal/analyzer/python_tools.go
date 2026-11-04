package analyzer

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
)

// Python 分析，基于 ruff
//
// Fizzra 包装工具而不是重新实现它们，ruff 就是 Python 侧的 golangci-lint，
// 一个二进制涵盖 pycodestyle、pyflakes、isort、pylint 的一部分，重要的还有
// bandit 的安全规则。所以一次接入就能覆盖 Python 项目的大部分需求

// RuffAnalyzer 运行 ruff
type RuffAnalyzer struct{ pythonOnly }

// NewRuffAnalyzer 构造该分析器
func NewRuffAnalyzer() Analyzer { return &RuffAnalyzer{} }

func (a *RuffAnalyzer) Name() string { return "ruff" }

func (a *RuffAnalyzer) Description() string {
	return "Runs ruff: correctness, imports, style, complexity and the bandit-derived security rules for Python."
}

func (a *RuffAnalyzer) Category() string   { return "lint" }
func (a *RuffAnalyzer) Requires() []string { return []string{"ruff"} }

// InstallHint 优先给模块形式，因为 Windows 上直接 pip install ruff 会把控制台
// 脚本装到一个常常不在 PATH 上的目录，也就是 ProbeCommands 在运行时绕开的
// 同一个问题
func (a *RuffAnalyzer) InstallHint() string {
	return "pip install ruff\n" +
		"      `python -m ruff` also works, and is found even when the console script is not on PATH."
}

// ProbeCommands 告诉可用性探测 python -m ruff 同样能满足该分析器
//
// 没有它时，界面会对一个用户确实装了的工具报「缺失」，因为 Windows 上 pip 会把
// 控制台脚本装到 PATH 之外。扫描本身其实还能跑，分析器运行时会回退，于是报告
// 和结果互相矛盾，那是最糟的组合
func (a *RuffAnalyzer) ProbeCommands() [][]string {
	return [][]string{{"ruff"}, {"python", "-m", "ruff"}, {"python3", "-m", "ruff"}}
}

// ruffFinding 对应 `ruff check --output-format json-lines` 中的一条记录
//
// 字段名有两处不合常规，都是对着真实输出而非文档确认的
//
//   - 位置在 location/end_location 之下，字段名是 row 而不是 line，两者都从
//     1 开始
//   - severity 存在但恒为字面量 "error"，纯风格规则也一样。它不携带信息，所以
//     严重级别改从规则码推导，见 mapRuffSeverity
type ruffFinding struct {
	Code    string `json:"code"`
	Name    string `json:"name"`
	Message string `json:"message"`
	File    string `json:"filename"`
	URL     string `json:"url"`
	// Severity 解析出来只为完整，刻意不使用
	Severity string `json:"severity"`
	Location struct {
		Row    int `json:"row"`
		Column int `json:"column"`
	} `json:"location"`
	EndLocation struct {
		Row    int `json:"row"`
		Column int `json:"column"`
	} `json:"end_location"`
	// Fix 承载 ruff 自己给出的可直接套用修正，只有具备确定性改写的规则才有
	// 大多数发现根本没有 fix 键，所以这里保持 nil，也就不会设置 FixExample
	//
	// edits 带的是替换后的文本而不是原文，content 是该 span 应该变成的内容，
	// 对 F401 这类删除型规则来说它是空串。原始行必须从文件里读回来，这正是
	// SnippetReader 的用途
	Fix *struct {
		Message string `json:"message"`
		// Applicability 取 safe 或 unsafe。应用该改写可能改变行为时 ruff 会标为
		// unsafe，而一个作者没读过就自动应用的改动，恰恰是本工具不该暗示它没
		// 问题的那种变更
		Applicability string `json:"applicability"`
		Edits         []struct {
			Content  string `json:"content"`
			Location struct {
				Row int `json:"row"`
			} `json:"location"`
			EndLocation struct {
				Row int `json:"row"`
			} `json:"end_location"`
		} `json:"edits"`
	} `json:"fix"`
}

func (a *RuffAnalyzer) Analyze(ctx context.Context, projectPath string) ([]Issue, error) {
	return nil, nil
}

func (a *RuffAnalyzer) AnalyzeWithContext(actx *Context) ([]Issue, error) {
	bin, err := ruffCommand(actx.Runner)
	if err != nil {
		return nil, ErrToolUnavailable("ruff")
	}

	// 规则集的取舍比看上去重要，两个极端都是错的
	//
	// ruff 默认只查风格，把它跑在一个含有 eval() 和 os.system() 的文件上，它
	// 不会报出任何安全问题，因为源自 bandit 的 S 规则默认不开启。Fizzra 用户
	// 看到「0 个问题」会以为代码是安全的
	//
	// --select ALL 能解决这点，但会严重过犹不及，风格类规则的数量会把安全问题埋掉，
	// 把作者的文档规范和一个命令注入并列报出来，只会教人忽略这个工具
	args := append(bin.args, ruffCheckArgs(func(key string) (string, bool) {
		return actx.Option(a.Name(), key)
	})...)

	out, err := actx.Runner.Run(actx.Context, actx.ProjectPath, bin.name, args...)
	if err != nil {
		if IsTimeout(err) {
			return nil, err
		}
		if len(out) == 0 {
			return nil, fmt.Errorf("ruff failed: %w", err)
		}
		// --exit-zero 本应避免这种情况，但退出码非零且有输出仍然说明发现了问题，
		// 所以照常解析而不是丢弃
		actx.Log.Debug("ruff exited non-zero with output", "err", err)
	}

	issues, skipped := parseRuffJSONLines(out, NewSnippetReader(actx.Files))
	if skipped > 0 {
		actx.Log.Debug("ruff emitted non-JSON lines", "count", skipped)
	}
	return issues, nil
}

// ruffRuleGroups 把配置开关映射到它启用的 ruff 规则前缀
//
// 除描述缺陷的那些之外，所有分组默认关闭，默认值就是一次无配置扫描报出来的东西。
// 这是刻意的取舍，风格类规则挤占注意力，会让用户忽略这个工具
//
// 风格分组之所以提供，是因为团队可能想要，而不是因为想要就不对。它们默认不开，
// 好让默认结论只关乎缺陷
var ruffRuleGroups = []struct {
	// Key 是 analyzerOptions.ruff 之下的配置键
	Key string
	// Prefixes 是该开关启用的 ruff 规则码
	Prefixes []string
	// Default 表示无配置时该分组是否开启
	Default bool
	// Description 显示在面板上，让运维不必去读 ruff 的文档就知道自己在开什么
	Description string
}{
	{
		Key:         "style",
		Prefixes:    []string{"E1", "E2", "E3", "E4", "E5", "E7", "W1", "W2", "W3", "W5"},
		Default:     false,
		Description: "Line length, whitespace, blank lines and other pycodestyle formatting. A formatter's job; off by default because it dominates the issue list.",
	},
	{
		Key:         "docstrings",
		Prefixes:    []string{"D"},
		Default:     false,
		Description: "Missing or malformed docstrings (pydocstyle). A documentation policy rather than a defect, and the single largest source of noise on a real project.",
	},
	{
		Key:         "annotations",
		Prefixes:    []string{"ANN"},
		Default:     false,
		Description: "Missing type annotations (flake8-annotations). Useful as a ratchet on new code; noisy on a codebase that predates it.",
	},
	{
		Key:         "copyright",
		Prefixes:    []string{"CPY"},
		Default:     false,
		Description: "Missing copyright notice at the top of each file. An organisation policy, not a code quality signal.",
	},
	{
		Key:         "naming",
		Prefixes:    []string{"N"},
		Default:     false,
		Description: "PEP 8 naming conventions (pep8-naming).",
	},
	{
		Key:         "pandas",
		Prefixes:    []string{"PD"},
		Default:     false,
		Description: "Pandas-specific anti-patterns (pandas-vet). Enable for data-science code.",
	},
	{
		Key:         "quotes",
		Prefixes:    []string{"Q"},
		Default:     false,
		Description: "Quote style consistency (flake8-quotes).",
	},
	{
		Key:         "todo",
		Prefixes:    []string{"TD"},
		Default:     false,
		Description: "Formatting of TODO comments (flake8-todos).",
	},
}

// ruffBasePrefixes 始终启用，这些类别描述的是缺陷
//
// 正确性、安全、真实的 bug 模式，以及能抓住运行时崩溃的检查。这些才是 Python
// 代码审查的意义所在，而且没有一项属于口味问题
var ruffBasePrefixes = []string{
	"E9",  // runtime pycodestyle: syntax errors, IOError
	"F",   // pyflakes: undefined names, unused imports
	"I",   // import ordering
	"S",   // bandit security
	"B",   // bugbear: real bug patterns
	"C90", // mccabe complexity
	"A",   // builtin shadowing
	"RUF", // ruff-specific correctness checks
	"UP",  // pyupgrade: outdated constructs
	"BLE", // blind except
	"PLE", // pylint errors (not the style half)
}

// ruffCheckArgs 构造 `ruff check` 的参数列表
//
// 从分析器里抽出来，测试因此可以在没装 ruff 的情况下断言它
//
// # 规则集
//
// ruff 默认只查风格，所以一个含有 eval() 和 os.system() 的文件报不出任何安全问题，
// 源自 bandit 的 S 规则默认不开，读者看到「0 个问题」会以为代码是安全的。
// --select ALL 能解决这点，但会把风格类规则和海量文档检查堆上来，盖过真正的缺陷。
// 所以规则集是基础类别加上运维自行启用的可选分组，各分组的内容见 ruffRuleGroups
//
// # 排除项
//
// 测试文件被排除，理由 Go 规则库已经写明，测试套件做的事情，生产代码合法地
// 不该做。S101 的存在是因为 python -O 会把 assert 从生产代码里剥掉，
// 在测试里 assert 本身就是那个测试
//
// 这里有两个陷阱
//
//   - 必须用 --extend-exclude 而不是 --exclude。后者是替换 ruff 的默认排除项
//     而不是追加，而默认项正是跳过 .venv、site-packages 和 __pycache__ 的那一批。
//     用 --exclude 会让分析器去遍历项目已安装的依赖
//   - 匹配模式用裸名字，不能写成 **/name。含 **/ 的模式会破坏 ruff 的目录剪枝，
//     .venv 里的文件会被全部扫进来。裸形式排除同样的文件，又不动默认项
//
// opts 是每个分析器的配置，nil 表示使用默认值
func ruffCheckArgs(opts func(key string) (string, bool)) []string {
	args := []string{
		"check",
		// 用 json-lines 而不是 json，ruff 会在 JSON 文档之前往 stdout 打印警告，
		// 一旦这些行排在前面，数组形式就没法解析了。每行一个完整对象则不受影响
		"--output-format", "json-lines",
		"--no-cache",
		// 让发现不再影响退出码，退出码非零就明确表示 ruff 本身失败了。这与已经
		// 传给 golangci-lint 的 --issues-exit-code 0 一致
		"--exit-zero",

		"--select", strings.Join(ruffSelectedPrefixes(opts), ","),

		// 测试文件。裸名字形式很重要，见上面的说明
		"--extend-exclude", "tests",
		"--extend-exclude", "test_*.py",
		"--extend-exclude", "*_test.py",
		"--extend-exclude", "conftest.py",
	}

	return append(args, ".")
}

// ruffSelectedPrefixes 返回本次运行要选择的规则前缀
func ruffSelectedPrefixes(opts func(key string) (string, bool)) []string {
	out := append([]string{}, ruffBasePrefixes...)

	for _, group := range ruffRuleGroups {
		enabled := group.Default
		if opts != nil {
			if raw, ok := opts(group.Key); ok {
				enabled = parseBool(raw, group.Default)
			}
		}
		if enabled {
			out = append(out, group.Prefixes...)
		}
	}
	return out
}

// parseBool 读取配置值，遇到不认识的内容时回退
//
// 配置在加载时已经校验过，等分析器运行到这里，坏值早就报过了，在这里回退可以
// 避免每次扫描都重复报一遍
func parseBool(raw string, fallback bool) bool {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "true", "yes", "on", "1":
		return true
	case "false", "no", "off", "0":
		return false
	}
	return fallback
}

// Options 实现 OptionProvider，把开关暴露给面板
//
// 声明在这里而不是在界面里写死，这样新增一个分组只需改 ruffRuleGroups 的一行，
// 控件会自己出现
func (a *RuffAnalyzer) Options() []DescriptorOption {
	out := make([]DescriptorOption, 0, len(ruffRuleGroups))
	for _, g := range ruffRuleGroups {
		out = append(out, DescriptorOption{
			Key:         g.Key,
			Default:     g.Default,
			Description: g.Description,
		})
	}
	return out
}

// ruffInvocation 描述如何调到 ruff
type ruffInvocation struct {
	name string
	args []string
}

// ruffCommand 定位 ruff，回退到 python -m ruff
//
// pip 安装常把控制台脚本放在 PATH 之外（Windows 上落在
// %APPDATA%\Python\Python3xx\Scripts），而用户有理由认为自己装好了。同时
// 探测模块形式，能把「装了但不在 PATH 上，扫描因此静默什么都不做」变成一个
// 能正常工作的扫描
func ruffCommand(r Runner) (ruffInvocation, error) {
	if path, err := LookCandidates(r, "ruff", "ruff.exe"); err == nil {
		return ruffInvocation{name: path}, nil
	}
	for _, py := range []string{"python", "python3"} {
		if _, err := r.LookPath(py); err != nil {
			continue
		}
		return ruffInvocation{name: py, args: []string{"-m", "ruff"}}, nil
	}
	return ruffInvocation{}, fmt.Errorf("ruff: not found")
}

// parseRuffJSONLines 每行解析一条发现，跳过不是 JSON 的行
//
// 跳过条数返回给调用方而不是在这里记日志，由调用方决定怎么处理，少量非 JSON
// 行是正常的（ruff 会在前面打印警告），但整个文件都不是 JSON 就说明解析器错了，
// 静默会掩盖这一点
func parseRuffJSONLines(out []byte, snippets *SnippetReader) (issues []Issue, skipped int) {
	sc := bufio.NewScanner(bytes.NewReader(out))
	// 发现可能带着源代码行，所以把默认的 64KiB 上限调高
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	for sc.Scan() {
		line := bytes.TrimSpace(sc.Bytes())
		if len(line) == 0 || line[0] != '{' {
			if len(line) > 0 {
				skipped++
			}
			continue
		}

		var f ruffFinding
		if err := json.Unmarshal(line, &f); err != nil {
			skipped++
			continue
		}

		rel := filepath.ToSlash(f.File)
		issues = append(issues, Issue{
			Analyzer: "ruff",
			Rule:     f.Code,
			Severity: mapRuffSeverity(f.Code),
			Message:  strings.TrimSpace(f.Message),
			Advice:   adviceForRuff(f.Code),
			Fix:      ruffFix(f, snippets),
			File:     rel,
			Line:     f.Location.Row,
			Column:   f.Location.Column,
			Code:     snippets.Line(rel, f.Location.Row),
			DocURL:   f.URL,
		})
	}
	return issues, skipped
}

// ruffFix 把 ruff 的结构化编辑变成修改前后对照示例
//
// 这个形态能成立，全靠 ruff 把替换的两端都报出来，编辑自身的 span 给出被替换的
// 行，content 给出替换成什么。这覆盖了重要的两种情形，而它们是相反的，I001
// 重写一个块（content 是排好序的 import），F401 删掉一行（content 为空）
//
// 缺任何一部分就返回零值是有意为之。半成品示例比没有更糟，读者分不清「工具
// 没有建议」和「工具的建议在转换中丢了」，然后会把空的那一半粘贴进去
func ruffFix(f ruffFinding, snippets *SnippetReader) FixExample {
	if f.Fix == nil || len(f.Fix.Edits) == 0 {
		return FixExample{}
	}

	rel := filepath.ToSlash(f.File)
	var before, after []string
	sawEdit := false

	for _, edit := range f.Fix.Edits {
		start, end := edit.Location.Row, edit.EndLocation.Row
		if start <= 0 {
			continue
		}
		if end < start {
			end = start
		}
		sawEdit = true

		// 被该编辑替换掉的那些行。reader 为 nil（或文件读不了）时这里为空，但
		// 下面的替换内容单看仍然值得展示
		for row := start; row <= end; row++ {
			if line := snippets.Line(rel, row); line != "" {
				before = append(before, line)
			}
		}

		// ruff 的 content 就是替换文本本身。只去掉结束最后一行的那个换行，不能
		// 清掉所有结尾换行，那会删掉分隔被改写块与后续内容的空行。ruff 自己针对
		// 一个结束于第 4 行的块给出的 I001 输出是 "import os\nimport sys\n\n\n"，
		// 那两行空行正是修复的一部分
		after = append(after, strings.Split(strings.TrimSuffix(edit.Content, "\n"), "\n")...)
	}

	if !sawEdit {
		return FixExample{}
	}

	// 即使被替换的行一行都读不出来，仍要展示 ruff 会写入的替换内容，所以保留
	// 单侧的示例。两侧全空则不值得渲染
	if len(before) == 0 && len(after) == 0 {
		return FixExample{}
	}

	note := strings.TrimSpace(f.Fix.Message)
	switch {
	case note == "":
		note = "Ruff can apply this fix automatically with `ruff check --fix`."
	case f.Fix.Applicability == "unsafe":
		// unsafe 的改写可能改变行为。说明这点，才不至于把一条建议说成「这个改动
		// 是等价的」
		note += " (Ruff marks this fix unsafe — review it before applying.)"
	default:
		note += ". Ruff can apply this automatically with `ruff check --fix`."
	}

	return FixExample{
		Before:   strings.Join(before, "\n"),
		After:    strings.Join(after, "\n"),
		Note:     note,
		Language: "python",
	}
}

// mapRuffSeverity 从规则码推导严重级别
//
// ruff 对每条规则都报 severity: "error"，纯风格也不例外，所以那个字段没用，
// 只能按规则码的类别前缀来映射
//
//	S   bandit 安全            → 视规则而定为 critical 或 error
//	F82/F83 flake8 危险用法     → error
//	E9  pycodestyle 运行时      → error
//	B   bugbear（真实 bug）     → warning
//	C90 mccabe 复杂度           → warning
//	F   其余 flake8             → warning
//	A   遮蔽内置名              → warning
//	D/ANN/N/I/W/E  风格         → info
//
// 前缀的判断顺序很重要，"ANN201" 以 A 开头，所以类型注解的判断必须排在
// 遮蔽内置名之前，否则每一条缺少注解都会被报成真正的警告
func mapRuffSeverity(code string) string {
	upper := strings.ToUpper(strings.TrimSpace(code))

	switch {
	case strings.HasPrefix(upper, "S"):
		if criticalRuffRules[upper] {
			return SeverityCritical
		}
		return SeverityError
	case strings.HasPrefix(upper, "F82"), strings.HasPrefix(upper, "F83"):
		// 未定义的名字之类：代码会在运行时失败
		return SeverityError
	case strings.HasPrefix(upper, "E9"):
		// 偏运行时的 pycodestyle：语法错误、IOError
		return SeverityError
	case strings.HasPrefix(upper, "ANN"):
		// 类型注解。必须排在下方的 A 分支之前
		return SeverityInfo
	case strings.HasPrefix(upper, "B"):
		return SeverityWarning
	case strings.HasPrefix(upper, "C90"):
		return SeverityWarning
	case strings.HasPrefix(upper, "F"):
		// flake8 其余的检查：未使用的导入、重复定义
		return SeverityWarning
	case strings.HasPrefix(upper, "A"):
		// 遮蔽内置名
		return SeverityWarning
	case strings.HasPrefix(upper, "D"),
		strings.HasPrefix(upper, "N"),
		strings.HasPrefix(upper, "I"),
		strings.HasPrefix(upper, "W"),
		strings.HasPrefix(upper, "E"):
		return SeverityInfo
	}
	return SeverityWarning
}

// criticalRuffRules 是源自 bandit 的、严重到该占最高档的检查
//
// 与 Go 侧对 gosec 的处理一致，代码执行、注入和凭据泄露算 critical，安全家族里
// 其余的是 error。一刀切地把 S 全算 critical，会让一个弱哈希警告和远程代码执行
// 平起平坐
var criticalRuffRules = map[string]bool{
	"S102": true, // exec
	"S103": true, // eval
	"S201": true, // flask debug
	"S301": true, // pickle
	"S302": true, // marshal
	"S307": true, // insecure function (eval/exec family)
	"S308": true, // insecure deserialization
	"S501": true, // requests without verification
	"S506": true, // unsafe YAML load
	"S602": true, // subprocess with shell=True
	"S605": true, // os.system / shell invocation
	"S607": true, // partial executable path
	"S608": true, // shell with wildcard
	"S609": true, // os.system-like
	"S610": true, // django extra()
	"S611": true, // django RawSQL
	"S701": true, // jinja2 autoescape
	"S702": true, // jinja2 autoescape false
	"S704": true, // mark_safe
	"S707": true, // jinja2 from_string
}

// adviceForRuff 为那些光看规则码不知道该怎么办的规则族给出修复提示
//
// 长尾规则不给建议，而不是泛泛复述消息，「修好这个问题」是噪音，而且规则自己的
// 文档链接已经附上了
func adviceForRuff(code string) string {
	switch strings.ToUpper(strings.TrimSpace(code)) {
	case "S102", "S103", "S307":
		return "Avoid dynamic execution. If the input is genuinely trusted, say why in a comment and suppress the rule explicitly rather than leaving it as an accident."
	case "S301", "S302", "S308":
		return "Deserialising untrusted data can execute arbitrary code. Prefer a data-only format such as JSON."
	case "S506":
		return "yaml.load without SafeLoader can construct arbitrary Python objects. Use yaml.safe_load."
	case "S602", "S605", "S609":
		return "Avoid a shell. Pass arguments as a list so metacharacters in the input are inert."
	case "S608":
		return "Building SQL by string interpolation allows injection. Use parameterised queries."
	case "S501":
		return "verify=False disables certificate validation. Pin the expected CA instead."
	case "S701", "S702", "S704", "S707":
		return "Disabling Jinja2 autoescaping enables cross-site scripting. Keep it on and mark trusted values explicitly."
	}
	return ""
}
