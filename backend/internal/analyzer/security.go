package analyzer

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"

	"strconv"
	"strings"
)

// ---------------------------------------------------------------------------
// gosec
// ---------------------------------------------------------------------------

// GosecAnalyzer 通过 gosec 做安全方向的静态分析
type GosecAnalyzer struct{ goOnly }

// NewGosecAnalyzer 构造该分析器
func NewGosecAnalyzer() Analyzer { return &GosecAnalyzer{} }

func (a *GosecAnalyzer) Name() string { return "gosec" }

func (a *GosecAnalyzer) Description() string {
	return "Runs gosec: SQL/command injection, insecure crypto, hard-coded credentials, permissive file permissions and other OWASP-relevant Go patterns."
}

func (a *GosecAnalyzer) Category() string   { return "security" }
func (a *GosecAnalyzer) Requires() []string { return []string{"gosec"} }

// gosecJSON 对应 `gosec -fmt=json -quiet` 的输出
type gosecJSON struct {
	Issues []struct {
		Severity   string `json:"severity"`
		Confidence string `json:"confidence"`
		CWE        struct {
			ID  string `json:"id"`
			URL string `json:"url"`
		} `json:"cwe"`
		RuleID  string `json:"rule_id"`
		Details string `json:"details"`
		File    string `json:"file"`
		// Code 是 gosec 自带的上下文块，这里刻意不解析
		//
		// 它不是命中所在的那一行，而是 gosec 截取的周围一段，每行还带上了行号，
		// 所以单行命中会变成一个多行块，多行命中则是整个范围
		//
		// 页面上这块内容挂在「源码」标题下，含义就是该位置的代码。而起点早于
		// 报告行的窗口，每一条命中都会违背这个含义，且靠裁剪修不好，因为无法
		// 判断块内哪一行才是命中行，读文件才能正确确定
		Code string `json:"code"`
		// Line 是字符串，多行命中时是范围（如 "492-508"）而不是数字，
		// 详见 parseGosecLine
		Line   string `json:"line"`
		Column string `json:"column"`
	} `json:"Issues"`
	Errors []struct {
		Err string `json:"err"`
	} `json:"GosecErrors"`
}

func (a *GosecAnalyzer) Analyze(ctx context.Context, projectPath string) ([]Issue, error) {
	return nil, nil
}

func (a *GosecAnalyzer) AnalyzeWithContext(actx *Context) ([]Issue, error) {
	if _, err := actx.Runner.LookPath("gosec"); err != nil {
		return nil, ErrToolUnavailable("gosec")
	}
	if !actx.Files.Exists("go.mod") {
		return nil, ErrToolUnavailable("gosec (no go.mod in project root)")
	}

	// -quiet 会抑制 "N files scanned" 这行输出，否则 JSON 会被破坏
	out, err := actx.Runner.Run(actx.Context, actx.ProjectPath,
		"gosec", "-fmt=json", "-quiet", "-nosec-tag", "nosec", "./...")
	if err != nil {
		if IsTimeout(err) {
			return nil, err
		}
		if len(out) == 0 {
			return nil, fmt.Errorf("gosec failed: %w", err)
		}
		// 退出码非零但仍有输出，说明发现已经产生了，不算失败
		actx.Log.Debug("gosec exited non-zero with output", "err", err)
	}

	var report gosecJSON
	if err := json.Unmarshal(out, &report); err != nil {
		return nil, fmt.Errorf("parse gosec output: %w", err)
	}

	var issues []Issue
	snippets := NewSnippetReader(actx.Files)
	for _, it := range report.Issues {
		line := parseGosecLine(it.Line)
		col, _ := strconv.Atoi(strings.TrimSpace(it.Column))
		rel := filepath.ToSlash(it.File)

		issues = append(issues, Issue{
			Analyzer: a.Name(),
			Rule:     it.RuleID,
			Severity: mapGosecSeverity(it.Severity, it.RuleID),
			Message:  strings.TrimSpace(it.Details),
			Advice:   adviceForGosec(it.RuleID),
			File:     rel,
			Line:     line,
			Column:   col,
			Code:     snippets.Line(rel, line),
			DocURL:   gosecDocURL(it.RuleID, it.CWE.URL),
		})
	}

	// 把工具层面的错误也暴露出来，否则部分可用的扫描会被当成完全正常
	for _, e := range report.Errors {
		if strings.TrimSpace(e.Err) != "" {
			actx.Log.Warn("gosec reported an internal error", "err", e.Err)
		}
	}
	return issues, nil
}

// parseGosecLine 解析 gosec 的 line 字段，它不总是数字
//
// 多行命中时 gosec 把这个跨度报成范围（如 "492-508"），直接转整数会失败，
// 让发现的行号留成 0。这不是少了个细节，而是给了个错的，发现照样列出、照样
// 计入评分，却指向不了任何位置，读者无从下手。取范围起点即可，那是被标记的
// 结构开始的地方，也是源码片段应该读的那一行
func parseGosecLine(raw string) int {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return 0
	}
	// 范围，取起点
	if i := strings.IndexByte(raw, '-'); i > 0 {
		raw = strings.TrimSpace(raw[:i])
	}
	line, err := strconv.Atoi(raw)
	if err != nil {
		return 0
	}
	return line
}

// mapGosecSeverity 把 gosec 的 LOW/MEDIUM/HIGH 归一到本项目的等级
//
// gosec 的 HIGH 是它自己的最高档，对注入和凭据这两类规则映射为 critical，
// 其余映射为 error，因为权限类规则报出的 HIGH 和 SQL 注入不是一回事
func mapGosecSeverity(raw, ruleID string) string {
	switch strings.ToUpper(strings.TrimSpace(raw)) {
	case "HIGH":
		if criticalGosecRules[ruleID] {
			return SeverityCritical
		}
		return SeverityError
	case "MEDIUM":
		return SeverityWarning
	case "LOW":
		return SeverityInfo
	default:
		return SeverityWarning
	}
}

var criticalGosecRules = map[string]bool{
	"G101": true, // 硬编码凭据
	"G102": true, // 绑定所有网卡
	"G103": true, // unsafe 块
	"G104": true, // 未处理的错误
	"G201": true, // 用 fmt 拼接 SQL
	"G202": true, // SQL 字符串拼接
	"G204": true, // 变量作为子进程参数
	"G304": true, // 来自不可信输入的文件路径
	"G401": true, // 弱加密原语
	"G402": true, // 不安全的 TLS 配置
	"G404": true, // 用 math/rand 做安全用途
	"G501": true, // 引入 md5
	"G502": true, // 引入 des
	"G505": true, // 引入 sha1
}

func adviceForGosec(ruleID string) string {
	switch ruleID {
	case "G101":
		return "Move the value into an environment variable or secret manager, then rotate the exposed credential."
	case "G104":
		return "Check the error. If it genuinely cannot fail, assign to _ and add a comment explaining why."
	case "G201", "G202":
		return "Use parameterised queries. Pass values as bind arguments, never as SQL text."
	case "G204":
		return "Pass arguments as an argv slice rather than a shell string, and validate anything user supplied."
	case "G304":
		return "Resolve the path with filepath.Clean and verify it stays under the allowed base directory."
	case "G401", "G501", "G502", "G505":
		return "Replace with SHA-256/512, AES-GCM or bcrypt/argon2 depending on the use case."
	case "G402":
		return "Never disable certificate verification; configure RootCAs for private CAs instead."
	case "G404":
		return "Use crypto/rand for anything security relevant."
	case "G306":
		return "Use 0600 for files containing data, 0755 only for executables and directories."
	}
	return ""
}

func gosecDocURL(ruleID, cweURL string) string {
	if cweURL != "" {
		return cweURL
	}
	if ruleID == "" {
		return ""
	}
	return "https://github.com/securego/gosec#available-rules"
}

// ---------------------------------------------------------------------------
// govulncheck
// ---------------------------------------------------------------------------

// VulnerabilityAnalyzer 接入 govulncheck
//
// govulncheck 是最慢、也最依赖网络的分析器，它要查询 Go 漏洞库。离线模式下
// 直接跳过而不运行，因为它没有绕开网络的办法，详见 AnalyzeWithContext 的说明
type VulnerabilityAnalyzer struct{ goOnly }

// NewVulnerabilityAnalyzer 构造该分析器
func NewVulnerabilityAnalyzer() Analyzer { return &VulnerabilityAnalyzer{} }

func (a *VulnerabilityAnalyzer) Name() string { return "govulncheck" }

func (a *VulnerabilityAnalyzer) Description() string {
	return "Runs govulncheck against the Go vulnerability database to find reachable CVEs in module dependencies."
}

func (a *VulnerabilityAnalyzer) Category() string   { return "vulnerability" }
func (a *VulnerabilityAnalyzer) Requires() []string { return []string{"govulncheck"} }

// Options 暴露该分析器的离线开关
//
// 虽然全局配置 scan.offline 也有同样效果，这里仍要声明，因为面板按这份列表
// 渲染控件，不写就没法在界面上单独关掉它的网络访问。更麻烦的是
// SetAnalyzerOptions 恰好按这份列表做校验，不声明的话这个选项会被当成未知
// 选项静默拒绝。配置文件接受、API 却拒绝同一个键，会让用户觉得他当时没在看
// 的那一侧有 bug
func (a *VulnerabilityAnalyzer) Options() []DescriptorOption {
	return []DescriptorOption{{
		Key:     "offline",
		Default: false,
		Description: "Skip govulncheck, which must reach the Go vulnerability database. " +
			"There is no local-database mode, so offline means skipping rather than degrading. " +
			"The global `offline` setting has the same effect on every analyzer.",
	}}
}

// vulnEntry 是 govulncheck JSON 流中的一条记录
type vulnEntry struct {
	OSV struct {
		ID       string   `json:"id"`
		Summary  string   `json:"summary"`
		Details  string   `json:"details"`
		Aliases  []string `json:"aliases"`
		Affected []struct {
			Package struct {
				Name string `json:"name"`
			} `json:"package"`
		} `json:"affected"`
	} `json:"osv"`
	// Finding 只在 finding 记录上出现
	Finding *struct {
		OSV   string `json:"osv"`
		Trace []struct {
			Module   string `json:"module"`
			Version  string `json:"version"`
			Package  string `json:"package"`
			Function string `json:"function"`
			Position *struct {
				Filename string `json:"filename"`
				Line     int    `json:"line"`
				Column   int    `json:"column"`
			} `json:"position"`
		} `json:"trace"`
	} `json:"finding"`
	// Config 在 config 记录上携带顶层模块信息
	Config *struct {
		ProtocolVersion string `json:"protocol_version"`
	} `json:"config"`
}

func (a *VulnerabilityAnalyzer) Analyze(ctx context.Context, projectPath string) ([]Issue, error) {
	return nil, nil
}

func (a *VulnerabilityAnalyzer) AnalyzeWithContext(actx *Context) ([]Issue, error) {
	if _, err := actx.Runner.LookPath("govulncheck"); err != nil {
		return nil, ErrToolUnavailable("govulncheck")
	}
	if !actx.Files.Exists("go.mod") {
		return nil, ErrToolUnavailable("govulncheck (no go.mod in project root)")
	}

	// 离线时跳过而不是运行
	//
	// govulncheck 没有「用已有数据、不联网」的开关，它唯一的网络参数 -db 指向另一个
	// 数据库，而不是避开数据库。连不上 vuln.go.dev 时它会一直阻塞到 socket 超时，
	// 所以离线只能跳过。跳过会被单独记录，不会被误读成「没有发现漏洞」
	//
	// 顺序要紧：这段放在工具存在性检查之后，未安装的工具因此仍报未安装，而不是离线
	if actx.Offline || actx.OptionBool(a.Name(), "offline", false) {
		return nil, ErrNothingToCheck("offline mode: govulncheck needs network access to the Go vulnerability database")
	}

	out, err := actx.Runner.Run(actx.Context, actx.ProjectPath, "govulncheck", "-json", "./...")
	if err != nil && len(out) == 0 {
		if IsTimeout(err) {
			// 保留网络相关的提示，对 govulncheck 来说超时基本都是取数据库，
			// 知道这点才能判断该提高超时上限还是该开离线模式。时长来自错误
			// 本身，它知道实际生效的预算
			return nil, fmt.Errorf("%w (it needs network access to the vulnerability database)", err)
		}
		return nil, fmt.Errorf("govulncheck failed: %w", err)
	}

	return a.parse(actx, out)
}

// parse 解析 govulncheck 的按行分隔的 JSON 流
//
// 该格式是流而不是数组，且发现通过 ID 引用 OSV 记录，所以边解析边记下 OSV
func (a *VulnerabilityAnalyzer) parse(actx *Context, raw []byte) ([]Issue, error) {
	osvSummary := map[string]string{}
	osvAliases := map[string][]string{}
	var findings []vulnEntry

	for _, line := range splitLines(string(raw)) {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var entry vulnEntry
		if err := json.Unmarshal([]byte(line), &entry); err != nil {
			continue // tolerate the occasional non-JSON banner line
		}
		if entry.OSV.ID != "" {
			osvSummary[entry.OSV.ID] = firstNonEmpty(entry.OSV.Summary, entry.OSV.Details)
			osvAliases[entry.OSV.ID] = entry.OSV.Aliases
		}
		if entry.Finding != nil {
			findings = append(findings, entry)
		}
	}

	issues := make([]Issue, 0, len(findings))
	for _, f := range findings {
		id := f.Finding.OSV
		msg := firstNonEmpty(osvSummary[id], "known vulnerability "+id)
		if aliases := osvAliases[id]; len(aliases) > 0 {
			msg += " (" + strings.Join(aliases, ", ") + ")"
		}

		// trace 的最后一帧是本模块内的调用点，前面的帧是依赖链，
		// 优先取带位置的帧
		trace := f.Finding.Trace
		module, version := "", ""
		file, line, col := "", 0, 0
		if len(trace) > 0 {
			first := trace[0]
			module, version = first.Module, first.Version
			// 从后往前找第一个带位置的帧，它最深
			for i := len(trace) - 1; i >= 0; i-- {
				if trace[i].Position != nil {
					file = filepath.ToSlash(trace[i].Position.Filename)
					line = trace[i].Position.Line
					col = trace[i].Position.Column
					break
				}
			}
		}
		if module != "" {
			msg = fmt.Sprintf("[%s@%s] %s", module, version, msg)
		}

		// 可触达的发现（在本模块内有调用点）比模块级的通告紧急得多
		severity := SeverityWarning
		if file != "" {
			severity = SeverityError
		}
		if isCriticalVuln(osvAliases[id]) {
			severity = SeverityCritical
		}

		issues = append(issues, Issue{
			Analyzer: a.Name(),
			Rule:     id,
			Severity: severity,
			Message:  msg,
			Advice:   vulnAdvice(module, version, file),
			File:     file,
			Line:     line,
			Column:   col,
			Code:     "",
			DocURL:   "https://pkg.go.dev/vuln/" + id,
		})
	}
	return issues, nil
}

func isCriticalVuln(aliases []string) bool {
	for _, a := range aliases {
		if strings.HasPrefix(a, "CVE-") {
			// 仅凭别名判断不了 CVSS，一律返回 false 而不是猜，避免虚报
			return false
		}
	}
	return false
}

func vulnAdvice(module, version, file string) string {
	if module == "" {
		return "Update the affected dependency."
	}
	base := fmt.Sprintf("Upgrade %s (currently %s) to a fixed version.", module, version)
	if file != "" {
		return base + " The vulnerable function is reachable from " + file + ", so this is exploitable in practice."
	}
	return base + " No reachable call site was found in this module, so exposure is limited — still worth updating."
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}
