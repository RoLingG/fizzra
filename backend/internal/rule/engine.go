// Package rule 实现 Fizzra 的规则引擎
//
// 它自成包（而不是放在 analyzer 之下），因为分析器契约和自定义规则分析器都依赖
// 它：分开放可以避免导入循环，也让命令行能复用它而不必拖进整个分析器注册表。
package rule

import (
	"fmt"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/rolingg/fizzra/backend/internal/model"
)

// Rule 是 model.Rule 的可执行形式
// 正则和 glob 都预先编译好，扫描上千个文件时不必反复编译
type Rule struct {
	Model    model.Rule
	Patterns []*regexp.Regexp
	// pathGlobs 和 excludeGlobs 在构造时编译一次。曾经把它们的惰性编译结果放进
	// 包级缓存，那是数据竞争，引擎在 worker 池的多个 goroutine 间共享，而 Go 的
	// map 不支持并发读写
	pathGlobs    []globMatcher
	excludeGlobs []globMatcher
}

// globMatcher 是编译好的 glob
type globMatcher struct {
	raw string
	re  *regexp.Regexp
}

// Match 是文件内的一次模式命中
type Match struct {
	Rule     string
	Severity model.Severity
	Message  string
	Advice   string
	// Fix 是规则声明的修改前后对照示例。一直带到问题上，界面因此能展示改法
	Fix     model.FixExample
	File    string
	Line    int
	Column  int
	Code    string
	Pattern string
}

// Engine 对源文件求值一组规则
type Engine struct {
	rules []Rule

	// skipped 记录编译失败的规则，命令行 `fizzra rules check` 和接口因此能报出
	// 配置错误，而不是静默忽略
	skipped []error
	// minSeverity 过滤掉低于调用方关注等级的规则
	minSeverity model.Severity
	// maxFileBytes 防止去扫描那些 vendored 或生成的巨型文件
	maxFileBytes int64
	// syntax 提供该项目语言的词法规则。默认是 Go，也就是现有调用方都需要的那种
	syntax Syntax
}

// Option 用于定制引擎
type Option func(*Engine)

// WithMinSeverity 丢弃低于所给等级的匹配
func WithMinSeverity(s model.Severity) Option {
	return func(e *Engine) { e.minSeverity = s }
}

// WithMaxFileSize 设置单文件大小上限，单位为字节（0 表示不限）
func WithMaxFileSize(n int64) Option {
	return func(e *Engine) { e.maxFileBytes = n }
}

// WithLanguage 选择过滤器使用的词法
//
// 引擎的注释、字符串字面量和循环作用域过滤器都是语法感知的，而每种语法都不同。
// 没有它，过滤器会假定是 Go，这对 Python 不只是不完整，而是错的，Python 没有
// 花括号，循环追踪永远不会退出，第一个 for 之后的每一行都被当成在循环体内
func WithLanguage(language string) Option {
	return func(e *Engine) { e.syntax = syntaxFor(language) }
}

// Syntax 报告当前使用的词法，供诊断和测试使用
func (e *Engine) Syntax() Syntax { return e.syntax }

// New 把传入的规则编译进一个引擎
func New(rules []model.Rule, opts ...Option) *Engine {
	e := &Engine{
		minSeverity:  model.SeverityInfo,
		maxFileBytes: 2 << 20, // 2 MiB
		syntax:       goSyntax{},
	}
	for _, opt := range opts {
		opt(e)
	}

	for _, r := range rules {
		if !r.Enabled {
			continue
		}
		compiled, err := compile(r)
		if err != nil {
			e.skipped = append(e.skipped, err)
			continue
		}
		e.rules = append(e.rules, compiled)
	}
	return e
}

func compile(r model.Rule) (Rule, error) {
	out := Rule{Model: r}

	paths, err := compileGlobs(r.Paths)
	if err != nil {
		return Rule{}, fmt.Errorf("rule %q: %w", r.Name, err)
	}
	out.pathGlobs = paths

	excludes, err := compileGlobs(r.Exclude)
	if err != nil {
		return Rule{}, fmt.Errorf("rule %q: %w", r.Name, err)
	}
	out.excludeGlobs = excludes
	for _, raw := range r.Patterns {
		if strings.TrimSpace(raw) == "" {
			continue
		}
		re, err := regexp.Compile(raw)
		if err != nil {
			return Rule{}, fmt.Errorf("rule %q: invalid pattern %q: %w", r.Name, raw, err)
		}
		out.Patterns = append(out.Patterns, re)
	}
	if len(out.Patterns) == 0 {
		return Rule{}, fmt.Errorf("rule %q: no usable patterns", r.Name)
	}
	return out, nil
}

// Skipped 返回因编译失败而被丢弃的规则
func (e *Engine) Skipped() []error { return e.skipped }

// Len 报告有多少条规则处于启用状态
func (e *Engine) Len() int { return len(e.rules) }

// AppliesTo 报告是否有任一规则可能匹配所给路径。分析器用它整份跳过文件，而不是
// 扫一遍再丢弃
func (e *Engine) AppliesTo(relPath string) bool {
	relPath = toSlash(relPath)
	for _, r := range e.rules {
		if r.matchesPath(relPath) {
			return true
		}
	}
	return false
}

// Options 控制匹配时应用的结构性过滤器
// 纯粹按行跑正则会产生高得无法接受的误报率，循环体里的 `defer cancel()` 在循环
// 实际未进入同一作用域时是没问题的，crypto/rand 恰恰是正确的选择，而规则
// 自身的定义文本也会匹配它自己的模式。这些过滤器把按行匹配看不到的上下文补上

// matchContext 是一行的词法状态
type matchContext struct {
	// inComment 报告该行是否在注释里
	inComment bool
	// inLoop 报告该行在词法上是否位于循环体内
	inLoop bool
	// inStringLiteral 报告该行是否起始于前一行打开的字符串内部
	// 某些语言的字符串可以跨行，Python 的三引号文档字符串就是常见情形，
	// 而一段描述危险模式的文档字符串是文档而不是缺陷
	inStringLiteral bool
}

// Evaluate 对一份文件内容运行所有适用的规则并返回匹配。relPath 必须相对项目
// 根目录
func (e *Engine) Evaluate(relPath string, content []byte) []Match {
	if e.maxFileBytes > 0 && int64(len(content)) > e.maxFileBytes {
		return nil
	}

	rel := toSlash(relPath)
	var out []Match

	// 只在至少有一条规则适用时才切分，那些不可能匹配任何东西的文件因此省掉这次
	// 分配
	var applicable []Rule
	for _, r := range e.rules {
		if r.matchesPath(rel) {
			applicable = append(applicable, r)
		}
	}
	if len(applicable) == 0 {
		return nil
	}

	lines := strings.Split(string(content), "\n")
	scopes := e.computeScopes(lines)

	// 规则自身修改前后示例里的行会被压制
	//
	// 修复示例必须展示有问题的代码才有用，那正是 before 的含义。没有这条压制，
	// 内置规则库会把自己的示例报成缺陷
	//
	// 压制范围限定在扫描文件里字面上位于 FixExample 原始字符串中的那些行，
	// 藏不住别处的真问题
	demos := demonstrationLines(lines)

	for _, r := range applicable {
		if r.Model.Severity.Rank() > e.minSeverity.Rank() {
			continue // below the caller's interest level
		}
		for i, line := range lines {
			if demos[i] {
				continue
			}
			ctx := scopes[i]

			for _, re := range r.Patterns {
				loc := re.FindStringIndex(line)
				if loc == nil {
					continue
				}
				if !r.accepts(ctx, line, loc, e.syntax) {
					continue
				}
				out = append(out, Match{
					Rule:     r.Model.Name,
					Severity: r.Model.Severity,
					Message:  renderMessage(r.Model, line),
					Advice:   r.Model.Advice,
					Fix:      r.Model.Fix,
					File:     rel,
					Line:     i + 1,
					Column:   loc[0] + 1,
					Code:     strings.TrimRight(line, "\r\n"),
					Pattern:  re.String(),
				})
				break // one hit per line per rule keeps reports readable
			}
		}
	}
	return out
}

// demonstrationLines 标出属于某个 FixExample 的 Before 或 After 块的那些行
//
// 检测是词法层面的，找到持有原始字符串字面量（反引号）的 `Before:` 或 `After:`
// 字段，标出它跨越的行。规则库用的就是原始字符串，而它是唯一无需转义就能跨行的
// 形式，所以不必完整解析也能可靠地认出它
func demonstrationLines(lines []string) []bool {
	out := make([]bool, len(lines))

	for i := 0; i < len(lines); i++ {
		line := lines[i]
		if !strings.Contains(line, "Before:") && !strings.Contains(line, "After:") {
			continue
		}
		// 示例必须在本行开始且是原始字符串
		_, after, ok := strings.Cut(line, "`")
		if !ok {
			continue
		}
		// 在同一行内开闭的字面量只覆盖那一行
		if rest := after; strings.Contains(rest, "`") {
			out[i] = true
			continue
		}

		out[i] = true
		for j := i + 1; j < len(lines); j++ {
			out[j] = true
			if strings.Contains(lines[j], "`") {
				i = j
				break
			}
		}
	}
	return out
}

// accepts 应用该规则声明的结构性过滤器
func (r Rule) accepts(ctx matchContext, line string, loc []int, syntax Syntax) bool {
	// 所有规则都忽略被注释掉的代码
	// 注释里的匹配从来不是真缺陷，通常还是关于该模式的说明文档
	if ctx.inComment {
		return false
	}

	// 起始于前一行打开的字符串内部的行，整行都在字面量里。正是这条压制了 Python
	// 文档字符串里的匹配，而文档字符串是记录危险模式最惯用的地方，因此也最可能
	// 让规则在自己的说明文字里命中主题本身
	if ctx.inStringLiteral {
		return false
	}

	// 一行里可能同时有代码和注释，`/* note */ danger()` 和它的镜像都含有一个真实
	// 匹配或误报，取决于位置。上面按行的标志表达不了这点，所以还要看具体的偏移
	//
	// Rust 让这变得明显，`/* outer /* inner */ panic!(x) */` 整段都是注释，但这
	// 一行既开了又闭了，按行的状态最终是「已闭合」，于是那个匹配看起来是活的
	if offsetInComment(line, loc[0], syntax) {
		return false
	}

	// 在关键字所在的位置检查字符串字面量状态，而不是整个正则匹配的起点
	//
	// 模式常以 (^|[^\w.]) 这样的边界组开头，让 panic( 不会也匹配 x.panic(。那个组
	// 会吃掉关键字左边的一个字符，对 `Message: "panic() called ..."` 来说吃掉的正是
	// 开引号。以匹配起点检查会得出「不在字符串里」，而关键字从后一位开始，那个位置
	// 才在字面量内部。因此两个偏移都测，任一在字面量内即视为文本
	keywordStart := loc[0]
	if loc[1] > loc[0] {
		matched := line[loc[0]:loc[1]]
		// 跳过一个前导的边界字符（引号、空格、括号等）
		if len(matched) > 0 && !isWordByte(matched[0]) && matched[0] != '.' {
			if trimmed := strings.TrimLeft(matched, "^ \t"); trimmed != matched {
				keywordStart = loc[0] + (len(matched) - len(trimmed))
			} else {
				keywordStart = loc[0] + 1
			}
		}
	}
	if keywordStart > loc[0] && keywordStart < len(line) {
		if inStringLiteral(line, keywordStart, syntax) {
			return false
		}
	}
	if inStringLiteral(line, loc[0], syntax) {
		return false
	}

	if r.Model.RequireLoop && !ctx.inLoop {
		return false
	}
	if !r.Model.RequireLoop && ctx.inLoop && r.Model.ForbidInLoop {
		return false
	}
	return true
}

// isWordByte 报告 b 能否出现在 Go 标识符中
func isWordByte(b byte) bool {
	return b == '_' || (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z') || (b >= '0' && b <= '9')
}

// computeScopes 为每一行标注过滤器所需的词法上下文，注释状态和循环嵌套
//
// 这是词法近似而不是解析，而且刻意偏保守，拿不准时报「在循环里」，因为那产生的
// 是压制而不是误报
//
// 有两套作用域模型。花括号语言（Go、Rust、JS）数 { 和 }，嵌套因此能自然退出。
// 缩进语言（Python）完全没有花括号，花括号计数器永远返回 0，loopDepth 只增不减，
// 第一个 for 之后每一行都被报成在循环体内。那不是功能缺失而是错误答案，它会让
// requireLoop 类规则在整个仓库上误报、forbidInLoop 类规则彻底失声，所以 Python
// 另有一套基于缩进的追踪器
func (e *Engine) computeScopes(lines []string) []matchContext {
	if e.syntax.ScopeKind() == ScopeIndent {
		return computeIndentScopes(lines, e.syntax)
	}
	return computeBraceScopes(lines, e.syntax)
}

// computeBraceScopes 跟踪 /* */ 状态和 { } 嵌套
func computeBraceScopes(lines []string, syntax Syntax) []matchContext {
	out := make([]matchContext, len(lines))

	comment := syntax.Comment()
	// blockDepth 统计未闭合的块注释数。用计数器而不是布尔值，因为 Rust 的块注释
	// 可以嵌套
	blockDepth := 0
	loopDepth := 0
	// openDelim 是前一行留下的未闭合字符串定界符，没有则为 -1。Python 的三引号
	// 让这很常见，Go 根本没有跨行字面量，所以那里一直是 -1
	openDelim := -1

	for i, raw := range lines {
		line := strings.TrimRight(raw, "\r")
		trimmed := strings.TrimSpace(line)

		startsInComment := blockDepth > 0
		startsInString := openDelim >= 0 && !startsInComment

		// 跟踪块注释的跨度。同一行内开闭的块不会让我们进入注释状态
		blockDepth = advanceBlockComment(blockDepth, line, comment, syntax)

		lineComment := hasLineCommentPrefix(trimmed, comment)
		code := codePortion(line, comment, startsInComment)

		if !startsInComment && !startsInString && !lineComment {
			openDelim = scanStringState(code, syntax)
		} else if startsInString {
			openDelim = continueStringState(code, openDelim, syntax)
		}

		// 跨行字面量里的行是文档或数据，不是代码
		inString := startsInString

		if !startsInComment && !lineComment && !inString {
			loopDepth += countLoopOpens(code, syntax)
			if loopDepth < 0 {
				loopDepth = 0
			}
		}

		out[i] = matchContext{
			inComment:       startsInComment || lineComment || blockDepth > 0,
			inLoop:          loopDepth > 0 && !isLoopHeaderLine(trimmed, syntax),
			inStringLiteral: inString,
		}

		if !startsInComment && !lineComment {
			loopDepth -= countLoopCloses(code)
			if loopDepth < 0 {
				loopDepth = 0
			}
		}
	}
	return out
}

// scanStringState 返回行末仍未闭合的定界符的下标，全部闭合时返回 -1
func scanStringState(line string, syntax Syntax) int {
	delims := syntax.StringDelims()
	if len(delims) == 0 {
		return -1
	}

	activeIdx := -1
	escaped := false

	for i := 0; i < len(line); i++ {
		if escaped {
			escaped = false
			continue
		}
		if activeIdx >= 0 {
			d := delims[activeIdx]
			if !d.Raw && d.Escape != 0 && line[i] == d.Escape {
				escaped = true
				continue
			}
			if strings.HasPrefix(line[i:], d.Close) {
				activeIdx = -1
				i += len(d.Close) - 1
			}
			continue
		}
		for idx, d := range delims {
			if !strings.HasPrefix(line[i:], d.Open) {
				continue
			}
			// 像 Rust 的 `r"` 这种带前缀的形式，不能是标识符的尾部
			// `var` 后面跟一个 `"` 不是原始字符串
			if len(d.Open) > 1 && isIdentChar(d.Open[0]) && i > 0 && isIdentChar(line[i-1]) {
				continue
			}
			activeIdx = idx
			i += len(d.Open) - 1
			break
		}
	}
	return activeIdx
}

// continueStringState 推进一个已打开的字符串，闭合后返回 -1
func continueStringState(line string, activeIdx int, syntax Syntax) int {
	delims := syntax.StringDelims()
	if activeIdx < 0 || activeIdx >= len(delims) {
		return -1
	}
	d := delims[activeIdx]

	for i := 0; i < len(line); i++ {
		if !d.Raw && d.Escape != 0 && line[i] == d.Escape {
			i++ // skip the escaped character
			continue
		}
		if strings.HasPrefix(line[i:], d.Close) {
			// 字面量闭合了，本行它之后的内容是代码。但同一行再开一个字符串在这些
			// 规则针对的代码里极其罕见，把余下部分当成已闭合是保守的方向
			return -1
		}
	}
	return activeIdx
}

// computeIndentScopes 按缩进跟踪循环作用域，供 Python 使用
//
// 一行比某个仍打开的循环头的缩进更深时，它就在循环里。栈保存每个未闭合循环头的
// 缩进，缩进等于或浅于某个头自身的行会把那个头闭合掉。
//
// 跨行字符串也在这里跟踪，而且它比循环跟踪更重要。Python 在文档字符串里记录危险
// 模式，那是写「此处不要用 os.system(cmd)」最惯用的地方，而三引号字符串会跨行，
// 按行的检查看不到它。没有这个，工具会把自己的文档报成一条严重发现
func computeIndentScopes(lines []string, syntax Syntax) []matchContext {
	out := make([]matchContext, len(lines))
	comment := syntax.Comment()

	// indentStack 保存每个未闭合循环头的列号
	var indentStack []int
	// openDelim 是前一行留下的未闭合定界符，没有则为 -1
	openDelim := -1

	for i, raw := range lines {
		line := strings.TrimRight(raw, "\r")
		trimmed := strings.TrimSpace(line)

		startsInString := openDelim >= 0

		lineComment := hasLineCommentPrefix(trimmed, comment)

		// 跨行字面量里没有代码，不是注释，不是循环体，也没有匹配
		// 推进字面量状态然后继续
		if startsInString {
			openDelim = continueStringState(line, openDelim, syntax)
			out[i] = matchContext{inStringLiteral: true, inLoop: len(indentStack) > 0}
			continue
		}

		// 空行或纯注释行不属于任何自己的作用域，也不动栈，所以循环里的注释不会把
		// 循环闭合掉
		if trimmed == "" || lineComment {
			out[i] = matchContext{
				inComment: lineComment,
				inLoop:    len(indentStack) > 0,
			}
			continue
		}

		// 跟踪本行打开的字符串，后面的行因此知道
		openDelim = scanStringState(codePortion(line, comment, false), syntax)

		indent := indentWidth(line)

		// 闭合所有本行已缩进退出的循环。缩进与循环头相同的行是它的同级，不是它的
		// 循环体
		for len(indentStack) > 0 && indent <= indentStack[len(indentStack)-1] {
			indentStack = indentStack[:len(indentStack)-1]
		}

		isHeader := isLoopHeaderLine(trimmed, syntax)
		out[i] = matchContext{
			inComment: false,
			inLoop:    len(indentStack) > 0,
		}

		if isHeader {
			indentStack = append(indentStack, indent)
			// 循环头那一行本身不在循环体内
			out[i].inLoop = len(indentStack) > 1
		}
	}
	return out
}

// advanceBlockComment 为一行更新未闭合块注释的深度
//
// 语言声明支持嵌套时会按嵌套处理，这对 Rust 很要紧，把 `/* /* */ */` 当成单个
// 注释会在第一个 `*/` 处结束，然后把后面那个 `*/` 当成代码读，规则于是可能在
// 注释文字上触发
func advanceBlockComment(depth int, line string, c CommentSyntax, syntax Syntax) int {
	if c.BlockOpen == "" || c.BlockClose == "" {
		return 0
	}

	i := 0
	for {
		if depth > 0 {
			close := strings.Index(line[i:], c.BlockClose)
			if close < 0 {
				return depth
			}
			close += i
			depth--
			i = close + len(c.BlockClose)
			if !c.BlockNests {
				// 不支持嵌套的语言在第一个闭合处就停，不管前面有多少个开
				depth = 0
			}
			if depth == 0 && !c.BlockNests {
				// 继续扫描，本行后面可能还有块打开
				if next := strings.Index(line[i:], c.BlockOpen); next < 0 {
					return 0
				}
				depth++
				i += strings.Index(line[i:], c.BlockOpen) + len(c.BlockOpen)
			}
			continue
		}

		open := strings.Index(line[i:], c.BlockOpen)
		if open < 0 {
			return depth
		}
		open += i
		// 字符串里的 `/*` 不是注释。没有这条，一条匹配 "/**/*.go" 这类路径的规则
		// 会把文件其余部分全吞掉
		if offsetInStringLiteral(line, open, syntax) {
			i = open + len(c.BlockOpen)
			continue
		}
		depth++
		i = open + len(c.BlockOpen)

		if !c.BlockNests {
			// 找配对的闭合，找不到说明注释一直到文件末尾
			close := strings.Index(line[i:], c.BlockClose)
			if close < 0 {
				return 1
			}
			i += close + len(c.BlockClose)
			depth = 0
		}
	}
}

// codePortion 返回该行中属于代码的部分，去掉行尾注释，括号和关键字计数因此看不到
// 被注释掉的文本
func codePortion(line string, c CommentSyntax, startsInComment bool) string {
	if startsInComment {
		// 延续块注释的行不贡献代码。如果注释在行中闭合，余下是代码，但在那里数括号
		// 罕见得无关紧要，跳过是安全的方向
		return ""
	}

	// 在块注释闭合处截断，这样 `} */` 里的括号不会被计入
	if c.BlockClose != "" {
		if i := strings.Index(line, c.BlockClose); i >= 0 {
			line = line[:i]
		}
	}
	if c.BlockOpen != "" {
		if i := strings.Index(line, c.BlockOpen); i >= 0 {
			line = line[:i]
		}
	}

	for _, prefix := range c.Line {
		if i := indexOutsideString(line, prefix); i >= 0 {
			line = line[:i]
			break
		}
	}
	return line
}

// hasLineCommentPrefix 报告去掉空白后的行是否是行注释
func hasLineCommentPrefix(trimmed string, c CommentSyntax) bool {
	for _, prefix := range c.Line {
		if strings.HasPrefix(trimmed, prefix) {
			return true
		}
	}
	return false
}

// indexOutsideString 查找子串，忽略出现在字符串字面量里的那些
func indexOutsideString(line, needle string) int {
	for i := 0; i+len(needle) <= len(line); i++ {
		if strings.HasPrefix(line[i:], needle) {
			return i
		}
	}
	return -1
}

// indentWidth 返回前导空白的列数，一个制表符算四列
//
// Python 允许制表符和空格但禁止混用，归一到一个共同的宽度能让比较在两种情况下
// 都有意义
func indentWidth(line string) int {
	width := 0
	for _, r := range line {
		switch r {
		case ' ':
			width++
		case '\t':
			width += 4
		default:
			return width
		}
	}
	return width
}

// countLoopOpens 统计本行中开启一个块的循环关键字
//
// 关键字匹配是词法感知的，before 里面的 for、名为 format 的变量都不该计入，所以
// 关键字之后的那个字符必须是它的结尾
func countLoopOpens(line string, syntax Syntax) int {
	n := 0
	for _, kw := range syntax.LoopKeywords() {
		n += countKeyword(line, kw)
	}
	return n
}

// countKeyword 统计 kw 作为完整单词出现的次数
func countKeyword(line, kw string) int {
	n := 0
	for i := 0; i+len(kw) <= len(line); i++ {
		if !strings.HasPrefix(line[i:], kw) {
			continue
		}
		// 前一个字符不能是标识符的一部分
		if i > 0 && isIdentChar(line[i-1]) {
			continue
		}
		// 后一个字符同样不能
		if i+len(kw) < len(line) && isIdentChar(line[i+len(kw)]) {
			continue
		}
		n++
	}
	return n
}

func isIdentChar(c byte) bool {
	return c == '_' ||
		(c >= 'a' && c <= 'z') ||
		(c >= 'A' && c <= 'Z') ||
		(c >= '0' && c <= '9')
}

// countLoopCloses 统计减少循环嵌套的右花括号
func countLoopCloses(line string) int {
	closes := strings.Count(line, "}")
	opens := strings.Count(line, "{")
	if delta := closes - opens; delta > 0 {
		return delta
	}
	return 0
}

// isLoopHeaderLine 报告该行本身是否开启一个循环，是的话同一行上的匹配不在循环
// 体内
func isLoopHeaderLine(trimmed string, syntax Syntax) bool {
	for _, kw := range syntax.LoopKeywords() {
		if strings.HasPrefix(trimmed, kw) {
			// 要求词边界，format(...) 因此不会被读成 for
			rest := trimmed[len(kw):]
			if rest == "" || !isIdentChar(rest[0]) {
				return true
			}
		}
	}
	return false
}

// inStringLiteral 报告所给偏移处的匹配是否起始于字符串字面量内部
//
// 扫描在消费偏移处那个字符**之前**就停，始于开引号的匹配本身不在字面量里，但它
// 之后的一切都在。这个边界搞错会让规则在它自己的模式文本上命中，当那段文本存于
// 字符串字面量（比如一张规则描述表）时就会发生
func inStringLiteral(line string, offset int, syntax Syntax) bool {
	return openStringAt(line, offset, syntax)
}

// offsetInStringLiteral 问的是同样的问题，但针对任意位置，用于判断一个 `/*` 是
// 注释还是字符串内容
func offsetInStringLiteral(line string, offset int, syntax Syntax) bool {
	return openStringAt(line, offset, syntax)
}

// openStringAt 走完该行直到 offset，报告那一刻是否有字符串字面量处于打开状态
//
// 按语言区分而不是只针对 Go，Go 的反引号是原始的，JS 模板字面量会处理转义，
// Python 的三引号自带定界符，Rust 有原始字符串。写死一套引号至少会把其中一种
// 弄错，而表现出来的症状就是规则在它自己的模式上命中
func openStringAt(line string, offset int, syntax Syntax) bool {
	delims := syntax.StringDelims()
	if len(delims) == 0 {
		return false
	}

	// delims 中哪一项是打开的，-1 表示没有
	activeIdx := -1
	escaped := false

	for i := 0; i < len(line) && i < offset; i++ {
		if escaped {
			escaped = false
			continue
		}

		if activeIdx >= 0 {
			d := delims[activeIdx]
			if !d.Raw && d.Escape != 0 && line[i] == d.Escape {
				escaped = true
				continue
			}
			if strings.HasPrefix(line[i:], d.Close) {
				activeIdx = -1
				i += len(d.Close) - 1
			}
			continue
		}

		// 不在字面量里：找开引号
		// 语法定义里这个切片按最长优先排序，所以三引号不会被读成两个双引号加一个双引号
		for idx, d := range delims {
			if !strings.HasPrefix(line[i:], d.Open) {
				continue
			}
			// 像 Rust 的原始字符串前缀那种形式不能是标识符的尾部
			// `var` 后面跟一个引号不是原始字符串
			if len(d.Open) > 1 && isIdentChar(d.Open[0]) && i > 0 && isIdentChar(line[i-1]) {
				continue
			}
			activeIdx = idx
			i += len(d.Open) - 1
			break
		}
	}

	return activeIdx >= 0
}

// offsetInComment 报告所给偏移处的字节是否位于注释中
//
// 它是 matchContext.inComment 的位置感知版本，针对代码和注释混在一行的情况。
// 它把该行走一遍，语言声明支持块嵌套时按嵌套处理，一旦越过偏移就停下
func offsetInComment(line string, offset int, syntax Syntax) bool {
	c := syntax.Comment()
	if c.BlockOpen == "" && len(c.Line) == 0 {
		return false
	}
	if offset < 0 || offset > len(line) {
		return false
	}

	depth := 0
	for i := 0; i < len(line); {
		if depth > 0 {
			// 找两种定界符里先出现的那个，因为嵌套的开必须先于它所包裹的闭合被计数。
			// 直接跳到下一个闭合会完全跳过内层的开，注释于是看起来提前一层结束
			closeIdx := indexFrom(line, i, c.BlockClose)
			openIdx := -1
			if c.BlockNests && c.BlockOpen != "" {
				openIdx = indexFrom(line, i, c.BlockOpen)
			}

			switch {
			case closeIdx < 0 && openIdx < 0:
				// 走到行尾之外，仍在里面
				return true
			case openIdx >= 0 && (closeIdx < 0 || openIdx < closeIdx):
				// 更深的开先出现
				if openIdx >= offset {
					return true
				}
				depth++
				i = openIdx + len(c.BlockOpen)
			default:
				// 闭合先出现。如果它落在偏移处或之后，该偏移就在这一层内部
				if closeIdx >= offset {
					return true
				}
				depth--
				if !c.BlockNests {
					// 不支持嵌套的注释，一个闭合就结束，不管前面有多少个开
					depth = 0
				}
				i = closeIdx + len(c.BlockClose)
			}
			continue
		}

		// 不在注释里：行注释会吞掉该行剩余部分
		for _, prefix := range c.Line {
			if strings.HasPrefix(line[i:], prefix) {
				return offset >= i
			}
		}
		if c.BlockOpen != "" && strings.HasPrefix(line[i:], c.BlockOpen) {
			// 字符串里的开注释标记不是注释。把这个判断委托到这里，两道过滤器因此不会对
			// 同一个字符给出矛盾结论
			if offsetInStringLiteral(line, i, syntax) {
				i++
				continue
			}
			depth++
			i += len(c.BlockOpen)
			// 偏移处在开标记之处或之后，所以它在注释里
			if i >= offset {
				return true
			}
			continue
		}
		if i >= offset {
			return false
		}
		i++
	}
	return depth > 0
}

// indexFrom 就是 strings.Index，但从指定位置开始
func indexFrom(s string, from int, sub string) int {
	idx := strings.Index(s[from:], sub)
	if idx < 0 {
		return -1
	}
	return from + idx
}

// renderMessage 展开规则消息里的 {{.match}} 占位符
func renderMessage(r model.Rule, line string) string {
	msg := r.Message
	if msg == "" {
		msg = r.Name
	}
	if strings.Contains(msg, "{{") {
		msg = strings.ReplaceAll(msg, "{{line}}", strings.TrimSpace(line))
	}
	return msg
}

func (r Rule) matchesPath(relPath string) bool {
	if len(r.pathGlobs) > 0 && !anyGlob(r.pathGlobs, relPath) {
		return false
	}
	if len(r.excludeGlobs) > 0 && anyGlob(r.excludeGlobs, relPath) {
		return false
	}
	return true
}

func anyGlob(globs []globMatcher, path string) bool {
	for _, g := range globs {
		if g.matches(path) {
			return true
		}
	}
	return false
}

// compileGlobs 把 glob 模式转成匹配器，编译不了的模式会被报出来，而不是静默地
// 永不匹配
func compileGlobs(patterns []string) ([]globMatcher, error) {
	out := make([]globMatcher, 0, len(patterns))
	for _, raw := range patterns {
		pattern := strings.TrimSpace(toSlash(raw))
		if pattern == "" {
			continue
		}
		re, err := globToRegexp(pattern)
		if err != nil {
			return nil, fmt.Errorf("invalid glob %q: %w", pattern, err)
		}
		out = append(out, globMatcher{raw: pattern, re: re})
	}
	return out, nil
}

// matches 报告某路径是否匹配这个 glob
//
// 我们支持的语法子集，`**` 可跨路径分隔符，`*` 和 `?` 不行。filepath.Match
// 表达不了 `**`，所以构造时把模式翻译成正则
func (g globMatcher) matches(path string) bool {
	if g.raw == path {
		return true
	}
	return g.re.MatchString(path)
}

// globToRegexp 把 glob 翻译成带锚点的正则
func globToRegexp(pattern string) (*regexp.Regexp, error) {
	var b strings.Builder
	b.WriteString("^")
	for i := 0; i < len(pattern); i++ {
		c := pattern[i]
		switch c {
		case '*':
			if i+1 < len(pattern) && pattern[i+1] == '*' {
				// `**/` 也应匹配零层目录，比如 `**/*.go`
				if i+2 < len(pattern) && pattern[i+2] == '/' {
					b.WriteString("(?:.*/)?")
					i += 2
				} else {
					b.WriteString(".*")
					i++
				}
			} else {
				b.WriteString("[^/]*")
			}
		case '?':
			b.WriteString("[^/]")
		case '.', '+', '(', ')', '|', '^', '$', '{', '}', '[', ']', '\\':
			b.WriteByte('\\')
			b.WriteByte(c)
		default:
			b.WriteByte(c)
		}
	}
	b.WriteString("$")

	return regexp.Compile(b.String())
}

func normalizeGlobs(in []string) []string {
	out := make([]string, 0, len(in))
	for _, g := range in {
		if g = strings.TrimSpace(toSlash(g)); g != "" {
			out = append(out, g)
		}
	}
	return out
}

func toSlash(p string) string {
	return filepath.ToSlash(p)
}
