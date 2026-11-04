package analyzer

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
)

// PHP 分析基于 PHPStan
//
// 选它是因为它同时满足三点，单个 PHAR 分发、无需改项目配置、输出结构化 JSON。
// Psalm 需要项目里装 composer 依赖，phpcs 只查风格
//
// 默认的 0 级几乎只查语法错误，这里用 5 级，能查出调用不存在的函数、属性拼错、
// 传参类型不符，又不至于把每处缺 docblock 都报出来

// PhpstanAnalyzer 运行 PHPStan
type PhpstanAnalyzer struct{ phpOnly }

// NewPhpstanAnalyzer 构造该分析器
func NewPhpstanAnalyzer() Analyzer { return &PhpstanAnalyzer{} }

func (a *PhpstanAnalyzer) Name() string { return "phpstan" }

func (a *PhpstanAnalyzer) Description() string {
	return "Runs PHPStan: undefined methods, wrong argument types, unreachable branches and other type errors in PHP."
}

func (a *PhpstanAnalyzer) Category() string { return "bugrisk" }

// Requires 报的是 phpstan 而非 php，用户该去装 PHPStan，不是核对 PHP 版本
// 探测不走这里，ProbeCommands 覆盖 PHAR 的两种执行形式
func (a *PhpstanAnalyzer) Requires() []string { return []string{"phpstan"} }

// InstallHint 给 PHAR 下载地址，因为 composer 全局目录常常不在 PATH 上
func (a *PhpstanAnalyzer) InstallHint() string {
	return "https://github.com/phpstan/phpstan/releases\n" +
		"      Needs a PHP 7.4+ interpreter on PATH."
}

// ProbeCommands 认 PATH 上的 phpstan，以及被解释器直接执行的 PHAR
func (a *PhpstanAnalyzer) ProbeCommands() [][]string {
	return [][]string{{"phpstan"}, {"phpstan.phar"}}
}

// AvailableIn 在全局探测之外补上 FIZZRA_PHPSTAN_HOME 这一种位置
//
// 不查项目目录，Fizzra 发布的是单个可执行文件，没有「往项目里放一个 phar」
// 这种装法，每个被扫描的项目都该用同一个全局安装
//
// 只做文件系统与 PATH 检查，不起子进程。探测与扫描共用 --timeout，
// 慢的探测会被杀掉，于是一个装好的工具会被报成缺失
func (a *PhpstanAnalyzer) AvailableIn(projectPath string) bool {
	if projectPath == "" {
		return false
	}
	// PATH 上有独立二进制，或 FIZZRA_PHPSTAN_HOME 里有 phar，才算装了本体
	hasTool := lookPathAny("phpstan", "phpstan.bat", "phpstan.phar")
	if !hasTool {
		if home := toolHome("FIZZRA_PHPSTAN_HOME"); home != "" {
			for _, cand := range []string{"phpstan.phar", "phpstan"} {
				if fileExists(filepath.Join(home, cand)) {
					hasTool = true
					break
				}
			}
		}
	}
	if !hasTool {
		return false
	}
	// 无论哪种形式，最终都要有 PHP 解释器来执行
	return lookPathAny("php", "php.exe")
}

// lookPathAny 报告候选名里是否有一个能在 PATH 上解析，供拿不到 Runner 的
// 可用性检查使用
func lookPathAny(names ...string) bool {
	for _, name := range names {
		if _, err := exec.LookPath(name); err == nil {
			return true
		}
	}
	return false
}

// phpstanReport 对应 `phpstan analyse --error-format=json` 的输出
type phpstanReport struct {
	Files map[string]phpstanFile `json:"files"`
	// Errors 是不归属于任何文件的问题，比如配置错误，没有位置可报，忽略
	Errors []string `json:"errors"`
}

type phpstanFile struct {
	Messages []phpstanMessage `json:"messages"`
}

type phpstanMessage struct {
	Message string `json:"message"`
	Line    int    `json:"line"`
	// Ignorable 是 PHPStan 自己标注的「可被 ignore 掉」，与是否严重无关
	Ignorable bool `json:"ignorable"`
	// Identifier 是 PHPStan 1.11 起给每个错误附的稳定标识，形如
	// argument.type 或 method.notFound。旧版本没有这个字段
	Identifier string `json:"identifier"`
}

func (a *PhpstanAnalyzer) Analyze(ctx context.Context, projectPath string) ([]Issue, error) {
	return nil, nil
}

func (a *PhpstanAnalyzer) AnalyzeWithContext(actx *Context) ([]Issue, error) {
	cmd, err := phpstanCommand(actx)
	if err != nil {
		return nil, err
	}

	// 早于 7.4 的解释器会以 "unexpected 'bool' (T_STRING)" 退出，
	// 那句错误指向不了真正原因，所以先确认版本，直接报版本不足
	if err := ensurePhpVersion(actx, cmd); err != nil {
		return nil, err
	}

	args := append([]string{}, cmd.args...)
	args = append(args,
		"analyse",
		"--error-format=json",
		"--no-progress",
		// 级别写死 5，不读项目自己的 phpstan.neon，也不做成分析器选项。
		// 前者可能不存在，配置缺失时 PHPStan 会静默跑在默认级别上，
		// 后者受 DescriptorOption 的 Default 是 bool 所限，表达不了数值档位
		"--level=5",
		// 内存上限拉高，PHPStan 在大型项目上默认的 128M 会 OOM 退出
		"--memory-limit=1G",
		actx.ProjectPath,
	)

	out, runErr := actx.Runner.Run(actx.Context, actx.ProjectPath, cmd.name, args...)
	if IsTimeout(runErr) {
		return nil, runErr
	}
	// PHPStan 发现问题时返回非零，这是正常路径而不是失败。只有输出为空且带
	// 错误时才是真失败
	if len(out) == 0 && runErr != nil {
		return nil, fmt.Errorf("phpstan failed: %w", runErr)
	}

	var report phpstanReport
	if err := json.Unmarshal(trimToJSON(out), &report); err != nil {
		return nil, fmt.Errorf("parse phpstan report: %w", err)
	}

	snippets := NewSnippetReader(actx.Files)
	var issues []Issue
	for path, file := range report.Files {
		rel := relToProject(path, actx.ProjectPath)
		for _, m := range file.Messages {
			issues = append(issues, Issue{
				Analyzer: a.Name(),
				Rule:     phpstanRule(m.Identifier),
				Severity: phpstanSeverity(m.Identifier),
				Message:  m.Message,
				File:     rel,
				Line:     m.Line,
				Code:     snippetAt(snippets, rel, m.Line),
				DocURL:   phpstanDocURL(m.Identifier),
			})
		}
	}
	return issues, nil
}

// phpstanInvocation 是解析好的命令
type phpstanInvocation struct {
	name string
	args []string
}

// phpstanCommand 定位 PHPStan：先 PATH 上的独立二进制，再 FIZZRA_PHPSTAN_HOME 里的 phar
func phpstanCommand(actx *Context) (phpstanInvocation, error) {
	if path, err := LookCandidates(actx.Runner, "phpstan", "phpstan.bat", "phpstan.phar"); err == nil {
		return phpstanInvocation{name: path}, nil
	}

	if home := toolHome("FIZZRA_PHPSTAN_HOME"); home != "" {
		for _, cand := range []string{"phpstan.phar", "phpstan"} {
			p := filepath.Join(home, cand)
			if fileExists(p) {
				return phpstanInvocation{name: p}, nil
			}
		}
	}
	return phpstanInvocation{}, ErrToolUnavailable("phpstan")
}

// phpInterpreter 找可用的 PHP 解释器
func phpInterpreter(actx *Context) (string, error) {
	if path, err := LookCandidates(actx.Runner, "php", "php.exe"); err == nil {
		return path, nil
	}
	return "", ErrToolUnavailable("php")
}

// phpstanMinVersion 是 PHPStan 2.x 需要的最低 PHP 版本
// PHPStan 2.3.1 本身用了 7.4 才有的语法（typed property、箭头函数），
// 更老的解释器会以解析错误退出，报的还不是被分析代码的问题
const phpstanMinVersion = "7.4"

// ensurePhpVersion 在跑 PHPStan 之前确认解释器版本够新
//
// 只在拿得到解释器时检查，PHPStan 也可能是 PATH 上带 shebang 的独立文件，
// 那种情况下真正的解释器由内核决定，硬报错会把一个能跑的环境挡在门外
func ensurePhpVersion(actx *Context, cmd phpstanInvocation) error {
	php, err := phpInterpreter(actx)
	if err != nil {
		return nil
	}

	out, err := actx.Runner.Run(actx.Context, actx.ProjectPath, php, "-r", "echo PHP_VERSION;")
	if err != nil || len(out) == 0 {
		return nil
	}

	got := strings.TrimSpace(string(out))
	if phpVersionAtLeast(got, phpstanMinVersion) {
		return nil
	}
	return fmt.Errorf(
		"phpstan needs PHP %s or newer, but %s is being used: upgrade PHP, or point FIZZRA_PHPSTAN_HOME at a PHPStan build that supports %s",
		phpstanMinVersion, got, got)
}

// phpVersionAtLeast 比较 "7.4.33" 这类版本号是否不低于最低要求
//
// 只比前两段，PHP 的小版本号决定语法支持，补丁号无关，而多比一段会在
// "7.4" 与 "7.4.0" 这种写法差异上出错
func phpVersionAtLeast(got, min string) bool {
	g := parseMajorMinor(got)
	m := parseMajorMinor(min)
	if g[0] != m[0] {
		return g[0] > m[0]
	}
	return g[1] >= m[1]
}

// parseMajorMinor 取版本号的前两个数字段，解析不出来的段记 0
func parseMajorMinor(v string) [2]int {
	var out [2]int
	parts := strings.SplitN(strings.TrimSpace(v), ".", 3)
	for i := 0; i < 2 && i < len(parts); i++ {
		n := 0
		for j := 0; j < len(parts[i]); j++ {
			c := parts[i][j]
			if c < '0' || c > '9' {
				break
			}
			n = n*10 + int(c-'0')
		}
		out[i] = n
	}
	return out
}

// phpstanRule 取错误的稳定标识作为规则名，旧版 PHPStan 没有标识时回退到
// 粗分类，让去重与分组至少有个可用的键
func phpstanRule(identifier string) string {
	if identifier != "" {
		return identifier
	}
	return "phpstan-error"
}

// phpstanSeverity 按标识判断严重级别
//
// PHPStan 本身不给严重级别，它认为每个报出的问题都值得修。
// 这里按标识分类，调用与类型错误（method.notFound、argument.type 一类）
// 会让程序在运行时出错，报 error，风格与冗余类报 info
func phpstanSeverity(identifier string) Severity {
	// 未知标识时保守报 warning，而不是当成干净
	if identifier == "" {
		return SeverityWarning
	}
	for _, prefix := range phpstanInfoPrefixes {
		if strings.HasPrefix(identifier, prefix) {
			return SeverityInfo
		}
	}
	return SeverityError
}

// phpstanInfoPrefixes 是不指向真实缺陷的标识前缀
//
// 不在表里的标识一律落到 error，判成风格问题会悄悄降级，
// 判成错误只是多看一眼，后者更安全
var phpstanInfoPrefixes = []string{
	"missingType",
	"deprecated",
	"unused",
	"redundant",
	"deadCode",
	"class.implements",
	// 变量始终为真/假这类提示，不影响运行
	"booleanAnd.always",
	"booleanOr.always",
	"if.always",
}

// phpstanDocURL 给稳定标识拼出文档地址，PHPStan 的规则文档按标识命名
func phpstanDocURL(identifier string) string {
	if identifier == "" {
		return ""
	}
	// 标识形如 argument.type，文档路径就是 /error-identifiers/argument.type
	return "https://phpstan.org/error-identifiers/" + identifier
}

// trimToJSON 去掉 JSON 之前的输出，PHPStan 偶尔会在前面打印 warning，
// 直接 Unmarshal 会失败
func trimToJSON(out []byte) []byte {
	if i := strings.Index(string(out), "{"); i > 0 {
		return out[i:]
	}
	return out
}

// 编译期断言
var _ Analyzer = (*PhpstanAnalyzer)(nil)
