package rule

import "strings"

// 规则引擎过滤器所用的按语言词法定义
//
// 引擎的三道过滤器（注释压制、字符串字面量压制和循环作用域跟踪）是照 Go 的语法写的。
// 它们对 Go 正确，对其他语言则错误，而且错向危险的一侧：Python 没有花括号，
// countLoopCloses 什么都返回不了，loopDepth 只增不减。第一个 for 之后，文件里
// 每一行都被当成在循环体内，这让 requireLoop 类规则在整个仓库上误报、
// forbidInLoop 类规则在文件余下部分彻底失声
//
// 本文件把这些词法规则显式化并按语言区分。引擎会问 Syntax 它看到的是什么，而
// 不是自行假定

// Syntax 描述一种语言的词法表面
// 注释如何开始、字符串字面量长什么样、循环体如何界定
//
// Syntax 是按行查询的。实现必须是无状态的，因为引擎会在并发遍历多个文件时调用它们
type Syntax interface {
	// Name 是语言标识，取值同 model.Language*
	Name() string
	// Comment 描述行注释和块注释的形式
	Comment() CommentSyntax
	// StringDelims 返回开启和闭合字面量的引号字符。同一个定界符出现在两处（单个引号）
	// 表示切换状态，不同的一对（Python 三引号）则表示开和闭
	StringDelims() []StringDelim
	// ScopeKind 报告该语言如何界定块，这决定了循环嵌套怎么跟踪
	ScopeKind() ScopeKind
	// LoopKeywords 列出开启循环的记号
	LoopKeywords() []string
}

// CommentSyntax 描述一种语言的注释形式
type CommentSyntax struct {
	// Line 列出能把该行剩余部分注释掉的 prefix
	// Go、Rust、JS 用 "//"，Python 用 "#"
	Line []string
	// BlockOpen 和 BlockClose 界定块注释，该语言没有块注释时为空
	// Python 没有，Go、Rust、JS 都用 /* */
	BlockOpen, BlockClose string
	// BlockNests 报告块注释是否可嵌套，Rust 可以而 Go、JS 不行
	// Rust 里不跟踪这点，`/* /* */ */` 会在第一个 `*/` 处结束，后面的 `*/` 被当成代码读
	BlockNests bool
}

// StringDelim 是一种字符串字面量形式
type StringDelim struct {
	// Open 和 Close 是定界符。单个引号字符时两者相同
	Open, Close string
	// Escape 是在字面量内转义下一个字符的字符，不接受转义的原始形式为零
	Escape byte
	// Raw 对 Escape 不适用的形式为真
	// Go 的反引号、Rust 的 r"..." 和 r#"..."#。JS 模板字面量不算原始，因为它会处理反引号转义
	Raw bool
}

// ScopeKind 表示一种语言如何界定块，它决定循环嵌套能怎么数
type ScopeKind int

const (
	// ScopeBraces 用 { 和 } 数嵌套：Go、Rust、JS/TS
	ScopeBraces ScopeKind = iota
	// ScopeIndent 没有花括号，必须用缩进，Python
	//
	// 这里根本没法靠花括号来数，所以循环作用域是靠比较循环头的缩进与它下面各行
	// 的缩进来跟踪的
	ScopeIndent
)

// syntaxFor 返回某语言的语法定义，默认按 Go 处理
//
// 默认而不是报错是有意的，未知语言仍然应该拿到注释和字符串过滤器，它们才是防止
// 规则命中自己文本的关键。只有循环过滤器会受影响，而它退化成「忽略 requireLoop」，
// 不会变成一场误报风暴
func syntaxFor(language string) Syntax {
	switch strings.ToLower(strings.TrimSpace(language)) {
	case "python":
		return pythonSyntax{}
	case "rust":
		return rustSyntax{}
	case "javascript", "typescript":
		return javascriptSyntax{}
	case "php":
		return phpSyntax{}
	default:
		return goSyntax{}
	}
}

// ---------------------------------------------------------------------------
// Go
// ---------------------------------------------------------------------------

type goSyntax struct{}

func (goSyntax) Name() string { return "go" }

func (goSyntax) Comment() CommentSyntax {
	return CommentSyntax{Line: []string{"//"}, BlockOpen: "/*", BlockClose: "*/"}
}

func (goSyntax) StringDelims() []StringDelim {
	return []StringDelim{
		{Open: `"`, Close: `"`, Escape: '\\'},
		{Open: "`", Close: "`", Raw: true},
		{Open: "'", Close: "'", Escape: '\\'},
	}
}

func (goSyntax) ScopeKind() ScopeKind { return ScopeBraces }

func (goSyntax) LoopKeywords() []string { return []string{"for"} }

// ---------------------------------------------------------------------------
// Rust
// ---------------------------------------------------------------------------

type rustSyntax struct{}

func (rustSyntax) Name() string { return "rust" }

func (rustSyntax) Comment() CommentSyntax {
	return CommentSyntax{
		Line:       []string{"//"},
		BlockOpen:  "/*",
		BlockClose: "*/",
		// Rust 的块注释可嵌套：/* /* */ */ 是一段注释而不是两段
		BlockNests: true,
	}
}

func (rustSyntax) StringDelims() []StringDelim {
	return []StringDelim{
		// 原始字符串，检查顺序在普通形式之前，否则 r"..." 会被读成标识符后面跟一个字符串
		{Open: `r#"`, Close: `"#`, Raw: true},
		{Open: `r"`, Close: `"`, Raw: true},
		{Open: `"`, Close: `"`, Escape: '\\'},
		// 字节串就是带前缀的普通转义字符串
		{Open: `b"`, Close: `"`, Escape: '\\'},
		// 注意：没有单引号形式
		// Rust 里 `'` 既引入生命周期（&'a str）又引入字符字面量，把每个 `'` 都当成
		// 字符字面量定界符，会在遇到生命周期时翻转字符串状态，压制该行余下部分的真实匹配。
	}
}

func (rustSyntax) ScopeKind() ScopeKind { return ScopeBraces }

func (rustSyntax) LoopKeywords() []string { return []string{"for", "while", "loop"} }

// ---------------------------------------------------------------------------
// Python
// ---------------------------------------------------------------------------

type pythonSyntax struct{}

func (pythonSyntax) Name() string { return "python" }

func (pythonSyntax) Comment() CommentSyntax {
	return CommentSyntax{Line: []string{"#"}}
}

func (pythonSyntax) StringDelims() []StringDelim {
	return []StringDelim{
		// 三引号排在前面：三引号不能被读成一个空字符串后面再跟一个开引号
		{Open: `"""`, Close: `"""`, Escape: '\\'},
		{Open: `'''`, Close: `'''`, Escape: '\\'},
		{Open: `"`, Close: `"`, Escape: '\\'},
		{Open: `'`, Close: `'`, Escape: '\\'},
	}
}

func (pythonSyntax) ScopeKind() ScopeKind { return ScopeIndent }

// LoopKeywords 返回 Python 的循环开启关键字
//
// for 和 while 是唯二开启循环体的结构。注意推导式（`[x for x in y]`）也含 for
// 但不开一个缩进块，引擎里的缩进检查会排除它们，因为推导式之后的行并不比它更
// 缩进
func (pythonSyntax) LoopKeywords() []string { return []string{"for", "while"} }

// ---------------------------------------------------------------------------
// JavaScript / TypeScript
// ---------------------------------------------------------------------------

type javascriptSyntax struct{}

func (javascriptSyntax) Name() string { return "javascript" }

func (javascriptSyntax) Comment() CommentSyntax {
	return CommentSyntax{Line: []string{"//"}, BlockOpen: "/*", BlockClose: "*/"}
}

func (javascriptSyntax) StringDelims() []StringDelim {
	return []StringDelim{
		// 模板字面量会处理转义（不像 Go 的反引号），而它的 ${...} 插值不被跟踪
		// 在这些规则针对的代码里，插值内部再套字符串很罕见，把整个字面量当作字符串是保守的选择
		{Open: "`", Close: "`", Escape: '\\'},
		{Open: `"`, Close: `"`, Escape: '\\'},
		{Open: "'", Close: "'", Escape: '\\'},
	}
}

func (javascriptSyntax) ScopeKind() ScopeKind { return ScopeBraces }

func (javascriptSyntax) LoopKeywords() []string { return []string{"for", "while", "do"} }

// ---------------------------------------------------------------------------
// PHP
// ---------------------------------------------------------------------------

type phpSyntax struct{}

func (phpSyntax) Name() string { return "php" }

// Comment 返回 PHP 的注释形式
//
// PHP 三种注释并存，`//`、`#`、`/* */`。`#` 在 PHP 8 的属性语法（#[Attribute]）
// 里也是合法的，把它当注释会连带忽略掉属性行，但属性行本就不含规则要找的
// 危险调用，代价可接受。反过来漏掉 `#` 注释会让注释里的示例代码被当真实代码匹配
func (phpSyntax) Comment() CommentSyntax {
	return CommentSyntax{
		Line:       []string{"//", "#"},
		BlockOpen:  "/*",
		BlockClose: "*/",
	}
}

// StringDelims 返回 PHP 的字符串定界形式
//
// heredoc/nowdoc（<<<EOT ... EOT）刻意不在这里处理，它的结束标记必须独占一行，
// 而 StringDelim 的模型是单行内的定界符配对，表达不了跨行形式。不识别它是更安全
// 的错法，heredoc 里出现规则要找的危险函数调用的概率很低，而错误识别会让字符串
// 状态跨越整个文件的余下部分
func (phpSyntax) StringDelims() []StringDelim {
	return []StringDelim{
		{Open: `"`, Close: `"`, Escape: '\\'},
		{Open: `'`, Close: `'`, Escape: '\\'},
		// 反引号执行 shell 命令，必须识别
		// 它本身就是危险模式，而把它的内容当代码读会让里面的字符串把状态搞乱
		{Open: "`", Close: "`", Escape: '\\'},
	}
}

func (phpSyntax) ScopeKind() ScopeKind { return ScopeBraces }

func (phpSyntax) LoopKeywords() []string { return []string{"for", "foreach", "while", "do"} }
