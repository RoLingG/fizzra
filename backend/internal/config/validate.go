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
	for t != nil && t.Kind() == reflect.Ptr {
		t = t.Elem()
	}
	return t
}

// 已加载配置的校验
//
// 促使这段代码出现的失败是这样的，一个键拼错的配置文件加载成功，然后静默什么都不
// 做。YAML 没有必填字段，所以 `maxCritcal: 0`（少一个字母）会产生一份完全合法的
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
	for i := 0; i < typ.NumField(); i++ {
		field := typ.Field(i)
		tag := field.Tag.Get("yaml")
		if tag == "" || tag == "-" {
			continue
		}
		name := strings.Split(tag, ",")[0]
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

// analyzerOptionsPath 是唯一一个形状无法用结构体标签表达的配置段
const analyzerOptionsPath = "scan.analyzerOptions"

// knownAnalyzerOptionKeys 把每个分析器名映射到它接受的选项键
//
// 从分析器那边复制过来而不是导入，因为 internal/analyzer 是个很大的包，而配置是
// 所有东西都要加载的。为了回答「这个键合法吗」而把它拖进来，会让配置层依赖整个
// 分析栈
//
// 这份复制由 TestAnalyzerOptionKeysMatchTheAnalyzers 把关，它会遍历真实的注册表
// 并在两者脱节时报错。正因为有这个测试，这张表放在这里才是安全的，没有它，在
// 分析器里新增一个选项会让它在 YAML 中静默不可用，而那恰恰是本段代码要修的 bug
//
// 两个要联网的分析器都列了 offline。它在 scan.offline 有全局等价物，也声明在
// 各自的 Options() 里，所以面板上也会提供它，配置与接口接受同一批键
var knownAnalyzerOptionKeys = map[string][]string{
	"ruff": {"style", "docstrings", "annotations", "copyright", "naming",
		"pandas", "quotes", "todo"},
	"cargo-audit": {"offline", "scoredTransitive"},
	"govulncheck": {"offline"},
	"biome":       {"css"},
	"oxlint":      {"vuePlugin"},
}

// analyzerOptionProblem 校验 analyzerOptions 段中的一项
//
// 名字必须是真实的分析器，键必须是该分析器声明过的。两半都要紧，未知的分析器名
// 几乎总是某个真实名字的拼写错误，而真实分析器下的未知键则是一个会加载但什么都不
// 做的设置
func analyzerOptionProblem(nameNode, valNode *yaml.Node) error {
	name := nameNode.Value
	validKeys, ok := knownAnalyzerOptionKeys[name]
	if !ok {
		names := make([]string, 0, len(knownAnalyzerOptionKeys))
		for k := range knownAnalyzerOptionKeys {
			names = append(names, k)
		}
		slices.Sort(names)
		if best := closestMatch(name, names); best != "" {
			return fmt.Errorf("line %d: unknown analyzer %q in analyzerOptions — did you mean %q?",
				nameNode.Line, name, best)
		}
		return fmt.Errorf("line %d: unknown analyzer %q in analyzerOptions (analyzers with options: %s)",
			nameNode.Line, name, strings.Join(names, ", "))
	}

	// null 或空值表示合法的「什么都不设」，不携带任何键
	if valNode.Kind != yaml.MappingNode {
		return nil
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
				"line %d: unknown option %q for analyzer %q — did you mean %q?",
				keyNode.Line, keyNode.Value, name, best))
			continue
		}
		problems = append(problems, fmt.Errorf(
			"line %d: unknown option %q for analyzer %q (valid options: %s)",
			keyNode.Line, keyNode.Value, name, strings.Join(validKeys, ", ")))
	}

	// 返回第一个，调用方因此每行追加一个问题，其余的会在下一轮报告，而不是
	// 被丢掉
	if len(problems) > 0 {
		return problems[0]
	}
	return nil
}

// Validate 按表结构检查一份原始 YAML 文档，返回发现的所有问题
func Validate(raw []byte) []error {
	var doc yaml.Node
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		return []error{fmt.Errorf("parse configuration: %w", err)}
	}
	if len(doc.Content) == 0 {
		return nil
	}

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

				// analyzerOptions 是唯一的开放式段落
				// 键是分析器名，值是该分析器声明的那些键，两者都无法表达成结构体标签。
				// map 类型给不了表结构遍历器任何可推导的东西，于是它一个键都不接受，
				// 把每条记录都当成未知拒绝掉，连随发布提供的 ruff 与 cargo-audit 选项也不例外。
				// 见 analyzerOptionProblem
				if prefix == analyzerOptionsPath {
					if msg := analyzerOptionProblem(keyNode, valNode); msg != nil {
						problems = append(problems, msg)
					}
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
				walk(valNode, path)
			}
		}
	}
	walk(&doc, "")

	return problems
}

// ruleKeyError 报告规则条目内的未知键
func ruleKeyError(node *yaml.Node, valid map[string]bool) error {
	names := make([]string, 0, len(valid))
	for k := range valid {
		names = append(names, k)
	}
	slices.Sort(names)

	if best := closestMatch(node.Value, names); best != "" {
		return fmt.Errorf("line %d: unknown key %q in a rule entry — did you mean %q?",
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
		return fmt.Errorf("line %d: unknown configuration key %q — did you mean %q?",
			node.Line, path, replaceLastSegment(path, best))
	}
	return fmt.Errorf("line %d: unknown configuration key %q (valid keys in %s: %s)",
		node.Line, path, parentOf(path), strings.Join(candidates, ", "))
}

// siblingsOf 列出与某路径共享同一父路径的已知键
func siblingsOf(path string, known keySet) []string {
	parent := parentOf(path)
	var out []string
	for k := range known {
		if parentOf(k) == parent && !strings.Contains(k, "rules") {
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

// closestMatch 返回与 want 编辑距离很小的候选
//
// 对短的选项名和固定的字典来说 Levenshtein 太重了，所以这里用便宜的相似度检查，
// 至少三个字符的共同前缀，或者很小的编辑距离
func closestMatch(want string, candidates []string) string {
	best := ""
	bestScore := 0
	lower := strings.ToLower(want)

	for _, c := range candidates {
		score := similarity(lower, strings.ToLower(c))
		if score > bestScore {
			bestScore = score
			best = c
		}
	}
	if bestScore < 3 {
		return ""
	}
	return best
}

// similarity 是共同前缀的长度，加上一个给长度接近的候选的加权，足以抓住像
// "maxCritcal" 对 "maxCritical" 这样的拼写错误，又不必引入依赖
func similarity(a, b string) int {
	common := 0
	for common < len(a) && common < len(b) && a[common] == b[common] {
		common++
	}
	if common < 3 {
		return 0
	}
	// 优先取长度与输入最接近的候选
	diff := len(a) - len(b)
	if diff < 0 {
		diff = -diff
	}
	return common - diff
}
