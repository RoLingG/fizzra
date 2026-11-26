package model

import (
	"database/sql/driver"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"gorm.io/gorm"
	"gorm.io/gorm/schema"
)

// 非标量字段的持久化辅助
// SQLite 没有数组类型，切片以 JSON 存进 TEXT 列。必须同时实现 GormDBDataType 返回 text，
// 否则 GORM 会把切片发成行元组 `("a","b")`，被 SQLite 以 "row value misused" 拒绝

// ---------------------------------------------------------------------------
// StringSlice
// ---------------------------------------------------------------------------

// StringSlice 以 JSON 数组持久化的 []string
type StringSlice []string

func (s StringSlice) Value() (driver.Value, error) {
	if s == nil {
		return "[]", nil
	}
	b, err := json.Marshal([]string(s))
	if err != nil {
		return nil, err
	}
	return string(b), nil
}

// Scan 把列值还原成字符串切片
//
// JSON 解析失败时只回退到逗号分隔，且仅在值看起来确实不是 JSON 时才回退。
// 以前对任何解析失败都按逗号切，于是截断的 `["a","b` 被切成 `["a"` 与 `"b]`、
// 对象被原样塞进切片、二进制垃圾也照样“成功”，坏数据一路流到界面
func (s *StringSlice) Scan(src any) error {
	raw, err := bytesFrom(src)
	if err != nil {
		return err
	}
	if len(raw) == 0 {
		*s = nil
		return nil
	}

	var out []string
	if err := json.Unmarshal(raw, &out); err != nil {
		if !looksLikeLegacyList(raw) {
			return fmt.Errorf("column holds malformed JSON: %w", err)
		}
		out = nil
		for p := range strings.SplitSeq(string(raw), ",") {
			if p = strings.TrimSpace(p); p != "" {
				out = append(out, p)
			}
		}
		*s = out
		return nil
	}
	*s = out
	return nil
}

// looksLikeLegacyList 报告值是否可能是旧的逗号分隔格式
//
// 两种值不能猜：以 `[` 或 `{` 开头的说明本该是 JSON，解析失败即损坏；
// 含控制字符的说明不是人写出来的配置，是二进制垃圾
func looksLikeLegacyList(raw []byte) bool {
	for _, b := range raw {
		if b < 0x20 && b != '\t' && b != '\r' && b != '\n' {
			return false
		}
	}
	for _, b := range raw {
		switch b {
		case ' ', '\t', '\r', '\n':
			continue
		case '[', '{':
			return false
		default:
			return true
		}
	}
	return false
}

func (*StringSlice) GormDataType() string { return "text" }

func (*StringSlice) GormDBDataType(*gorm.DB, *schema.Field) string { return "text" }

// ---------------------------------------------------------------------------
// MultiString
// ---------------------------------------------------------------------------

// MultiString 的行为与 StringSlice 相同，但还能接受 YAML 里的单个标量，手写配置因此更宽容
type MultiString []string

// Value 用值接收器、Scan 用指针接收器是 driver.Valuer 与 sql.Scanner 的要求，
// 与 Go 标准库的 sql.NullString 一致。字段声明为值类型时，指针接收器的 Value
// 不在其方法集里，GORM 认不出 driver.Valuer，同样会掉进上面的行元组错误
func (m MultiString) Value() (driver.Value, error) { return StringSlice(m).Value() }

func (m *MultiString) Scan(src any) error { return (*StringSlice)(m).Scan(src) }

func (*MultiString) GormDataType() string { return "text" }

func (*MultiString) GormDBDataType(*gorm.DB, *schema.Field) string { return "text" }

func (m *MultiString) UnmarshalYAML(unmarshal func(any) error) error {
	var many []string
	if err := unmarshal(&many); err == nil {
		*m = many
		return nil
	}
	var one string
	if err := unmarshal(&one); err != nil {
		return err
	}
	if one == "" {
		*m = nil
		return nil
	}
	*m = []string{one}
	return nil
}

// ---------------------------------------------------------------------------
// Metrics
// ---------------------------------------------------------------------------

// Metrics 以 JSON 大字段持久化
type Metrics struct {
	Files        int `json:"files"`
	Lines        int `json:"lines"`
	CodeLines    int `json:"codeLines"`
	CommentLines int `json:"commentLines"`
	BlankLines   int `json:"blankLines"`
	Functions    int `json:"functions"`

	// AvgComplexity 每个函数的平均圈复杂度
	AvgComplexity float64 `json:"avgComplexity"`
	// MaxComplexity 项目里最差的函数
	MaxComplexity int `json:"maxComplexity"`
	// ComplexFunctions 列出超过配置阈值的函数
	ComplexFunctions []ComplexFunction `json:"complexFunctions"`

	// LongFunctions 列出超过长度阈值的函数
	LongFunctions []ComplexFunction `json:"longFunctions"`

	// Duplication 是重复行占比（0 到 100）
	Duplication float64 `json:"duplication"`
	// DuplicateBlocks 列出找到的最大的克隆组
	DuplicateBlocks []DuplicateBlock `json:"duplicateBlocks"`

	// Coverage 是测试覆盖率百分比，未知时为 0
	Coverage float64 `json:"coverage"`

	// TestFiles 与 TestFunctions 描述测试面
	TestFiles     int `json:"testFiles"`
	TestFunctions int `json:"testFunctions"`

	// LargestFiles 是最大的源文件，最糟的在前
	//
	// 只看总计数字回答不了「哪个文件有问题」，而且一个 4000 行的文件和四十个
	// 100 行的文件是完全不同的审查问题
	LargestFiles []FileSize `json:"largestFiles"`
}

// FileSize 逐文件大小明细中的一项
type FileSize struct {
	File  string `json:"file"`
	Lines int    `json:"lines"`
	Code  int    `json:"code"`
	// IsTest 标记测试文件，界面据此把它们与线上代码分开
	IsTest bool `json:"isTest"`
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

// DuplicateBlock 一组完全相同的代码片段
type DuplicateBlock struct {
	Lines   int      `json:"lines"`
	Tokens  int      `json:"tokens"`
	Files   []string `json:"files"`
	Snippet string   `json:"snippet"`
}

func (m Metrics) Value() (driver.Value, error) {
	b, err := json.Marshal(m)
	if err != nil {
		return nil, err
	}
	return string(b), nil
}

func (m *Metrics) Scan(src any) error {
	raw, err := bytesFrom(src)
	if err != nil {
		return err
	}
	if len(raw) == 0 {
		return nil
	}
	var out Metrics
	if err := json.Unmarshal(raw, &out); err != nil {
		return err
	}
	*m = out
	return nil
}

func (*Metrics) GormDataType() string { return "text" }

// GormDBDataType 供 SQLite 迁移器选择列类型
func (*Metrics) GormDBDataType(*gorm.DB, *schema.Field) string { return "text" }

// ---------------------------------------------------------------------------
// 辅助函数
// ---------------------------------------------------------------------------

// bytesFrom 把数据库扫描出来的值归一成字节切片
func bytesFrom(src any) ([]byte, error) {
	switch v := src.(type) {
	case nil:
		return nil, nil
	case []byte:
		return v, nil
	case string:
		return []byte(v), nil
	default:
		return nil, errors.New("unsupported scan type for JSON column")
	}
}
