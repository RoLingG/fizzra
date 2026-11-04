package analyzer

import (
	"cmp"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
)

// MetricsAnalyzer 计算支撑质量评分的代码指标，规模、圈复杂度、重复度和测试面
//
// 它是唯一既产生问题（越过阈值）又产生指标快照的分析器，快照通过 Metrics()
// 访问器暴露
//
// 它不限定语言。行数、文件大小、重复度是纯文本的，适用于任何语言，只有圈复杂度、
// 函数长度、参数个数需要一个真正的解析器。对每种语言都运行它对评分很关键，只限
// Go 会让一个 Rust 项目的这几项维度缺失，而缺失和记零不是一回事
type MetricsAnalyzer struct {
	// complexityThreshold 是函数被报成问题的圈复杂度下限
	complexityThreshold int
	// functionLengthThreshold 是函数被上报的行数下限
	functionLengthThreshold int
	// paramThreshold 是函数被上报的参数个数下限
	paramThreshold int
	// duplicateLineThreshold 是算作克隆的最小块行数
	duplicateLineThreshold int

	// snapshot 保存最近一次运行的指标。分析器每次分析都会新建实例，所以它不会在
	// 并发扫描之间共享
	snapshot MetricsSnapshot
}

// NewMetricsAnalyzer 构造该分析器
func NewMetricsAnalyzer() Analyzer {
	return &MetricsAnalyzer{
		complexityThreshold:     20,
		functionLengthThreshold: 80,
		paramThreshold:          5,
		duplicateLineThreshold:  12,
	}
}

func (a *MetricsAnalyzer) Name() string { return "metrics" }

func (a *MetricsAnalyzer) Description() string {
	return "Computes code size, cyclomatic complexity, function length, parameter counts and duplication. Produces the metrics behind the quality score."
}

func (a *MetricsAnalyzer) Category() string   { return "metrics" }
func (a *MetricsAnalyzer) Requires() []string { return nil }

func (a *MetricsAnalyzer) Analyze(ctx context.Context, projectPath string) ([]Issue, error) {
	return nil, nil
}

// ResultReporter 让分析器在问题之外同时贡献结构化指标
type ResultReporter interface {
	Metrics() MetricsSnapshot
}

// AnalyzeWithContext 对源码做一遍扫描就算出全部指标
func (a *MetricsAnalyzer) AnalyzeWithContext(actx *Context) ([]Issue, error) {
	fset := token.NewFileSet()
	syntax := metricsSyntaxFor(actx.ProjectLanguage)
	approxRules := complexityRulesFor(actx.ProjectLanguage)

	var (
		totalLines, codeLines, commentLines, blankLines int
		testFiles, testFunctions                        int
		functions                                       []ComplexFunction
		fileSizes                                       []FileSize
		issues                                          []Issue
	)

	// 本项目对应语言的源文件。行数统计、文件大小和重复度是基于文本的，对它们
	// 都适用，下面那趟 AST 遍历才是需要按语言解析器的部分
	sourceFiles := metricsFiles(actx)
	for _, fi := range sourceFiles {
		if err := actx.Context.Err(); err != nil {
			return issues, err
		}

		raw, err := actx.Files.Read(fi, 4<<20)
		if err != nil {
			actx.Log.Debug("metrics: read failed", "file", fi.Rel, "err", err)
			continue
		}

		fileLines := splitLines(string(raw))
		isTest := syntax.isTestFile(fi.Rel)
		if isTest {
			testFiles++
		}

		// 行的分类基于文本，因为解析器的注释流不会告诉我们空行的情况
		// 注释形式依语言而异，所以取自语法定义而不是写死 Go 的
		var inBlockComment bool
		fileCode := 0
		for _, line := range fileLines {
			totalLines++
			trimmed := strings.TrimSpace(line)
			switch {
			case trimmed == "":
				blankLines++
			case inBlockComment:
				commentLines++
				if strings.Contains(trimmed, syntax.blockClose) {
					inBlockComment = false
				}
			case syntax.isLineComment(trimmed):
				commentLines++
			case syntax.blockOpen != "" && strings.HasPrefix(trimmed, syntax.blockOpen):
				commentLines++
				if !strings.Contains(trimmed, syntax.blockClose) {
					inBlockComment = true
				}
			default:
				codeLines++
				fileCode++
			}
		}

		fileSizes = append(fileSizes, FileSize{
			File:   fi.Rel,
			Lines:  len(fileLines),
			Code:   fileCode,
			IsTest: isTest,
		})

		// Go 用 AST 精确计算；其余语言走词法近似，结果标记为估算值
		if actx.ProjectLanguage != "" && actx.ProjectLanguage != modelLanguageGo {
			for _, fn := range approxComplexity(fi.Rel, string(raw), approxRules) {
				functions = append(functions, ComplexFunction{
					File:       fi.Rel,
					Name:       fn.Name,
					Line:       fn.Line,
					Complexity: fn.Complexity,
					Length:     fn.Length,
				})
				if fn.Complexity > a.complexityThreshold {
					issues = append(issues, Issue{
						Analyzer: a.Name(),
						Rule:     "high-complexity",
						Severity: severityForComplexity(fn.Complexity, a.complexityThreshold),
						Message:  fmt.Sprintf("%s has an estimated cyclomatic complexity of %d (threshold %d)", fn.Name, fn.Complexity, a.complexityThreshold),
						Advice:   "Split the function along its decision points. Each extracted branch is usually independently testable.",
						File:     fi.Rel,
						Line:     fn.Line,
						Column:   1,
					})
				}
				if fn.Length > a.functionLengthThreshold {
					issues = append(issues, Issue{
						Analyzer: a.Name(),
						Rule:     "long-function",
						Severity: SeverityInfo,
						Message:  fmt.Sprintf("%s is %d lines long (threshold %d)", fn.Name, fn.Length, a.functionLengthThreshold),
						Advice:   "Extract cohesive blocks into named functions.",
						File:     fi.Rel,
						Line:     fn.Line,
						Column:   1,
					})
				}
			}
			continue
		}

		file, err := parser.ParseFile(fset, fi.Abs, raw, parser.ParseComments)
		if err != nil {
			// 解析不了的文件仍然计入上面的行数统计，语法错误由编译器来报，我们
			// 不重复报一遍
			actx.Log.Debug("metrics: parse failed", "file", fi.Rel, "err", err)
			continue
		}

		ast.Inspect(file, func(n ast.Node) bool {
			fn, ok := n.(*ast.FuncDecl)
			if !ok {
				return true
			}
			if isTest {
				testFunctions++
			}

			start := fset.Position(fn.Pos()).Line
			end := fset.Position(fn.End()).Line
			body := fn.Body
			if body == nil {
				return true // declaration without a body (assembly stub, cgo)
			}

			cf := ComplexFunction{
				File:       fi.Rel,
				Name:       fn.Name.Name,
				Line:       start,
				Complexity: cyclomaticComplexity(body),
				Length:     end - start + 1,
				Params:     fn.Type.Params.NumFields(),
				Receiver:   receiverIdent(fn),
			}
			functions = append(functions, cf)

			if cf.Complexity > a.complexityThreshold {
				issues = append(issues, Issue{
					Analyzer: a.Name(),
					Rule:     "high-complexity",
					Severity: severityForComplexity(cf.Complexity, a.complexityThreshold),
					Message:  fmt.Sprintf("%s has cyclomatic complexity %d (threshold %d)", qualifiedName(cf), cf.Complexity, a.complexityThreshold),
					Advice:   "Split the function along its decision points. Each extracted branch is usually independently testable.",
					File:     fi.Rel,
					Line:     start,
					Column:   1,
				})
			}
			if cf.Length > a.functionLengthThreshold {
				issues = append(issues, Issue{
					Analyzer: a.Name(),
					Rule:     "long-function",
					Severity: SeverityInfo,
					Message:  fmt.Sprintf("%s is %d lines long (threshold %d)", qualifiedName(cf), cf.Length, a.functionLengthThreshold),
					Advice:   "Extract cohesive blocks into named helpers. A long function usually hides several responsibilities.",
					File:     fi.Rel,
					Line:     start,
					Column:   1,
				})
			}
			if cf.Params > a.paramThreshold {
				issues = append(issues, Issue{
					Analyzer: a.Name(),
					Rule:     "too-many-params",
					Severity: SeverityInfo,
					Message:  fmt.Sprintf("%s takes %d parameters (threshold %d)", qualifiedName(cf), cf.Params, a.paramThreshold),
					Advice:   "Group related parameters into a request struct. It also makes future signature changes non-breaking.",
					File:     fi.Rel,
					Line:     start,
					Column:   1,
				})
			}
			return true
		})
	}

	// 重复度需要全项目视角，所以放在逐文件那趟之后运行
	dupPercent, dupBlocks, dupIssues := a.detectDuplication(actx)
	issues = append(issues, dupIssues...)

	coverage, covIssue := a.detectCoverage(actx)
	if covIssue != nil {
		issues = append(issues, *covIssue)
	}

	metrics := MetricsSnapshot{
		Files:            len(sourceFiles),
		Lines:            totalLines,
		CodeLines:        codeLines,
		CommentLines:     commentLines,
		BlankLines:       blankLines,
		Functions:        len(functions),
		ComplexFunctions: topComplex(functions, a.complexityThreshold, 25),
		LongFunctions:    topLong(functions, a.functionLengthThreshold, 25),
		Duplication:      dupPercent,
		DuplicateBlocks:  dupBlocks,
		Coverage:         coverage,
		TestFiles:        testFiles,
		TestFunctions:    testFunctions,
		LargestFiles:     topFilesBySize(fileSizes, 20),
	}
	metrics.AvgComplexity, metrics.MaxComplexity = complexityStats(functions)

	a.snapshot = metrics
	return issues, nil
}

// Metrics 返回最近一次 AnalyzeWithContext 调用产生的快照
func (a *MetricsAnalyzer) Metrics() MetricsSnapshot { return a.snapshot }

// MetricsSnapshot 是 metrics 分析器产出的代码指标记录
//
// 它与 model.Metrics 对应。这种重复是有意的，本包不能导入持久层模型，映射由
// service 层完成
type MetricsSnapshot struct {
	Files        int `json:"files"`
	Lines        int `json:"lines"`
	CodeLines    int `json:"codeLines"`
	CommentLines int `json:"commentLines"`
	BlankLines   int `json:"blankLines"`
	Functions    int `json:"functions"`

	AvgComplexity float64 `json:"avgComplexity"`
	MaxComplexity int     `json:"maxComplexity"`

	ComplexFunctions []ComplexFunction `json:"complexFunctions"`
	LongFunctions    []ComplexFunction `json:"longFunctions"`

	Duplication     float64          `json:"duplication"`
	DuplicateBlocks []DuplicateBlock `json:"duplicateBlocks"`

	Coverage float64 `json:"coverage"`

	TestFiles     int `json:"testFiles"`
	TestFunctions int `json:"testFunctions"`

	LargestFiles []FileSize `json:"largestFiles"`
}

// FileSize 是逐文件大小明细中的一项
type FileSize struct {
	File   string `json:"file"`
	Lines  int    `json:"lines"`
	Code   int    `json:"code"`
	IsTest bool   `json:"isTest"`
}

// ComplexFunction 描述指标报告中的一个函数
type ComplexFunction struct {
	File       string `json:"file"`
	Name       string `json:"name"`
	Line       int    `json:"line"`
	Complexity int    `json:"complexity"`
	Length     int    `json:"length"`
	Params     int    `json:"params"`
	Receiver   string `json:"receiver,omitempty"`
}

// DuplicateBlock 是一组完全相同的代码片段
type DuplicateBlock struct {
	Lines   int      `json:"lines"`
	Tokens  int      `json:"tokens"`
	Files   []string `json:"files"`
	Snippet string   `json:"snippet"`
}

// ---------------------------------------------------------------------------
// 复杂度
// ---------------------------------------------------------------------------

// cyclomaticComplexity 统计函数体里的判定点，采用标准的 McCabe 定义
// （1 加上分支数）
func cyclomaticComplexity(body *ast.BlockStmt) int {
	complexity := 1

	ast.Inspect(body, func(n ast.Node) bool {
		switch node := n.(type) {
		case *ast.IfStmt:
			complexity++
		case *ast.ForStmt:
			complexity++
		case *ast.RangeStmt:
			complexity++
		case *ast.CaseClause:
			complexity++
		case *ast.CommClause:
			// default 分支本身不增加判定点
			if node.Comm != nil {
				complexity++
			}
		case *ast.BinaryExpr:
			switch node.Op {
			case token.LAND, token.LOR: // && and ||
				complexity++
			}
		case *ast.SelectStmt:
			complexity++
		}
		return true
	})
	return complexity
}

func severityForComplexity(value, threshold int) string {
	switch {
	case value >= threshold*3:
		return SeverityError
	case value >= threshold*2:
		return SeverityWarning
	default:
		return SeverityWarning
	}
}

func qualifiedName(cf ComplexFunction) string {
	if cf.Receiver != "" {
		return cf.Receiver + "." + cf.Name
	}
	return cf.Name
}

func complexityStats(fns []ComplexFunction) (avg float64, max int) {
	if len(fns) == 0 {
		return 0, 0
	}
	var sum int
	for _, f := range fns {
		sum += f.Complexity
		if f.Complexity > max {
			max = f.Complexity
		}
	}
	return float64(sum) / float64(len(fns)), max
}

func topComplex(fns []ComplexFunction, threshold, limit int) []ComplexFunction {
	var out []ComplexFunction
	for _, f := range fns {
		if f.Complexity > threshold {
			out = append(out, f)
		}
	}
	slices.SortFunc(out, func(a, b ComplexFunction) int { return cmp.Compare(b.Complexity, a.Complexity) })
	if len(out) > limit {
		out = out[:limit]
	}
	return out
}

// topFilesBySize 返回最大的源文件，非测试文件排在前面
//
// 一个 4000 行的文件和四十个 100 行的文件是完全不同的审查问题，光看总计数字
// 分辨不出是哪种情况
func topFilesBySize(files []FileSize, limit int) []FileSize {
	out := make([]FileSize, len(files))
	copy(out, files)
	sort.Slice(out, func(i, j int) bool {
		// 大小相近时测试文件排在源文件之后
		// 它们通常是表驱动的，长度不是同一个信号
		if out[i].IsTest != out[j].IsTest {
			return !out[i].IsTest
		}
		return out[i].Lines > out[j].Lines
	})
	if len(out) > limit {
		out = out[:limit]
	}
	return out
}

func topLong(fns []ComplexFunction, threshold, limit int) []ComplexFunction {
	var out []ComplexFunction
	for _, f := range fns {
		if f.Length > threshold {
			out = append(out, f)
		}
	}
	slices.SortFunc(out, func(a, b ComplexFunction) int { return cmp.Compare(b.Length, a.Length) })
	if len(out) > limit {
		out = out[:limit]
	}
	return out
}

// ---------------------------------------------------------------------------
// 重复度
// ---------------------------------------------------------------------------

// detectDuplication 用行级滚动哈希找出重复的代码块
//
// 这是经典的「对归一化后的行做 Rabin-Karp」，把空白和注释归一化掉，对每个 N 行
// 的窗口取哈希，再把哈希相同的窗口归为一组。复杂度是行数的 O(n)，这才让它能在
// 一次扫描里跑完整个仓库
func (a *MetricsAnalyzer) detectDuplication(actx *Context) (float64, []DuplicateBlock, []Issue) {
	const window = 12

	type blockKey struct {
		hash  string
		lines int
	}

	// 哈希到出现位置（文件加起始行）的映射
	occurrences := map[string][]blockOccurrence{}
	normalizedByFile := map[string][]string{}
	// rawByFile 保留未归一化的原始行，只用于生成展示用的代码片段。归一化会去掉
	// 缩进、空行和注释，下标与原始行并不对应，所以片段必须另取
	rawByFile := map[string][]string{}
	// normToRaw 记录每行归一化结果在原始文件中的行号
	normToRaw := map[string][]int{}

	syntax := metricsSyntaxFor(actx.ProjectLanguage)
	for _, fi := range metricsFiles(actx) {
		if syntax.isTestFile(fi.Rel) {
			continue
		}
		raw, err := actx.Files.Read(fi, 2<<20)
		if err != nil {
			continue
		}
		rawLines := splitLines(string(raw))
		rawByFile[fi.Rel] = rawLines
		norm, indexes := normalizeForDuplication(string(raw))
		normalizedByFile[fi.Rel] = norm
		normToRaw[fi.Rel] = indexes
		if len(norm) < window {
			continue
		}
		for i := 0; i+window <= len(norm); i++ {
			h := hashLines(norm[i : i+window])
			occurrences[h] = append(occurrences[h], blockOccurrence{file: fi.Rel, line: i + 1})
		}
	}

	// 收集出现在多处以上的哈希
	var duplicateHashes []string
	for h, occ := range occurrences {
		if len(occ) > 1 {
			duplicateHashes = append(duplicateHashes, h)
		}
	}
	slices.Sort(duplicateHashes)

	// 把相邻的重复窗口合并成最大块并去掉重叠，这样一个 100 行的克隆是一条发现
	// 而不是 89 条
	seen := map[string]bool{}
	var blocks []DuplicateBlock
	duplicatedLines := map[string]map[int]bool{}

	for _, h := range duplicateHashes {
		occ := occurrences[h]
		for _, o := range occ {
			key := fmt.Sprintf("%s:%d", o.file, o.line)
			if seen[key] {
				continue
			}
			seen[key] = true

			if duplicatedLines[o.file] == nil {
				duplicatedLines[o.file] = map[int]bool{}
			}
			for i := range window {
				duplicatedLines[o.file][o.line+i] = true
			}
		}

		// 为界面记下一个代表性块
		files := map[string]bool{}
		for _, o := range occ {
			files[o.file] = true
		}
		if len(files) < 2 && len(occ) < 2 {
			continue
		}
		fileList := make([]string, 0, len(files))
		for f := range files {
			fileList = append(fileList, f)
		}
		slices.Sort(fileList)

		snippet := ""
		if lines := normalizedByFile[occ[0].file]; occ[0].line-1 < len(lines) {
			end := min(occ[0].line-1+window, len(lines))
			snippet = snippetFromRaw(rawByFile[occ[0].file], normToRaw[occ[0].file], occ[0].line-1, end)
		}

		blocks = append(blocks, DuplicateBlock{
			Lines:   window,
			Files:   fileList,
			Snippet: snippet,
		})
	}

	// 先按涉及范围再按大小排序，影响最大的克隆排在前面
	sort.Slice(blocks, func(i, j int) bool {
		if len(blocks[i].Files) != len(blocks[j].Files) {
			return len(blocks[i].Files) > len(blocks[j].Files)
		}
		return blocks[i].Lines > blocks[j].Lines
	})
	if len(blocks) > 20 {
		blocks = blocks[:20]
	}

	// 在非测试代码行上计算重复率
	totalLines, dupLines := 0, 0
	for file, lines := range normalizedByFile {
		totalLines += len(lines)
		dupLines += len(duplicatedLines[file])
	}
	var percent float64
	if totalLines > 0 {
		percent = float64(dupLines) / float64(totalLines) * 100
	}

	var issues []Issue
	if percent > 10 {
		severity := SeverityInfo
		if percent > 30 {
			severity = SeverityWarning
		}
		issues = append(issues, Issue{
			Analyzer: a.Name(),
			Rule:     "high-duplication",
			Severity: severity,
			Message:  fmt.Sprintf("%.1f%% of non-test lines are duplicated", percent),
			Advice:   "Extract the repeated blocks into shared helpers. Duplicated logic drifts: a fix applied to one copy usually misses the others.",
			File:     "",
			Line:     0,
			Column:   0,
		})
	}
	return percent, blocks, issues
}

type blockOccurrence struct {
	file string
	line int
}

// normalizeForDuplication 去掉注释、空行和缩进，让格式差异不至于掩盖克隆
//
// 第二个返回值是每行归一化结果对应的原始行号（从 0 开始），生成展示片段时要用，
// 因为归一化后的行与原始行不再一一对应
func normalizeForDuplication(src string) ([]string, []int) {
	var out []string
	var indexes []int
	inBlockComment := false

	for lineNo, raw := range splitLines(src) {
		line := strings.TrimSpace(raw)

		if inBlockComment {
			if idx := strings.Index(line, "*/"); idx >= 0 {
				line = strings.TrimSpace(line[idx+2:])
				inBlockComment = false
			} else {
				continue
			}
		}
		if line == "" {
			continue
		}
		// 去掉行尾注释（粗糙但有效，字符串字面量里出现 // 的情况罕见，对克隆检测
		// 没有影响）
		if idx := strings.Index(line, "//"); idx >= 0 {
			line = strings.TrimSpace(line[:idx])
		}
		if strings.HasPrefix(line, "/*") {
			if idx := strings.Index(line, "*/"); idx >= 0 {
				line = strings.TrimSpace(line[idx+2:])
			} else {
				inBlockComment = true
				continue
			}
		}
		if line == "" {
			continue
		}
		// 折叠内部的连续空白，对齐方式因此无关紧要
		out = append(out, strings.Join(strings.Fields(line), " "))
		indexes = append(indexes, lineNo)
	}
	return out, indexes
}

// snippetFromRaw 从原始源码中截取 [start, end) 这一段归一化行所对应的代码
//
// 归一化会剥掉缩进和行尾注释，直接拼归一化行会让展示出来的代码没有缩进。这里按行号
// 回到原始行，并保留中间的注释行和空行，截取范围以首尾两行的原始行号为界
func snippetFromRaw(raw []string, indexes []int, start, end int) string {
	if len(indexes) == 0 || start < 0 || start >= len(indexes) {
		return ""
	}
	first := indexes[start]
	last := indexes[min(end, len(indexes))-1]
	if first < 0 || last >= len(raw) || last < first {
		return ""
	}
	lines := make([]string, 0, last-first+1)
	for _, line := range raw[first : last+1] {
		lines = append(lines, strings.TrimRight(line, "\r"))
	}
	return strings.Join(lines, "\n")
}

func hashLines(lines []string) string {
	h := sha256.New()
	for _, l := range lines {
		h.Write([]byte(l))
		h.Write([]byte{'\n'})
	}
	return hex.EncodeToString(h.Sum(nil))[:16]
}

// ---------------------------------------------------------------------------
// 覆盖率
// ---------------------------------------------------------------------------

// detectCoverage 读取项目自己工具链产出的覆盖率报告
//
// Fizzra 从不自己跑测试，每个生态都已在流水线里产出了这个数字。格式因语言而异：
//
//	Go      coverage.out   （原生 profile）
//	Rust    lcov.info      （cargo llvm-cov --lcov、cargo tarpaulin）
//	Python  coverage.xml / coverage.json（coverage.py）
//	JS/TS   lcov.info / coverage-final.json（Istanbul）
func (a *MetricsAnalyzer) detectCoverage(actx *Context) (float64, *Issue) {
	for _, candidate := range coverageCandidates(actx.ProjectLanguage) {
		if !actx.Files.Exists(candidate) {
			continue
		}
		fi := findInSet(actx.Files, candidate)
		if fi == nil {
			continue
		}
		raw, err := actx.Files.Read(*fi, 16<<20)
		if err != nil {
			continue
		}
		if percent, ok := parseCoverageReport(string(raw)); ok {
			return percent, nil
		}
	}
	return 0, nil
}

// coverageCandidates 列出要查找的报告路径，按优先级排列
//
// 该语言自己的惯例排在最前，其余跟在后面，因为项目可能是被另一个生态的工具
// 插桩的。只列出 parseCoverageReport 认识的格式
func coverageCandidates(language string) []string {
	shared := []string{
		"coverage/lcov.info", "lcov.info",
		"coverage.xml", "coverage/coverage.xml",
		"coverage.json", "coverage/coverage.json", "coverage/coverage-final.json",
	}
	goOnly := []string{"coverage.out", "cover.out", "coverage.txt", "coverage/coverage.out"}

	var out []string
	switch strings.ToLower(strings.TrimSpace(language)) {
	case modelLanguageRust:
		// cargo llvm-cov 和 cargo tarpaulin 都输出 lcov 或 Cobertura
		out = append(out, "lcov.info", "target/lcov.info", "coverage/lcov.info", "cobertura.xml", "coverage.xml")
	case modelLanguagePython:
		out = append(out, "coverage.xml", "coverage/coverage.xml", "coverage.json", ".coverage.json")
	case modelLanguageJavaScript:
		out = append(out, "coverage/lcov.info", "coverage/coverage-final.json", "lcov.info")
	default:
		out = append(out, goOnly...)
	}
	return append(out, shared...)
}

// parseCoverageReport 嗅探格式并解析
//
// 靠嗅探而不是信任扩展名，因为同一个文件名在不同生态里对应不同格式，项目也可能
// 改过输出名。每个解析器都足够严格，猜错会失败而不是返回一个看似合理的错数字
func parseCoverageReport(src string) (float64, bool) {
	if percent, ok := parseGoCoverageProfile(src); ok {
		return percent, true
	}
	if percent, ok := parseLCOV(src); ok {
		return percent, true
	}
	if percent, ok := parseCobertura(strings.TrimSpace(src)); ok {
		return percent, true
	}
	if percent, ok := parseIstanbulJSON(src); ok {
		return percent, true
	}
	return 0, false
}

// parseLCOV 读取 lcov 追踪文件，这是 Rust 和 JS/TS 的常见输出
//
// 格式：`SF:<path>` 开启一条文件记录，`LF:<n>` 给出总行数，`LH:<n>` 给出命中的
// 行数。总计在各条记录间累加，lcov 自己的汇总也是这么算的
func parseLCOV(src string) (float64, bool) {
	var found, hit int
	sawRecord := false

	for line := range strings.SplitSeq(src, "\n") {
		line = strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(line, "LF:"):
			n, err := strconv.Atoi(strings.TrimSpace(line[3:]))
			if err == nil {
				found += n
				sawRecord = true
			}
		case strings.HasPrefix(line, "LH:"):
			n, err := strconv.Atoi(strings.TrimSpace(line[3:]))
			if err == nil {
				hit += n
			}
		}
	}

	if !sawRecord || found == 0 {
		return 0, false
	}
	return float64(hit) / float64(found) * 100, true
}

// parseCobertura 读取 Cobertura XML 的汇总，coverage.py 和 cargo tarpaulin 都用
// 这个格式
//
// 只读根元素的属性而不遍历整个文档，line-rate 已经是聚合好的数字，遍历每个
// <class> 去重算只会更慢，也不会更准
func parseCobertura(src string) (float64, bool) {
	if !strings.HasPrefix(src, "<?xml") && !strings.HasPrefix(src, "<coverage") {
		return 0, false
	}

	rate := regexp.MustCompile(`(?s)<coverage[^>]*\bline-rate\s*=\s*"([0-9.]+)"`).FindStringSubmatch(src)
	if rate == nil {
		return 0, false
	}
	value, err := strconv.ParseFloat(rate[1], 64)
	if err != nil {
		return 0, false
	}
	// Cobertura 给的是分数而不是百分比。乘以 100，而不是假定文件已经乘过，
	// 就是报 0.9% 和报 90% 的区别
	return value * 100, true
}

// parseIstanbulJSON 读取 Istanbul 的 coverage-final.json，这是 JS/TS 的默认格式
//
// 每一项是一个文件，含语句、分支和函数的映射，映射值带着命中次数。这里采用语句
// 覆盖率，与 Go profile 报的口径一致，这样同一个数字在不同语言里含义相同
func parseIstanbulJSON(src string) (float64, bool) {
	trimmed := strings.TrimSpace(src)
	if !strings.HasPrefix(trimmed, "{") {
		return 0, false
	}

	var report map[string]struct {
		StatementMap map[string]json.RawMessage `json:"statementMap"`
		S            map[string]int             `json:"s"`
	}
	if err := json.Unmarshal([]byte(trimmed), &report); err != nil {
		return 0, false
	}

	total, hit := 0, 0
	for _, file := range report {
		for id := range file.StatementMap {
			total++
			if file.S[id] > 0 {
				hit++
			}
		}
	}
	if total == 0 {
		return 0, false
	}
	return float64(hit) / float64(total) * 100, true
}

// parseGoCoverageProfile 从 Go 覆盖率 profile（`go test -coverprofile`）计算语句
// 覆盖率总计
//
// 格式：先是 `mode: set`，随后每行是
// `file:startLine.startCol,endLine.endCol numStmt count`
func parseGoCoverageProfile(src string) (float64, bool) {
	var totalStmt, coveredStmt int
	sawData := false

	for _, line := range splitLines(src) {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "mode:") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) != 3 {
			continue
		}
		var stmt, count int
		if _, err := fmt.Sscanf(fields[1], "%d", &stmt); err != nil {
			continue
		}
		if _, err := fmt.Sscanf(fields[2], "%d", &count); err != nil {
			continue
		}
		sawData = true
		totalStmt += stmt
		if count > 0 {
			coveredStmt += stmt
		}
	}
	if !sawData || totalStmt == 0 {
		return 0, false
	}
	return float64(coveredStmt) / float64(totalStmt) * 100, true
}

func findInSet(files *FileSet, rel string) *FileInfo {
	rel = filepath.ToSlash(rel)
	for _, fi := range files.All() {
		if fi.Rel == rel {
			f := fi
			return &f
		}
	}
	return nil
}

// metricsSyntax 持有行分类器需要的词法信息
//
// 只包含影响计数的部分，什么前缀开始行注释、什么界定块注释，以及如何认出测试
// 文件。规则引擎为它的匹配过滤器准备了一套更丰富的 Syntax，这一套刻意做到最小，
// 因为指标统计永远不需要了解字符串或作用域
type metricsSyntax struct {
	lineComment []string
	blockOpen   string
	blockClose  string
	testSuffix  []string
	testPrefix  []string
}

func (s metricsSyntax) isLineComment(trimmed string) bool {
	for _, prefix := range s.lineComment {
		if strings.HasPrefix(trimmed, prefix) {
			return true
		}
	}
	return false
}

// isTestFile 报告某个路径在该语言下是否为测试文件
//
// Go 和 Rust 用后缀标记测试（_test.go、_test.rs），Python 和 JavaScript 用前缀
// 或目录（test_*.py、*.test.ts、__tests__/）。判断错了会歪曲可维护性维度里的
// 测试覆盖率，所以它按语言区分，而不是一个 Go 形状的后缀检查
func (s metricsSyntax) isTestFile(rel string) bool {
	base := rel
	if idx := strings.LastIndex(rel, "/"); idx >= 0 {
		base = rel[idx+1:]
	}
	for _, suffix := range s.testSuffix {
		if strings.HasSuffix(base, suffix) {
			return true
		}
	}
	for _, prefix := range s.testPrefix {
		if strings.HasPrefix(base, prefix) {
			return true
		}
	}
	return false
}

// metricsSyntaxFor 返回某语言用于计数的语法，默认按 Go 处理
func metricsSyntaxFor(language string) metricsSyntax {
	switch strings.ToLower(strings.TrimSpace(language)) {
	case modelLanguagePython:
		return metricsSyntax{
			lineComment: []string{"#"},
			testSuffix:  []string{"_test.py"},
			testPrefix:  []string{"test_"},
		}
	case modelLanguageRust:
		return metricsSyntax{
			lineComment: []string{"//"},
			blockOpen:   "/*",
			blockClose:  "*/",
			testSuffix:  []string{"_test.rs"},
			testPrefix:  []string{"test_"},
		}
	case modelLanguageJavaScript:
		return metricsSyntax{
			lineComment: []string{"//"},
			blockOpen:   "/*",
			blockClose:  "*/",
			testSuffix: []string{
				".test.js", ".test.jsx", ".test.ts", ".test.tsx",
				".spec.js", ".spec.jsx", ".spec.ts", ".spec.tsx",
			},
		}
	case modelLanguageJava:
		return metricsSyntax{
			lineComment: []string{"//"},
			blockOpen:   "/*",
			blockClose:  "*/",
			testSuffix:  []string{"Test.java", "Tests.java", "IT.java"},
		}
	case modelLanguageCSharp:
		return metricsSyntax{
			lineComment: []string{"//"},
			blockOpen:   "/*",
			blockClose:  "*/",
			testSuffix:  []string{"Test.cs", "Tests.cs"},
		}
	case modelLanguageCpp:
		return metricsSyntax{
			lineComment: []string{"//"},
			blockOpen:   "/*",
			blockClose:  "*/",
			testSuffix:  []string{"_test.c", "_test.cc", "_test.cpp", "_tests.cpp"},
			testPrefix:  []string{"test_"},
		}
	case modelLanguagePHP:
		// PHP 三种行注释并存，且 `#` 常用于注解风格，# 起始的行要能被剥掉
		return metricsSyntax{
			lineComment: []string{"//", "#"},
			blockOpen:   "/*",
			blockClose:  "*/",
			testSuffix:  []string{"Test.php", "Tests.php"},
		}
	default:
		return metricsSyntax{
			lineComment: []string{"//"},
			blockOpen:   "/*",
			blockClose:  "*/",
			testSuffix:  []string{"_test.go"},
		}
	}
}

// metricsFiles 返回某语言项目下要度量的源文件
func metricsFiles(actx *Context) []FileInfo {
	switch actx.ProjectLanguage {
	case modelLanguagePython:
		return actx.Files.WithExt(".py", ".pyi")
	case modelLanguageRust:
		return actx.Files.WithExt(".rs")
	case modelLanguageJavaScript:
		return actx.Files.WithExt(".js", ".jsx", ".ts", ".tsx", ".mjs", ".cjs")
	case modelLanguageJava:
		return actx.Files.WithExt(".java")
	case modelLanguageCSharp:
		return actx.Files.WithExt(".cs")
	case modelLanguageCpp:
		return actx.Files.WithExt(
			".c", ".h", ".cc", ".cpp", ".cxx", ".hpp", ".hxx", ".hh")
	case modelLanguagePHP:
		return actx.Files.WithExt(".php")
	default:
		return actx.Files.GoFiles()
	}
}
