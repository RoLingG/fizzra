package analyzer

import (
	"bufio"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// defaultSkipDirs 是永远不值得扫描的目录
// 构建产物和依赖是已经看过的内容的重复，VCS 元数据根本不是用户代码
var defaultSkipDirs = map[string]bool{
	".git":         true,
	".idea":        true,
	".vscode":      true,
	".github":      false, // keep: workflow files are reviewed
	"node_modules": true,
	"vendor":       true,
	"dist":         true,
	"build":        true,
	"bin":          true,
	"tmp":          true,
	".cache":       true,
	".next":        true,
	"coverage":     true,
	"testdata":     false, // keep: rules may target testdata explicitly
}

// FileSet 是项目树的、尊重忽略规则的视图
//
// 只遍历一次并复用结果很重要，一个有五千个文件的项目否则会被每个分析器各遍历
// 一遍，而指标分析器反正需要文件内容
type FileSet struct {
	root  string
	files []FileInfo
	// ignores 保存来自 .gitignore 的模式，加上调用方额外提供的
	ignores []string
}

// FileInfo 描述集合中的一个文件
type FileInfo struct {
	// Abs 是绝对路径
	Abs string
	// Rel 是相对项目根目录、以斜杠分隔的路径
	Rel string
	// Ext 含点号且为小写（".go"）
	Ext string
	// Size 是文件大小，单位为字节
	Size int64
}

// NewFileSet 遍历 root 并收集值得分析的文件
func NewFileSet(root string) (*FileSet, error) {
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	if _, err := os.Stat(absRoot); err != nil {
		return nil, err
	}

	set := &FileSet{root: absRoot, ignores: readGitignore(absRoot)}

	err = filepath.WalkDir(absRoot, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			// 某个分支上的权限问题不能让整个扫描中断
			if d != nil && d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}

		rel, relErr := filepath.Rel(absRoot, path)
		if relErr != nil {
			return nil
		}
		rel = filepath.ToSlash(rel)
		if rel == "." {
			return nil
		}

		if d.IsDir() {
			if shouldSkipDir(d.Name(), rel) || set.ignored(rel+"/") {
				return fs.SkipDir
			}
			return nil
		}
		if !d.Type().IsRegular() {
			return nil
		}
		if set.ignored(rel) {
			return nil
		}

		info, infoErr := d.Info()
		if infoErr != nil {
			return nil
		}

		set.files = append(set.files, FileInfo{
			Abs:  path,
			Rel:  rel,
			Ext:  strings.ToLower(filepath.Ext(path)),
			Size: info.Size(),
		})
		return nil
	})
	if err != nil {
		return nil, err
	}

	sort.Slice(set.files, func(i, j int) bool { return set.files[i].Rel < set.files[j].Rel })
	return set, nil
}

// Root 是项目的绝对根目录
func (f *FileSet) Root() string { return f.root }

// All 返回集合中的所有文件
func (f *FileSet) All() []FileInfo { return f.files }

// WithExt 返回带有所给任一扩展名的文件（扩展名含前导点）
func (f *FileSet) WithExt(exts ...string) []FileInfo {
	want := make(map[string]bool, len(exts))
	for _, e := range exts {
		want[strings.ToLower(e)] = true
	}
	out := make([]FileInfo, 0, len(f.files))
	for _, fi := range f.files {
		if want[fi.Ext] {
			out = append(out, fi)
		}
	}
	return out
}

// GoFiles 返回 Go 源文件，含测试
func (f *FileSet) GoFiles() []FileInfo { return f.WithExt(".go") }

// TestFiles 返回 Go 测试文件
func (f *FileSet) TestFiles() []FileInfo {
	var out []FileInfo
	for _, fi := range f.files {
		if fi.Ext == ".go" && strings.HasSuffix(fi.Rel, "_test.go") {
			out = append(out, fi)
		}
	}
	return out
}

// NonTestGoFiles 返回非测试的 Go 文件
func (f *FileSet) NonTestGoFiles() []FileInfo {
	var out []FileInfo
	for _, fi := range f.files {
		if fi.Ext == ".go" && !strings.HasSuffix(fi.Rel, "_test.go") {
			out = append(out, fi)
		}
	}
	return out
}

// Exists 报告某路径（文件或目录）相对根目录是否存在
func (f *FileSet) Exists(rel string) bool {
	_, err := os.Stat(filepath.Join(f.root, filepath.FromSlash(rel)))
	return err == nil
}

// Read 返回集合中某文件的内容，上限为 maxBytes，以约束病态输入下的内存占用
func (f *FileSet) Read(fi FileInfo, maxBytes int64) ([]byte, error) {
	if maxBytes > 0 && fi.Size > maxBytes {
		fh, err := os.Open(fileiPath(fi))
		if err != nil {
			return nil, err
		}
		defer fh.Close()
		buf := make([]byte, maxBytes)
		n, err := fh.Read(buf)
		if err != nil && n == 0 {
			return nil, err
		}
		return buf[:n], nil
	}
	return os.ReadFile(fileiPath(fi))
}

func fileiPath(fi FileInfo) string { return fi.Abs }

func shouldSkipDir(name, rel string) bool {
	if skip, ok := defaultSkipDirs[name]; ok {
		return skip
	}
	// 后端仓库里嵌套的模块和前端产物
	if name == "frontend" && rel != "frontend" {
		return false
	}
	return false
}

// readGitignore 在存在时从 .gitignore 加载忽略模式
func readGitignore(root string) []string {
	fh, err := os.Open(filepath.Join(root, ".gitignore"))
	if err != nil {
		return nil
	}
	defer fh.Close()

	var patterns []string
	scanner := bufio.NewScanner(fh)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		patterns = append(patterns, strings.TrimPrefix(line, "/"))
	}
	return patterns
}

// ignored 报告某路径是否匹配已加载的忽略模式
//
// 这是 gitignore 语义的一个务实子集，精确名字、目录前缀和 `*` 通配符。它的存在
// 是为了把明显无关的文件挡在报告之外，而不是做一个忠实的 gitignore 实现
func (f *FileSet) ignored(rel string) bool {
	if len(f.ignores) == 0 {
		return false
	}
	rel = strings.TrimSuffix(filepath.ToSlash(rel), "/")
	if rel == "" {
		return false
	}
	base := rel
	if i := strings.LastIndex(rel, "/"); i >= 0 {
		base = rel[i+1:]
	}

	for _, p := range f.ignores {
		if p == "" {
			continue
		}
		dirOnly := strings.HasSuffix(p, "/")
		p = strings.TrimSuffix(p, "/")

		if dirOnly && !strings.Contains(rel, "/") && !strings.HasSuffix(filepath.ToSlash(rel), "/") {
			// "build/" 这种只针对目录的模式，也能匹配树中更深处同名的目录
			if base == p {
				return true
			}
			continue
		}
		if matchIgnorePattern(p, rel) {
			return true
		}
	}
	return false
}

func matchIgnorePattern(pattern, rel string) bool {
	if strings.HasSuffix(pattern, "*") {
		return strings.HasPrefix(rel, strings.TrimSuffix(pattern, "*"))
	}
	if strings.HasPrefix(pattern, "*") {
		return strings.HasSuffix(rel, strings.TrimPrefix(pattern, "*"))
	}
	if pattern == rel {
		return true
	}
	// 裸名字在任意深度都匹配
	if !strings.Contains(pattern, "/") {
		for _, part := range strings.Split(rel, "/") {
			if part == pattern {
				return true
			}
		}
	}
	return false
}
