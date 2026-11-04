package analyzer

import (
	"bufio"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// 依赖类发现的来源取值
//
// 声明在这里而不是复用 model.Origin*，是为了让本包不沾持久层模型，与分析器包的
// 其余部分一致。两组取值在 service 层的映射里有断言保证相等，一旦漂移会以测试
// 失败的形式暴露，而不是变成静默匹配不上的字符串
const (
	// OriginDirect 是项目自己声明的依赖
	OriginDirect = "direct"
	// OriginTransitive 是只能经由另一个依赖到达的依赖
	OriginTransitive = "transitive"
)

/*
 * Cargo.lock dependency graph.
 *
 * cargo-audit reports an advisory against whichever locked package matches,
 * with no notion of whether the reader can act on it. For a Rust project that
 * distinction is the whole question: a `tauri` app's advisory list is almost
 * entirely packages it never chose, reached through `reqwest` or `tauri`
 * itself, and "upgrade `rustls-webpki`" is not something its author can do —
 * they do not depend on it.
 *
 * This file answers the one question the advisory cannot: is this package a
 * dependency the project declared, or something further down the chain, and if
 * so, through what path?
 *
 * The lockfile is the only reliable source. Cargo.toml lists what was asked
 * for; Cargo.lock records what was actually resolved, including every
 * transitive pin, and that is what the advisories match against.
 */

// lockPackage 是一个 `[[package]]` 块
type lockPackage struct {
	name    string
	version string
	// deps 是原始的依赖条目，每项形如 "name" 或 "name version"。锁文件里该名字
	// 只有一个版本时用裸形式，有多个时用带版本的形式区分
	deps []string
	// 没有 source 行的包 local 为 true，表示它是工作区成员而不是从注册表拉下来的
	// 东西
	//
	// 默认就是 local，出现 source 行时才清掉，因为该键的缺席正是工作区成员的
	// 标志。初始化为 false 会让任何包都成不了根，遍历于是什么都找不到
	local bool
}

// newLockPackage 返回一个带默认值的包块
// 默认值由 Cargo.lock 的形态决定：每个条目一开始都像工作区成员，直到出现 source 行说明不是
func newLockPackage() *lockPackage { return &lockPackage{local: true} }

// CargoLockGraph 是解析好的 Cargo.lock，为可达性查询建了索引
type CargoLockGraph struct {
	// byKey 把 "name version" 映射到它的包
	byKey map[string]*lockPackage
	// byName 把裸名字映射到所有已存在的版本。有多项就意味着裸依赖形式是有歧义的
	byName map[string][]string
	// pathTo 记录从工作区根到每个包的链路，最外层在前，不含根。由 walk 填充
	pathTo map[string][]string
	// roots 是工作区成员，可达性查询的起点
	roots []string
}

// packageKey 是一个已锁定包的标准标识
func packageKey(name, version string) string { return name + " " + version }

var (
	lockNameRe    = regexp.MustCompile(`^name = "(.+)"$`)
	lockVersionRe = regexp.MustCompile(`^version = "(.+)"$`)
	// 依赖条目带引号并以逗号分隔，但 Cargo 会省略最后一项之后的逗号。两种形式都
	// 会出现，所以显式匹配逗号而不是要求它缺席，锚定末尾的 `"` 会静默丢掉每一个
	// 带逗号的条目
	lockDepRe = regexp.MustCompile(`^"(.+?)",?$`)
)

/*
 * ParseCargoLock reads a Cargo.lock into a graph.
 *
 * The parser is deliberately hand-rolled rather than pulled from a TOML
 * library. Cargo.lock is a generated file with a fixed, narrow shape — a flat
 * sequence of `[[package]]` tables whose values are strings and string arrays —
 * and the module already avoids adding dependencies for file formats it can
 * read in thirty lines. If Cargo's format ever grows a construct this misses,
 * the failure is a parse error rather than a wrong answer, because anything
 * unrecognised is skipped rather than guessed at.
 */
func ParseCargoLock(path string) (*CargoLockGraph, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	g := &CargoLockGraph{
		byKey:  map[string]*lockPackage{},
		byName: map[string][]string{},
		pathTo: map[string][]string{},
	}

	var current *lockPackage
	var inDeps bool

	flush := func() {
		if current == nil || current.name == "" {
			return
		}
		key := packageKey(current.name, current.version)
		g.byKey[key] = current
		g.byName[current.name] = append(g.byName[current.name], key)
		if current.local {
			g.roots = append(g.roots, key)
		}
	}

	scanner := bufio.NewScanner(file)
	// 锁文件的行都很短，但仍然调高缓冲上限，免得一个不寻常的（或手工生成的）
	// 文件在 64KiB 处静默让扫描失败
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())

		if line == "[[package]]" {
			flush()
			current = newLockPackage()
			inDeps = false
			continue
		}
		if current == nil {
			continue
		}

		// 新表格会终结任何进行中的依赖列表
		if strings.HasPrefix(line, "[") {
			inDeps = false
			continue
		}

		if inDeps {
			if line == "]" {
				inDeps = false
				continue
			}
			if m := lockDepRe.FindStringSubmatch(line); m != nil {
				current.deps = append(current.deps, m[1])
			}
			continue
		}

		switch {
		case lockNameRe.MatchString(line):
			current.name = lockNameRe.FindStringSubmatch(line)[1]
		case lockVersionRe.MatchString(line):
			current.version = lockVersionRe.FindStringSubmatch(line)[1]
		case line == "dependencies = [":
			inDeps = true
		case strings.HasPrefix(line, "source = "):
			// 只是个存在标记，值用不上
			current.local = false
		}
	}
	flush()

	if err := scanner.Err(); err != nil {
		return nil, err
	}

	g.walk()
	return g, nil
}

// resolve 把原始依赖条目映射到它可能指向的包键
//
// 裸形式会解析到该名字的所有版本，这也是 walk 接收一个集合而不是单个键的原因，
// 锁文件里合法地可能同时有一个 crate 的两个大版本，猜错就会给出错误的路径。
// 可达性不受这个歧义影响，只要任一版本可达，这个名字就可达
func (g *CargoLockGraph) resolve(dep string) []string {
	if key := dep; g.byKey[key] != nil {
		return []string{key}
	}
	return g.byName[dep]
}

// walk 按依赖顺序计算从工作区根到每个可达包的最短路径
//
// 从所有根同时做广度优先，所以有两条路可达的包会归给较短的那条。这对展示给读者
// 的消息很重要，说出两跳的链路比说出五跳的有用得多
func (g *CargoLockGraph) walk() {
	type item struct {
		key  string
		path []string
	}

	var queue []item
	for _, root := range g.roots {
		for _, dep := range g.byKey[root].deps {
			for _, target := range g.resolve(dep) {
				queue = append(queue, item{target, []string{target}})
			}
		}
	}

	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]

		if _, seen := g.pathTo[cur.key]; seen {
			continue
		}
		g.pathTo[cur.key] = cur.path

		pkg := g.byKey[cur.key]
		if pkg == nil {
			continue
		}
		for _, dep := range pkg.deps {
			for _, target := range g.resolve(dep) {
				if _, seen := g.pathTo[target]; seen {
					continue
				}
				// 复制切片，往共享的底层数组上追加会让一个分支的路径污染另一个分支
				next := make([]string, len(cur.path), len(cur.path)+1)
				copy(next, cur.path)
				queue = append(queue, item{target, append(next, target)})
			}
		}
	}
}

// Origin 对照依赖图给一个包名加版本分类
//
// 返回 model.OriginDirect、model.OriginTransitive，或在该包根本不在图里时返回
// 空串。那意味着通告命中的东西锁文件里没有，最稳妥的读法是把它当成项目自己的
// 事，而不是替它编一条链路出来
func (g *CargoLockGraph) Origin(name, version string) string {
	if g == nil {
		return ""
	}
	path, ok := g.pathTo[packageKey(name, version)]
	if !ok {
		return ""
	}
	// 长度为 1 的路径意味着该包是某个工作区成员的直接依赖，也就是读者自己声明的
	if len(path) <= 1 {
		return OriginDirect
	}
	return OriginTransitive
}

// Path 返回引入某个包的依赖链，最外层在前，包名本身在最后
//
// 只给名字不带版本。调用方把它渲染成一句叙述（"reached through reqwest ->
// rustls -> rustls-webpki"），那里每环都带版本只是噪音，受影响的版本在发现
// 本身里已经有了，这条链的作用是说明该动哪个依赖，而不是该看哪个固定版本
//
// 该包是直接依赖、未知或工作区根时返回空，这三种情况都没有链需要解释
func (g *CargoLockGraph) Path(name, version string) []string {
	if g == nil {
		return nil
	}
	path, ok := g.pathTo[packageKey(name, version)]
	if !ok || len(path) <= 1 {
		return nil
	}
	names := make([]string, 0, len(path))
	for _, key := range path {
		// 去掉 packageKey 加上去的尾部 " version"
		if idx := strings.LastIndex(key, " "); idx > 0 {
			names = append(names, key[:idx])
			continue
		}
		names = append(names, key)
	}
	return names
}

// LoadCargoLock 解析 crate 清单旁边的锁文件，没有则返回 nil
//
// nil 图是可以用的值，所有查询都报 unknown，退化成把发现当成项目自己的问题。那
// 正是本文件出现之前的行为，所以缺锁文件只会损失分类，绝不会丢掉发现
func LoadCargoLock(crateDir string) *CargoLockGraph {
	g, err := ParseCargoLock(filepath.Join(crateDir, "Cargo.lock"))
	if err != nil {
		return nil
	}
	return g
}
