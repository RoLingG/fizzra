# Fizzra

[![Go Version](https://img.shields.io/badge/Go-1.27.3-00ADD8.svg)](https://go.dev) [![License](https://img.shields.io/badge/License-GPL--3.0-blue.svg)](LICENSE) ![Release](https://img.shields.io/github/v/release/RoLingG/fizzra?style=flat&color=orange)

面向多语言项目的轻量级代码质量审查工具。调用各语言已有的 linter，解析输出，汇总成一个可据以行动的分数，通过 Web 看板、命令行和 CI 三种方式呈现。覆盖 Go / Wails、Python、JavaScript / TypeScript、Rust、Java、C#、C / C++、PHP 八种生态。给个人和小团队用，**没有集群、没有多租户、没有权限系统——一个二进制、一个 SQLite 文件、不需要任何外部服务**。

## 特性

- **多语言** —— 八种生态共用一套评分与报告，按项目语言自动选择分析器
- **统一评分** —— 安全、缺陷风险、可维护性、复杂度、覆盖率五个维度加权成扫描分数
- **质量门禁** —— 分数、严重度计数、复杂度任一不达标即失败，CI 里直接阻断合并
- **三种出口** —— 看板、CLI、SARIF（GitHub / GitLab 代码扫描内联标注）
- **本地优先** —— 数据全在本地 SQLite，除漏洞库查询外不联网
- **不假通过** —— 什么都没检查的扫描会明确报不完整并让门禁失败，而不是给个高分
- **Wails 专项** —— 生命周期钩子滥用、事件风暴、绑定面设计，通用 linter 查不到的部分

## 快速开始

克隆下来后**不能只跑 `go build`**：仪表盘的构建产物不入库，必须先构建前端。

```bash
# 需要 Go 1.27.3+；构建看板另需 Node 22+
go generate ./backend/internal/web/...              # 1. 构建前端，产物写进 backend/internal/web/dist/
go build -o fizzra.exe ./backend/cmd/fizzra         # 2. 编译，前端产物被 go:embed 打进去
./fizzra.exe serve                                  # 3. 启动，打开 http://127.0.0.1:8080
```

顺序不能反，`go:embed` 在编译时快照文件。跳过第一步直接编译，得到的是没有 Web UI 的纯命令行二进制。

编译后纯命令行的二进制文件也可以直接扫描任意支持语言的项目：

```bash
./fizzra.exe scan /path/to/your/project             # 打印分数与问题摘要
./fizzra.exe analyzers                              # 查看哪些检查工具已就绪
```

---

## 安全须知

Fizzra 调用各语言已有的 linter，而**部分 linter 会构建、甚至执行被扫描项目的代码**。对来源不可信的代码库，这一点必须先想清楚。

| 分析器 | 是否执行项目代码 |
|---|---|
| `clippy` | **会**。`cargo clippy` 完整构建 crate，`build.rs` 与 proc-macro 在此阶段运行 |
| `golangci-lint` | **可能**。部分 linter 会类型检查并编译包 |
| `go-vet` | **可能**。分析依赖时可能触发构建 |
| `cargo-audit` | 不会，只读 `Cargo.lock` |
| `rustfmt` | 不会，只读源码 |
| `ruff`、`oxlint`、`biome` | 不会，原生二进制只读文件 |
| `tsc`、`vue-tsc` | 不会，只做类型分析 |
| `pmd` | 不会，读源码 |
| `spotbugs` | 不会，读已编译的 class |
| `phpstan` | 不会，静态分析 |

**扫描你信任的代码没有问题**，这些工具本来就是给开发者日常使用的。需要留意的是**扫描陌生来源的仓库**时：

- 在容器或隔离环境里跑，不要在有凭据的机器上直接扫
- 先用 `--analyzers` 只启用不执行代码的分析器：

```bash
fizzra scan ./untrusted --analyzers custom,metrics,ruff,oxlint
```

`--offline` 只禁止联网，**不阻止代码执行**，两者不能互相替代。

---

## 环境要求与安装

| 组件 | 版本 | 何时需要 |
|---|---|---|
| **Go** | 1.27.3 或更高 | 总是，需与 `go.mod` 的声明一致 |
| **Node.js** | 22 或更高 | 仅在构建 Web 看板时 |

**Node 只在构建看板时需要。** 只用 CLI 和 API 可以不装，二进制编译只依赖于 Go 环境本身，`serve` 会显示一个提示如何构建看板的页面。

### 构建方式

| 方式 | 产物 | 命令 |
|---|---|---|
| 完整版 | 带 Web UI 的单一二进制 | `go generate` 后 `go build` |
| 仅 CLI | 无看板，扫描与门禁照常 | 直接 `go build` |

因为 `go:embed` 快照的需求，`go generate` 必须优先执行，生成前端产物以满足后端编译要求。指令相关代码写在 [`backend/internal/web/embed.go`](backend/internal/web/embed.go) 里，也可以手动分步执行：

```bash
cd frontend && npm install && npm run build   # 产物输出到 backend/internal/web/dist/
cd .. && go build -o fizzra.exe ./backend/cmd/fizzra
```

### 用 `go install` 安装

```bash
go install github.com/RoLingG/fizzra/backend/cmd/fizzra@latest
```

**这样装出来的是无看板的 CLI-only 版本。** `go install` 在模块缓存里编译，没有 `dist` 目录，`go:embed` 因此嵌不进去。没有 Node 的环境也能装、也能在 CI 里跑门禁。需要完整项目 Web UI 看板时请用发布产物，或按上面的方式本地构建。

### 启动服务

```bash
fizzra serve                    # 默认 127.0.0.1:8080
fizzra serve --port 9090        # 指定端口
fizzra --config my.yaml serve   # 指定配置文件
```

### 数据位置

全部在 `~/.fizzra/`（Windows 下是 `%USERPROFILE%\.fizzra\`）：

```
~/.fizzra/
└── fizzra.db        # SQLite：项目、分析记录、问题、规则
```

删掉这个目录即完全重置。

---


## 四种使用方式

### 1. Web UI看板（主要方式）

```bash
fizzra serve
```

| 页面 | 内容 |
|---|---|
| **Dashboard** | 总评分、问题分布、安全风险、覆盖率、评分趋势 |
| **Projects** | 项目列表，注册/编辑/删除，手动触发扫描 |
| **Project Detail** | 问题（筛选/排序/分页）、概览、度量、历史记录、设置（质量门禁） |
| **Rules** | 全部规则，可开关、改等级、写自定义规则 |
| **System** | 分析器可用性、缺失工具的安装命令、CI 配置示例 |

点击任意问题行打开详情抽屉，可以看到**修复建议的 before/after 代码对比**。修复示例只在工具自身提供机器可读的替换文本时才有（目前是 ruff 和 clippy）。

### 2. 命令行扫描

```bash
fizzra scan                              # 扫描当前目录
fizzra scan ./my-project                 # 扫描指定目录
fizzra scan --project my-app             # 扫描已注册的项目
fizzra scan ./app --analyzers custom,metrics,wails   # 只跑指定分析器
```

示例输出（以某个真实项目为例，数字随项目而异）：

```bash
  example-project
  ────────────

  Score          87.4/100  (B+)
  Gate           passed
  Issues         50 total
                 0 critical · 1 error · 10 warning · 39 info
  Code           44 files · 16647 lines (12786 code)
  Complexity     avg 4.2 · worst 43
  Duplication    1.5%
  Duration       191ms
  Note           skipped analyzers (tool not installed): gosec, govulncheck, staticcheck
```

注意 `--config` / 配置查找：Fizzra 按 `--config` → `./fizzra.yaml` → `~/.fizzra/config.yaml` 的顺序查找，其中 `./` 指**当前终端所在工作目录**。所以在子目录里运行会静默退回内置默认值——内置默认值不含你在 `fizzra.yaml` 里写的项目专属豁免，分数和门禁结果都会不同。想复现仓库自己的基线，就在仓库根目录运行。

**扫描一个目录时会自动将其注册成测试项目**，重复扫描同一路径会复用记录。

### 3. CI 门禁

```bash
fizzra ci . --fail-on critical,error
```

退出码：**0** 通过 · **1** 门禁失败或扫描失败 · **2** 用法错误

### 4. 生成报告

```bash
fizzra report --project my-app --format html  --output report.html
fizzra report --project my-app --format json  --output report.json
fizzra report --project my-app --format sarif --output fizzra.sarif
```

| 格式 | 用途 |
|---|---|
| `text` | 终端输出（`scan` 默认） |
| `json` | 机器消费，完整问题列表 |
| `html` | **单文件自包含**，无 CDN、无网络依赖，可直接打印或作为 CI 制品 |
| `sarif` | SARIF 2.1.0，GitHub / GitLab 代码扫描内联标注 |

### 全部命令

```bash
fizzra help
```

```
serve                 启动 Web 看板和 API
scan [路径|项目]       扫描并打印摘要
ci [路径|项目]         扫描，门禁失败时返回非 0
report [路径|项目]     生成 JSON / HTML / SARIF 报告
rules                 规则列表 / 导出 / 校验
projects              项目列表
analyzers             分析器可用性
version               版本
```

通用参数：`--project` `--format` `--output` `--analyzers` `--min-score` `--fail-on` `--timeout` `--offline` `--quiet`

每个参数的作用、适用命令和组合示例见 **[guide/cli.md](guide/cli.md)**。

---

## 支持的语言

Fizzra 是**编排器**：它不自己实现静态分析，而是调用各语言已有的成熟工具、解析其 JSON 输出、汇总成统一评分。

| 语言 | 工具 | 说明 |
|---|---|---|
| **Go / Wails** | golangci-lint、staticcheck、go vet、gofmt、gosec、govulncheck，外加 24 条内置规则 | 完整支持，含 Wails 专项检查 |
| **Python** | **ruff**（开启 `--select ALL`，含 bandit 安全规则） | 一个工具覆盖 flake8、isort、pylint 子集、复杂度与安全 |
| **JavaScript / TypeScript** | **oxlint**、**tsc**、**biome** | oxlint 覆盖常规 lint（含 31 条 Vue 规则）；tsc 补上类型错误；biome 补上 oxlint 漏掉的正确性规则，并兼查 CSS |
| **Rust** | **clippy**、**rustfmt**、**cargo-audit** | clippy 与 rustfmt 随官方工具链，cargo-audit 查依赖漏洞 |
| **Java** | **PMD**、**SpotBugs** | PMD 覆盖面广（含 CWE 标注），SpotBugs 分析字节码查数据流缺陷 |
| **C#** | **Roslynator** | 编译器诊断、.NET 内置分析器与代码质量规则，不改动工程文件 |
| **C / C++** | **cppcheck**、**clang-tidy** | cppcheck 无需构建，clang-tidy 补上命令注入等安全规则 |
| **PHP** | **PHPStan** | 未定义方法与属性、传参类型不符、不可达分支 |

**按项目选择语言**：新建/编辑项目时选择语言，决定运行哪些分析器。也可以由 CLI 按清单文件自动识别（`go.mod` / `Cargo.toml` / `pyproject.toml` / `package.json`）。

支持 **Tauri 布局**（`Cargo.toml` 在 `src-tauri/`）—— 自动检测会向下查一层，cargo 也在正确的目录里执行。

> **语言不支持时不会假通过。** 如果某语言还没有分析器，或所需工具没装，扫描会明确报告 **INCOMPLETE** 并让门禁失败。

---

## 安装外部检查工具

Fizzra 编排各语言已有的 linter，工具装在 PATH 上即可被发现。缺失的工具会被跳过而不是报错，装上它们能显著提升检测能力。

```bash
fizzra analyzers        # 查看哪些工具已就绪、哪些缺失
```

各语言对应的工具、安装命令、检查项开关与检查边界，见 **[guide/analyzers.md](guide/analyzers.md)**。

---

## 测试

`go test ./backend/...` 跑后端测试，`-race` 用于竞态检测。完整的验收流程、自扫描验证与「假通过」回归检查见 **[guide/development.md](guide/development.md)**。

---

## 配置

Fizzra 会按顺序查找：`./fizzra.yaml` → `~/.fizzra/config.yaml`。

完整带注释的示例见 [`configs/fizzra.example.yaml`](configs/fizzra.example.yaml)。仓库根目录的 `fizzra.yaml` 是本地配置，不入库。

```yaml
server:
  host: 127.0.0.1     # 无鉴权，默认只监听本机
  port: 8080
  mode: release       # debug | release | test

database:
  dsn: ~/.fizzra/fizzra.db

scan:
  concurrency: 2      # 并发分析数，不超过 CPU 核心数
  timeout: 10m        # 单次分析总预算
  perToolTimeout: 3m  # 单个外部工具的预算
  offline: false      # true = 禁止分析器联网，见 guide/analyzers.md
  # 单个分析器的设置，可选
  # analyzerOptions:
  #   cargo-audit:
  #     offline: "true"
  #   ruff:
  #     style: "true"        # 可选项见 guide/analyzers.md#分析器检查项开关
  #     docstrings: "true"

quality:
  minScore: 70
  maxCritical: 0
  maxHigh: 0
  maxComplexity: 20
  minCoverage: 0      # 0 = 关闭覆盖度检查
```

环境变量可覆盖：`FIZZRA_HOST` `FIZZRA_PORT` `FIZZRA_MODE` `FIZZRA_DB_DSN` `FIZZRA_WORKSPACE` `FIZZRA_CONCURRENCY`

---

## 规则与修复建议

内置 24 条规则，零依赖开箱可用。

```bash
fizzra rules list      # 规则列表（等级、开关状态）
fizzra rules export    # 导出成 YAML 供编辑
fizzra rules check     # 校验所有正则能否编译
```

### 修复建议：每条规则都带 before/after 代码

**只报告问题不给修改方向的审查工具是半成品。**示例会出现在**看板抽屉、独立 issue 页面、HTML 报告**里，并且**存在 issue 上而非规则上** —— 所以历史问题永远保留它被报出时的建议，即使规则后来改了。

在 issue 列表里带示例的行会显示绿色的 `fix available` 标记。

### 自定义规则

```yaml
rules:
  - name: no-fmt-println
    severity: warning
    message: fmt.Println left in library code
    advice: Use the project logger so output can be filtered by level.
    patterns:
      - '^\s*fmt\.Print(ln|f)\s*\('
    paths:
      - '**/*.go'
    exclude:
      - '**/*_test.go'
      - '**/cmd/**'
    enabled: true
```

也可以在 Web UI 的 **Rules** 页面创建和编辑。等级：`critical`、`error`、`warning`、`info`。

### 规则引擎的两个过滤器

正则匹配源码行本身误报率极高，所以引擎会自动处理：

1. **注释永不匹配** —— 规则不该在描述这个反模式的文档上触发
2. **字符串字面量永不匹配** —— 否则规则会匹配到它自己的模式文本
3. **循环作用域** —— 规则可声明 `requireLoop: true`，只在循环体内报告。这让 `defer` 在循环里能报出真问题，而不会误报循环外的 `defer wg.Done()`

---

## Wails 专项检查

`wails` 分析器是这个工具相对于 golangci-lint 包装器的核心价值。它用 Go AST 而非正则，因为关键问题是**调用出现在哪里**。

**项目结构** —— `wails.json`、`frontend/`、`main.go`、`go.mod`、`.gitignore` 完整性、v1/v2 配置漂移。对普通 Go 项目**完全静默**。

**运行时 API**：

| 检查 | 为什么重要 |
|---|---|
| 循环里调用 `EventsEmit` | 每次迭代都发事件会淹没 JS bridge，大数据量下界面直接卡死。批量发送的正确写法不会被报 |
| 未同步的 goroutine 里调用 `EventsEmit` | `OnDomReady` 之前发的事件会被静默丢弃 |
| `OnShutdown` 里调用 `EventsEmit` | 运行时正在拆除，可能 panic |
| 生命周期钩子外调用 `Window*` | 窗口 API 只在 `OnStartup` 和 `OnShutdown` 之间有效 |
| 丢弃 dialog 返回值 | dialog 返回 `(result, error)`。把用户取消当成确认是真实 bug |
| `BrowserOpenURL` 未校验 URL | `file://` 和自定义 scheme 会触达本地处理器，攻击者可控的链接等于本地代码执行。字面量的非 http URL 报 **critical** |
| 匿名 struct 作为事件载荷 | 含非导出字段的载荷在 JS 侧序列化成空对象 |

**绑定设计** —— 统计暴露给 JavaScript 的方法数量、标记过大的接口面、标记无 nil 保护的解引用（前端可能在 `OnStartup` 前调用）、标记做了 I/O 却不返回 error 的方法。

这些都同时锁定了**正例**和**不该报的写法**（批量发送、已校验的 URL、`OnStartup` 里的窗口调用）。

---

## 评分与质量门禁

### 分数构成

```
Score = 0.30·Security + 0.30·Bug risk + 0.20·Maintainability + 0.12·Complexity + 0.08·Coverage
```

每个维度都可单独解释，看板上会显示各自贡献了多少、为什么被扣分。

- **Security** —— 每个 critical 扣 22%，另加对数项，所以数量持续起作用（5 个和 500 个 critical 分数明显不同）
- **Bug risk** —— 每千行 warning 密度，参照点刻意宽松（25/千行才见底）。基于规则的工具总会报出些东西，把几条建议当严重缺陷对待，只会让人关掉工具
- **Maintainability** —— 重复度对标 25% 上限，外加对完全无注释的小幅扣分
- **Complexity** —— 平均圈复杂度，加上对最差函数的封顶扣分
- **Coverage** —— 读取 `coverage.out`。**Fizzra 从不运行你的测试**：扫描时执行任意项目代码是安全和可靠性问题

等级：`A+ ≥95`、`A ≥90`、`B+ ≥85`、`B ≥80`、`C ≥70`、`D ≥60`、`F`

### 质量门禁

```yaml
quality:
  minScore: 70
  maxCritical: 0
  maxHigh: 0
  maxComplexity: 20
  minCoverage: 0
```

每个项目可以在 Web UI 里单独覆盖全局配置。**所有失败条件都会列出，不只是第一条**，方便一次改完。

门禁失败的三种表现：Web 显示失败 · CLI 返回非 `0`· CI 阻断。

门禁还有一道**否决**：如果这次扫描没有任何分析器真正检查过代码（全部跳过或失败），即使各项指标都通过，门禁也**强制失败**。一个什么都没查的扫描报高分，比报错危险得多。

---

## CI 集成

```bash
fizzra ci . --format sarif --output fizzra.sarif --fail-on critical,error
```

现成模板在 [`deploy/`](deploy/)：

- [`deploy/github-actions.yml`](deploy/github-actions.yml) —— 扫描、上传 SARIF（问题会内联标注在 PR diff 上）、上传 HTML 报告、门禁失败时让 job 失败
- [`deploy/gitlab-ci.yml`](deploy/gitlab-ci.yml) —— 同上，另有一个不阻断的 MR 咨询任务

两个模板都会**先上传报告再让构建失败**，SARIF 恰恰在门禁失败时最有用。

### 选择要安装的分析器工具

默认只有 Go 的必装工具，其余 19 个分析器按语言可选。缺工具的后果不是失败，而是**跳过**——扫描会如实报 `INCOMPLETE` 并让门禁失败，不会假通过。所以装哪些只影响检测面，不影响正确性。

GitHub 模板用 `with-analyzers` 控制安装哪些，取值是空格分隔的语言列表：

```yaml
with-analyzers: go                     # 默认：golangci-lint、gosec、govulncheck
with-analyzers: go js python           # 再加 JS/TS 与 Python 的工具
with-analyzers: all                    # 全部语言
```

支持的分组：`go`、`js`、`python`、`rust`、`java`、`csharp`、`cpp`、`php`、`all`。分组是**整词匹配**，所以 `js` 不会连带触发其他语言（例如 `java`）。

这个输入只挂在 `workflow_dispatch` 下。**push 和 pull request 触发时 `inputs` 为空，一律走默认的 `go`。** 有两种用法：

- 想按需多装：在 Actions 页面手动 **Run workflow**，填 `with-analyzers`
- 想推送时也全量扫：把模板里那一步的 `inputs.with-analyzers || 'go'` 改成 `'all'`，或把 `default: go` 改成 `default: all`

每个语言对应哪个工具、在 Ubuntu 上怎么装，见 [`deploy/README.md`](deploy/README.md) 的语言矩阵。

最小的 GitHub Actions 用法：

```yaml
- name: Fizzra
  run: fizzra ci . --format sarif --output fizzra.sarif

- name: Upload SARIF
  if: always()
  uses: github/codeql-action/upload-sarif@v3
  with:
    sarif_file: fizzra.sarif
```

### 本地 pre-commit

```sh
#!/bin/sh
# .git/hooks/pre-commit
fizzra scan . --quiet --analyzers custom,metrics --fail-on critical || {
  echo "存在 critical 问题。运行 'fizzra scan .' 查看详情。"
  exit 1
}
```

只跑始终可用的分析器，不依赖任何外部工具。

---

## 开发与贡献

项目结构、日常开发命令、测试方式、如何新增分析器或规则，见 **[guide/development.md](guide/development.md)**。

---

## 更多文档

| 文档 | 内容 |
|---|---|
| [guide/cli.md](guide/cli.md) | 全部命令行参数、组合用法与退出码 |
| [guide/analyzers.md](guide/analyzers.md) | 分析器一览、工具安装、检查项开关与边界 |
| [guide/faq.md](guide/faq.md) | 常见问题：跳过、误报、覆盖率、配置写法 |
| [guide/design.md](guide/design.md) | 设计取舍：为什么用 SQLite 而不是 PostgreSQL |
| [guide/development.md](guide/development.md) | 开发、测试、验收与扩展 |
| [deploy/README.md](deploy/README.md) | GitHub Actions composite action 的完整用法 |

---

## 许可证

GNU General Public License v3.0。全文见 [`LICENSE`](LICENSE)。
