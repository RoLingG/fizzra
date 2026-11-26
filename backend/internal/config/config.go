package config

import (
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// Config  Fizzra 的根配置
//
// 加载顺序（后者覆盖前者）：
//   - 内置默认值
//   - YAML 文件（--config、./fizzra.yaml、~/.fizzra/config.yaml）
//   - 环境变量（FIZZRA_*）
//   - 显式命令行参数
type Config struct {
	Server    ServerConfig    `yaml:"server"`
	Database  DatabaseConfig  `yaml:"database"`
	Workspace WorkspaceConfig `yaml:"workspace"`
	Scan      ScanConfig      `yaml:"scan"`
	Quality   QualityConfig   `yaml:"quality"`
	Rules     []RuleConfig    `yaml:"rules"`

	// Source 记录配置从哪里加载，供 /api/v1/system/config 使用
	Source string `yaml:"-"`
	// warnings 保存被修正而非致命的问题
	warnings []Warning `yaml:"-"`
}

type ServerConfig struct {
	Host string `yaml:"host"`
	Port int    `yaml:"port"`
	// Mode 取值之一：debug、release、test
	Mode string `yaml:"mode"`
	// CORSOrigins 开发用前端允许的来源列表
	CORSOrigins []string `yaml:"corsOrigins"`
}

type DatabaseConfig struct {
	// DSN 是 SQLite 文件路径（或 ":memory:"），相对路径按工作区目录解析
	DSN string `yaml:"dsn"`
	// AutoMigrate 在启动时创建或更新表结构
	AutoMigrate bool `yaml:"autoMigrate"`
}

type WorkspaceConfig struct {
	// Root 存放克隆下来的仓库和报告的目录
	Root string `yaml:"root"`
}

type ScanConfig struct {
	// Concurrency 同时执行的分析数
	Concurrency int `yaml:"concurrency"`
	// Timeout 单次分析的墙上时间预算
	Timeout time.Duration `yaml:"timeout"`
	// PerToolTimeout 单个外部工具的墙上时间预算
	PerToolTimeout time.Duration `yaml:"perToolTimeout"`
	// EnabledAnalyzers 限定分析器注册表的范围，为空表示全部
	EnabledAnalyzers []string `yaml:"enabledAnalyzers"`
	// AutoInstall 允许 Fizzra 为缺失的工具尝试 `go install`
	// 默认关闭，它会往 GOPATH 里写东西，而且很慢
	AutoInstall bool `yaml:"autoInstall"`
	// MaxIssues 限制单次分析持久化的问题数量，保护数据库不被病态项目拖垮
	MaxIssues int `yaml:"maxIssues"`

	// Offline 阻止分析器访问网络
	//
	// 两个漏洞扫描器需要它，cargo-audit 和 govulncheck 要取各自的通告库，而在
	// 连不上 GitHub 的机器上，这个取不会快速失败，会一直卡到 socket 超时，
	// 把两秒的扫描变成好几分钟，然后报不出任何有用的东西
	//
	// 置位后，能靠本地数据库工作的分析器照常工作，不能的则带原因跳过，而不是卡住
	Offline bool `yaml:"offline"`

	// AnalyzerOptions 保存按分析器名索引的逐分析器设置
	//
	// 用一个大口袋而不是每个开关一个字段，工具集会增长，每个都带来自己的旋钮，
	// 而一个每当工具新增选项就得改的结构体，就是个会过时的结构体。取值用字符串，
	// 这样拼错会变成启动错误而不是静默的零值
	AnalyzerOptions map[string]map[string]string `yaml:"analyzerOptions"`
}

// AnalyzerOption 返回某个逐分析器设置，未设置时返回 fallback
//
// 查找顺序是分析器自己的选项，然后是全局 Offline 标志，那是唯一有全局等价物的键。
// 把回退逻辑放在这里而不是每个调用点，「离线」这个含义在整个工具里就只有一种
func (c *Config) AnalyzerOption(analyzerName, key, fallback string) string {
	if opts, ok := c.Scan.AnalyzerOptions[analyzerName]; ok {
		if value, ok := opts[key]; ok && value != "" {
			return value
		}
	}
	if key == "offline" {
		if c.Scan.Offline {
			return "true"
		}
		return "false"
	}
	return fallback
}

// AnalyzerFlag 把分析器选项解释成布尔值
//
// 除了明确的 true/false 之外任何内容都是配置错误：把拼写错误静默读成 false，
// 正是设置看起来开着实际却关着的原因
func (c *Config) AnalyzerFlag(analyzerName, key string, fallback bool) (bool, error) {
	raw := c.AnalyzerOption(analyzerName, key, "")
	if raw == "" {
		return fallback, nil
	}
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "true", "yes", "on", "1":
		return true, nil
	case "false", "no", "off", "0":
		return false, nil
	}
	return fallback, fmt.Errorf("scan.analyzerOptions.%s.%s: %q is not a boolean", analyzerName, key, raw)
}

type QualityConfig struct {
	// MinScore 整体质量评分的通过阈值
	MinScore float64 `yaml:"minScore"`
	// MaxCritical 与 MaxHigh 是门禁容忍的计数上限
	MaxCritical int `yaml:"maxCritical"`
	MaxHigh     int `yaml:"maxHigh"`
	// MaxComplexity 在最差圈复杂度超过它时让门禁不通过
	MaxComplexity int `yaml:"maxComplexity"`
	// MinCoverage 按测试覆盖率百分比做门禁（0 表示关闭该检查）
	MinCoverage float64 `yaml:"minCoverage"`
}

// RuleConfig 对应 model.Rule，但声明在这里，配置因此不依赖别的包
type RuleConfig struct {
	Name     string   `yaml:"name"`
	Analyzer string   `yaml:"analyzer"`
	Severity string   `yaml:"severity"`
	Message  string   `yaml:"message"`
	Patterns []string `yaml:"patterns"`
	Paths    []string `yaml:"paths"`
	Exclude  []string `yaml:"exclude"`
	Advice   string   `yaml:"advice"`
	Enabled  *bool    `yaml:"enabled"`
	Builtin  bool     `yaml:"builtin"`
	// 作用域修饰符，与 model.Rule 一致
	// 没有它们，写在配置里的规则就无法表达内置规则赖以规避误报的循环作用域限定
	//
	// 用 *bool 是为了区分「没写」和「显式写 false」，普通 bool 的零值把两者
	// 混为一谈，用户便无法用 requireLoop: false 关掉内置规则的循环限定
	RequireLoop  *bool `yaml:"requireLoop"`
	ForbidInLoop *bool `yaml:"forbidInLoop"`
}

// Default 返回没有任何其他配置时使用的出厂配置
func Default() *Config {
	home, _ := os.UserHomeDir()
	root := filepath.Join(home, ".fizzra")
	if home == "" {
		// 没有 HOME（容器、精简 CI 镜像）时 UserHomeDir 返回空串，起点会变成
		// 相对路径 ".fizzra"，再经 normalizePaths 拼接就叠成
		// ".fizzra/workspace/.fizzra/fizzra.db"。落到绝对路径避免二次拼接
		if abs, err := filepath.Abs(".fizzra"); err == nil {
			root = abs
		} else {
			root = ".fizzra"
		}
	}

	return &Config{
		Server: ServerConfig{
			Host:        "127.0.0.1",
			Port:        8080,
			Mode:        "release",
			CORSOrigins: []string{"http://localhost:5173", "http://127.0.0.1:5173"},
		},
		Database: DatabaseConfig{
			DSN:         filepath.Join(root, "fizzra.db"),
			AutoMigrate: true,
		},
		Workspace: WorkspaceConfig{
			Root: filepath.Join(root, "workspace"),
		},
		Scan: ScanConfig{
			Concurrency:    2,
			Timeout:        10 * time.Minute,
			PerToolTimeout: 3 * time.Minute,
			AutoInstall:    false,
			MaxIssues:      5000,
		},
		Quality: QualityConfig{
			MinScore:      70,
			MaxCritical:   0,
			MaxHigh:       0,
			MaxComplexity: 20,
			MinCoverage:   0,
		},
		Source: "defaults",
	}
}

// Load 从所给路径读取配置。路径为空时走标准搜索顺序。文件缺失不算错误，除非是
// 显式指定的路径
//
// 未知键是硬错误。配置静默失效是最糟的结果，用户以为自己的门禁在生效，实际没有。
// 见 validate.go
func Load(path string) (*Config, error) {
	cfg := Default()

	resolved, explicit, err := resolvePath(path)
	if err != nil {
		return nil, err
	}
	if resolved != "" {
		raw, err := os.ReadFile(resolved)
		if err != nil {
			return nil, fmt.Errorf("read config %s: %w", resolved, err)
		}

		// 一次解析，之后在节点树上原地归一与校验，行号因此始终指向原文件
		doc, err := parseYAMLNode(raw)
		if err != nil {
			return nil, fmt.Errorf("config %s: %w", resolved, err)
		}

		var problems []error
		if doc != nil {
			problems = append(normaliseAliasesNode(doc), validateNode(doc)...)
		}
		if len(problems) > 0 {
			msgs := make([]string, 0, len(problems))
			for _, p := range problems {
				msgs = append(msgs, p.Error())
			}
			return nil, fmt.Errorf("config %s has %d problem(s):\n  - %s",
				resolved, len(problems), strings.Join(msgs, "\n  - "))
		}

		if doc != nil {
			if err := doc.Decode(cfg); err != nil {
				return nil, fmt.Errorf("parse config %s: %w", resolved, err)
			}
		}
		cfg.Source = resolved
	} else if explicit {
		return nil, fmt.Errorf("config file %s not found", path)
	}

	// applyEnv 先于 normalize，因为 normalize 要修正 env 给出的值。normalize
	// 首行会清空 warnings 以保证自身幂等，因此 env 阶段的告警要在它之后再并回去
	envWarnings := applyEnv(cfg)
	if err := cfg.normalize(); err != nil {
		return nil, err
	}
	cfg.warnings = append(cfg.warnings, envWarnings...)
	return cfg, nil
}

// alias 把一个被接受的备选键名映射到规范键名
//
// 原始规范文档写的是 `quality: { coverage, criticalIssue, highSecurity,
// maxComplexity }`。这些名字继续可用，这样一份按规范写的文件会照它看起来的
// 样子生效，而不是直接报错
var aliases = map[string]string{
	"quality.coverage":      "quality.minCoverage",
	"quality.criticalIssue": "quality.maxCritical",
	"quality.highSecurity":  "quality.maxHigh",
}

// 规则条目内部接受的规范拼法
var ruleAliases = map[string]string{
	"level":   "severity",
	"pattern": "patterns",
}

// normaliseAliasesNode 把别名键改写成规范拼写，返回冲突错误
//
// 直接在 YAML 节点树上原地改写，不重编码成字节流，这样注释与行号保持指向原文，
// 后续校验错误引用的行号才是用户能照着找到的那一行
//
// 同一映射里别名与规范键并存是冲突而不是覆盖，两个键指向同一项设置，用户无从
// 判断哪一行生效，因此报错要求只保留一个
func normaliseAliasesNode(doc *yaml.Node) []error {
	if doc == nil || len(doc.Content) == 0 {
		return nil
	}

	var problems []error

	// ruleSection 标记「我们正处在 rules 列表的某一项里」
	const ruleSection = "rules[]"
	visited := map[*yaml.Node]bool{}

	var walk func(node *yaml.Node, prefix string)
	walk = func(node *yaml.Node, prefix string) {
		switch node.Kind {
		case yaml.DocumentNode:
			for _, child := range node.Content {
				walk(child, prefix)
			}

		case yaml.SequenceNode:
			// rules 下面是序列时，每个元素就是一条规则条目
			childPrefix := prefix
			if prefix == "rules" {
				childPrefix = ruleSection
			}
			for _, child := range node.Content {
				walk(child, childPrefix)
			}

		case yaml.MappingNode:
			inRule := prefix == ruleSection
			// 归一后本层出现过的键，用来发现「别名和规范键都写了」
			seen := make(map[string]*yaml.Node, len(node.Content)/2)
			for i := 0; i+1 < len(node.Content); i += 2 {
				keyNode, valNode := node.Content[i], node.Content[i+1]

				var canonical string
				if inRule {
					if c, ok := ruleAliases[keyNode.Value]; ok {
						canonical = c
					}
				} else {
					path := keyNode.Value
					if prefix != "" {
						path = prefix + "." + keyNode.Value
					}
					if c, ok := aliases[path]; ok {
						canonical = lastSegment(c)
					}
				}

				// 别名改写后的键名才是本层真正的键。冲突既可能是别名排在已写的
				// 规范键之后，也可能是规范键排在别名之后，两种都要抓
				finalName := keyNode.Value
				if canonical != "" {
					finalName = canonical
				}
				if prev, dup := seen[finalName]; dup {
					problems = append(problems, fmt.Errorf(
						"line %d: key %q conflicts with %q on line %d — they mean the same setting; keep only one",
						keyNode.Line, keyNode.Value, prev.Value, prev.Line))
					continue
				}
				keyNode.Value = finalName

				seen[keyNode.Value] = keyNode

				// 规则里的 pattern 允许写成单个标量，解码成 []string 前先升维
				if inRule && keyNode.Value == "patterns" && isScalarValue(valNode) {
					wrapped := &yaml.Node{
						Kind:    yaml.SequenceNode,
						Tag:     "!!seq",
						Line:    valNode.Line,
						Column:  valNode.Column,
						Content: []*yaml.Node{valNode},
					}
					node.Content[i+1] = wrapped
					continue
				}

				if !inRule {
					path := keyNode.Value
					if prefix != "" {
						path = prefix + "." + keyNode.Value
					}
					walk(valNode, path)
				}
			}

		case yaml.AliasNode:
			// 别名指向的内容也要归一，否则 `server: *x` 里的 level 不会被改写成 severity
			if node.Alias != nil && !visited[node.Alias] {
				visited[node.Alias] = true
				walk(node.Alias, prefix)
			}
		}
	}
	walk(doc, "")

	return problems
}

// isScalarValue 报告节点是否是一个有值的标量（null 不算，它表示「没写」）
func isScalarValue(node *yaml.Node) bool {
	return node.Kind == yaml.ScalarNode && node.Tag != "!!null"
}

// resolvePath 返回要加载的配置文件。explicit 为真表示调用方点名了一个路径，
// 那种情况下文件缺失就是错误
func resolvePath(path string) (resolved string, explicit bool, err error) {
	if path != "" {
		abs, err := filepath.Abs(path)
		if err != nil {
			return "", true, err
		}
		info, err := os.Stat(abs)
		if err != nil {
			return "", true, err
		}
		// 目录能通过 Stat，随后 ReadFile 才报错，用户拿到的是一条读文件错误
		// 而不是「你把目录当配置文件传了」
		if info.IsDir() {
			return "", true, fmt.Errorf("config path %s is a directory", abs)
		}
		return abs, true, nil
	}

	candidates := []string{"fizzra.yaml", "fizzra.yml"}
	if home, err := os.UserHomeDir(); err == nil {
		candidates = append(candidates,
			filepath.Join(home, ".fizzra", "config.yaml"),
			filepath.Join(home, ".fizzra", "config.yml"),
		)
	}
	for _, c := range candidates {
		if abs, err := filepath.Abs(c); err == nil {
			// 同名目录会被 Stat 命中，接着 ReadFile 失败，整个加载就断了。
			// 隐式扫描本该跳过它继续找下一个候选
			if info, err := os.Stat(abs); err == nil && !info.IsDir() {
				return abs, false, nil
			}
		}
	}
	return "", false, nil
}

// applyEnv 用环境变量覆盖配置，返回这一阶段发现的、被忽略的问题
//
// 非法值不覆盖已经合法的配置，也不静默丢弃。一个被忽略的 FIZZRA_PORT 如果不留
// 告警，用户会以为端口已经改了
func applyEnv(cfg *Config) []Warning {
	var warnings []Warning
	warn := func(field, format string, args ...any) {
		warnings = append(warnings, Warning{Field: field, Message: fmt.Sprintf(format, args...)})
	}

	if v := os.Getenv("FIZZRA_HOST"); v != "" {
		cfg.Server.Host = v
	}
	if v := os.Getenv("FIZZRA_PORT"); v != "" {
		if p, err := parseStrictInt(v); err == nil && p >= 1 && p <= 65535 {
			cfg.Server.Port = p
		} else if err != nil {
			warn("FIZZRA_PORT", "%q is not a number; keeping %d", v, cfg.Server.Port)
		} else {
			warn("FIZZRA_PORT", "%d is not a valid port; keeping %d", p, cfg.Server.Port)
		}
	}
	if v := os.Getenv("FIZZRA_MODE"); v != "" {
		cfg.Server.Mode = v
	}
	if v := os.Getenv("FIZZRA_DB_DSN"); v != "" {
		cfg.Database.DSN = v
	}
	if v := os.Getenv("FIZZRA_WORKSPACE"); v != "" {
		cfg.Workspace.Root = v
	}
	if v := os.Getenv("FIZZRA_CONCURRENCY"); v != "" {
		if n, err := parseStrictInt(v); err == nil && n > 0 {
			cfg.Scan.Concurrency = n
		} else if err != nil {
			warn("FIZZRA_CONCURRENCY", "%q is not a number; keeping %d", v, cfg.Scan.Concurrency)
		} else {
			warn("FIZZRA_CONCURRENCY", "%d must be positive; keeping %d", n, cfg.Scan.Concurrency)
		}
	}
	return warnings
}

// Warning 一个被修正而非致命的问题
type Warning struct {
	Field   string
	Message string
}

func (w Warning) String() string {
	return fmt.Sprintf("%s: %s", w.Field, w.Message)
}

// Warnings 返回归一过程中发现的问题
func (c *Config) Warnings() []Warning { return c.warnings }

func (c *Config) warn(field, format string, args ...any) {
	c.warnings = append(c.warnings, Warning{
		Field:   field,
		Message: fmt.Sprintf(format, args...),
	})
}

// normalize 为未设置的值填入默认值，并修复越界的值
//
// 它只报告自己修不了的问题。凡是能夹取的一律记成警告而不是错误
func (c *Config) normalize() error {
	c.warnings = nil
	def := Default()
	c.normalizeServer(def.Server)
	c.normalizePaths(def.Workspace)
	c.normalizeScan(def.Scan)
	c.normalizeQuality(def.Quality)
	c.normalizeRules()
	return nil
}

func (c *Config) normalizeServer(def ServerConfig) {
	if c.Server.Host == "" {
		c.Server.Host = def.Host
	}
	if c.Server.Port <= 0 || c.Server.Port > 65535 {
		if c.Server.Port != 0 {
			c.warn("server.port", "%d is not a valid port; using %d", c.Server.Port, def.Port)
		}
		c.Server.Port = def.Port
	}

	if c.Server.Mode == "" {
		c.Server.Mode = def.Mode
	}
	switch c.Server.Mode {
	case "debug", "release", "test":
	default:
		c.warn("server.mode", "%q is not one of debug, release, test; using %q", c.Server.Mode, def.Mode)
		c.Server.Mode = def.Mode
	}
}

func (c *Config) normalizePaths(def WorkspaceConfig) {
	if c.Workspace.Root == "" {
		c.Workspace.Root = def.Root
	}
	if c.Database.DSN == "" {
		c.Database.DSN = filepath.Join(c.Workspace.Root, "..", "fizzra.db")
	} else if !filepath.IsAbs(c.Database.DSN) && !IsSQLiteURIDSN(c.Database.DSN) {
		c.Database.DSN = filepath.Join(c.Workspace.Root, c.Database.DSN)
	}
}

// IsSQLiteURIDSN 报告该 DSN 是否由驱动直接解释、不该被当成文件路径处理
//
// 配置层的路径拼接和存储层的建库目录判断共用这一条规则。两处各写一份时，
// 边界差一个字符就会出现「配置层认作 URI、存储层当成路径去建目录」的分歧
func IsSQLiteURIDSN(dsn string) bool {
	return dsn == ":memory:" || strings.HasPrefix(dsn, "file:")
}

func (c *Config) normalizeScan(def ScanConfig) {
	// 负数是被写坏的配置，要告警。0 表示「没写」，按零值填充，与 normalizeServer
	// 对 Port == 0 的处理一致
	if c.Scan.Concurrency < 0 {
		c.warn("scan.concurrency", "%d must not be negative; using %d", c.Scan.Concurrency, def.Concurrency)
	}
	if c.Scan.Concurrency <= 0 {
		c.Scan.Concurrency = def.Concurrency
	}

	if c.Scan.Timeout < 0 {
		c.warn("scan.timeout", "%s must not be negative; using %s", c.Scan.Timeout, def.Timeout)
	}
	if c.Scan.Timeout <= 0 {
		c.Scan.Timeout = def.Timeout
	}

	if c.Scan.PerToolTimeout < 0 {
		c.warn("scan.perToolTimeout", "%s must not be negative; using %s", c.Scan.PerToolTimeout, def.PerToolTimeout)
	}
	if c.Scan.PerToolTimeout <= 0 {
		c.Scan.PerToolTimeout = def.PerToolTimeout
	}

	if c.Scan.MaxIssues < 0 {
		c.warn("scan.maxIssues", "%d must not be negative; using %d", c.Scan.MaxIssues, def.MaxIssues)
	}
	if c.Scan.MaxIssues <= 0 {
		c.Scan.MaxIssues = def.MaxIssues
	}
}

func (c *Config) normalizeQuality(def QualityConfig) {
	c.Quality.MinScore = clampAndWarn(c, "quality.minScore", c.Quality.MinScore, 0, 100, def.MinScore)
	c.Quality.MinCoverage = clampAndWarn(c, "quality.minCoverage", c.Quality.MinCoverage, 0, 100, def.MinCoverage)
	c.Quality.MaxCritical = clampIntAndWarn(c, "quality.maxCritical", c.Quality.MaxCritical, 0, 1000, def.MaxCritical)
	c.Quality.MaxHigh = clampIntAndWarn(c, "quality.maxHigh", c.Quality.MaxHigh, 0, 1000, def.MaxHigh)
	c.Quality.MaxComplexity = clampIntAndWarn(c, "quality.maxComplexity", c.Quality.MaxComplexity, 0, 1000, def.MaxComplexity)
}

func (c *Config) normalizeRules() {
	if len(c.Rules) == 0 {
		return
	}
	kept := c.Rules[:0]
	for i, r := range c.Rules {
		if strings.TrimSpace(r.Name) == "" {
			c.warn(fmt.Sprintf("rules[%d]", i), "rule has no name and was ignored")
			continue
		}
		kept = append(kept, r)
	}
	c.Rules = kept
}

// clampAndWarn 校验浮点数在范围内，越界时退回 fallback 并记一条警告
func clampAndWarn(c *Config, field string, value, lo, hi, fallback float64) float64 {
	if value >= lo && value <= hi {
		return value
	}
	c.warn(field, "%.1f is outside %g-%g; using %.1f", value, lo, hi, fallback)
	return fallback
}

// clampIntAndWarn 校验整数在范围内，越界时退回 fallback 并记一条警告
func clampIntAndWarn(c *Config, field string, value, lo, hi, fallback int) int {
	if value >= lo && value <= hi {
		return value
	}
	c.warn(field, "%d is outside %d-%d; using %d", value, lo, hi, fallback)
	return fallback
}

// Addr  HTTP 服务绑定的 host:port
func (c *Config) Addr() string {
	return net.JoinHostPort(c.Server.Host, strconv.Itoa(c.Server.Port))
}

func parsePort(s string) (int, error) {
	return parseStrictInt(s)
}

// parseStrictInt 严格解析整数字串，拒绝尾部夹带非数字字符的输入
//
// 环境变量里出现 "8080abc" 这种值几乎总是手滑，解析出一个前缀数字就当作有效会
// 让用户以为设置已生效，因此整串必须是合法整数
func parseStrictInt(s string) (int, error) {
	return strconv.Atoi(strings.TrimSpace(s))
}
