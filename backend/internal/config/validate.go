package config

import (
	"fmt"
	"reflect"
	"slices"
	"strings"

	"gopkg.in/yaml.v3"
)

const structKind = reflect.Struct

// reflectNew 返回一个零值，其类型可被检查
func reflectNew(t reflect.Type) any {
	return reflect.New(t).Elem().Interface()
}

// derefType 剥掉指针
func derefType(v any) reflect.Type {
	t := reflect.TypeOf(v)
	for t != nil && t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	return t
}

// 已加载配置的校验
//
// 促使这段代码出现的失败是这样的，一个键拼错的配置文件加载成功，然后静默什么都不做。
// YAML 没有必填字段，所以 `maxCritcal: 0`（少一个字母）会产生一份完全合法的
// 文档，什么都不设，而用户配置的门禁从未运行
//
// 一个静默忽略你设置的审查工具，比一个拒绝启动的更糟，所以未知键是硬错误，并且
// 消息会给出最接近的合法拼写

// knownKeys 是全部可接受的键路径，从结构体标签构建，因此不会与实际表结构脱节
type keySet map[string]bool

// topLevelKeys 返回所有可接受的带点键路径
func topLevelKeys() keySet {
	out := keySet{}
	addStruct(out, "", &Config{})
	return out
}

// addStruct 遍历一个结构体类型并记录其带 yaml 标签的字段路径
func addStruct(out keySet, prefix string, v any) {
	typ := derefType(v)
	if typ.Kind() != structKind {
		return
	}
	for field := range typ.Fields() {
		tag := field.Tag.Get("yaml")
		if tag == "" || tag == "-" {
			continue
		}
		name, _, _ := strings.Cut(tag, ",")
		path := name
		if prefix != "" {
			path = prefix + "." + name
		}
		out[path] = true

		// 递归进嵌套结构体，但不进结构体切片，rules 列表由它自己的类型逐字段校验
		ft := derefType(reflectNew(field.Type))

		if ft.Kind() == structKind && !strings.Contains(path, "rules") {
			addStruct(out, path, reflectNew(field.Type))
		}
	}
}

// analyzerOptionsPath 唯一一个形状无法用结构体标签表达的配置段
const analyzerOptionsPath = "scan.analyzerOptions"

// knownAnalyzerOptionKeys 把每个分析器名映射到它接受的选项键
//
// 从分析器那边复制过来而不是导入：internal/analyzer 是很大的包，而配置层是所有
// 东西都要加载的，为了回答「这个键合法吗」把它拖进来，会让配置依赖整个分析栈。
// 代价是分析器新增选项时要同步这张表，否则该选项在 YAML 里静默不可用
//
// 两个要联网的分析器都列了 offline，与 scan.offline 的全局设置等价，配置与接口
// 因此接受同一批键
var knownAnalyzerOptionKeys = map[string][]string{
	"ruff": {"style", "docstrings", "annotations", "copyright", "naming",
		"pandas", "quotes", "todo"},
	"cargo-audit": {"offline", "scoredTransitive"},
	"govulncheck": {"offline"},
	"biome":       {"css"},
	"oxlint":      {"vuePlugin"},
}

// analyzerOptionProblems 校验 analyzerOptions 段中的一项，返回全部问题
//
// 名字必须是真实的分析器，键必须是该分析器声明过的。未知的分析器名几乎总是某个真实名字的拼写错误，
// 而真实分析器下的未知键则是一个会加载但什么都不做的设置
//
// 返回切片而不是单个 error，同一分析器下写了多个拼错的键时一次报全，用户不用改一个撞一个
func analyzerOptionProblems(nameNode, valNode *yaml.Node) []error {
	name := nameNode.Value
	validKeys, ok := knownAnalyzerOptionKeys[name]
	if !ok {
		names := make([]string, 0, len(knownAnalyzerOptionKeys))
		for k := range knownAnalyzerOptionKeys {
			names = append(names, k)
		}
		slices.Sort(names)
		if best := closestMatch(name, names); best != "" {
			return []error{fmt.Errorf("line %d: unknown analyzer %q in analyzerOptions — did you mean %q? ",
				nameNode.Line, name, best)}
		}
		return []error{fmt.Errorf("line %d: unknown analyzer %q in analyzerOptions (analyzers with options: %s)",
			nameNode.Line, name, strings.Join(names, ", "))}
	}

	// YAML null 表示合法的「什么都不设」。标量或序列（`ruff: true`、`ruff: [style]`）
	// 是写错了形状而非空配置，要报出来
	if valNode.Kind != yaml.MappingNode {
		if valNode.Kind == yaml.ScalarNode && valNode.Tag == "!!null" {
			return nil
		}
		return []error{fmt.Errorf(
			"line %d: analyzerOptions.%s must be a mapping of option names to values, got %s",
			nameNode.Line, name, yamlShape(valNode))}
	}

	known := map[string]bool{}
	for _, k := range validKeys {
		known[k] = true
	}

	var problems []error
	for i := 0; i+1 < len(valNode.Content); i += 2 {
		keyNode := valNode.Content[i]
		if known[keyNode.Value] {
			continue
		}
		if best := closestMatch(keyNode.Value, validKeys); best != "" {
			problems = append(problems, fmt.Errorf(
				"line %d: unknown option %q for analyzer %q — did you mean %q? ",
				keyNode.Line, keyNode.Value, name, best))
			continue
		}
		problems = append(problems, fmt.Errorf(
			"line %d: unknown option %q for analyzer %q (valid options: %s)",
			keyNode.Line, keyNode.Value, name, strings.Join(validKeys, ", ")))
	}

	return problems
}

// yamlShape 给节点一个简短的类型描述，用于「期望 mapping 却拿到别的」这类错误
func yamlShape(node *yaml.Node) string {
	switch node.Kind {
	case yaml.SequenceNode:
		return "a sequence"
	case yaml.ScalarNode:
		return fmt.Sprintf("%q", node.Value)
	case yaml.MappingNode:
		return "a mapping"
	case yaml.AliasNode:
		return "an alias"
	default:
		return "an unexpected node"
	}
}

// Validate 按表结构检查一份原始 YAML 文档，返回发现的所有问题
//
// 与 Load 走同一条流水线（归一别名再校验），否则同一份文件在两个入口会得到
// 相反的结论，Load 接受 quality.coverage，Validate 却把它当未知键拒掉
func Validate(raw []byte) []error {
	doc, err := parseYAMLNode(raw)
	if err != nil {
		return []error{err}
	}
	if doc == nil {
		return nil
	}
	// 归一阶段只改键名与容器形状，键值类型对不对要由校验阶段兜住
	return append(normaliseAliasesNode(doc), validateNode(doc)...)
}

// parseYAMLNode 只解析一次。返回 nil 表示空文档
//
// 后续的归一与校验都在这个节点树上原地进行，行号因此始终指向用户写的那份原文
func parseYAMLNode(raw []byte) (*yaml.Node, error) {
	var doc yaml.Node
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		return nil, fmt.Errorf("parse configuration: %w", err)
	}
	if len(doc.Content) == 0 {
		return nil, nil
	}
	return &doc, nil
}

// validateNode 检查已归一化的节点树，返回全部问题
func validateNode(doc *yaml.Node) []error {
	known := topLevelKeys()

	// 规则条目的表结构，从结构体推导，因此不会脱节
	ruleKeys := keySet{}
	addStruct(ruleKeys, "", &RuleConfig{})
	// 去掉 addStruct 没有被传入的结构体名前缀，这里的键是裸名
	ruleFieldNames := map[string]bool{}
	for k := range ruleKeys {
		if !strings.Contains(k, ".") {
			ruleFieldNames[k] = true
		}
	}
	// 记下别名，因为归一化已经把它们改写过了
	ruleFieldNames["severity"] = true
	ruleFieldNames["patterns"] = true

	var problems []error

	const ruleSection = "rules[]"
	visited := map[*yaml.Node]bool{}

	var walk func(node *yaml.Node, prefix string)
	walk = func(node *yaml.Node, prefix string) {
		switch node.Kind {
		case yaml.DocumentNode:
			for _, child := range node.Content {
				walk(child, prefix)
			}

		case yaml.SequenceNode:
			childPrefix := prefix
			if prefix == "rules" {
				childPrefix = ruleSection
			}
			for _, child := range node.Content {
				walk(child, childPrefix)
			}

		case yaml.MappingNode:
			for i := 0; i+1 < len(node.Content); i += 2 {
				keyNode, valNode := node.Content[i], node.Content[i+1]

				if prefix == ruleSection {
					if !ruleFieldNames[keyNode.Value] {
						problems = append(problems, ruleKeyError(keyNode, ruleFieldNames))
					}
					continue
				}

				// analyzerOptions 唯一的开放式段落
				// 键是分析器名，值是该分析器声明的那些键，两者都无法表达成结构体标签。
				// map 类型给不了表结构遍历器任何可推导的东西，于是它一个键都不接受，
				// 把每条记录都当成未知拒绝掉，连随发布提供的 ruff 与 cargo-audit 选项也不例外
				if prefix == analyzerOptionsPath {
					problems = append(problems, analyzerOptionProblems(keyNode, valNode)...)
					continue
				}

				path := keyNode.Value
				if prefix != "" {
					path = prefix + "." + keyNode.Value
				}
				if !known[path] {
					problems = append(problems, unknownKeyError(keyNode, path, known))
					continue
				}
				// 有子键说明这里该是一个配置段。null 是合法的空段，其余标量说明整段被压成了一行
				if valNode.Kind != yaml.MappingNode && valNode.Tag != "!!null" && hasChildKeys(path, known) {
					problems = append(problems, fmt.Errorf(
						"line %d: %s expects a mapping of settings, got %q",
						valNode.Line, path, valNode.Value))
					continue
				}
				walk(valNode, path)
			}

		case yaml.AliasNode:
			// 展开别名让 `server: *x` 里的问题也能被发现。同一节点只走一次，
			// 否则被多处引用时同样的错误会重复报
			if node.Alias != nil && !visited[node.Alias] {
				visited[node.Alias] = true
				walk(node.Alias, prefix)
			}
		}
	}
	walk(doc, "")

	return problems
}

// hasChildKeys 报告某个路径是否是配置段（它下面还有已知子键）
func hasChildKeys(path string, known keySet) bool {
	prefix := path + "."
	for k := range known {
		if strings.HasPrefix(k, prefix) {
			return true
		}
	}
	return false
}

// ruleKeyError 报告规则条目内的未知键
func ruleKeyError(node *yaml.Node, valid map[string]bool) error {
	names := make([]string, 0, len(valid))
	for k := range valid {
		names = append(names, k)
	}
	slices.Sort(names)

	if best := closestMatch(node.Value, names); best != "" {
		return fmt.Errorf("line %d: unknown key %q in a rule entry — did you mean %q? ",
			node.Line, node.Value, best)
	}
	return fmt.Errorf("line %d: unknown key %q in a rule entry (valid keys: %s)",
		node.Line, node.Value, strings.Join(names, ", "))
}

// unknownKeyError 构造一个给出最接近合法键的错误
func unknownKeyError(node *yaml.Node, path string, known keySet) error {
	// 给出最近的同级键，让拼写错误一眼可见
	// 再给出该段的全部键，这样即使名字完全写错也仍能指向有用的地方
	candidates := siblingsOf(path, known)
	if best := closestMatch(lastSegment(path), candidates); best != "" {
		return fmt.Errorf("line %d: unknown configuration key %q — did you mean %q? ",
			node.Line, path, replaceLastSegment(path, best))
	}
	// 顶层没有父名，写成 "(valid keys in : ...)" 读起来像出了 bug
	if parent := parentOf(path); parent == "" {
		return fmt.Errorf("line %d: unknown configuration key %q (valid top-level keys: %s)",
			node.Line, path, strings.Join(candidates, ", "))
	}
	return fmt.Errorf("line %d: unknown configuration key %q (valid keys in %s: %s)",
		node.Line, path, parentOf(path), strings.Join(candidates, ", "))
}

// siblingsOf 列出与某路径共享同一父路径的已知键
//
// rules 也在其中。addStruct 不展开结构体切片，known 里只有顶层的 "rules"，
// 而它是合法键，拼错时正是最该被提示的那个
func siblingsOf(path string, known keySet) []string {
	parent := parentOf(path)
	var out []string
	for k := range known {
		if parentOf(k) == parent {
			out = append(out, lastSegment(k))
		}
	}
	slices.Sort(out)
	return out
}

func parentOf(path string) string {
	if i := strings.LastIndex(path, "."); i >= 0 {
		return path[:i]
	}
	return ""
}

func lastSegment(path string) string {
	if i := strings.LastIndex(path, "."); i >= 0 {
		return path[i+1:]
	}
	return path
}

func replaceLastSegment(path, segment string) string {
	if i := strings.LastIndex(path, "."); i >= 0 {
		return path[:i+1] + segment
	}
	return segment
}

// closestMatch 返回与 want 编辑距离足够小的候选，没有足够接近的返回空串
//
// 距离含相邻换位（算一次编辑），因此 hsot→host、prot→port 这类单字符手滑与
// 换位都能命中
func closestMatch(want string, candidates []string) string {
	lower := strings.ToLower(want)
	if lower == "" {
		return ""
	}

	// 阈值随词长收紧。短词差一个字符就面目全非，长词容忍两个，
	// 同时要求差异不超过词长的一半，避免对无关词乱猜
	limit := func(word string) int {
		maxLen := max(len(lower), len(word))
		if maxLen <= 4 {
			return 1
		}
		if dist := min(2, maxLen/2); dist > 0 {
			return dist
		}
		return 1
	}

	best := ""
	bestDist := 0
	for _, c := range candidates {
		dist := damerauLevenshtein(lower, strings.ToLower(c))
		if dist > limit(c) {
			continue
		}
		// 严格小于，同分时保留先出现的，调用方传进来的候选已排好序，
		// 结果因此是稳定的
		if best == "" || dist < bestDist {
			best = c
			bestDist = dist
		}
	}
	return best
}

// damerauLevenshtein 计算两个字符串的最优字符串对齐距离
//
// 相邻字符换位算一次编辑（hsot ↔ host），拼写错误里换位很常见，算作两次编辑
// 会让这类手滑落出阈值
func damerauLevenshtein(a, b string) int {
	ar, br := []rune(a), []rune(b)
	rows, cols := len(ar)+1, len(br)+1

	d := make([][]int, rows)
	for i := range d {
		d[i] = make([]int, cols)
		d[i][0] = i
	}
	for j := range cols {
		d[0][j] = j
	}

	for i := 1; i < rows; i++ {
		for j := 1; j < cols; j++ {
			cost := 1
			if ar[i-1] == br[j-1] {
				cost = 0
			}
			d[i][j] = min(
				d[i-1][j]+1,      // 删除
				d[i][j-1]+1,      // 插入
				d[i-1][j-1]+cost, // 替换
			)
			// 相邻换位
			if i > 1 && j > 1 && ar[i-1] == br[j-2] && ar[i-2] == br[j-1] {
				d[i][j] = min(d[i][j], d[i-2][j-2]+1)
			}
		}
	}
	return d[rows-1][cols-1]
}
