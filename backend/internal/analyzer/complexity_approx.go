package analyzer

import (
	"strings"
)

// 无 AST 语言的近似圈复杂度
//
// Go 走 parser + AST，其余语言没有可用的解析器，所以这里做词法层面的近似，
// 剥掉注释和字符串字面量之后，按语言的判定点表计数。字符串剥离是必需的，
// 消息里的 "if (x)" 会让计数据偏高
//
// 近似意味着它在结构上是错的时候也会给出一个数字，所以调用方要用
// Estimated 标出来，界面据此说明这是估算值

// complexityRules 描述一门语言的判定点与函数头形状
type complexityRules struct {
	// decision 是每个都会增加一个判定点的关键字
	decision []string
	// conditions 是同样算判定点的运算符
	conditions []string
	// caseLabels 为真时，switch/case 的每个 case 计一次
	caseLabels bool
	// catchCounts 为真时 catch 计一次（与 if 类似）
	catchCounts bool
	// funcHeader 判断一行是否是函数定义的开始
	funcHeader func(trimmed string) bool
	// hashComment 为真时 `#` 也起始行注释，PHP 与 Python 都如此
	hashComment bool
}

// complexityRulesFor 返回某语言的近似规则
func complexityRulesFor(language string) complexityRules {
	switch language {
	case modelLanguageJava, modelLanguageCSharp:
		return complexityRules{
			decision:    []string{"if", "for", "while"},
			conditions:  []string{"&&", "||", "?"},
			caseLabels:  true,
			catchCounts: true,
			funcHeader:  javaLikeFuncHeader,
		}
	case modelLanguageCpp:
		return complexityRules{
			decision:   []string{"if", "for", "while"},
			conditions: []string{"&&", "||", "?"},
			caseLabels: true,
			funcHeader: cppFuncHeader,
		}
	case modelLanguagePHP:
		// PHP 有 elseif/switch/match，布尔运算符写法与 C 系一致
		return complexityRules{
			decision:    []string{"if", "elseif", "for", "foreach", "while", "catch"},
			conditions:  []string{"&&", "||", "?"},
			caseLabels:  true,
			catchCounts: false, // catch 已在 decision 里，避免重复计数
			funcHeader:  phpFuncHeader,
			hashComment: true,
		}
	}
	return complexityRules{}
}

// approxComplexity 在源码上近似统计一个文件里的复杂度
//
// 返回的每个条目是 {name, line, complexity}。无法识别函数边界时退化为整文件
// 一个条目，名字用文件名
func approxComplexity(rel string, src string, rules complexityRules) []approxFunction {
	if len(rules.decision) == 0 {
		return nil
	}

	lines := splitLines(stripCommentsAndStrings(src, rules))
	var out []approxFunction

	var current *approxFunction
	depth := 0
	sawOpen := false

	for i, raw := range lines {
		trimmed := strings.TrimSpace(raw)
		if trimmed == "" {
			continue
		}

		// 还没在函数里，且这一行看起来是函数头
		if current == nil && rules.funcHeader(trimmed) {
			current = &approxFunction{Name: guessFunctionName(trimmed), Line: i + 1, Complexity: 1}
			depth = 0
			sawOpen = false
		}

		if current == nil {
			continue
		}

		current.Complexity += countDecisions(trimmed, rules)

		opens := strings.Count(trimmed, "{")
		closes := strings.Count(trimmed, "}")
		if opens > 0 {
			sawOpen = true
		}
		depth += opens - closes

		// 函数体结束：见过左大括号且深度回到零
		if sawOpen && depth <= 0 {
			current.Length = i + 1 - current.Line + 1
			out = append(out, *current)
			current = nil
		}
	}

	if current != nil {
		current.Length = len(lines) - current.Line + 1
		out = append(out, *current)
	}
	if len(out) == 0 {
		out = append(out, approxFunction{
			Name:       rel,
			Line:       1,
			Complexity: countFileDecisions(lines, rules),
			Length:     len(lines),
		})
	}
	return out
}

// approxFunction 是一个近似识别出的函数
type approxFunction struct {
	Name       string
	Line       int
	Length     int
	Complexity int
}

// stripCommentsAndStrings 去掉注释并把字符串字面量替换成空串
//
// 只保留代码骨架，判定点计数因此不会把注释和消息文本算进去
func stripCommentsAndStrings(src string, rules complexityRules) string {
	var b strings.Builder
	b.Grow(len(src))

	inLineComment := false
	inBlockComment := false
	var quote byte
	escaped := false

	for i := 0; i < len(src); i++ {
		c := src[i]

		if inLineComment {
			if c == '\n' {
				inLineComment = false
				b.WriteByte(c)
			}
			continue
		}
		if inBlockComment {
			if c == '*' && i+1 < len(src) && src[i+1] == '/' {
				inBlockComment = false
				i++
			} else if c == '\n' {
				b.WriteByte('\n')
			}
			continue
		}
		if quote != 0 {
			if escaped {
				escaped = false
				continue
			}
			if c == '\\' {
				escaped = true
				continue
			}
			if c == quote {
				quote = 0
				b.WriteByte(' ')
			}
			continue
		}

		switch {
		case c == '/' && i+1 < len(src) && src[i+1] == '/':
			inLineComment = true
			i++
		case c == '/' && i+1 < len(src) && src[i+1] == '*':
			inBlockComment = true
			i++
		case rules.hashComment && c == '#':
			inLineComment = true
		case c == '"' || c == '\'':
			quote = c
			b.WriteByte(' ')
		default:
			b.WriteByte(c)
		}
	}
	return b.String()
}

// countDecisions 统计一行里出现的判定点
func countDecisions(line string, rules complexityRules) int {
	n := 0
	for _, kw := range rules.decision {
		if hasKeyword(line, kw) {
			n++
		}
	}
	for _, op := range rules.conditions {
		n += strings.Count(line, op)
	}
	if rules.caseLabels && hasKeyword(line, "case") {
		n++
	}
	if rules.catchCounts && hasKeyword(line, "catch") {
		n++
	}
	return n
}

// countFileDecisions 在文件级别统计，用于识别不出函数边界时的兜底
func countFileDecisions(lines []string, rules complexityRules) int {
	n := 1
	for _, l := range lines {
		n += countDecisions(strings.TrimSpace(l), rules)
	}
	return n
}

// hasKeyword 报告某行是否以完整单词的形式包含关键字
//
// 用边界检查而不是子串匹配，否则 "shift" 会被当成 "if"，"format" 会被当成 "for"
func hasKeyword(line, kw string) bool {
	idx := 0
	for {
		pos := strings.Index(line[idx:], kw)
		if pos < 0 {
			return false
		}
		pos += idx
		beforeOK := pos == 0 || !isIdentByte(line[pos-1])
		after := pos + len(kw)
		afterOK := after >= len(line) || !isIdentByte(line[after])
		if beforeOK && afterOK {
			return true
		}
		idx = pos + len(kw)
	}
}

func isIdentByte(c byte) bool {
	return c == '_' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9'
}

// javaLikeFuncHeader 识别 Java/C# 的方法声明
//
// 形如 `public String classify(int score) {` 或 `private void run()`，
// 以修饰符或类型开头，含括号，行尾是 { 或 )
func javaLikeFuncHeader(trimmed string) bool {
	if !strings.Contains(trimmed, "(") {
		return false
	}
	// 字段声明、调用、控制语句不算
	for _, bad := range []string{"if (", "if(", "for (", "for(", "while (", "while(",
		"switch (", "switch(", "catch (", "catch(", "return ", "new ", "else"} {
		if strings.HasPrefix(trimmed, bad) {
			return false
		}
	}
	if strings.HasSuffix(trimmed, ";") {
		return false
	}
	// 方法名在第一个 ( 之前，且前面还有返回类型或修饰符
	paren := strings.Index(trimmed, "(")
	if paren <= 0 {
		return false
	}
	head := strings.Fields(strings.TrimSpace(trimmed[:paren]))
	return len(head) >= 2
}

// cppFuncHeader 识别 C/C++ 的函数定义
//
// C 函数没有修饰符，形如 `int classify(int score, ...)` 且行尾是 {。控制语句和
// 调用语句要排除掉
func cppFuncHeader(trimmed string) bool {
	if !strings.Contains(trimmed, "(") || strings.HasSuffix(trimmed, ";") {
		return false
	}
	for _, bad := range []string{"if", "for", "while", "switch", "catch", "return",
		"else", "#define", "#include", "sizeof"} {
		if hasKeyword(strings.TrimSpace(trimmed), bad) && strings.HasPrefix(strings.TrimSpace(trimmed), bad) {
			return false
		}
	}
	paren := strings.Index(trimmed, "(")
	if paren <= 0 {
		return false
	}
	// 必须是「类型 名字(」的形状，至少两个词
	head := strings.Fields(strings.TrimSpace(trimmed[:paren]))
	return len(head) >= 2
}

// guessFunctionName 从函数头里取函数名
func guessFunctionName(trimmed string) string {
	paren := strings.Index(trimmed, "(")
	if paren <= 0 {
		return "anonymous"
	}
	head := strings.Fields(strings.TrimSpace(trimmed[:paren]))
	if len(head) == 0 {
		return "anonymous"
	}
	name := head[len(head)-1]
	if idx := strings.LastIndexAny(name, "*&"); idx >= 0 {
		name = name[idx+1:]
	}
	if name == "" {
		return "anonymous"
	}
	return name
}

// phpFuncHeader 识别 PHP 的函数与方法声明
//
// 形如 `function classify($score) {` 或 `public function run(): void {`。
// 与 C 系相反，函数名在 `function` 关键字之后，所以 guessFunctionName 取
// 最后一词恰好也对，`public function run` 的词尾就是 run
func phpFuncHeader(trimmed string) bool {
	if !strings.Contains(trimmed, "(") || strings.HasSuffix(trimmed, ";") {
		return false
	}
	// 控制语句和调用语句不是函数定义
	for _, bad := range []string{"if", "elseif", "for", "foreach", "while", "switch",
		"catch", "return", "echo", "new", "match"} {
		if strings.HasPrefix(trimmed, bad+" ") || strings.HasPrefix(trimmed, bad+"(") {
			return false
		}
	}
	// 必须真的出现 function 关键字，且它后面跟着名字
	idx := strings.Index(trimmed, "function")
	if idx < 0 {
		return false
	}
	after := strings.TrimSpace(trimmed[idx+len("function"):])
	if after == "" || !strings.Contains(after, "(") {
		return false
	}
	// `function (` 是匿名闭包，没有名字可报
	return !strings.HasPrefix(after, "(")
}
