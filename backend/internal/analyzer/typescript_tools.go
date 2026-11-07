package analyzer

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
)

// TypeScript 类型检查，使用项目自己的编译器
//
// 这个分析器做的是编译而不是 lint，由此有三点约束：
//
//   - 需要项目的 tsconfig.json。类型检查只有对照项目自己的严格度才有意义，用默认
//     设置既会报出用户构建不认为是错误的问题，也会漏掉它认为是的问题
//   - 用项目自己的编译器。tsc 是项目的 devDependency，通过 npx --no-install 解析到
//     本地那份，版本与项目构建时一致
//   - 慢。完整类型检查是秒级而不是毫秒级，但它仍是 JS/TS 里价值最高的单项检查，
//     oxlint 的类型相关规则覆盖不到它

// TscAnalyzer 以只检查模式运行 TypeScript 编译器
type TscAnalyzer struct{ javascriptOnly }

// NewTscAnalyzer 构造该分析器
func NewTscAnalyzer() Analyzer { return &TscAnalyzer{} }

func (a *TscAnalyzer) Name() string { return "tsc" }

func (a *TscAnalyzer) Description() string {
	return "Runs the project's TypeScript compiler in check-only mode: type errors, impossible values, unsafe null handling and contract violations. Requires a tsconfig.json."
}

func (a *TscAnalyzer) Category() string { return "correctness" }

// Requires 报的是 npx 而不是 tsc
//
// tsc 是项目的 devDependency，不是全局安装的工具，所以必须在 PATH 上存在的是
// Node 的包运行器。这里要求 "tsc" 会让一台实际上能跑起来的机器被判为缺失
func (a *TscAnalyzer) Requires() []string { return []string{"npx"} }

// InstallHint 给的是装进被扫描项目的命令，因为分析器跑的是项目自己的编译器，
// 版本与严格度设置都取自该项目
//
// 完整说明在 toolHints 词条里
func (a *TscAnalyzer) InstallHint() string {
	return "npm install --save-dev typescript"
}

// ProbeCommands 让可用性探测同时认这两种编译器
//
// 运行时会针对 Vue 项目选择 vue-tsc，只装了 vue-tsc 的项目否则会被报成缺失，
// 而扫描其实是正常工作的
func (a *TscAnalyzer) ProbeCommands() [][]string {
	return [][]string{{"npx"}}
}

// AvailableIn 报告这个项目里能不能跑类型检查
//
// Requires 报的是 npx，真正要跑的是项目自己的编译器，只看 PATH 会把「项目里
// 没装 typescript」误判成就绪。两个编译器认其一即可，与 tscCommand 的选择一致
func (a *TscAnalyzer) AvailableIn(projectPath string) bool {
	return analyzerAvailableIn(projectPath, "vue-tsc") || analyzerAvailableIn(projectPath, "tsc")
}

// tsDiagnostic 是 `tsc --pretty false` 输出中的一行
//
// 编译器没有 JSON 输出（tsc --help 只提供 --pretty），所以解析文本形式。它稳定
// 且是机器形状的：
//
//	src/main.ts(12,17): error TS2307: Cannot find module './App.vue'.
//
// 路径里可能含空格和括号（比如 Windows 上位于 "Program Files (x86)" 这类目录
// 下的路径），所以位置要从右往左匹配，(line,col) 那一组锚定在冒号之前最后一个括号上
var tsDiagnosticRe = regexp.MustCompile(`^(.*)\((\d+),(\d+)\): (error|warning|message) (TS\d+): (.*)$`)

type tsDiagnostic struct {
	File     string
	Line     int
	Column   int
	Code     string
	Message  string
	Severity string
}

// tsconfigNames 是让类型检查有意义的那些配置文件
var tsconfigNames = []string{"tsconfig.json", "jsconfig.json"}

func (a *TscAnalyzer) Analyze(ctx context.Context, projectPath string) ([]Issue, error) {
	return nil, nil
}

func (a *TscAnalyzer) AnalyzeWithContext(actx *Context) ([]Issue, error) {
	config := findTSConfig(actx.ProjectPath)
	if config == "" {
		// 没有配置是跳过而不是失败，纯 JavaScript 项目没有东西可做类型检查，
		// 把它报成错误会让每个 JS 项目看起来都是坏的
		return nil, ErrNothingToCheck("no tsconfig.json or jsconfig.json in the project root")
	}

	// Vue 项目的类型检查要用 vue-tsc
	// 纯 tsc 不认识 .vue 单文件组件，会把每一处导入都报成 "Cannot find module"
	//
	// vue-tsc 对普通 .ts 文件也会委托给 tsc，所以它是超集，只要存在就优先用它，
	// 而不只是在有 .vue 文件时才用
	invocation, err := tscCommand(actx, config)
	if err != nil {
		return nil, err
	}

	// --pretty false 是关键，默认输出带颜色还会折行，会破坏按行解析
	// --noEmit 让检查不会在源码旁边写出文件
	args := append([]string{"--no-install"}, invocation.args...)
	args = append(args, "--noEmit", "--pretty", "false")

	out, err := actx.Runner.Run(actx.Context, actx.ProjectPath, invocation.name, args...)
	if err != nil {
		if IsTimeout(err) {
			// 大型项目上首次类型检查确实要跑一会儿，所以消息说明是撞到了哪个
			// 上限，而不是笼统说「失败」
			return nil, err
		}
		// 退出码非零是常态，tsc 发现错误时退出 1 或 2
		// 下面仍然会从 stdout 解析发现，所以只有完全没有输出时才把错误当致命
		var exit *ExitError
		if !errors.As(err, &exit) || len(bytes.TrimSpace(out)) == 0 {
			// TypeScript 7 上 vue-tsc 会直接崩掉，吐的是 Node 的模块解析错误而不是
			// 类型诊断。这种情况给一句能行动的解释，而不是把调用栈丢给读者
			if hint := typescript7Hint(actx); hint != "" {
				return nil, fmt.Errorf("tsc failed: %w\n%s", err, hint)
			}
			return nil, fmt.Errorf("tsc failed: %w", err)
		}
		actx.Log.Debug("tsc exited non-zero with diagnostics", "err", err)
	}

	issues, skipped := parseTscOutput(out, actx.ProjectPath)
	if skipped > 0 {
		// 非诊断行是正常的（比如 "Found N errors" 汇总行），但整个文件都解析不
		// 出来就意味着格式变了，解析器正在静默地什么都不报
		actx.Log.Debug("tsc output had unparsed lines", "skipped", skipped, "parsed", len(issues))
	}
	return issues, nil
}

// tscInvocation 是解析好的命令加上它的前置参数
type tscInvocation struct {
	name string
	args []string
}

// tscCommand 在 vue-tsc 和 tsc 之间选择，优先前者
//
// 解析走 npx --no-install，它运行项目自己 node_modules/.bin 下的二进制，并拒绝
// 从网络拉取。这个拒绝很关键，没装 TypeScript 的项目必须报工具缺失，而不是静默
// 下载一个不同版本的编译器，用项目从未选择过的设置来检查代码
func tscCommand(actx *Context, config string) (tscInvocation, error) {
	// 先试 vue-tsc。它认识 .vue 文件，普通 .ts 又委托给 tsc，所以只要存在就
	// 严格更强。只装了 vue-tsc 的 Vue 项目不能被逼着用纯 tsc，后者读不了它的
	// 组件
	if hasLocalBin(actx, "vue-tsc") {
		return tscInvocation{name: "npx", args: []string{"vue-tsc", "-p", config}}, nil
	}
	if !hasLocalBin(actx, "tsc") {
		return tscInvocation{}, ErrToolUnavailable("typescript (tsc or vue-tsc)")
	}

	// 只有纯 tsc 却有 .vue 文件的项目没法被正确检查，tsc 会把每一处组件导入都报成缺失
	// 拒绝是诚实的结果，否则就是每个组件导入七条用户无法处理的误报。消息里会写明该装什么
	if hasVueFiles(actx.Files) && !hasVueShim(actx.Files) {
		return tscInvocation{}, ErrToolUnavailable(
			"vue-tsc (this project has .vue files; install vue-tsc or add a *.vue module declaration)")
	}
	return tscInvocation{name: "npx", args: []string{"tsc", "-p", config}}, nil
}

// hasLocalBin 报告项目的 node_modules/.bin 下是否有某个二进制
//
// 只查文件系统，不跑 --version 子进程，因为探测和扫描共用 --timeout，预算紧时
// 子进程探测会被杀掉，把一个装好的工具报成缺失
//
// 扩展名依平台而定，Windows 上 npx 调用的是 .cmd 垫片，POSIX 上则是不带扩展名
// 的符号链接
func hasLocalBin(actx *Context, name string) bool {
	return hasLocalBinIn(actx.ProjectPath, name)
}

// hasLocalBinIn 报告某个项目目录下的 node_modules/.bin 里有没有这个二进制
//
// 与上面的区别只在入参，可用性检查拿不到分析用的 Context，只有一个项目路径，
// 而两处判断的是同一件事，所以实现收在这里
func hasLocalBinIn(projectPath, name string) bool {
	bin := filepath.Join(projectPath, "node_modules", ".bin", name)
	candidates := []string{bin}
	for _, suffix := range binSuffixes {
		candidates = append(candidates, bin+suffix)
	}
	for _, c := range candidates {
		if info, err := os.Stat(c); err == nil && !info.IsDir() {
			return true
		}
	}
	return false
}

// binSuffixes 是 node_modules/.bin 里的垫片可能带的扩展名，按平台而定
//
// Windows 上 npm 生成的是 .cmd 批处理，POSIX 上是不带扩展名的符号链接。多列几个
// 是无害的，找不到就是找不到，而漏掉一个就会把装好的工具报成缺失
var binSuffixes = func() []string {
	if runtime.GOOS == "windows" {
		return []string{".cmd", ".exe", ".bat"}
	}
	return nil
}()

// typescript7Hint 在项目用的是 TypeScript 7 时给出一句解释，否则返回空串
//
// TS 7 是 Go 重写的原生编译器，不提供编程接口也不提供插件机制。vue-tsc 依赖
// typescript/lib/tsc 这个导出路径，TS 7 的 package.json 不再提供它，于是在这类项目
// 上 vue-tsc 直接崩在模块解析，抛出的不是类型诊断。命令交给项目自己的 vue-tsc 执行，
// 调用方无法绕过。上游计划在 TS 7.1 恢复
//
// 只在失败之后给出提示，不按版本号提前预警：项目里可能已经换了别的检查器，按版本预警
// 会在一切正常的项目上报假警
func typescript7Hint(actx *Context) string {
	major := localTypeScriptMajor(actx.ProjectPath)
	if major < 7 {
		return ""
	}
	return "       This project uses TypeScript " + strconv.Itoa(major) +
		", which vue-tsc cannot run against:\n" +
		"       TypeScript 7 ships no programmatic API, so vue-tsc has nothing to call.\n" +
		"       Support is planned for TypeScript 7.1 (microsoft/TypeScript#63800).\n" +
		"       Until then, pinning typescript to 5.x or 6.x restores this check."
}

// localTypeScriptMajor 读项目 package.json 里声明的 TypeScript 主版本号
//
// 读声明而不是读实际安装的版本，是因为这里只需要判断「这个项目在 TS 7 这条线上」，
// 而 package.json 是唯一稳定可得的信号，node_modules 的布局在 npm / pnpm / yarn
// 之间差别很大，逐层去找解析出来的版本既脆又慢
//
// 解析失败一律当成「不是 7」，宁可少提示也不能误报
func localTypeScriptMajor(projectPath string) int {
	data, err := os.ReadFile(filepath.Join(projectPath, "package.json"))
	if err != nil {
		return 0
	}
	return typeScriptMajorFromJSON(data)
}

// typeScriptMajorFromJSON 从 package.json 的内容里取出 TypeScript 的主版本号
//
// 不引入 JSON 解析是权衡后的结果，要取的只有一个字段，而 dependencies 与
// devDependencies 两处都可能出现它。这里对两段范围各做一次正则匹配，比为一个字段
// 反序列化整份文件更简单，也不受文件里其他内容的影响
func typeScriptMajorFromJSON(data []byte) int {
	m := typeScriptVersionRe.FindSubmatch(data)
	if m == nil {
		return 0
	}
	// 版本串形如 ^7.0.2 / ~7.0.2 / >=7 / 7.0.2-beta，主版本号是第一段数字
	major, err := strconv.Atoi(string(m[1]))
	if err != nil {
		return 0
	}
	return major
}

// 匹配 `"typescript": "^7.0.2"` 里的版本串，允许常见的范围前缀
var typeScriptVersionRe = regexp.MustCompile(`"typescript"\s*:\s*"[~^>=<\s]*(\d+)`)

// findTSConfig 返回用于检查的配置路径，没有则返回空
//
// 返回的是相对项目的路径，因为 -p 拿到的就是这个，而来自另一个工作目录的绝对
// 路径出现在诊断消息里会让人困惑
func findTSConfig(projectPath string) string {
	for _, name := range tsconfigNames {
		if _, err := os.Stat(filepath.Join(projectPath, name)); err == nil {
			return name
		}
	}
	// 工作区可能把配置放在下一层（仓库根目录是另一种语言，前端在 frontend/），
	// 这正是当初迫使语言检测往下一层看的那种布局
	entries, err := os.ReadDir(projectPath)
	if err != nil {
		return ""
	}
	for _, e := range entries {
		if !e.IsDir() || strings.HasPrefix(e.Name(), ".") {
			continue
		}
		// 复用 FileSet 的跳过集合，node_modules 或 dist 里的配置因此不会
		// 被误当成项目自己的
		if defaultSkipDirs[e.Name()] {
			continue
		}
		for _, name := range tsconfigNames {
			if _, err := os.Stat(filepath.Join(projectPath, e.Name(), name)); err == nil {
				return filepath.ToSlash(filepath.Join(e.Name(), name))
			}
		}
	}
	return ""
}

// hasVueFiles 报告项目里是否存在 .vue 组件
func hasVueFiles(files *FileSet) bool {
	return files != nil && len(files.WithExt(".vue")) > 0
}

// hasVueShim 报告是否存在 `*.vue` 的模块声明
//
// 有了这个声明，纯 tsc 才能读这个项目，它告诉编译器把每处 .vue 导入当成不透明
// 的组件，而不是缺失的模块
func hasVueShim(files *FileSet) bool {
	if files == nil {
		return false
	}
	for _, fi := range files.WithExt(".d.ts") {
		data, err := files.Read(fi, 256*1024)
		if err != nil {
			continue
		}
		s := string(data)
		if strings.Contains(s, "declare module '*.vue'") ||
			strings.Contains(s, `declare module "*.vue"`) {
			return true
		}
	}
	return false
}

// parseTscOutput 从 `tsc --pretty false` 输出解析诊断
//
// 跳过条数返回给调用方而不是丢掉，这样调用方能区分「项目是干净的」和「解析器
// 已经对不上格式」，后一种情况下静默看起来和成功一模一样
func parseTscOutput(out []byte, projectPath string) (issues []Issue, skipped int) {
	sc := bufio.NewScanner(bytes.NewReader(out))
	// 诊断可能带着很长的类型表达式，调高默认 64KiB 行上限的理由与 ruff 的
	// 读取器相同
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		d, ok := parseTSDiagnostic(line)
		if !ok {
			skipped++
			continue
		}
		issues = append(issues, Issue{
			Analyzer: "tsc",
			Rule:     d.Code,
			Severity: mapTscSeverity(d.Code),
			Message:  d.Message,
			Advice:   tscAdvice(d.Code),
			// 复用 Rust 那边的辅助函数，tsc 报出的路径是相对 tsconfig 所在目录的，
			// 用了 -p frontend/tsconfig.json 之后就是相对那个目录，必须重新锚定到项目根目录。
			// 该辅助函数还会剥掉 Windows 的 verbatim 前缀并统一分隔符，在这里同样适用
			File:   relToProject(d.File, projectPath),
			Line:   d.Line,
			Column: d.Column,
			DocURL: tscDocURL(d.Code),
		})
	}
	return issues, skipped
}

// parseTSDiagnostic 把一行拆成各个部分
//
// 与扫描器分开，是为了能直接用那些难缠的输入测试它，带空格的路径、带括号的
// 路径，以及不能被误当成诊断的汇总行
func parseTSDiagnostic(line string) (tsDiagnostic, bool) {
	m := tsDiagnosticRe.FindStringSubmatch(line)
	if m == nil {
		return tsDiagnostic{}, false
	}
	lineNo, err := strconv.Atoi(m[2])
	if err != nil {
		return tsDiagnostic{}, false
	}
	col, err := strconv.Atoi(m[3])
	if err != nil {
		return tsDiagnostic{}, false
	}
	return tsDiagnostic{
		File:     m[1],
		Line:     lineNo,
		Column:   col,
		Severity: m[4],
		Code:     m[5],
		Message:  m[6],
	}, true
}

// mapTscSeverity 把诊断码映射到 Fizzra 的严重级别
//
// tsc 把所有东西都报成 error，这太粗，它把「这段编不过」和「这个导入路径解析
// 不了，通常是缺了个 devDependency」混为一谈。所以由诊断码决定，而不是级别
//
// 对门禁而言要紧的区分是代码错了还是环境错了。类型错误意味着代码有错，
// 该由作者修，缺模块或缺类型几乎总是没装包，改源码解决不了。第二类会被降级，
// 以免它拖垮质量门禁
func mapTscSeverity(code string) string {
	switch code {
	// 环境或配置问题，不是作者的代码问题
	case "TS2307", // Cannot find module
		"TS2792",  // Cannot find module (moduleResolution hint)
		"TS2688",  // Cannot find type definition file
		"TS6053",  // File not found
		"TS18003", // No inputs were found in config
		"TS5023",  // Unknown compiler option
		"TS5107":  // Deprecated compiler option
		return SeverityInfo

	// 缺少显然的注解，或只影响工具链的导入
	case "TS6133", // Declared but never read
		"TS6192", // All imports in declaration are unused
		"TS6196": // Declared but never used
		return SeverityWarning
	}

	// 其余都是真正的类型错误：代码编不过，而修法是作者能做出的一处改动
	return SeverityError
}

// tscAdvice 为那些值得解释的诊断码给出简短可操作的提示
//
// tsc 自己的消息已经很具体（"Property 'fullName' does not exist on type
// 'User'"），所以只在该消息看不出怎么修时才给建议，主要就是环境那一类，它的
// 消息读起来像代码错误，成因却是缺了个包
func tscAdvice(code string) string {
	switch code {
	case "TS2307":
		return "A package or file could not be resolved. Run `npm install` (or the equivalent) to make sure the dependency is installed, and check the path is correct."
	case "TS2688":
		return "Type definitions are missing. Install the package's @types entry as a devDependency, or remove it from the `types` array in tsconfig.json."
	case "TS18003":
		return "The tsconfig `include`/`files` patterns match no files. Usually the config is meant for a different directory."
	case "TS5107":
		return "This compiler option is deprecated and will stop working in the next major TypeScript release."
	case "TS6133", "TS6196":
		return "Remove the unused declaration, or prefix an unused parameter with `_`."
	case "TS2322":
		return "The value's type does not match the target type. Narrow the value, or widen the annotation if the assignment is intended."
	case "TS2339":
		return "The property is not part of the declared type. Either add it to the type, or the value is a different type than assumed."
	case "TS2345":
		return "The argument's type does not match the parameter's. Check whether the value needs narrowing or the signature needs widening."
	case "TS18048", "TS2532":
		return "This value may be `undefined`. Guard it with a check, a default, or a non-null assertion if it is genuinely always set."
	}
	return ""
}

// tscDocURL 指向某个诊断码在 TypeScript 错误参考里的位置
func tscDocURL(code string) string {
	return "https://typescript.tv/errors/#" + strings.ToLower(code)
}
