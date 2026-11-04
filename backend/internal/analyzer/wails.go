package analyzer

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"regexp"
	"strings"
)

// wailsChecker 实现 Go/Wails 专项审查
//
// 它合并了三类检查
//
//   - 结构，这看起来是不是一个 Wails 项目，约定的文件是否齐全
//   - 运行时 API 用法，EventsEmit / Window* / Dialog* / BrowserOpenURL 是否被
//     用在会在运行时出错的方式上（启动前、关闭后、紧循环里、参数未校验）
//   - 绑定设计，向 JavaScript 暴露了多少面，有没有防护
//
// 后两类用 Go AST 而不是正则，因为关键问题是调用处在什么位置（哪个函数
// 里、是否在循环里、是否在 goroutine 里），这是按行匹配回答不了的
type wailsChecker struct {
	ctx      *Context
	analyzer string
	issues   []Issue

	// 每次运行的缓存。一次分析可能把同一个文件解析好几遍（运行时扫描、绑定
	// 扫描），有缓存就是一次解析和三遍的区别
	snippets        *SnippetReader
	runtimeAliases  map[string]map[string]bool
	validationCache map[string]bool
	// parents 把调用表达式映射到它的直接父节点，用来判断返回值是被接收了还是
	// 被丢掉了
	parents map[ast.Node]ast.Node
	// nilDerefReported 记录哪些接收者类型已经在 nil 防护检查里报过，这样一个
	// 暴露面很广的 service 只会产生一条发现而不是几十条
	nilDerefReported []string
}

func newWailsChecker(ctx *Context, name string) *wailsChecker {
	return &wailsChecker{
		ctx:             ctx,
		analyzer:        name,
		snippets:        NewSnippetReader(ctx.Files),
		runtimeAliases:  map[string]map[string]bool{},
		validationCache: map[string]bool{},
		parents:         map[ast.Node]ast.Node{},
	}
}

// wailsRuntimeFuncs 按类别对运行时 API 分组
var wailsRuntimeGroups = map[string]string{
	"EventsEmit":           "event",
	"EventsOn":             "event",
	"EventsOff":            "event",
	"EventsOnce":           "event",
	"WindowSetTitle":       "window",
	"WindowShow":           "window",
	"WindowHide":           "window",
	"WindowSetSize":        "window",
	"WindowSetPosition":    "window",
	"WindowCenter":         "window",
	"WindowMaximise":       "window",
	"WindowMinimise":       "window",
	"WindowFullscreen":     "window",
	"WindowUnfullscreen":   "window",
	"WindowSetAlwaysOnTop": "window",
	"MessageDialog":        "dialog",
	"QuestionDialog":       "dialog",
	"ErrorDialog":          "dialog",
	"InfoDialog":           "dialog",
	"WarningDialog":        "dialog",
	"OpenFileDialog":       "dialog",
	"SaveFileDialog":       "dialog",
	"BrowserOpenURL":       "browser",
}

// lifecycleFuncs 是 Wails 的应用钩子
var lifecycleFuncs = map[string]bool{
	"OnStartup":     true,
	"OnShutdown":    true,
	"OnDomReady":    true,
	"OnBeforeClose": true,
}

func (c *wailsChecker) Run() ([]Issue, error) {
	c.checkStructure()

	if err := c.checkRuntimeUsage(); err != nil {
		return c.issues, err
	}
	c.checkBindings()
	return c.issues, nil
}

func (c *wailsChecker) add(rule, severity, message, advice, file string, line, col int, code string) {
	c.issues = append(c.issues, Issue{
		Analyzer: c.analyzer,
		Rule:     rule,
		Severity: severity,
		Message:  message,
		Advice:   advice,
		File:     file,
		Line:     line,
		Column:   col,
		Code:     code,
	})
}

// ---------------------------------------------------------------------------
// 结构
// ---------------------------------------------------------------------------

func (c *wailsChecker) checkStructure() {
	files := c.ctx.Files

	hasWailsJSON := files.Exists("wails.json")
	hasFrontend := files.Exists("frontend")
	hasMainGo := files.Exists("main.go")
	hasGoMod := files.Exists("go.mod")

	// 即使没有 wails.json 也要检测 Wails 用法：项目可能正处于迁移中途
	usesWailsRuntime := c.projectImportsWails()

	if !hasWailsJSON && !usesWailsRuntime {
		// 这个项目跟 Wails 无关，保持安静而不是唠叨
		return
	}

	if !hasWailsJSON {
		c.add("wails-missing-config", SeverityWarning,
			"wails.json is missing but Wails runtime packages are imported",
			"Add wails.json at the repository root so `wails build` can resolve the frontend and the build commands.",
			"wails.json", 0, 0, "")
	}
	if !hasFrontend {
		c.add("wails-missing-frontend", SeverityWarning,
			"frontend/ directory is missing",
			"Keep the frontend sources (or at least frontend/dist) in the repository, or configure the correct frontend:dir in wails.json.",
			"frontend", 0, 0, "")
	}
	if !hasMainGo {
		c.add("wails-missing-main", SeverityInfo,
			"main.go is missing at the repository root",
			"Wails expects the application entry point at the module root; use a non-default layout only if wails.json reflects it.",
			"main.go", 0, 0, "")
	}
	if !hasGoMod {
		c.add("wails-missing-gomod", SeverityError,
			"go.mod is missing",
			"A Wails application is a Go module. Run `go mod init` and commit go.mod/go.sum.",
			"go.mod", 0, 0, "")
	}

	c.checkWailsJSONContent()
	c.checkGitignore()
}

// checkWailsJSONContent 校验那些经常让构建出问题的字段
func (c *wailsChecker) checkWailsJSONContent() {
	fi := c.findFile("wails.json")
	if fi == nil {
		return
	}
	raw, err := c.ctx.Files.Read(*fi, 1<<20)
	if err != nil {
		return
	}
	content := string(raw)

	if !strings.Contains(content, "frontend:install") && !strings.Contains(content, "frontend:build") {
		c.add("wails-build-commands", SeverityInfo,
			"wails.json does not declare frontend:install / frontend:build",
			"Declare both commands so that `wails build` and CI know how to install dependencies and build the frontend.",
			"wails.json", 1, 0, "")
	}
	// 从 Wails 1 迁到 2 时最常见的错误
	if strings.Contains(content, `"frontend:dir"`) && strings.Contains(content, `"wailsjs"`) {
		c.add("wails-legacy-config", SeverityInfo,
			"wails.json looks like a Wails v1 configuration",
			"Wails 2 renamed several fields (e.g. frontend:dir). Compare against a fresh `wails init` output.",
			"wails.json", 1, 0, "")
	}
}

// checkGitignore 报告构建产物是否被正确忽略
func (c *wailsChecker) checkGitignore() {
	fi := c.findFile(".gitignore")
	if fi == nil {
		c.add("wails-missing-gitignore", SeverityInfo,
			".gitignore is missing",
			"At minimum ignore build/, frontend/dist/ and frontend/node_modules/ so generated artifacts stay out of the repository.",
			".gitignore", 0, 0, "")
		return
	}
	raw, err := c.ctx.Files.Read(*fi, 1<<20)
	if err != nil {
		return
	}
	content := string(raw)
	for _, entry := range []string{"build/", "node_modules/"} {
		if !strings.Contains(content, entry) {
			c.add("wails-gitignore-incomplete", SeverityInfo,
				fmt.Sprintf(".gitignore does not ignore %s", entry),
				"Add the standard Wails build artifacts to .gitignore: build/, frontend/dist/, frontend/node_modules/.",
				".gitignore", 1, 0, "")
		}
	}
}

// projectImportsWails 报告是否有 Go 文件导入了 Wails 运行时
func (c *wailsChecker) projectImportsWails() bool {
	for _, fi := range c.ctx.Files.GoFiles() {
		raw, err := c.ctx.Files.Read(fi, 256<<10)
		if err != nil {
			continue
		}
		if strings.Contains(string(raw), "github.com/wailsapp/wails") {
			return true
		}
	}
	return false
}

// ---------------------------------------------------------------------------
// 运行时 API 用法
// ---------------------------------------------------------------------------

func (c *wailsChecker) checkRuntimeUsage() error {
	for _, fi := range c.ctx.Files.NonTestGoFiles() {
		if err := c.ctx.Context.Err(); err != nil {
			return err
		}
		if !wailsRelatedFile(fi.Abs) {
			continue
		}
		if err := c.inspectFile(fi); err != nil {
			// 解析不了的文件本身就是个问题，但该由编译器来报，不是我们
			c.ctx.Log.Debug("wails: skip unparsable file", "file", fi.Rel, "err", err)
		}
	}
	return nil
}

// wailsRelatedFile 是一个廉价的预筛，只解析那些有可能含 Wails 调用的文件
func wailsRelatedFile(abs string) bool {
	return strings.HasSuffix(abs, ".go")
}

func (c *wailsChecker) inspectFile(fi FileInfo) error {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, fi.Abs, nil, parser.ParseComments)
	if err != nil {
		return err
	}

	c.cacheRuntimeAliases(fi, file)

	// 遍历器携带决定严重级别的上下文：调用是否在循环里、是否在 goroutine 里，
	// 以及外层是哪个函数
	var walk func(n ast.Node, inLoop, inGoroutine bool, enclosing string)
	walk = func(n ast.Node, inLoop, inGoroutine bool, enclosing string) {
		if n == nil {
			return
		}

		switch node := n.(type) {
		case *ast.FuncDecl:
			inner := node.Name.Name
			c.checkBoundMethodSurface(fset, fi, node)
			walk(node.Body, false, false, inner)
			return

		case *ast.FuncLit:
			walk(node.Body, inLoop, inGoroutine, enclosing)
			return

		case *ast.ForStmt:
			walk(node.Init, true, inGoroutine, enclosing)
			walk(node.Cond, true, inGoroutine, enclosing)
			walk(node.Post, true, inGoroutine, enclosing)
			walk(node.Body, true, inGoroutine, enclosing)
			return

		case *ast.RangeStmt:
			walk(node.X, inLoop, inGoroutine, enclosing)
			walk(node.Body, true, inGoroutine, enclosing)
			return

		case *ast.GoStmt:
			// 下降之前先记录父链接，和下面那条通用路径的做法一致。这个分支原先
			// 调的是一个空实现，导致 go 语句里的调用没有父节点，而 resultIsUsed
			// 把「没有父节点」当成「结果被用到了」，于是
			// go runtime.MessageDialog(...) 会被静默漏报
			c.parents[node.Call] = node
			walk(node.Call, inLoop, true, enclosing)
			return

		case *ast.CallExpr:
			c.checkCall(fset, fi, node, inLoop, inGoroutine, enclosing)
		}

		// 默认下降：先记录父链接好让 resultIsUsed 能工作，然后递归子节点
		for _, child := range astChildren(n) {
			if child != nil {
				c.parents[child] = n
				walk(child, inLoop, inGoroutine, enclosing)
			}
		}
	}

	walk(file, false, false, "")
	return nil
}

// astChildren 列出一个节点的直接子节点
//
// 这是不引入 golang.org/x/tools/go/ast/inspector 的代价，标准库的 ast.Inspect
// 提供深度优先遍历但不给父子关系，而 Wails 检查需要知道某个调用是表达式语句
// （返回值被丢弃）还是被赋值消费了
//
// 表驱动的形态很重要。早先的版本是一个 47 分支的 type switch，圈复杂度 51，
// 这种函数最容易漏掉某个分支。现在结构一眼可见，而且漏写某种节点类型只会退化成
// 「没有子节点」，不会给出错误答案
func astChildren(n ast.Node) []ast.Node {
	switch node := n.(type) {
	// --- 语句 -------------------------------------------------------------
	case *ast.BlockStmt:
		return nodesOf(node.List)
	case *ast.ExprStmt:
		return []ast.Node{node.X}
	case *ast.AssignStmt:
		return join(nodesOf(node.Lhs), nodesOf(node.Rhs))
	case *ast.ReturnStmt:
		return nodesOf(node.Results)
	case *ast.IfStmt:
		return compact(node.Init, node.Cond, node.Body, node.Else)
	case *ast.ForStmt:
		return compact(node.Init, node.Cond, node.Post, node.Body)
	case *ast.RangeStmt:
		return compact(node.Key, node.Value, node.X, node.Body)
	case *ast.SwitchStmt:
		return compact(node.Init, node.Tag, node.Body)
	case *ast.TypeSwitchStmt:
		return compact(node.Init, node.Assign, node.Body)
	case *ast.SelectStmt:
		return []ast.Node{node.Body}
	case *ast.CaseClause:
		return join(nodesOf(node.List), nodesOf(node.Body))
	case *ast.CommClause:
		return compact(append([]ast.Node{node.Comm}, nodesOf(node.Body)...)...)
	case *ast.LabeledStmt:
		return []ast.Node{node.Stmt}
	case *ast.DeclStmt:
		return []ast.Node{node.Decl}
	case *ast.GoStmt:
		return []ast.Node{node.Call}
	case *ast.DeferStmt:
		return []ast.Node{node.Call}
	case *ast.SendStmt:
		return []ast.Node{node.Chan, node.Value}
	case *ast.IncDecStmt:
		return []ast.Node{node.X}

	// --- 表达式 -----------------------------------------------------------
	case *ast.CallExpr:
		return append([]ast.Node{node.Fun}, nodesOf(node.Args)...)
	case *ast.SelectorExpr:
		// 只取 X：Sel 是字段或方法的名字标识符，不是遍历器需要访问的子表达式
		return []ast.Node{node.X}
	case *ast.BinaryExpr:
		return []ast.Node{node.X, node.Y}
	case *ast.UnaryExpr:
		return []ast.Node{node.X}
	case *ast.ParenExpr:
		return []ast.Node{node.X}
	case *ast.StarExpr:
		return []ast.Node{node.X}
	case *ast.IndexExpr:
		return []ast.Node{node.X, node.Index}
	case *ast.SliceExpr:
		return compact(node.X, node.Low, node.High, node.Max)
	case *ast.KeyValueExpr:
		return []ast.Node{node.Key, node.Value}
	case *ast.TypeAssertExpr:
		return []ast.Node{node.X}
	case *ast.CompositeLit:
		return nodesOf(node.Elts)
	case *ast.FuncLit:
		return []ast.Node{node.Type, node.Body}

	// --- 类型与声明 -------------------------------------------------------
	case *ast.ArrayType:
		return compact(node.Len, node.Elt)
	case *ast.MapType:
		return []ast.Node{node.Key, node.Value}
	case *ast.ChanType:
		return []ast.Node{node.Value}
	case *ast.Ellipsis:
		return []ast.Node{node.Elt}
	case *ast.Field:
		return append(nodesOf(node.Names), node.Type)
	case *ast.FieldList:
		return nodesOf(node.List)
	case *ast.GenDecl:
		return nodesOf(node.Specs)
	case *ast.ValueSpec:
		return append(nodesOf(node.Names), nodesOf(node.Values)...)
	case *ast.TypeSpec:
		return []ast.Node{node.Type}
	case *ast.File:
		return nodesOf(node.Decls)
	case *ast.FuncDecl:
		if node.Body == nil {
			return nil
		}
		return []ast.Node{node.Body}
	}
	return nil
}

// compact 构造子节点列表，丢掉 nil 项
//
// AST 的可选槽位有一半是 nil（没有 Else 的 IfStmt、没有 Key 的 RangeStmt），
// 而遍历器绝不能拿到 nil 节点
func compact(nodes ...ast.Node) []ast.Node {
	out := make([]ast.Node, 0, len(nodes))
	for _, n := range nodes {
		if n != nil {
			out = append(out, n)
		}
	}
	return out
}

// node 是每个 AST 节点类型都满足的约束
type node interface {
	ast.Node
}

// nodesOf 把具体类型的节点切片转成 []ast.Node
//
// 尽管每个元素都满足该接口，Go 也不允许把 []ast.Stmt 当作 []ast.Node 用，所以
// 必须显式转换。用一个泛型辅助函数把它收敛成一份实现，而不是九份
func nodesOf[T node](in []T) []ast.Node {
	if len(in) == 0 {
		return nil
	}
	out := make([]ast.Node, 0, len(in))
	for _, n := range in {
		out = append(out, n)
	}
	return out
}

// join 拼接两个节点切片
func join(a, b []ast.Node) []ast.Node {
	if len(b) == 0 {
		return a
	}
	out := make([]ast.Node, 0, len(a)+len(b))
	return append(append(out, a...), b...)
}

// cacheRuntimeAliases 记下本文件里哪些导入名指向 Wails 运行时，这样
// import wr "github.com/wailsapp/wails/v2/pkg/runtime" 也能识别
func (c *wailsChecker) cacheRuntimeAliases(fi FileInfo, file *ast.File) {
	aliases := map[string]bool{}
	for _, imp := range file.Imports {
		path := strings.Trim(imp.Path.Value, `"`)
		if !strings.HasSuffix(path, "wails/v2/pkg/runtime") {
			continue
		}
		name := "runtime" // the package's own name
		if imp.Name != nil {
			name = imp.Name.Name
			if name == "_" || name == "." {
				// 点导入会让每个运行时调用都变成裸标识符，无法可靠区分，所以
				// 跳过而不是误报
				c.runtimeAliases[fi.Rel] = map[string]bool{}
				return
			}
		}
		aliases[name] = true
	}
	c.runtimeAliases[fi.Rel] = aliases
}

// checkCall 检查单个调用表达式是否有 Wails 运行时误用
func (c *wailsChecker) checkCall(fset *token.FileSet, fi FileInfo, call *ast.CallExpr, inLoop, inGoroutine bool, enclosing string) {
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		// 也捕获点导入产生的裸标识符形式
		if ident, ok := call.Fun.(*ast.Ident); ok && c.runtimeAliases[fi.Rel][""] {
			c.evaluateRuntimeCall(fset, fi, ident.Name, call, inLoop, inGoroutine, enclosing)
		}
		return
	}

	pkgIdent, ok := sel.X.(*ast.Ident)
	if !ok {
		// 结构体字段上的方法调用（如 a.mu.Lock），不是运行时调用
		return
	}
	// Wails 运行时习惯上以 runtime 为名导入，这里解析文件实际使用的别名
	if !c.isWailsRuntimeAlias(fi, pkgIdent.Name) {
		return
	}

	c.evaluateRuntimeCall(fset, fi, sel.Sel.Name, call, inLoop, inGoroutine, enclosing)
}

// isWailsRuntimeAlias 把导入别名解析为 Wails 运行时包
func (c *wailsChecker) isWailsRuntimeAlias(fi FileInfo, name string) bool {
	aliases, ok := c.runtimeAliases[fi.Rel]
	if !ok {
		return name == "runtime" || name == "wailsruntime"
	}
	return aliases[name]
}

func (c *wailsChecker) evaluateRuntimeCall(fset *token.FileSet, fi FileInfo, fn string, call *ast.CallExpr, inLoop, inGoroutine bool, enclosing string) {
	group, known := wailsRuntimeGroups[fn]
	if !known {
		return
	}
	pos := fset.Position(call.Pos())
	code := c.snippet(fi, pos.Line)
	inLifecycle := lifecycleFuncs[enclosing]

	switch group {
	case "event":
		c.checkEventCall(fn, fi, pos, code, inLoop, inGoroutine, inLifecycle, enclosing, call)

	case "window":
		// Window API 只在启动到关闭之间有效
		if !inLifecycle {
			c.add("wails-window-outside-lifecycle", SeverityWarning,
				fmt.Sprintf("runtime.%s is called from %s, outside any Wails lifecycle hook", fn, describeScope(enclosing)),
				"Window operations are only valid after OnStartup and before OnShutdown. Move the call into a lifecycle hook, or gate it on a ready flag so it cannot run during teardown.",
				fi.Rel, pos.Line, pos.Column, code)
		}
		if inGoroutine && !inLifecycle {
			c.add("wails-window-race", SeverityWarning,
				fmt.Sprintf("runtime.%s is called from a goroutine that is not synchronized with startup", fn),
				"An unsynchronized goroutine can outlive the window. Wait for OnStartup (or use a done channel) before touching the window.",
				fi.Rel, pos.Line, pos.Column, code)
		}

	case "dialog":
		c.checkDialogCall(fn, fi, pos, code, call)

	case "browser":
		c.checkBrowserCall(fn, fi, pos, code, call)
	}
}

func (c *wailsChecker) checkEventCall(fn string, fi FileInfo, pos token.Position, code string, inLoop, inGoroutine, inLifecycle bool, enclosing string, call *ast.CallExpr) {
	if fn != "EventsEmit" && fn != "EventsOnce" {
		return
	}
	if inLoop {
		c.add("wails-event-emit-in-loop", SeverityWarning,
			"runtime."+fn+" is called inside a loop",
			"Batch the payload and emit once after the loop. Emitting per iteration floods the JS bridge and freezes the UI on large data sets.",
			fi.Rel, pos.Line, pos.Column, code)
	}
	if inGoroutine && !inLifecycle {
		c.add("wails-event-emit-before-ready", SeverityWarning,
			"runtime."+fn+" is called from a goroutine that may run before the frontend is listening",
			"Events emitted before OnDomReady are lost silently. Buffer the events, or emit from OnStartup/OnDomReady.",
			fi.Rel, pos.Line, pos.Column, code)
	}
	// 在 OnShutdown 里发送事件是有记录的 panic 来源
	if enclosing == "OnShutdown" {
		c.add("wails-event-emit-after-shutdown", SeverityError,
			"runtime."+fn+" is called from OnShutdown",
			"The runtime is being torn down; emitting here can panic. Tell the frontend to stop listening instead, or emit just before shutdown begins.",
			fi.Rel, pos.Line, pos.Column, code)
	}
	// EventsEmit(ctx, eventName, optionalData) 的载荷是第三个参数
	// 用带未导出字段的匿名结构体当载荷，序列化到 JS 侧会变成空对象，静默且难以察觉
	if payload, ok := c.runtimeArg(call, 2); ok {
		if _, isLit := payload.(*ast.CompositeLit); isLit {
			c.add("wails-event-payload-untyped", SeverityInfo,
				"runtime."+fn+" is given an anonymous struct literal as payload",
				"Define a named type for the payload. Anonymous structs with unexported fields serialise to an empty object, which is a common and hard-to-spot bug.",
				fi.Rel, pos.Line, pos.Column, code)
		}
	}
}

func (c *wailsChecker) checkDialogCall(fn string, fi FileInfo, pos token.Position, code string, call *ast.CallExpr) {
	// Dialog 调用返回 (result, error)，忽略这个错误是最常见的 Wails 错误
	if !c.resultIsUsed(call) {
		c.add("wails-dialog-result-ignored", SeverityWarning,
			"runtime."+fn+" result is discarded",
			fn+" returns (result, error). Check the error and honour the user's answer — treating a cancelled dialog as a positive result is a real bug.",
			fi.Rel, pos.Line, pos.Column, code)
	}
}

// checkBrowserCall 检查 runtime.BrowserOpenURL
//
// 签名是 BrowserOpenURL(ctx, url)，所以 URL 是第二个参数。读 Args[0]
// 会去检查 context，从而把每个调用都判为未校验，这条签名说明正是为了防止
// 这个 bug
func (c *wailsChecker) checkBrowserCall(fn string, fi FileInfo, pos token.Position, code string, call *ast.CallExpr) {
	urlArg, ok := c.runtimeArg(call, 1)
	if !ok {
		return
	}

	// 字面量 URL 直接对照允许的协议检查
	if lit, ok := urlArg.(*ast.BasicLit); ok {
		raw := strings.Trim(lit.Value, "`\"")
		if !hasAllowedScheme(raw) {
			c.add("wails-browser-url-scheme", SeverityCritical,
				fmt.Sprintf("runtime.%s opens a non-http(s) URL (%s)", fn, raw),
				"Only open http/https URLs. Other schemes (file:, javascript:, custom handlers) can trigger local code execution.",
				fi.Rel, pos.Line, pos.Column, code)
		}
		return
	}

	// 动态 URL 需要在文件的某处做校验
	if !c.hasSchemeValidation(fi) {
		c.add("wails-browser-url-unvalidated", SeverityError,
			"runtime."+fn+" is called with a dynamic URL that is not validated",
			"Validate the scheme before opening: parse the URL and reject anything that is not http/https. An attacker-controlled URL here is a local code execution vector.",
			fi.Rel, pos.Line, pos.Column, code)
	}
}

// hasAllowedScheme 报告字面量 URL 是否使用 http 或 https
func hasAllowedScheme(raw string) bool {
	lower := strings.ToLower(strings.TrimSpace(raw))
	return strings.HasPrefix(lower, "https://") || strings.HasPrefix(lower, "http://")
}

// runtimeArg 返回 Wails 运行时调用的第 n 个参数，跳过开头的 context
//
// 每个运行时函数都把 context 放在第一位，用辅助函数可以把位置逻辑收在一处，
// 而不是散落一堆魔法下标
func (c *wailsChecker) runtimeArg(call *ast.CallExpr, n int) (ast.Expr, bool) {
	if len(call.Args) <= n {
		return nil, false
	}
	return call.Args[n], true
}

// hasSchemeValidation 在文件里查找 url.Parse 配合协议检查的写法
var schemeValidationPattern = regexp.MustCompile(`(url\.Parse|url\.ParseRequestURI)[\s\S]{0,400}?Scheme\s*(!=|==)`)

func (c *wailsChecker) hasSchemeValidation(fi FileInfo) bool {
	if cached, ok := c.validationCache[fi.Rel]; ok {
		return cached
	}
	raw, err := c.ctx.Files.Read(fi, 1<<20)
	if err != nil {
		return false
	}
	ok := schemeValidationPattern.Match(raw)
	c.validationCache[fi.Rel] = ok
	return ok
}

// resultIsUsed 报告调用的返回值是被消费了还是被丢弃了
//
// 这个区分才让「你忽略了 dialog 的返回值」成为一条真正的发现而不是噪音。
// 有两种包装形式要穿透，它们会安排或返回调用却不消费其返回值
//
//   - go f()，goroutine 语句丢弃返回值的方式和裸语句完全一样。并发改变的是
//     它什么时候跑，而不是值有没有被用到，所以检查必须继续往上找
//   - defer f()，同理，返回值在返回时被丢弃
func (c *wailsChecker) resultIsUsed(call *ast.CallExpr) bool {
	parent := c.parents[call]
	if parent == nil {
		// 父子关系未知。返回 true 让检查保持安静而不是猜，因为误报「你忽略了
		// 返回值」比漏报更糟
		return true
	}

	switch p := parent.(type) {
	case *ast.ExprStmt:
		// runtime.MessageDialog(...) 单独占一行，这是最经典的错误
		return p.X != call

	case *ast.GoStmt, *ast.DeferStmt:
		// 穿透包装看一层：语句本身会丢弃返回值，所以答案取决于包装的外层是什么
		enclosing := c.parents[parent]
		if enclosing == nil {
			return false // `go f()` at statement level: the result is dropped
		}
		switch enclosing.(type) {
		case *ast.ExprStmt, *ast.BlockStmt, *ast.CaseClause, *ast.CommClause, *ast.LabeledStmt:
			return false
		}
		return true

	case *ast.AssignStmt, *ast.ReturnStmt, *ast.IfStmt, *ast.ForStmt,
		*ast.BinaryExpr, *ast.CallExpr, *ast.KeyValueExpr, *ast.CompositeLit,
		*ast.ParenExpr:
		return true
	}
	return true
}

// snippet 读取指定行（从 1 开始）的源码
//
// 委托给共享的读取器，它按文件缓存，读不了的文件就不给片段。只保留一份实现
// 意味着这里的大小上限和失败返回空串的约定，与外部分析器完全一致
func (c *wailsChecker) snippet(fi FileInfo, line int) string {
	return c.snippets.Line(fi.Rel, line)
}

// ---------------------------------------------------------------------------
// 绑定面
// ---------------------------------------------------------------------------

// checkBindings 审查传给 main.go 里 app.Bind 的那个结构体
func (c *wailsChecker) checkBindings() {
	main := c.findFile("main.go")
	if main == nil {
		return
	}
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, main.Abs, nil, parser.ParseComments)
	if err != nil {
		return
	}

	bindCount := 0
	ast.Inspect(file, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok || sel.Sel.Name != "Bind" {
			return true
		}
		for _, arg := range call.Args {
			bindCount++
			c.reviewBoundStruct(fset, main, arg)
		}
		return true
	})

	if bindCount == 0 {
		// 不算错误：有些应用在别处构建 app
		c.ctx.Log.Debug("wails: no app.Bind call found in main.go")
	}
}

// reviewBoundStruct 报告向 JS 暴露的接口面的大小和形态
func (c *wailsChecker) reviewBoundStruct(fset *token.FileSet, main *FileInfo, arg ast.Expr) {
	// app.Bind(&App{ ... })：拆掉取地址和复合字面量
	expr := arg
	if unary, ok := expr.(*ast.UnaryExpr); ok {
		expr = unary.X
	}
	lit, ok := expr.(*ast.CompositeLit)
	if !ok {
		// 通过变量绑定，没有类型信息的话在这里没法检查
		return
	}
	typeName := ""
	if ident, ok := lit.Type.(*ast.Ident); ok {
		typeName = ident.Name
	}

	// 统计该类型在整个包里导出的方法数
	methods, total := c.countExportedMethods(typeName)
	pos := fset.Position(lit.Pos())

	if total > 25 {
		c.add("wails-bind-large-surface", SeverityWarning,
			fmt.Sprintf("%s exposes %d methods to JavaScript", typeName, total),
			"A large binding surface is hard to keep stable: every exported method becomes part of the frontend contract. Split it into focused structs (e.g. FileService, ConfigService) and bind each separately.",
			main.Rel, pos.Line, pos.Column, "")
	}

	for _, m := range methods {
		if err := validateBoundMethodName(m.name); err != nil {
			c.add("wails-bind-naming", SeverityInfo,
				fmt.Sprintf("bound method %s.%s: %s", typeName, m.name, err.Error()),
				"JavaScript callers see the method name verbatim. Prefer descriptive names over abbreviations so the generated bindings are self-documenting.",
				m.file, m.line, 0, "func "+m.name)
		}
		if m.params > 4 {
			c.add("wails-bind-many-params", SeverityInfo,
				fmt.Sprintf("bound method %s.%s takes %d parameters", typeName, m.name, m.params),
				"Cross-language calls with many positional arguments are easy to get wrong. Pass a single request struct instead — it also survives signature changes.",
				m.file, m.line, 0, "func "+m.name)
		}
	}
}

type methodInfo struct {
	name   string
	file   string
	line   int
	params int
}

// checkBoundMethodSurface 检查单个方法声明，找出只有通过 Wails 绑定才会出现的
// 陷阱
//
// 被绑定结构体上每个导出方法，都能在任意时刻被 JavaScript 调用，包括 OnStartup
// 之前和窗口关闭之后。所以一段作为内部方法完全安全的代码，一旦从前端可达就可能
// panic，而这正是本检查要抓的那类 bug
func (c *wailsChecker) checkBoundMethodSurface(fset *token.FileSet, fi FileInfo, fn *ast.FuncDecl) {
	if fn.Recv == nil || !fn.Name.IsExported() || fn.Body == nil {
		return
	}

	pos := fset.Position(fn.Pos())
	code := c.snippet(fi, pos.Line)

	// 被绑定方法若不加 nil 检查就解引用字段，前端提前调用时会 panic
	//
	// 每个接收者类型只报一次，否则一个有 40 个方法的 service 会产生 40 条一模
	// 一样的发现，把报告里其他内容全淹了。出现一次就足以指出问题所在
	if !hasNilGuard(fn.Body) && dereferencesReceiverField(fn) {
		receiver := recvTypeName(fn)
		c.nilDerefReported = append(c.nilDerefReported, receiver)
		count := 0
		for _, r := range c.nilDerefReported {
			if r == receiver {
				count++
			}
		}
		if count == 1 {
			c.add("wails-bound-method-nil-deref", SeverityInfo,
				fmt.Sprintf("bound type %s dereferences state without a nil guard", receiver),
				"The frontend can invoke these methods before OnStartup has populated the struct, and any of them will panic on a nil dependency. Add a guard that returns an error, or initialise the dependencies in the constructor.",
				fi.Rel, pos.Line, pos.Column, code)
		}
	}

	// 从不返回 error 的方法，前端没法通过 promise 拒绝来感知失败
	if fn.Type.Results == nil || !returnsError(fn.Type.Results) {
		if isLikelyIO(fn.Body) {
			c.add("wails-bound-method-no-error", SeverityInfo,
				fmt.Sprintf("bound method %s performs I/O but returns no error", fn.Name.Name),
				"Wails turns a returned error into a rejected promise in JavaScript. Returning an error lets the frontend react; without it, failures are silent.",
				fi.Rel, pos.Line, pos.Column, code)
		}
	}
}

// dereferencesReceiverField 报告函数体是否经由接收者读取（如 a.service.X 或
// a.cfg.Y）
func dereferencesReceiverField(fn *ast.FuncDecl) bool {
	recv := receiverIdent(fn)
	if recv == "" {
		return false
	}
	found := false
	ast.Inspect(fn.Body, func(n ast.Node) bool {
		sel, ok := n.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		inner, ok := sel.X.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		if ident, ok := inner.X.(*ast.Ident); ok && ident.Name == recv {
			found = true
			return false
		}
		return true
	})
	return found
}

// hasNilGuard 在方法体里查找 nil 比较
func hasNilGuard(body *ast.BlockStmt) bool {
	found := false
	ast.Inspect(body, func(n ast.Node) bool {
		bin, ok := n.(*ast.BinaryExpr)
		if !ok || (bin.Op != token.NEQ && bin.Op != token.EQL) {
			return true
		}
		if _, ok := bin.X.(*ast.Ident); ok {
			if _, isNil := bin.Y.(*ast.Ident); isNil {
				found = true
				return false
			}
		}
		if _, ok := bin.Y.(*ast.Ident); ok {
			if _, isNil := bin.X.(*ast.Ident); isNil {
				found = true
				return false
			}
		}
		return true
	})
	return found
}

func returnsError(results *ast.FieldList) bool {
	if results == nil {
		return false
	}
	for _, f := range results.List {
		if ident, ok := f.Type.(*ast.Ident); ok && ident.Name == "error" {
			return true
		}
	}
	return false
}

// isLikelyIO 报告函数体是否调用了看起来像 I/O 的东西
func isLikelyIO(body *ast.BlockStmt) bool {
	ioPackages := map[string]bool{
		"os": true, "io": true, "ioutil": true, "net": true, "http": true,
		"sql": true, "gorm": true, "exec": true, "client": true,
	}
	found := false
	ast.Inspect(body, func(n ast.Node) bool {
		sel, ok := n.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		if ident, ok := sel.X.(*ast.Ident); ok && ioPackages[ident.Name] {
			found = true
			return false
		}
		return true
	})
	return found
}

func receiverIdent(fn *ast.FuncDecl) string {
	if fn.Recv == nil || len(fn.Recv.List) == 0 {
		return ""
	}
	field := fn.Recv.List[0]
	if len(field.Names) == 0 {
		return ""
	}
	return field.Names[0].Name
}

// countExportedMethods 找出声明在绑定类型上的方法
func (c *wailsChecker) countExportedMethods(typeName string) ([]methodInfo, int) {
	if typeName == "" {
		return nil, 0
	}
	var out []methodInfo
	for _, fi := range c.ctx.Files.NonTestGoFiles() {
		fset := token.NewFileSet()
		file, err := parser.ParseFile(fset, fi.Abs, nil, 0)
		if err != nil {
			continue
		}
		for _, decl := range file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Recv == nil || !fn.Name.IsExported() {
				continue
			}
			if recvTypeName(fn) != typeName {
				continue
			}
			out = append(out, methodInfo{
				name:   fn.Name.Name,
				file:   fi.Rel,
				line:   fset.Position(fn.Pos()).Line,
				params: fn.Type.Params.NumFields(),
			})
		}
	}
	return out, len(out)
}

func recvTypeName(fn *ast.FuncDecl) string {
	if fn.Recv == nil || len(fn.Recv.List) == 0 {
		return ""
	}
	switch t := fn.Recv.List[0].Type.(type) {
	case *ast.Ident:
		return t.Name
	case *ast.StarExpr:
		if ident, ok := t.X.(*ast.Ident); ok {
			return ident.Name
		}
	}
	return ""
}

// validateBoundMethodName 标出那些让生成的 JS 绑定变得不必要地晦涩的名字
func validateBoundMethodName(name string) error {
	if len(name) <= 2 {
		return fmt.Errorf("name is too short to be meaningful")
	}
	if allUpper(name) && len(name) > 4 {
		return fmt.Errorf("name is all-caps, which reads as an acronym")
	}
	return nil
}

func allUpper(s string) bool {
	hasLetter := false
	for _, r := range s {
		if r >= 'a' && r <= 'z' {
			return false
		}
		if r >= 'A' && r <= 'Z' {
			hasLetter = true
		}
	}
	return hasLetter
}

// ---------------------------------------------------------------------------
// 辅助函数
// ---------------------------------------------------------------------------

// findFile 把项目相对路径解析为对应的 FileInfo
//
// 它靠 snippet reader 的索引而不是遍历文件列表。原来的遍历每条发现都要跑一次，
// 于是一个有几百条发现、几千个文件的项目会把每个路径和每个文件都比一遍，在本来
// 就最慢的大型项目上退化成平方复杂度
func (c *wailsChecker) findFile(rel string) *FileInfo {
	return c.snippets.File(rel)
}

func describeScope(enclosing string) string {
	if enclosing == "" {
		return "package scope"
	}
	return enclosing
}
