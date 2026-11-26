package model

import (
	"database/sql/driver"
	"encoding/json"

	"gorm.io/gorm"
	"gorm.io/gorm/schema"
)

// FixExample 展示如何解决某个发现的具体修改前后对照
//
// 只报问题不给改法，审查工具只做了一半，用户被指出哪里不对，还得自己想「正确处理」
// 在他的代码里是什么意思。一个完整示例省掉这一步
//
// 示例刻意做得很小且自包含， 只有几行，目的是传达改法的形态而不是拿去编译。规则只有
// 唯一正确答案时（改用 crypto/rand、检查错误）这一对是精确的，改法取决于上下文时
// Before 展示问题模式，After 展示惯用写法
type FixExample struct {
	// Before 触发该发现的代码
	Before string `json:"before"`
	// After 改正后的同一段代码
	After string `json:"after"`
	// Note 在光看差异不明显时解释这处改动
	Note string `json:"note,omitempty"`
	// Language 是语法高亮的提示，取值由分析器填写（go、python、rust 等）
	Language string `json:"language,omitempty"`
}

// Value 实现 driver.Valuer，示例以 JSON 存进 TEXT 列，与本表结构中其他结构化列一致
//
// 必须是值接收器：Issue.Fix 是非指针字段，取值时是值类型，
// 指针接收器的方法不在值类型的方法集里，database/sql 会认不出 driver.Valuer
func (f FixExample) Value() (driver.Value, error) {
	if f.Before == "" && f.After == "" {
		return "{}", nil
	}
	b, err := json.Marshal(f)
	if err != nil {
		return nil, err
	}
	return string(b), nil
}

// Scan 实现 sql.Scanner
func (f *FixExample) Scan(src any) error {
	raw, err := bytesFrom(src)
	if err != nil {
		return err
	}
	if len(raw) == 0 || string(raw) == "{}" {
		return nil
	}
	var out FixExample
	if err := json.Unmarshal(raw, &out); err != nil {
		return err
	}
	*f = out
	return nil
}

// GormDataType 固定列类型
func (*FixExample) GormDataType() string { return "text" }

func (*FixExample) GormDBDataType(*gorm.DB, *schema.Field) string { return "text" }
