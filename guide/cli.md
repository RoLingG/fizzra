# 命令行参数参考

`scan`、`ci`、`report` 共享大部分参数，`ci` 额外有几个门禁相关的开关。所有命令的 `--help` 都会打印同一份说明。

## 目标

```bash
fizzra scan                      # 当前目录
fizzra scan ./my-app             # 指定路径，自动注册成项目
fizzra scan --project my-app     # 已注册的项目，按名字或 ID
```

路径和 `--project` 二选一。给路径时，如果该目录尚未注册，会即时注册；重复扫描同一路径复用原记录，不会产生重复项目。

同名目录会追加哈希后缀区分（如 `app-afdeef`），所以对同一批扫描反复取报告时，用扫描时输出的项目名而不是目录名。

## `--format`

| 值 | 说明 |
|---|---|
| `text` | 终端摘要，`scan` 的默认值 |
| `json` | 完整问题列表，机器消费 |
| `html` | 单文件自包含报告，无网络依赖 |
| `sarif` | SARIF 2.1.0，代码扫描内联标注 |

`ci` 和 `scan` 默认 `text`，`report` 默认 `html`。

```bash
fizzra scan . --format json --output scan.json
fizzra ci . --format sarif --output fizzra.sarif
```

## `--output`

不写则输出到 stdout，写了则写文件（父目录会自动创建）。

```bash
fizzra report --project my-app --format html --output reports/index.html
```

配合 `--format text` 无意义，`text` 只有终端摘要。

## `--analyzers`

逗号分隔的分析器名，只跑指定的那些。省略则跑当前语言适用的全部。

```bash
fizzra scan . --analyzers custom,metrics          # 只跑内置规则与度量，不需要外部工具
fizzra scan . --analyzers golangci,staticcheck    # 只跑两个 Go linter
fizzra ci . --analyzers custom,metrics --fail-on critical   # pre-commit 常用组合
```

拼错名字会报错并列出可用项，不会静默忽略。

## `--fail-on`（仅 `ci`）

逗号分隔的严重度，命中即失败。默认 `critical,error`。

```bash
fizzra ci . --fail-on critical                    # 最宽松，只要没有严重缺陷就通过
fizzra ci . --fail-on critical,error              # 默认
fizzra ci . --fail-on critical,error,warning      # 最严格，风格问题也算失败
```

`info` 不能用于 `--fail-on`——它按定义是提示性的。

## `--min-score`（仅 `ci`）

覆盖配置文件里的最低分。低于它就让门禁失败。

```bash
fizzra ci . --min-score 80
fizzra ci . --fail-on critical --min-score 90     # 两个条件同时生效，任一不满足即失败
```

`--fail-on` 和 `--min-score` 是**两条独立的失败路径**，同时给出时都生效。

## `--timeout`

整次扫描的总预算，默认 15 分钟。超时会把整个扫描标记为不完整。

```bash
fizzra ci . --timeout 5m
```

单个外部工具的预算由配置里的 `scan.perToolTimeout` 控制，两者取较小值。

## `--offline`

禁止分析器联网。`govulncheck` 会被跳过（没有本地库模式），`cargo-audit` 改用本地快照。

```bash
fizzra scan . --offline
```

在没有外网出口的机器上建议默认开启，否则两个漏洞扫描器会卡到 socket 超时。

## `--quiet`

只打印摘要行，不输出逐文件的问题列表。

```bash
fizzra scan . --quiet
```

## `--lang`（`ci` / `report`）

HTML 报告的界面语言，`en` 或 `zh`。JSON、SARIF、text 不受影响。

```bash
fizzra report --project my-app --lang zh --output 报告.html
```

## `--no-gate`（仅 `ci`）

无论结果如何都退出 0。适合第一次接入时观察现状。

```bash
fizzra ci . --no-gate
```

## `--allow-skipped`（仅 `ci`）

只放宽「分析器因工具缺失被跳过」这一种不完整。以下两种情况仍然失败：

- 分析器崩溃或超时——那是运行坏了，不是环境没配好
- 整次扫描没有任何分析器跑起来——什么都没检查

```bash
fizzra ci . --allow-skipped
```

## 退出码

| 码 | 含义 |
|---|---|
| 0 | 成功，`ci` 表示门禁通过 |
| 1 | `ci` 门禁失败，或扫描本身无法完成 |
| 2 | 用法错误：未知命令、未知参数、未知格式 |

`scan` 不因发现问题而返回 1，所以可以放在"只报告不阻断"的流水线步骤里。

## 组合示例

```bash
# CI：出 SARIF 给代码扫描，严格门禁
fizzra ci . --format sarif --output fizzra.sarif --fail-on critical,error --min-score 80

# 本地快速检查，只跑不需要外部工具的分析器
fizzra scan . --analyzers custom,metrics --quiet

# 离线机器上扫描并生成中文 HTML
fizzra scan . --offline --format html --output report.html
fizzra report --project my-app --lang zh --output 报告.html

# monorepo：分别扫描子目录，各自出报告
fizzra ci services/api --format json --output api.json
fizzra ci services/web --format json --output web.json
```
