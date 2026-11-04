package model

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
)

// NewID 返回带所给前缀的短标识，大致可排序，例如 "prj_9f2c1a4b3d5e6f70"
//
// 刻意不引入 UUID 依赖，16 字节随机十六进制后缀对单节点工具来说碰撞概率足够低，
// 在 URL 里也更好读
func NewID(prefix string) string {
	buf := make([]byte, 8)
	if _, err := rand.Read(buf); err != nil {
		// crypto/rand 失败已经很接近致命了，退回一个固定标记，让调用方拿到一个
		// 大致唯一的值而不是 panic
		return fmt.Sprintf("%s_%s", prefix, "0000000000000000")
	}
	return prefix + "_" + hex.EncodeToString(buf)
}

// Fingerprint 为问题推导出稳定标识，多次分析之间因此可以对比新增、持续和已修复
// 刻意排除行号，在文件顶部插入一个空行不该让所有问题都看起来是全新的
func Fingerprint(analyzer, rule, file, message string) string {
	h := sha256.New()
	h.Write([]byte(strings.Join([]string{
		strings.ToLower(analyzer),
		strings.ToLower(rule),
		strings.ReplaceAll(file, "\\", "/"),
		strings.ToLower(strings.TrimSpace(message)),
	}, "\x00")))
	return hex.EncodeToString(h.Sum(nil))[:16]
}
