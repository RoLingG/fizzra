package model

import (
	"database/sql/driver"
	"encoding/json"
	"errors"
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

// StringSlice 是以 JSON 数组持久化的 []string
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
		// 兼容旧的逗号分隔取值，避免查询直接失败
		out = nil
		for _, p := range strings.Split(string(raw), ",") {
			if p = strings.TrimSpace(p); p != "" {
				out = append(out, p)
			}
		}
	}
	*s = out
	return nil
}

func (*StringSlice) GormDataType() string { return "text" }

func (*StringSlice) GormDBDataType(*gorm.DB, *schema.Field) string { return "text" }

// ---------------------------------------------------------------------------
// MultiString
// ---------------------------------------------------------------------------

// MultiString 的行为与 StringSlice 相同，但还能接受 YAML 里的单个标量，手写配置因此更宽容
type MultiString []string

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

	// AvgComplexity 是每个函数的平均圈复杂度
	AvgComplexity float64 `json:"avgComplexity"`
	// MaxComplexity 是项目里最差的函数
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

// FileSize 是逐文件大小明细中的一项
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

// DuplicateBlock 是一组完全相同的代码片段
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
