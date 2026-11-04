package analyzer

import (
	"context"
	"encoding/xml"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Java 分析，包装 SpotBugs，读它的 XML 输出
//
// 与 PMD 互补。PMD 读源码，SpotBugs 读字节码，硬编码密码、SQL 拼接这两类 PMD
// 报不出，是这个分析器存在的理由。代价是需要编译产物，找不到时返回
// ErrNothingToCheck 而不是失败

// SpotBugsAnalyzer 运行 SpotBugs
type SpotBugsAnalyzer struct{ javaOnly }

// NewSpotBugsAnalyzer 构造该分析器
func NewSpotBugsAnalyzer() Analyzer { return &SpotBugsAnalyzer{} }

func (a *SpotBugsAnalyzer) Name() string { return "spotbugs" }

func (a *SpotBugsAnalyzer) Description() string {
	return "Runs SpotBugs against compiled Java classes: bytecode-level defects such as hard-coded credentials, SQL built from user input, unclosed resources and definite null dereferences. Requires a build output."
}

func (a *SpotBugsAnalyzer) Category() string { return "security" }

func (a *SpotBugsAnalyzer) Requires() []string { return []string{"spotbugs"} }

// InstallHint 给出可直接复制的下载地址，分步说明在前端 toolHints 词条里
func (a *SpotBugsAnalyzer) InstallHint() string {
	return "https://github.com/spotbugs/spotbugs/releases"
}

// ProbeCommands 让探测认 PATH 上的 spotbugs 以及 bin/spotbugs.bat
func (a *SpotBugsAnalyzer) ProbeCommands() [][]string {
	return [][]string{{"spotbugs"}, {"spotbugs.bat"}}
}

// sbBugInstance 对应 SpotBugs XML 里的一个 BugInstance
//
// SourceLine 按作用域嵌套三层（Class 下的、Method 下的、直接子节点下的），只有直接
// 子节点那条带真实行号，另两个是类和方法范围。取错会让每条发现都落在类起始行
type sbBugInstance struct {
	Type     string `xml:"type,attr"`
	Priority int    `xml:"priority,attr"`
	Rank     int    `xml:"rank,attr"`
	Category string `xml:"category,attr"`
	CWEID    string `xml:"cweid,attr"`

	ShortMessage string `xml:"ShortMessage"`
	LongMessage  string `xml:"LongMessage"`

	// Line 是直接子节点里的 SourceLine，类级问题（未使用字段）没有这一层，行号为 0
	Line *sbSourceLine `xml:"SourceLine"`

	Class *struct {
		Classname string `xml:"classname,attr"`
	} `xml:"Class"`
}

type sbSourceLine struct {
	Start      int    `xml:"start,attr"`
	End        int    `xml:"end,attr"`
	SourceFile string `xml:"sourcefile,attr"`
	SourcePath string `xml:"sourcepath,attr"`
}

// sbReport 对应 `spotbugs -textui -xml:withMessages` 的输出
type sbReport struct {
	XMLName xml.Name        `xml:"BugCollection"`
	Bugs    []sbBugInstance `xml:"BugInstance"`
	// Errors 里含 missingClasses：分析时缺了依赖类，结果的完整性因此打折
	Errors struct {
		MissingClasses []string `xml:"MissingClass"`
	} `xml:"Errors"`
}

func (a *SpotBugsAnalyzer) Analyze(ctx context.Context, projectPath string) ([]Issue, error) {
	return nil, nil
}

func (a *SpotBugsAnalyzer) AnalyzeWithContext(actx *Context) ([]Issue, error) {
	target, err := spotsBugsTarget(actx.ProjectPath)
	if err != nil {
		return nil, err
	}
	if target == "" {
		// 没构建过的项目不该被标成扫描失败
		return nil, ErrNothingToCheck(
			"no compiled output found; build the project first (target/classes or build/classes)")
	}

	cmd, err := spotBugsCommand(actx)
	if err != nil {
		return nil, err
	}

	// 结果写临时文件：文本模式下 SpotBugs 还会往 stdout 追加人类可读的汇总
	out, err := os.CreateTemp("", "fizzra-spotbugs-*.xml")
	if err != nil {
		return nil, fmt.Errorf("create spotbugs report file: %w", err)
	}
	outPath := out.Name()
	out.Close()
	defer os.Remove(outPath)

	args := []string{
		"-textui",
		"-xml:withMessages",
		"-output", outPath,
		// 默认是 -medium，会丢掉 priority 3 的发现，那里面有真实缺陷
		"-low",
		target,
	}

	// SpotBugs 找到问题时退出码也是 0，这里只反映「跑不跑得起来」
	if _, runErr := actx.Runner.Run(actx.Context, actx.ProjectPath, cmd.name, args...); runErr != nil {
		if IsTimeout(runErr) {
			return nil, runErr
		}
		return nil, fmt.Errorf("spotbugs failed: %w", runErr)
	}

	data, err := os.ReadFile(outPath)
	if err != nil {
		return nil, fmt.Errorf("read spotbugs report: %w", err)
	}

	var report sbReport
	if err := xml.Unmarshal(data, &report); err != nil {
		return nil, fmt.Errorf("parse spotbugs report: %w", err)
	}

	// 缺依赖类时部分检测器不会触发。不当作失败，但要留痕，否则「没有发现」会被
	// 读成「没有问题」
	if n := len(report.Errors.MissingClasses); n > 0 {
		actx.Log.Debug("spotbugs ran with missing classes; some detectors may not fire",
			"missing", n, "first", report.Errors.MissingClasses[0])
	}

	snippets := NewSnippetReader(actx.Files)
	// sourcepath 相对源码根而不是项目根（com/x/Probe.java，实际在
	// src/main/java/com/x/Probe.java）。用项目文件清单还原，否则路径打不开
	index := newPathIndex(actx.Files)

	issues := make([]Issue, 0, len(report.Bugs))
	for _, b := range report.Bugs {
		line, file := 0, ""
		switch {
		case b.Line != nil && b.Line.SourcePath != "":
			line = b.Line.Start
			file = index.resolve(b.Line.SourcePath)
		case b.Line != nil && b.Line.SourceFile != "":
			// 只有文件名没有路径：按文件名后缀查，查不到就只留文件名
			line = b.Line.Start
			file = index.resolve(b.Line.SourceFile)
		case b.Class != nil:
			// 类级发现（未使用字段、未使用方法）不给行号，但文件能还原
			file = index.resolve(classNameToPath(b.Class.Classname))
		}

		issues = append(issues, Issue{
			Analyzer: a.Name(),
			Rule:     b.Type,
			Severity: mapSpotBugsSeverity(b.Priority, b.Category),
			Message:  firstNonEmptyStr(b.LongMessage, b.ShortMessage),
			File:     file,
			Line:     line,
			Code:     snippetAt(snippets, file, line),
			DocURL:   spotBugsDocURL(b.Type),
		})
	}
	return issues, nil
}

// classNameToPath 把全限定类名转成源码路径，com.example.Probe 转成 com/example/Probe.java
func classNameToPath(classname string) string {
	if classname == "" {
		return ""
	}
	// 内部类名是 Outer$Inner，源码文件叫 Outer.java
	if i := strings.IndexByte(classname, '$'); i >= 0 {
		classname = classname[:i]
	}
	return strings.ReplaceAll(classname, ".", "/") + ".java"
}

// pathIndex 按后缀把「相对源码根」的路径还原成项目里的真实相对路径
//
// 唯一命中才采用，多命中或没命中就保留原样，猜错路径比不猜更糟
type pathIndex struct {
	bySuffix map[string][]string
}

func newPathIndex(files *FileSet) *pathIndex {
	idx := &pathIndex{bySuffix: map[string][]string{}}
	if files == nil {
		return idx
	}
	for _, fi := range files.WithExt(".java") {
		rel := fi.Rel
		idx.bySuffix[rel] = append(idx.bySuffix[rel], rel)
		// 各级后缀都登记，让 com/x/Probe.java 能匹配到 src/main/java/com/x/Probe.java
		parts := strings.Split(rel, "/")
		for i := 1; i < len(parts); i++ {
			suffix := strings.Join(parts[i:], "/")
			idx.bySuffix[suffix] = append(idx.bySuffix[suffix], rel)
		}
	}
	return idx
}

func (p *pathIndex) resolve(suffix string) string {
	if suffix == "" {
		return ""
	}
	if hits := p.bySuffix[suffix]; len(hits) == 1 {
		return hits[0]
	}
	return suffix
}

// spotsBugsTarget 找到要分析的编译产物，先 class 目录，再 jar
// 同时覆盖 Maven（target/）与 Gradle（build/）两种布局
func spotsBugsTarget(projectPath string) (string, error) {
	candidates := []string{
		filepath.Join(projectPath, "target", "classes"),
		filepath.Join(projectPath, "build", "classes", "java", "main"),
		filepath.Join(projectPath, "build", "classes"),
	}
	for _, c := range candidates {
		if info, err := os.Stat(c); err == nil && info.IsDir() {
			return c, nil
		}
	}

	// 再找 jar：target/*.jar 和 build/libs/*.jar
	for _, dir := range []string{
		filepath.Join(projectPath, "target"),
		filepath.Join(projectPath, "build", "libs"),
	} {
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, e := range entries {
			if e.IsDir() || !strings.HasSuffix(e.Name(), ".jar") {
				continue
			}
			// 跳过 sources/javadoc 包：那些不含 class
			if strings.HasSuffix(e.Name(), "-sources.jar") || strings.HasSuffix(e.Name(), "-javadoc.jar") {
				continue
			}
			return filepath.Join(dir, e.Name()), nil
		}
	}
	return "", nil
}

// spotBugsInvocation 是解析好的命令
type spotBugsInvocation struct {
	name string
}

// spotBugsCommand 定位 SpotBugs，先 PATH，再看 FIZZRA_SPOTBUGS_HOME
//
// 必须用 .bat/脚本入口而不是 lib/spotbugs.jar，4.x 的两个前端语法不兼容，直接跑
// jar 是老式驱动，认不出 analyze 之类的子命令
func spotBugsCommand(actx *Context) (spotBugsInvocation, error) {
	if path, err := LookCandidates(actx.Runner, "spotbugs", "spotbugs.bat"); err == nil {
		return spotBugsInvocation{name: path}, nil
	}
	if home := toolHome("FIZZRA_SPOTBUGS_HOME"); home != "" {
		for _, name := range []string{
			filepath.Join(home, "bin", "spotbugs.bat"),
			filepath.Join(home, "bin", "spotbugs"),
		} {
			if fileExists(name) {
				return spotBugsInvocation{name: name}, nil
			}
		}
	}
	return spotBugsInvocation{}, ErrToolUnavailable("spotbugs")
}

// mapSpotBugsSeverity 映射优先级
//
// 和 PMD 不同，这里的 priority 1-3 是缺陷严重度而不是规则置信度，可以直接用。
// CORRECTNESS 与 SECURITY 再提一档，它们是「会出错」和「会泄露」
func mapSpotBugsSeverity(priority int, category string) Severity {
	base := SeverityWarning
	switch priority {
	case 1:
		base = SeverityError
	case 2:
		base = SeverityWarning
	case 3:
		base = SeverityInfo
	}

	switch category {
	case "CORRECTNESS", "SECURITY":
		// 确定的空指针解引用不该因为置信度低就降成提示
		if base == SeverityInfo {
			return SeverityWarning
		}
		return SeverityError
	}
	return base
}

// spotBugsDocURL 给出规则说明的固定地址，报告里不带链接，但地址格式可推导
func spotBugsDocURL(kind string) string {
	if kind == "" {
		return ""
	}
	return "https://spotbugs.readthedocs.io/en/stable/bugDescriptions.html#" + strings.ToLower(kind)
}

// snippetAt 回读问题所在的源码行，分析的是字节码，行号要自己取
func snippetAt(snippets *SnippetReader, file string, line int) string {
	if snippets == nil || file == "" || line <= 0 {
		return ""
	}
	return snippets.Line(file, line)
}

// 编译期断言：两个分析器都必须满足 Analyzer 契约
var (
	_ Analyzer = (*PmdAnalyzer)(nil)
	_ Analyzer = (*SpotBugsAnalyzer)(nil)
)
