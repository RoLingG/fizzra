package report

import (
	"fmt"
	"strings"
)

/*
 * 报告的导出格式。
 *
 * 放在这里而不是各调用方自己解析字符串，是因为同一个取值要同时决定三件事：用哪个
 * 渲染器、文件名后缀、以及响应的 Content-Type。三者分散在各处就会漂移——命令行
 * 认得 "sarif" 而面板认不得，或者某一边忘了改后缀。
 */

// Format 是报告的输出格式
type Format string

const (
	FormatHTML  Format = "html"
	FormatJSON  Format = "json"
	FormatSARIF Format = "sarif"
)

// AllFormats 是全部可选格式，按给人看的顺序排列，供界面直接使用
var AllFormats = []Format{FormatHTML, FormatJSON, FormatSARIF}

// ParseFormat 解析调用方给的格式名
//
// 空串当作 HTML，命令行和面板都以 HTML 为默认，这也是「什么都没指定」时最可能的
// 意图。未知取值报错而不回退到默认，静默给出一份格式不对的文件比一句拒绝难查得多
func ParseFormat(raw string) (Format, error) {
	switch Format(strings.ToLower(strings.TrimSpace(raw))) {
	case "":
		return FormatHTML, nil
	case FormatHTML:
		return FormatHTML, nil
	case FormatJSON:
		return FormatJSON, nil
	case FormatSARIF:
		return FormatSARIF, nil
	}
	return "", fmt.Errorf("unknown report format %q (valid: html, json, sarif)", raw)
}

// Ext 是文件名后缀，不带点
func (f Format) Ext() string {
	switch f {
	case FormatJSON:
		return "json"
	case FormatSARIF:
		return "sarif"
	default:
		return "html"
	}
}

// ContentType 是浏览器和工具据此识别响应体的类型
//
// SARIF 用 application/json，它本身就是 JSON，规范也把它注册成这个类型，而
// application/sarif+json 尚未被广泛识别，用标准类型能少一类「下载下来打不开」
func (f Format) ContentType() string {
	switch f {
	case FormatJSON, FormatSARIF:
		return "application/json; charset=utf-8"
	default:
		return "text/html; charset=utf-8"
	}
}
