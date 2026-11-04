package analyzer

import (
	"path"
	"path/filepath"
	"strings"
	"sync"
)

// 为外部工具报出的发现提供源码片段
//
// Go 自家的工具会把出问题的源码附在输出里（golangci-lint 每条问题带一行
// source），所以那些分析器填 Issue.Code 是顺手的事。外部 linter 不这样，
// biome、ruff、clippy 只报文件名、行号和列号，它们指望终端渲染器自己把文件
// 读回来
//
// 这个读取不算贵但也不便宜，天真地做（每条发现读一次文件）最坏情况下是
// 平方复杂度，因为一趟 ruff 会在同一个文件里报出几十条发现。SnippetReader
// 的存在就是为了每个文件只读一次，并在读不了时安静失败，源码片段只是锦上
// 添花，丢了它扫描依然是完整的

// snippetMaxBytes 限定值得缓存的文件大小
//
// 比这更大的文件几乎肯定是生成物或被压缩过，例如构建产出的单行数兆的 JS。
// 对这类文件按换行切分只会得到一行巨大的内容，缓存几个就等于在整个扫描期间
// 把整包留在内存里，只为显示一行乱码。跳过是更划算的取舍，发现本身仍带着
// 文件名和行号
const snippetMaxBytes = 2 << 20 // 2 MiB

// snippetCacheMaxFiles 限定同时保留多少个文件
//
// 占内存的是每个文件切好的行，不是 map 的键，所以要设上限，否则扫描一个大
// monorepo 时内存会一直涨。超过上限后片段照算但不缓存，正确性不受影响，
// 只是失去了去重
//
// absent 刻意不设上限，它每个无法解析的路径只存一个 bool，上限由发现数量
// 决定而不是文件大小，设了上限反而会让之后的每条发现都去重试一次已知读不了
// 的读取
const snippetCacheMaxFiles = 512

// SnippetReader 读取发现背后的那一行源码
//
// 可并发使用，分析器并行运行且共用底层的 FileSet。它保留项目根目录，以便把
// 工具报出的绝对路径还原成索引使用的项目相对路径
type SnippetReader struct {
	files  *FileSet
	byRel  map[string]FileInfo
	root   string
	mu     sync.Mutex
	lines  map[string][]string
	absent map[string]bool
}

// NewSnippetReader 为文件集合建索引，便于按相对路径查找
func NewSnippetReader(files *FileSet) *SnippetReader {
	r := &SnippetReader{
		files:  files,
		lines:  map[string][]string{},
		absent: map[string]bool{},
	}
	if files != nil {
		all := files.All()
		r.byRel = make(map[string]FileInfo, len(all))
		for _, fi := range all {
			r.byRel[fi.Rel] = fi
		}
		r.root = filepath.ToSlash(files.Root())
	}
	return r
}

// Line 返回指定行（从 1 开始）的源码，取不到时返回空串
//
// 所有失败原因都只归为空串，文件未知、文件读不了、文件过大、行号超出末尾。
// 调用方把空串理解为「该分析器对这个发现没有片段」，界面会整块不渲染，所以
// 这些情况之间的区别不必让用户看到
func (r *SnippetReader) Line(rel string, line int) string {
	if r == nil || rel == "" || line <= 0 {
		return ""
	}
	rel = r.resolve(rel)
	if rel == "" {
		return ""
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	if r.absent[rel] {
		return ""
	}
	lines, ok := r.lines[rel]
	if !ok {
		fi, found := r.byRel[rel]
		if !found || fi.Size > snippetMaxBytes {
			r.absent[rel] = true
			return ""
		}
		raw, err := r.files.Read(fi, snippetMaxBytes)
		if err != nil {
			r.absent[rel] = true
			return ""
		}
		lines = splitLines(string(raw))
		if len(r.lines) < snippetCacheMaxFiles {
			r.lines[rel] = lines
		}
	}

	if line-1 >= len(lines) {
		return ""
	}
	return strings.TrimRight(lines[line-1], "\r")
}

// File 把项目相对路径解析为对应的 FileInfo，路径不在集合里时返回 nil
//
// 存在的意义是让已经持有路径的调用方不必再遍历 FileSet.All() 找记录，
// 索引在 NewSnippetReader 里只建一次
func (r *SnippetReader) File(rel string) *FileInfo {
	if r == nil || rel == "" {
		return nil
	}
	fi, ok := r.byRel[r.resolve(rel)]
	if !ok {
		return nil
	}
	return &fi
}

// resolve 把工具报出的路径映射成索引使用的项目相对路径，不属于本项目时返回空串
func (r *SnippetReader) resolve(p string) string {
	if p == "" {
		return ""
	}
	p = filepath.ToSlash(p)
	p = strings.TrimPrefix(p, "./")

	// 工具报绝对路径时剥掉项目根目录。ruff 总是这样（`C:ppad.py`），
	// biome 也是，它的 JSON 带的是绝对路径，biome 分析器因此要调 relToProject。
	// 少了这一步，这些工具的片段会全部静默为空，而且看不出来，因为空串同时
	// 也是「没有这个文件」的答案
	if filepath.IsAbs(filepath.FromSlash(p)) {
		if rel, err := filepath.Rel(filepath.FromSlash(r.root), filepath.FromSlash(p)); err == nil {
			rel = filepath.ToSlash(rel)
			// 根目录之外的路径不可能在集合里
			if strings.HasPrefix(rel, "../") {
				return ""
			}
			p = rel
		}
	}

	return path.Clean(p)
}
