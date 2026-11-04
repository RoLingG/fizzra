# Fizzra

> 面向多语言项目的轻量级代码质量审查工具。

Fizzra 把你的 linter 跑一遍，再加上通用工具没有的检查，最后汇总成一个可以据以行动的分数，通过 Web 看板、命令行和 CI 三种方式呈现。

覆盖 Go / Wails、Python、JavaScript / TypeScript、Rust、Java、C#、C / C++、PHP 八种生态。**它是编排器而不是分析器**，调用各语言已有的成熟工具，解析其输出，统一成一套评分和门禁。

它是给个人和小团队用的，**没有集群、没有多租户、没有权限系统。一个二进制、一个 SQLite 文件、不需要任何外部服务。**

---

## 目录

- [30 秒跑起来](#30-秒跑起来)
- [环境要求](#环境要求)
- [安装与运行](#安装与运行)
- [四种使用方式](#四种使用方式)
- [可选：安装外部检查工具](#可选安装外部检查工具)
- [启动项目](#启动项目)
- [测试项目](#测试项目)
- [配置](#配置)
- [规则与修复建议](#规则与修复建议)
- [Wails 专项检查](#wails-专项检查)
- [评分与质量门禁](#评分与质量门禁)
- [CI 集成](#ci-集成)
- [开发](#开发)
- [常见问题](#常见问题)
- [项目结构](#项目结构)

---

## 30 秒跑起来

> 克隆下来之后**不能只跑 `go build`**。仪表盘的构建产物（`backend/internal/web/dist/`）是生成物、不入库，第一步必须先生成它。这一步需要 Node 和 npm。

```bash
# 1. 构建前端（产物写进 backend/internal/web/dist/）
go generate ./backend/internal/web/...

# 2. 编译二进制（前端产物会被 go:embed 打进去）
go build -o fizzra.exe ./backend/cmd/fizzra

# 3. 启动
./fizzra.exe serve
```

> 顺序不能反。`go:embed` 在**编译时**快照文件，所以前端必须先构建好；跳过第一步直接编译，得到的二进制没有仪表盘，只有命令行可用。

启动之后，控制台会有日志，打开 <http://127.0.0.1:8080> 进入 Web UI，在项目里添加项目并进行项目扫描，等待扫描完毕后，即可查看扫描效果。

想先看效果，也可以使用二进制文件直接扫描任意 Go 项目：

```bash
./fizzra.exe scan /path/to/your/go/project
```

---

## 环境要求

| 组件 | 版本 | 是否必需 |
|---|---|---|
| **Go** | 1.24 或更高（开发用 1.27） | 必需 |
| **Node.js** | 22 或更高 | 仅构建前端时需要 |

> `go.mod` 声明 `go 1.24`，这是**真实的最低要求**——测试里用到了 `testing.B.Context`（Go 1.24 引入）。更高版本的工具链直接可用，无需改动。

**Node 只在构建前端时需要。** 如果只用 CLI 和 API，可以不装 Node —— 二进制照常编译，服务端会显示一个提示如何构建看板的页面。

### 技术栈版本

| 层 | 组件 | 版本 |
|---|---|---|
| 后端 | Go | 1.24+ |
| 后端 | Gin / GORM | 1.10 / 1.25 |
| 后端 | 数据库 | SQLite |
| 前端 | Vue | 3.5 |
| 前端 | vue-router / pinia | 5.x / 4.x |
| 前端 | Element Plus / ECharts | 2.14 / 6.x |
| 前端 | vue-i18n | 11.x |
| 前端 | Vite / TypeScript | 8.x / 6.x |

> **关于 TypeScript 版本**：项目用 TS 6 而非最新的 7.x。TypeScript 7 是原生重写版（tsgo），包结构已改变——不再导出 `typescript/lib/tsc`，而 `vue-tsc` 依赖该路径做 SFC 类型检查。两者目前不兼容，这是生态跟进问题而非配置问题。等 `vue-tsc` 支持 TS 7 后可直接升级。
>
> 已提前按 TS 7 的要求清理了 `tsconfig.json`：移除了 `baseUrl`（TS 7 会删除该选项），改用相对 `paths`。

---

## 安装与运行

### 完整构建（含 Web 看板）

```bash
go generate ./backend/internal/web/...    # 构建前端
go build -o fizzra.exe ./backend/cmd/fizzra
```

**为什么用 `go generate`**：`go:embed` 要求前端产物在编译前就存在，这个顺序必须有地方固定。`go generate` 是 Go 工具链自带的，不需要额外安装，也没有什么复杂的兼容问题。

对应封装指令都写在 [`backend/internal/web/embed.go`](backend/internal/web/embed.go) 里：

```go
//go:generate npm --prefix ../../../frontend ci
//go:generate npm --prefix ../../../frontend run build
```

想手动分步执行也可以：

```bash
cd frontend
npm install          # 首次需要，约 1 分钟
npm run build        # 产物输出到 backend/internal/web/dist/
cd ..
go build -o fizzra.exe ./backend/cmd/fizzra
```

### 仅 CLI（不装 Node）

```bash
go build -o fizzra.exe ./backend/cmd/fizzra
```

这样构建的二进制不含看板，但 `scan` / `ci` / `report` / `rules` 全部可用。

### 用 `go install` 安装（得到的是 CLI-only 版本）

```bash
go install github.com/rolingg/fizzra/backend/cmd/fizzra@latest
```

**这样装出来的二进制没有 Web 看板。** 原因在 `go:embed`：它在**编译时**把 `backend/internal/web/dist/` 快照进二进制，而这个目录是前端构建产物、不入库（见 `.gitignore`）。`go install` 是在模块缓存里编译的，那里没有 `dist`，于是嵌入失败、`web.Dist()` 返回 nil，二进制只剩命令行。

这不是缺陷，是刻意的降级：**没有 Node 的环境也能装、也能在 CI 里跑门禁**。`fizzra serve` 会正常启动，只是页面显示一个"如何构建看板"的提示页。

需要完整看板时有两条路：

| 方式 | 做法 |
|---|---|
| 用发布产物 | 从 Releases 下载对应平台的二进制，看板已内置 |
| 本地构建 | 克隆源码 → `go generate ./backend/internal/web/...` → `go build` |

`fizzra version` 分不出两者，因为版本号相同。判断方法很简单：`fizzra serve` 后打开首页，看到 Dashboard 就是完整版，看到"如何构建看板"的说明页就是 CLI-only 版。

### 启动服务

```bash
./fizzra.exe serve                    # 默认 127.0.0.1:8080
./fizzra.exe serve --port 9090        # 指定端口
./fizzra.exe --config my.yaml serve   # 指定配置文件
```

### 数据存放位置

默认全部在 `~/.fizzra/`（Windows: `C:\Users\<用户名>\.fizzra\`）：

```
~/.fizzra/
├── fizzra.db        # SQLite 数据库（项目、分析记录、问题、规则）
└── workspace/         # 克隆的仓库
```

删掉这个目录，即可完全重置项目。

---

## 四种使用方式

### 1. Web 看板（主要方式）

```bash
./fizzra.exe server
```

| 页面 | 内容 |
|---|---|
| **Dashboard** | 总评分、问题分布、安全风险、覆盖率、评分趋势 |
| **Projects** | 项目列表，注册/编辑/删除，手动触发扫描 |
| **Project Detail** | Issues（筛选/排序/分页）、Overview、Metrics、History、Settings（质量门禁） |
| **Rules** | 全部规则，可开关、改等级、写自定义规则 |
| **System** | 分析器可用性、缺失工具的安装命令、CI 配置示例 |

点击任意问题行打开详情抽屉，可以看到**修复建议的 before/after 代码对比**。

> 注：部分语言的扫描工具可能不支持对比输出，待兼容开发。

### 界面语言与主题

顶栏右侧有两个开关，选择会记在浏览器里，下次打开自动生效：

- **主题**：浅色 / 深色 / 跟随系统。跟随系统是默认值，会随操作系统的深浅色实时切换。
- **语言**：English / 中文。首次访问时跟随浏览器语言。

中文界面覆盖全部页面文案，**包括规则说明、修复建议与评分维度**。

### 2. 命令行扫描

```bash
./fizzra.exe scan                          # 扫描当前目录
./fizzra.exe scan ./my-project             # 扫描指定目录
./fizzra.exe scan --project my-app         # 扫描已注册的项目
./fizzra.exe scan ./app --analyzers custom,metrics,wails	# 使用指定工具，扫描指定目录
```

示例输出（以某个真实项目为例，数字随项目而异）：

```bash
  your-project
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

注意 `--config` / 配置查找：Fizzra 按 `--config` → `./fizzra.yaml` → `~/.fizzra/config.yaml` 的顺序查找，其中 `./` 指**当前工作目录**。所以在子目录里运行会静默退回内置默认值——内置默认值不含你在 `fizzra.yaml` 里写的项目专属豁免，分数和门禁结果都会不同。想复现仓库自己的基线，就在仓库根目录运行。

**扫描一个目录时会自动注册成项目**，重复扫描同一路径会复用记录，不会产生重复。

### 3. CI 门禁

```bash
./fizzra.exe ci . --fail-on critical,error
```

退出码：**0** 通过 · **1** 门禁失败或扫描失败 · **2** 用法错误

### 4. 生成报告

```bash
./fizzra.exe report --project my-app --format html  --output report.html
./fizzra.exe report --project my-app --format json  --output report.json
./fizzra.exe report --project my-app --format sarif --output fizzra.sarif
```

| 格式 | 用途 |
|---|---|
| `text` | 终端输出（`scan` 默认） |
| `json` | 机器消费，完整问题列表 |
| `html` | **单文件自包含**，无 CDN、无网络依赖，可直接打印或作为 CI 制品 |
| `sarif` | SARIF 2.1.0，GitHub / GitLab 代码扫描内联标注 |

### 全部命令

```bash
./fizzra.exe help
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

---

## 支持的语言

Fizzra 是**编排器**：它不自己实现静态分析，而是调用各语言已有的成熟工具、解析其 JSON 输出、汇总成统一评分。

| 语言 | 工具 | 说明 |
|---|---|---|
| **Go / Wails** | golangci-lint、staticcheck、go vet、gofmt、gosec、govulncheck + 内置 24 条规则 | 完整支持，含 Wails 专项检查 |
| **Python** | **ruff**(开启 `--select ALL`,含 bandit 安全规则) | 一个工具覆盖 flake8 + isort + pylint 子集 + 复杂度 + 安全 |
| **JavaScript / TypeScript** | **oxlint** + **tsc**(类型检查) + **biome** | oxlint 覆盖常规 lint(含 31 条 Vue 规则);tsc 补上类型错误(oxlint 默认规则集查不到);biome 补上 oxlint 漏掉的正确性规则,并兼查 **CSS** |
| **Rust** | **clippy**、**rustfmt**、**cargo-audit** | clippy/rustfmt 随官方工具链;cargo-audit 查依赖漏洞 |
| **Java** | **PMD**、**SpotBugs** | PMD 覆盖面广(含 CWE 标注);SpotBugs 分析字节码,查数据流相关缺陷 |
| **C#** | **Roslynator** | 编译器诊断 + .NET 内置分析器 + 代码质量规则,不改动工程文件 |
| **C / C++** | **cppcheck**、**clang-tidy** | cppcheck 无需构建;clang-tidy 补上命令注入等安全规则 |
| **PHP** | **PHPStan** | 未定义方法、传参类型不符、不可达分支等类型错误 |

**按项目选择语言**：新建/编辑项目时选择语言，决定运行哪些分析器。也可以由 CLI 按清单文件自动识别（`go.mod` / `Cargo.toml` / `pyproject.toml` / `package.json`）。

支持 **Tauri 布局**（`Cargo.toml` 在 `src-tauri/`）—— 自动检测会向下查一层，cargo 也在正确的目录里执行。

> **语言不支持时不会假通过。** 如果某语言还没有分析器，或所需工具没装，扫描会明确报告 **INCOMPLETE** 并让门禁失败，而不是给出一个高分。这是刻意的：一个什么都没检查的扫描报 94.4 分，比报错危险得多。

---

## 可选：安装外部检查工具

Fizzra 内置 16 个分析器，其中 13 个依赖外部工具（其余 3 个——`custom`、`metrics`、`wails`——是内置实现）。**缺失的工具会被跳过，不会导致扫描失败** —— 但装上它们能显著提升检测能力。

查看当前状态：

```bash
./fizzra.exe analyzers
```

```
  ANALYZER       CATEGORY   STATUS   REQUIRES
  ────────────────────────────────────────────────────────────
  custom         custom     ready    —
  go-vet         lint       ready    go
  gofmt          lint       ready    gofmt
  golangci       lint       ready    golangci-lint
  metrics        metrics    ready    —
  ruff           lint       ready    ruff
  clippy         lint       ready    cargo
  rustfmt        lint       ready    cargo
  wails          wails      ready    —
  cargo-audit    vulnerability ready cargo-audit
  biome          lint          missing  biome
  gosec          security      missing  gosec
  govulncheck    vulnerability missing  govulncheck
  oxlint         lint          missing  oxlint
  staticcheck    lint          missing  staticcheck
  tsc            correctness   missing  npx
```

安装缺失的工具：

```bash
# Go
go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@latest
go install honnef.co/go/tools/cmd/staticcheck@latest
go install github.com/securego/gosec/v2/cmd/gosec@latest
go install golang.org/x/vuln/cmd/govulncheck@latest

# Python
pip install ruff

# JavaScript / TypeScript
npm install --save-dev typescript vue-tsc oxlint @biomejs/biome

# Rust（clippy 和 rustfmt 随工具链，用 rustup 添加）
rustup component add clippy
rustup component add rustfmt
cargo install cargo-audit --locked
```

> **JS/TS 的工具装在项目里，不是全局。** `tsc` 分析器用的是**被扫描项目自己**的 `node_modules` 里的编译器（通过 `npx --no-install` 解析），不是 PATH 上的全局副本——否则检查的会是另一个 TypeScript 版本。Biome 的 npm 包名是 **`@biomejs/biome`**：npm 上不带作用域的 `biome` 是另一个已废弃的 0.3.x 包，装错了分析器会一直报 missing。`./fizzra.exe analyzers` 里的安装提示会带上这些区别。

装完后**重启 Fizzra**（可用性在启动时探测一次），`./fizzra.exe analyzers` 就会显示 `ready`。

> **`pip install` 装的工具常常不在 PATH 上**（Windows 上会落到 `%APPDATA%\Python\Python3xx\Scripts`）。Fizzra 会自动回退到 `python -m <模块>`，所以 `analyzers` 仍会显示 `ready`，扫描也能正常工作。

### 需要联网的分析器

`cargo-audit` 和 `govulncheck` 要拉取漏洞数据库。**网络不通时它们不是快速失败，而是会一直等到连接超时** —— 一个本该 2 秒的扫描会变成一分钟，然后什么都没报。

Fizzra 因此提供**离线模式**，三处都能设：

```bash
./fizzra.exe scan ./my-project --offline          # 单次扫描
```
```yaml
# fizzra.yaml
scan:
  offline: true                # 全局
  analyzerOptions:
    cargo-audit:
      offline: "true"          # 只针对某个分析器
```

Web 界面在 **System 页面 → 网络** 区块有开关，运行时即可切换，不必重启。

两个分析器在离线下的**行为不同**，因为它们能降级的程度不同：

| 分析器 | 离线时的行为 | 结果 |
|---|---|---|
| `cargo-audit` | 继续运行，改用本地数据库（`--no-fetch`） | **仍报问题**，但依据的快照可能过时；首次需联网跑一次建立快照 |
| `govulncheck` | 完全跳过，并给出原因 | **不报问题**，计入「跳过」；若因此没有任何分析器检查代码，门禁会失败 |

`cargo-audit` **实测从 3.0 秒降到 0.9 秒**；网络不通时差距更大。

> `govulncheck` 之所以选择跳过而不是降级：它没有本地数据库模式，而对着一份过时的数据库报 Go 漏洞比不报更危险。跳过不是静默的——它会计入跳过原因，在扫描结果和 Web 界面上都能看到。

### 分析器检查项开关

有些检查**是否算「问题」取决于团队规定**，而不是事实——比如「必须写 docstring」「必须加类型标注」。这类检查默认关闭，需要时可在 **System 页面 → 检查组** 打开。

以 Python 的 `ruff` 为例，8 组可切换，**默认全部关闭**：

| 开关 | 覆盖内容 | 为什么默认关 |
|---|---|---|
| `style` | 行长度、空白、空行等格式 | 格式化的职责；会淹没问题列表 |
| `docstrings` | 缺失或格式不对的 docstring | 文档规范，不是缺陷 |
| `annotations` | 缺失类型标注 | 对新代码有用，对老代码噪声大 |
| `copyright` | 缺失版权声明 | 组织规定 |
| `naming` | PEP 8 命名规范 | 约定 |
| `pandas` | pandas 反模式 | 只与数据科学代码相关 |
| `quotes` | 引号风格 | 约定 |
| `todo` | TODO 注释格式 | 约定 |

**实测效果**（真实 Python 项目，只开 `style` 一组）：

| 配置 | 问题数 | info |
|---|---|---|
| 默认 | 129 | 16 |
| 开启 `style` | **356** | **243** |

多出的 227 个是格式问题，映射为 `info`——所以它们会出现，但**不会影响质量门禁**。默认结论仍然只关于缺陷。

也可以在配置文件里设：

```yaml
scan:
  analyzerOptions:
    ruff:
      style: "true"
      docstrings: "true"
```

> 开关名写错会被**拒绝**并报错，而不是静默存下。一个什么都不做的设置比一个报错的设置更糟——用户会以为它生效了。
>
> 这条检查覆盖**分析器名和开关名两段**：写错分析器名（`rufff`）会提示最接近的名字，写错开关名（`styel`）同理，把开关写到不支持它的分析器下（`biome.docstrings`）也会被拒。

**JS/TS 的两个开关**（同样默认值如下）：

```yaml
scan:
  analyzerOptions:
    oxlint:
      vuePlugin: "true"    # 默认开。见下
    biome:
      css: "true"          # 默认开
```

> **`vuePlugin` 默认开，与 ruff 那些默认关的开关相反**，因为两者性质不同：ruff 的组开启的是"团队规范"类检查（docstring、版权头），会淹没问题列表；`vuePlugin` 加的是 oxlint 自己的 31 条 Vue 规则，不加噪声。实测（`--print-config`）：不开 111 条规则，开了 142 条，多出的 31 条全部是 `vue/*`。实测一个"computed 里有副作用"的组件：不开报 0 条，开了报 `vue(no-side-effects-in-computed-properties)`。
>
> 关掉它就等于放弃这 31 条检查——`.vue` 的 `<script>` 块两种情况下都会检查，差别只在模板相关的规则。

> **`css` 只覆盖 `.css`。** Biome 不读 `.scss` / `.less` / `.sass`——实测同时喂三个文件给它，只产出一条诊断，且指向 `.css` 那个。所以这个开关不会假装覆盖 Sass/Less；如果你的样式是预处理器写的，Biome 这条路是空的，需要另配 stylelint（见下）。

### 分析器一览

| 分析器 | 语言 | 检查内容 | 依赖 |
|---|---|---|---|
| `custom` | **全部语言** | 你在 Web UI 或 `fizzra.yaml` 里写的正则规则 | 无 |
| `metrics` | **全部语言** | 代码规模、重复度、测试面（复杂度仅 Go，见下） | 无 |
| `wails` | Go | Wails 项目结构、运行时 API 用法、绑定设计 | 无 |
| `gofmt` | Go | 格式化 | `gofmt` |
| `go-vet` | Go | 编译器接受的可疑写法 | `go` |
| `golangci-lint` | Go | 未使用代码、错误处理、风格、广度静态分析 | `golangci-lint` |
| `staticcheck` | Go | 深度缺陷与简化建议 | `staticcheck` |
| `gosec` | Go | 注入、弱加密、硬编码凭据、不安全权限 | `gosec` |
| `govulncheck` | Go | 依赖中的可达 CVE | `govulncheck` |
| `ruff` | Python | 正确性、导入、风格、复杂度、安全（bandit 规则族） | `ruff` |
| `oxlint` | JS/TS | 正确性、可疑写法、TypeScript、React、可访问性 | `oxlint` |
| `tsc` | JS/TS | 类型错误、不可能的值、不安全的 null 处理、契约违反（需 `tsconfig.json`） | `npx` |
| `biome` | JS/TS/**CSS** | 正确性、可疑写法、风格；补上 oxlint 默认规则集未启用的几条错误。**也检查 `.css`**：属性名拼写、重复声明、`!important`、空规则块 | `biome` |
| `clippy` | Rust | 正确性、可疑写法、性能、惯用写法 | `cargo` |
| `rustfmt` | Rust | 格式化 | `cargo` |
| `cargo-audit` | Rust | Cargo.lock 依赖中的已知漏洞（RustSec / CVE），**默认不计入评分**，见下 | `cargo-audit` |
| `pmd` | Java | 缺陷、性能、安全（含 CWE 标注）与代码规范 | `pmd` |
| `spotbugs` | Java | 字节码层面的缺陷与数据流问题（空指针、资源泄漏、硬编码凭据） | `spotbugs` |
| `roslynator` | C# | 编译器诊断、.NET 内置分析器、代码质量规则 | `roslynator` |
| `cppcheck` | C/C++ | 内存泄漏、缓冲区越界、悬垂指针等缺陷（无需构建） | `cppcheck` |
| `clang-tidy` | C/C++ | 命令注入、悬垂指针、内存泄漏、CERT C 规则 | `clang-tidy` |
| `phpstan` | PHP | 未定义方法与属性、传参类型不符、不可达分支 | `php` |

> **`cargo-audit` 的结果默认不进分数。** 它报的是依赖漏洞，不是你写的代码——修好它需要 `cargo update`，而依赖常被上游钉死。让它参与加权会让一个项目无论怎么改自己的代码都上不去分。
>
> 排除的只是**加权分**。问题照常显示在列表里、带**直接/传递依赖**徽标（悬停看完整依赖链），也照样触发 `maxCritical` / `maxHigh` 门禁。想计入就设 `cargo-audit.scoredTransitive: "true"`。

> **检查深度不止是「报了个问题」。** 每条问题都会显示**所在行的源码**——工具本身不在 JSON 里给源码文本的（`ruff`/`biome`/`oxlint`/`clippy` 都不给），由 Fizzra 读回该行。**修复示例（before/after）**则只在工具自己提供了机器可读的替换文本时才有：目前是 `ruff` 和 `clippy`。`biome`/`oxlint`/`tsc` 不提供，所以它们只显示源码、不显示修复示例——这里不会替工具猜一个补丁出来。详见 [常见问题](#q能扫非-go-项目吗)。

> **自定义规则适用于所有语言。** 规则引擎会按项目语言选择合适的注释符、字符串形式与作用域模型——Python 的 `#` 注释、三引号 docstring、缩进块，Rust 的嵌套块注释、生命周期，JavaScript 的模板字符串，PHP 的 `#` 注释与反引号，都能正确识别。所以一条 `paths: ['**/*.py']` 的规则在 Python 项目上会生效，且不会误报在注释或 docstring 里。

### 样式检查的边界

**CSS 有检测，Sass/Less 没有。** Biome 能读 `.css`，读不了预处理器方言——这不是配置问题，是它没有那些解析器。当前覆盖：

| 文件 | 谁在查 | 能查出什么 |
|---|---|---|
| `.css` | Biome | 属性名拼写错误、重复声明、`!important`、空规则块、选择器特异性 |
| `.scss` / `.less` / `.sass` | **无人** | — |
| 组件内的 `<style>` 块（`.vue`） | **无人** | Biome 的 `.vue` 结果整体不可信，见下 |

想覆盖 Sass/Less，标准工具是 **stylelint**（配 `stylelint-scss`）。Fizzra **没有集成它**，原因是它是配置驱动的工具：没有配置文件时它不报任何东西，集成它意味着要么替用户写一份配置（等于替他们选规则），要么在默认项目上什么都查不到。这和 ESLint 是同一个取舍。

如果你需要，可以自行引入 stylelint 并让它输出 JSON，Fizzra 的自定义规则引擎无法消费它——这一块目前是明确的空缺，而不是"悄悄通过"。

> **为什么 `.vue` 的样式块也没查：** Biome 没有 Vue 解析器，它在一个 `.vue` 文件里的结论整体是错的——`<script setup>` 里每一个绑定都会被报成"未使用"，因为唯一使用它们的 `<template>` 没有被解析。实测在本项目前端上 123 条诊断里 106 条来自 `.vue`，其中 99 条是这类误报。所以 `.vue` 路径被整体跳过：报出它们不只是噪声，而是**与代码相矛盾的报告**，正是这个项目要避免的失败模式。`vue-tsc` 仍在检查 `.vue` 的类型（它读得懂 SFC），所以 `.vue` 不是完全没人管，只是没有 lint 层面的覆盖。

### 覆盖率文件

Fizzra **不运行你的测试**——扫描时执行项目代码是安全与可靠性问题。它会读取你流水线已经生成的覆盖率文件，自动识别格式：

| 格式 | 由谁生成 |
|---|---|
| Go profile | `go test -coverprofile=coverage.out` |
| lcov | `cargo llvm-cov`、Istanbul、`cargo tarpaulin` |
| Cobertura XML | `coverage.py`、`cargo tarpaulin` |
| Istanbul JSON | Jest、nyc |

### 跨语言评分的边界

| 维度 | 覆盖情况 |
|---|---|
| 安全性 / 缺陷风险 / 可维护性 | **全部语言**（基于问题计数与文本度量） |
| 覆盖率 | **全部语言**（多格式自动识别） |
| 复杂度 | **仅 Go**——需要按语言实现 AST 遍历器 |

> **未测量的维度不会被算成满分。** 复杂度对 Rust/Python/JS 会明确显示「not measured」且**不参与加权**，其余维度重新归一化。这一点是刻意设计的：把"没测量"当作 0 会让非 Go 项目因为**信息更少而得分更高**。

---

## 启动项目

### 开发模式（前后端分离，改代码即时生效）

```bash
# 终端 1：后端 API
go run ./backend/cmd/fizzra serve

# 终端 2：前端开发服务器（带热更新）
cd frontend && npm run dev
```

打开 <http://localhost:5173>。Vite 会把 `/api` 自动代理到 8080 的后端，前端改动即时生效，不必重新构建。

> **别在 Vite 还开着的时候跑 `go generate`。**
>
> `go generate` 执行的是 `npm ci`，它会先删掉整个 `node_modules` 再重装。而 Vite 正持有 `node_modules/@rolldown/binding-win32-x64-msvc/rolldown-binding.win32-x64-msvc.node` 的句柄，Windows 不允许删除被占用的文件，于是命令失败并报：
>
> ```
> npm error code EPERM
> npm error syscall unlink
> npm error path ...\rolldown-binding.win32-x64-msvc.node
> ```
>
> **这不是依赖损坏，也不要用管理员权限硬来。** 停掉 dev server 再重跑即可。
>
> 这个报错特别容易误导，因为它长得像权限问题或 npm 缓存损坏，而实际上只是文件被占用。Windows 上「文件被占用」和「权限不足」共用同一个 `EPERM` 错误码。
>
> 如果忘了哪个终端开着，用 `Ctrl-C` 停掉那个 Vite 窗口；后台启动的进程用 `taskkill /F /IM node.exe` 清掉（注意这会杀掉所有 Node 进程，包括编辑器插件）。

### 生产模式（单二进制）

```bash
go generate ./backend/internal/web/...     # 构建前端产物
go build -o fizzra.exe ./backend/cmd/fizzra
./fizzra.exe serve
```

打开 <http://127.0.0.1:8080>。前端已打包进二进制，运行时不依赖 Node。

### 只跑 CLI（不装 Node）

```bash
go build -o fizzra.exe ./backend/cmd/fizzra
./fizzra.exe scan ./your-project
```

看板不会内嵌（服务端会提示如何构建），但 `scan` / `ci` / `report` / `rules` 全部可用。

### 数据与重置

所有状态都在 `~/.fizzra/`：

```
~/.fizzra/
├── fizzra.db        # SQLite（项目、分析记录、问题、规则）
└── workspace/         # 克隆的仓库
```

删掉这个目录即完全重置。

---

## 测试项目

### 一键验证

> 测试文件不随本仓库发布，下面这些命令只在完整源码树里可用。

```bash
go test -race ./backend/...
```

`-race` 不能省，数据竞态就是靠它发现的。九个包全绿说明后端正常。

### 分层测试

```bash
# 后端
go test ./backend/...                        # 全部
go test ./backend/internal/rule/...          # 单个包
go test -run TestLoopScoped ./backend/...    # 单个测试

# 覆盖率
go test -coverprofile=cov.out ./backend/...
go tool cover -html=cov.out

# 前端（这几项不依赖测试文件，公开副本里同样可用）
cd frontend
npm run typecheck                            # 类型检查
npm run check:locales                        # 校验中英词条完整性与复数形式
npm run build                                # 构建，并自动验证产物能否真正启动
npm run check:render                         # 在模拟浏览器里验证主题与语言确实生效
npm run check:served                         # 验证运行中的二进制内嵌了完整产物（需先 serve）
npm run check:theme                          # 校验 Element Plus 组件是否都跟随主题
```

> `npm run build` 串联了三步：类型检查 → 构建 → 启动验证。最后一步（`smoke:bundle`）会把产物在一个模拟浏览器里真正跑一遍，确认模块图能正确求值、Vue 能挂载。
>
> **这不是多余的仪式。** 产物白屏是最难靠肉眼发现的一类故障：构建成功、类型检查通过、所有 chunk 都能 200，但页面就是空的。原因是 Rollup 允许产出**循环的 chunk 依赖**，而 chunk 会在它 import 的那个 chunk 初始化完成之前就开始求值——于是某个库调用到了尚未定义的内部函数，浏览器只丢出一句 `t is not a function`。静态检查看不出这个问题，只有真正执行才能。

> `npm run check:served` 面向的是**部署**而非构建：`go:embed` 在编译时快照文件，所以一个在最后一次前端构建之前编译出的二进制，会内嵌旧产物——它照样返回 200。这项检查会枚举浏览器将来会请求的每一个资源（含路由懒加载的 JS 与 CSS，共 25 个），逐一确认服务端答得上来，并用内容哈希比对二进制内嵌的入口 chunk 与当前构建产物是否一致。它拦的是「首页能打开、点导航才 404」那类故障。

### 静态检查

```bash
go vet ./backend/...
gofmt -l backend/        # 有输出说明哪些文件需要格式化
```

### 自扫描 —— 最有说服力的一项

```bash
go build -o fizzra.exe ./backend/cmd/fizzra
./fizzra.exe ci ./backend
```

用工具检查它自己的代码。**预期：gate 通过，退出码 0。**

> 分数随代码增长会缓慢变化，不要把它当成固定值去对——它是这个仓库当前状态的快照，不是断言。重要的是 `gate passed` 与退出码 0。
>
> **必须在仓库根目录运行。** 配置查找用的是相对路径 `./fizzra.yaml`，在 `backend/` 里执行会找不到配置、退回内置默认值，于是 `fizzra.yaml` 里那些针对本仓库的豁免（比如 `no-panic` 对 `internal/analyzer/analyzer.go` 的排除）全部失效，结果会显示 gate failed。这不是代码问题，是运行位置问题——`ci` 的目标参数是 `./backend`，不是 `cd backend`。

规则有误报的话这里会立刻暴露——事实上确实暴露过：加入代码示例功能后，示例里的「反面代码」被自己的规则误报成 2 个 critical，因此实现了示例抑制逻辑。

### 手动验收清单

**构建与 CLI**

- [ ] `./fizzra.exe version` 打印版本
- [ ] `./fizzra.exe analyzers` 如实显示哪些工具缺失（不谎报 ready）
- [ ] `./fizzra.exe scan ./backend` 输出分数和问题统计
- [ ] `./fizzra.exe ci ./backend` 退出码为 0

**报告**

- [ ] `./fizzra.exe report --project backend --format html --output r.html` 生成可打开的单文件报告
- [ ] 报告里能看到 before/after 代码块

**Web 看板**

- [ ] `./fizzra.exe serve` 后浏览器能打开 <http://127.0.0.1:8080>
- [ ] 添加项目 → Scan now → 等扫描完成
- [ ] 项目详情 → Issues → 点任意一行，抽屉里有 **before/after 代码对比**
- [ ] 带示例的行显示绿色 `fix available` 标记
- [ ] 首次扫描的 issue 显示蓝色 `new` 标记；再扫一次后标记消失
- [ ] 勾选 **New only** 后列表只剩新问题
- [ ] Rules 页面能开关规则、修改等级
- [ ] 扫描 Rust 项目后，`cargo-audit` 的问题带 **直接/传递依赖** 徽标，悬停显示完整依赖链
- [ ] 问题列表顶部有**依赖问题说明**横幅，且不计入评分的条数正确
- [ ] `./fizzra.exe ci ./backend --timeout 1ns` 后，**项目详情页出现「扫描未完成」告警**，列出失败的分析器（不是只有 CLI 才有提示）
- [ ] System 页面 → **扫描缓存** 区块显示各缓存状态与所在位置
- [ ] System 页面 → **检查组** 点开 ruff 的配置抽屉，全部为中文（切到英文后为英文）

**主题切换**

- [ ] 全新配置（清掉 `localStorage`）打开时跟随系统，无闪白
- [ ] 浅色系统下选深色：立即变色，图表坐标轴与网格线跟着换色
- [ ] **深色系统下选浅色：界面确实变浅**（朴素实现最容易错的一项）
- [ ] 刷新后选择保留，且首屏无闪白
- [ ] 选回「跟随系统」后改变系统主题，界面实时跟随
- [ ] 在 Dashboard 与项目详情页切换主题，图表立即重绘；切到别的页面再切回，首屏配色正确
- [ ] **切换主题时整页交叉淡入**（View Transition），任意页面都不出现「卡一下再切完」
- [ ] Firefox（尚不支持该 API）下主题**立即切换**，无动画但不报错
- [ ] **Element Plus 组件全部跟随主题**（这是最容易漏的一类，见下）

> 第三方组件的适配是深色模式最常见的盲区：页面自己的元素都对了，但 Element Plus 的组件仍用库内的浅色默认值。切换主题后逐个看一眼：
>
> - 项目详情 → Issues **分页的页码格子**（1 2 3 4）：格子底色与当前页码的对比度是否正确
> - **开关**（规则页的启用开关、门禁的 Enabled）：关闭态在深色下不能是亮灰
> - **复选框与单选按钮**（Issues 页的 New only、项目类型、主题切换）：勾选后的对勾要看得见
> - **空状态插画**（无数据分析时的图示）：不能是白底上的浅灰图形
> - **提示气泡**（顶栏右侧不可用分析器、失效的 tooltip）
> - **下拉面板**（筛选器展开后的选项列表）
> - **数字输入框的加减按钮**（门禁设置）
>
> 这些现在由 `npm run check:theme` 在构建时把关：它把每个组件用到的颜色变量沿 `var()` 链解析到底，要求终点是项目的主题 token 而不是硬编码颜色。

**中英文切换**

- [ ] 切换语言后，菜单、页头、表格、按钮、表单、抽屉全部变为中文，无需刷新
- [ ] 浏览器标签页标题跟随语言变化
- [ ] Dashboard 的风险句式、覆盖率提示等**整句**为中文，不是拼起来的碎片
- [ ] 项目详情 5 个 tab（含 Settings 门禁表单）全中文
- [ ] 规则抽屉里 message / advice / 修复建议说明为中文，**代码示例保持英文**
- [ ] `el-pagination` 的分页文案为中文（验证 Element Plus 语言已跟随）
- [ ] **保持项目新建弹窗打开**，切换语言后触发校验，报错文案跟着变
- [ ] `false_positive` 等状态显示为「误报」，而非原始枚举值
- [ ] 相对时间显示为「3 分钟前」这类中文

> 最后两条针对两类静默失效：模块级 `FormRules` 不会响应语言切换，`format.ts` 里的翻译查询不会触发重算。二者都只在切换语言时才暴露。

**多语言项目**

- [ ] 新建项目时选 Rust / Python / JavaScript，保存后语言正确
- [ ] 扫描一个 Tauri 项目（`Cargo.toml` 在 `src-tauri/`）能自动识别为 Rust，而不是 JavaScript
- [ ] Rust 项目的漏洞指向 `src-tauri/Cargo.lock`，不是根目录的假路径
- [ ] Python 项目里 `eval()` / `os.system()` 被报为 critical
- [ ] 自定义规则在非 Go 项目上生效（写一条 `paths: ['**/*.py']` 的规则验证）
- [ ] 规则**不会**在 Python 注释（`#`）或 docstring（`"""`）里误报
- [ ] 未测量的复杂度显示为 `not measured`，而不是 `avg 0.0`
- [ ] **缺工具时门禁失败**：拿掉某个工具后扫描应报 INCOMPLETE，而不是给高分
- [ ] **离线模式**：System 页打开开关后，cargo-audit 明显变快（不卡在联网）

**分析器检查项开关**

- [ ] System 页的 ruff 卡片显示 8 个开关，**全部默认关闭**
- [ ] 打开 `style` 后重新扫描，问题数明显上升（实测 129 → 356）
- [ ] 关掉后重新扫描，问题数回到原来的值
- [ ] 在配置文件里写一个不存在的开关名，**扫描应报错**而不是静默忽略

### 验证「假通过已被堵住」

这是多语言支持要解决的核心问题，也是最危险的一类故障。

```bash
# 一个只有 Rust 清单、没有任何分析器能处理的目录
./fizzra.exe scan ./some-rust-dir
```

**预期**：报 `INCOMPLETE`，列出跳过的分析器及原因，门禁**失败**。

**对照**：修复前它报 `94.4 / A / 门禁通过`，而实际上一个文件都没检查过。

同理，如果某个项目只配了 lint 分析器但工具没装（例如 clippy 的 cargo 不在 PATH 上），扫描也应失败而不是通过 —— 因为度量分析器（metrics）会跑，但它找不到任何 bug。

### 验证「不完整扫描不会假通过」

这是本项目修过的最严重的缺陷：9 个分析器里 8 个超时失败，却报告 `gate=passed` 且退出码 0。在 CI 里等于门禁失效——而且因为问题数变少，分数反而比真实扫描**更高**。

```bash
./fizzra.exe ci ./backend --timeout 1ns      # 强制超时，模拟分析器大面积失败
```

**预期**：输出以 `!! INCOMPLETE` 开头，列出失败的分析器，`gate failed`，**退出码 1**。

对照正常扫描（退出码 0）：

```bash
./fizzra.exe ci ./backend
```

### 验证「参数拼写错误会报错」

```bash
./fizzra.exe ci ./backend --fail-on zzz       # 报错：unknown severity "zzz"
./fizzra.exe ci ./backend --fail-on errors    # 接受（复数可以识别）
```

此前任何无法识别的值都会被静默当成 warning——`--fail-on errors` 这种复数笔误会**意外启用最严格的模式**。

---


## 配置

Fizzra 会按顺序查找：`./fizzra.yaml` → `~/.fizzra/config.yaml`。

完整带注释的示例见 [`configs/fizzra.example.yaml`](configs/fizzra.example.yaml)，本仓库自己的配置见 [`fizzra.yaml`](fizzra.yaml)。

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
  offline: false      # true = 禁止分析器联网（见「需要联网的分析器」）
  # 单个分析器的设置，可选
  # analyzerOptions:
  #   cargo-audit:
  #     offline: "true"
  #   ruff:
  #     style: "true"        # 详见「分析器检查项开关」
  #     docstrings: "true"

quality:
  minScore: 70
  maxCritical: 0
  maxHigh: 0
  maxComplexity: 20
  minCoverage: 0      # 0 = 关闭覆盖度检查
```

环境变量可覆盖：`FIZZRA_HOST` `FIZZRA_PORT` `FIZZRA_MODE` `FIZZRA_DB_DSN` `FIZZRA_WORKSPACE` `FIZZRA_CONCURRENCY`

### 拼错 key 会报错，值不合法只警告

这是有意的区分：

```
$ ./fizzra.exe --config typo.yaml version
configuration error: config typo.yaml has 2 problem(s):
  - line 4: unknown configuration key "quality.maxCritcal" — did you mean "quality.maxCritical"?
  - line 5: unknown configuration key "quality.minScor" — did you mean "quality.minScore"?
```

**为什么拼错 key 必须报错**：YAML 没有必填字段，`maxCritcal`（少一个字母）会作为一份完全合法的文档加载成功，但什么都没设置 —— 你以为门禁生效了，实际用的是默认值。这种静默失败比启动失败危险得多。

**而值不合法只警告并自动纠正**：

```
WARN msg="configuration adjusted" field=server.port detail="80800 is not a valid port; using 8080"
WARN msg="configuration adjusted" field=quality.minScore detail="150.0 is outside 0-100; using 70.0"
```

程序照常启动，但明确告诉你改了什么。

> 原始需求文档里的 key 名（`coverage`、`criticalIssue`、`highSecurity`、`level`、单数 `pattern`）作为别名保留可用。

---

## 规则与修复建议

内置 24 条规则，零依赖开箱可用。

```bash
./fizzra.exe rules list      # 规则列表（等级、开关状态）
./fizzra.exe rules export    # 导出成 YAML 供编辑
./fizzra.exe rules check     # 校验所有正则能否编译
```

### 修复建议：每条规则都带 before/after 代码

**只报告问题不给修改方向的审查工具是半成品。** 所以每条确定性规则都配了真实的代码示例：

```
command-injection  @ internal/x.go:12
┌─ BEFORE ─────────────────────────────────────────
│ func run(userInput string) error {
│     // The shell interprets whatever the user supplied.
│     return exec.Command("sh", "-c", "convert "+userInput+" out.png").Run()
│ }
├─ AFTER ──────────────────────────────────────────
│ func run(userInput string) error {
│     // Arguments are passed as a list; no shell is involved.
│     return exec.Command("convert", userInput, "out.png").Run()
│ }
└──────────────────────────────────────────────────
  Pass discrete arguments and never route input through sh -c.
```

示例会出现在**看板抽屉、独立 issue 页面、HTML 报告**里，并且**存在 issue 上而非规则上** —— 所以历史问题永远保留它被报出时的建议，即使规则后来改了。

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

也可以在 Web UI 的 **Rules** 页面创建和编辑。

等级：`critical` `error` `warning` `info`

### 规则引擎的两个过滤器

正则匹配源码行本身误报率极高，所以引擎会自动处理：

1. **注释永不匹配** —— 规则不该在描述这个反模式的文档上触发
2. **字符串字面量永不匹配** —— 否则规则会匹配到它自己的模式文本
3. **循环作用域** —— 规则可声明 `requireLoop: true`，只在循环体内报告。这让 `defer` 在循环里能报出真问题，而不会误报循环外的 `defer wg.Done()`

> 早期版本的规则集在这个代码库上误报率是 **100%**：`defer` 在没有循环的地方被报、`crypto/rand` 被当成弱加密、规则匹配到自己的定义。这些案例现在都是 `backend/internal/rule/engine_test.go` 里的回归测试。

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

等级：`A+ ≥95` `A ≥90` `B+ ≥85` `B ≥80` `C ≥70` `D ≥60` `F`

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

门禁失败的三种表现：Web 显示失败 · CLI 返回非 0 · CI 阻断。

门禁还有一道**否决**：如果这次扫描没有任何分析器真正检查过代码（全部跳过或失败），即使各项指标都通过，门禁也**强制失败**。一个什么都没查的扫描报高分，比报错危险得多。

---

## CI 集成

```bash
./fizzra.exe ci . --format sarif --output fizzra.sarif --fail-on critical,error
```

现成模板在 [`deploy/`](deploy/)：

- [`deploy/github-actions.yml`](deploy/github-actions.yml) —— 扫描、上传 SARIF（问题会内联标注在 PR diff 上）、上传 HTML 报告、门禁失败时让 job 失败
- [`deploy/gitlab-ci.yml`](deploy/gitlab-ci.yml) —— 同上，另有一个不阻断的 MR 咨询任务

两个模板都会**先上传报告再让构建失败** —— SARIF 恰恰在门禁失败时最有用。

### 选择要安装的分析器工具

外面只有一个必装工具（Go 那三个），其余 19 个分析器按语言可选。缺工具的后果不是失败，而是**跳过**——扫描会如实报 `INCOMPLETE` 并让门禁失败，不会假通过。所以装哪些只影响检测面，不影响正确性。

GitHub 模板用 `with-analyzers` 控制安装哪些，取值是空格分隔的语言列表：

```yaml
with-analyzers: go                     # 默认：golangci-lint、gosec、govulncheck
with-analyzers: go js python           # 再加 JS/TS 与 Python 的工具
with-analyzers: all                    # 全部语言
```

支持的分组：`go` `js` `python` `rust` `java` `csharp` `cpp` `php` `all`。分组是**整词匹配**，所以 `js` 不会连带触发 `java`。

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

## 开发

### 日常命令

```bash
# 前后端一起开发（API 在 8080，Vite 在 5173，/api 自动代理）
go run ./backend/cmd/fizzra serve &
cd frontend && npm run dev

# 重新构建前端产物（改动前端后需要）
go generate ./backend/internal/web/...

# 只跑后端
go run ./backend/cmd/fizzra serve --verbose

# 测试
go test ./backend/...                # 全部
go test -race ./backend/...          # 含竞态检测（推荐）
go test -coverprofile=cov.out ./backend/... && go tool cover -html=cov.out

# 检查
go vet ./backend/...
gofmt -w backend/
cd frontend && npm run typecheck
cd frontend && npm run check:locales   # 中英词条是否对齐、复数形式是否正确
cd frontend && npm run check:render    # 主题与语言在产物里是否真的生效（需先 build）
cd frontend && npm run check:served    # 二进制内嵌的产物是否完整（需先 serve）
cd frontend && npm run check:theme     # Element Plus 组件的深色适配是否完整

# 用工具检查自己
./fizzra.exe scan ./backend
./fizzra.exe report ./backend --format html --output report.html
```

### 项目结构

```
fizzra/
├── backend/
│   ├── cmd/fizzra/          入口：serve / scan / ci / report / rules
│   └── internal/
│       ├── analyzer/          插件契约、注册表、22 个分析器
│       ├── rule/              规则引擎 + 24 条内置规则（含修复示例）
│       ├── quality/           评分与门禁
│       ├── service/           业务逻辑、分析引擎
│       ├── task/              异步工作池
│       ├── store/             持久化（接口 + SQLite 实现）
│       ├── model/             领域模型
│       ├── api/               HTTP 处理器、路由、中间件
│       ├── report/            JSON / HTML / SARIF 渲染
│       ├── config/            配置加载与校验
│       ├── logger/            结构化日志
│       └── web/               内嵌看板
├── frontend/                  Vue 3 + TypeScript + Element Plus + ECharts + vue-i18n
├── cli/                       CLI 说明文档（CLI 本体在二进制里）
├── configs/                   配置示例
└── deploy/                    CI 流程模板
```

### 更多细节

- [`cli/README.md`](cli/README.md) —— 为什么 CLI 不是独立二进制

### 测试覆盖

> 测试文件不随仓库发布，`go test` 在公开副本里跑不起来。下面记录的是各项测试原本锁定的行为，供改动对应包时对照。

| 包 | 锁定什么行为 |
|---|---|
| `rule` | 内置规则**不会**在注释、字符串字面量、自身定义上误报，同时**仍然**能查出真问题 |
| `quality` | 1 个 critical 比 10 个 warning 扣分更多；分数单调且有界；每个维度都有解释 |
| `analyzer` | 插件注册表、`ContextAware` 分发、去重、Wails 全套检查（正例 + 非正例）、可用性探测 |
| `service` | 端到端：状态、指标、计数、指纹的持久化一致性 |
| `api` | 请求绑定、状态码、响应信封、筛选、分页、CORS |
| `store` | `fix` 列的迁移与往返 |
| `config` | 未知 key 报错、非法值警告并纠正、别名兼容 |
| `task` | panic 隔离、孤儿恢复、背压、优雅关闭、关停时回置任务 |

### 新增一个分析器

只需两步：

1. 在 `backend/internal/analyzer/` 新建文件，实现两个方法：

```go
type Analyzer interface {
    Name() string
    Analyze(ctx context.Context, projectPath string) ([]Issue, error)
}
```

2. 在 `DefaultRegistry()` 里注册一行。

需要宿主服务（规则引擎、文件集、命令执行器）就额外实现可选的 `ContextAware` 接口，简单插件不用关心这些。

### 新增一条内置规则（含中文文案）

1. 在 `backend/internal/rule/builtin.go` 的 `BuiltinRules()` 里加一条，`Name` 用 kebab-case slug，并写好英文的 `Message` / `Advice` / `Fix`。
2. 在 `frontend/src/locales/en.ts` 与 `zh.ts` 的 `rule` 块里，按同一个 slug 各加一个 `{ message, advice, fixNote }`，英文与 Go 侧保持一致。
3. 在 `frontend/scripts/check-locales.mjs` 的 `SLUGS` 数组里补上该 slug——这是防止漏译的守卫。

漏了第 2 步不会报错，界面会**回退显示英文**（这是刻意的：比显示裸 key 好）。漏了第 3 步则守卫不起作用，所以别省。规则名不能改——它同时是指纹的输入与翻译的 key。

### 新增一种界面语言

1. 复制 `frontend/src/locales/en.ts` 为 `locales/<lang>.ts`，翻好全部词条。
2. 在 `locales/index.ts` 的 `LOCALES` 与 `isLocale`、`locales/ruleText.ts` 以及 `App.vue` 的 `languageOptions` 里各加一项。
3. `index.html` 的首屏脚本里加一条语言判断。

`zh.ts` 用 `typeof en` 的形状做类型约束，新语言照做即可，`vue-tsc` 会逐键检查是否漏翻。

---

## 常见问题

**Q：扫描说跳过了某些分析器？**

有三种情况，界面会区分，不要一律当成「缺工具」：

| 提示 | 含义 | 怎么办 |
|---|---|---|
| 检查某个工具是否安装 | 工具不在 PATH 上 | 跑 `./fizzra.exe analyzers`，安装命令会直接列出来 |
| **未完成** | 分析器跑失败了（超时、崩溃、输出无法解析） | 看告警里列出的失败分析器和原因 |
| 没有任何分析器运行 | 所有分析器都被跳过 | 扫描结果不可信，门禁会**失败** |

前两种只影响检测面，属于**正常状态**；第三种意味着这次扫描什么都没检查，分数不能当真——所以门禁不会放行。

**Q：Rust 项目扫出一堆依赖 CVE，但我改不了？**

`cargo-audit` 查的是 `Cargo.lock` 里的依赖漏洞，不是你写的代码。这类问题通常只能靠 `cargo update` 升级依赖解决，而依赖有时被上游钉死。

因此**这些结果默认不计入评分**，但**仍然显示**，并且仍会让 `maxCritical` / `maxHigh` 门禁失败。要恢复计入：

```yaml
scan:
  analyzerOptions:
    cargo-audit:
      scoredTransitive: "true"
```

每条依赖问题都带**来源徽标**（直接依赖 / 传递依赖），鼠标悬停能看到完整依赖链——13 条里有 13 条是间接依赖的情况并不罕见，那说明问题不在你的直接依赖选择上。

**Q：覆盖度一直是 n/a？**

Fizzra **不运行你的测试**。要得到覆盖度，先自己生成：

```bash
go test -coverprofile=coverage.out ./...
```

然后重新扫描。

**Q：分数为什么比预期低？**

打开项目详情的 **Overview** 标签，那里有每个维度的得分、权重和具体原因。

**Q：怎么调低误报？**

三条路：① Rules 页面关闭该规则；② 编辑规则的 `exclude` 缩小作用范围；③ 项目 Settings 里放宽门禁阈值。另外 `fizzra.yaml` 里可以覆盖内置规则的作用范围。

**Q：端口被占用？**

```bash
./fizzra.exe serve --port 9090
```

**Q：`go generate` 报 `EPERM: operation not permitted, unlink`？**

有进程正占着 `node_modules` 里的原生模块（最常见的是 `@rolldown/binding-win32-x64-msvc`），而 `npm ci` 需要先删除整个目录。

先确认是谁占着：

```bash
# Windows：列出 node 进程及其命令行
wmic process where "name='node.exe'" get ProcessId,CommandLine
```

关掉那个 Vite dev server 再重跑就行。**不要去改文件权限、也不要用管理员权限**——文件是被占用，不是没权限，Windows 上两者共用 `EPERM`，所以报错信息会把人带偏。

**Q：想完全重置？**

```bash
rm -rf ~/.fizzra      # Windows: rd /s /q %USERPROFILE%\.fizzra
```

**Q：能扫非 Go 项目吗？**

能扫，而且不止结构层面。非 Go 走的是各生态的真实工具链——`ruff`（Python）、`clippy`（Rust）、`tsc` + `oxlint` + `biome`（JS/TS）——报出来的就是这些工具自己的规则，不是文本匹配出来的近似结果。

检查深度分三档，差别在**工具给了多少信息**，而不是我们做了多少工作：

| 能力 | 覆盖范围 | 说明 |
|---|---|---|
| 定位（文件:行:列） | 全部分析器 | 基础 |
| **问题源码片段** | 全部分析器 | 工具本身不给源码文本时，由 Fizzra 读回该行显示 |
| **修复示例（before/after）** | `ruff`、`clippy` | 仅当工具自身给出机器可读的替换文本时才有 |

所以「只有结构层面」这个说法已经不成立了。真正的边界是**修复示例**：`biome`、`oxlint`、`tsc` 不提供机器可读的替换文本（Biome 只在人类可读的 reporter 里打印 diff，JSON reporter 会返回空的 `advices`），因此这些分析器的发现**只显示问题源码，不显示修复示例**。这里不会去猜一个补丁出来——把工具的散文建议包装成 before/after，等于替工具断言一件它没说的事。

仍然成立的一点：Go/Wails 是这个工具的主场。Go 有内建分析器（`go/ast` 自研检查 + `go vet` + `golangci-lint` + `gosec` + `govulncheck` + 覆盖率 + 自研规则引擎），深度和 Wails 框架感知的检查（绑定面、事件、运行时 API 误用）是别处没有的。其他语言是**接入成熟工具**，Go 是**深度定制**。

**Q：数据存在哪？会不会外传？**

全部在本地 `~/.fizzra/` 的 SQLite 里。除了 `govulncheck` 会查询 Go 官方漏洞数据库（`proxy.golang.org` / `vuln.go.dev`）之外，**没有任何网络请求**。

---

## 设计取舍

有两处刻意偏离了原始需求文档：

**SQLite + 进程内队列，而非 PostgreSQL + Redis + Asynq。**

原文档要求这三件套。但对个人和小团队工具而言，那是两个要安装、备份、监控、保持运行的额外服务，换来的能力这个工作负载并不需要。SQLite 的 WAL 模式完全能承载双 worker 的写入并发，从数据库恢复孤儿任务也提供了单机工具真正需要的持久性保证。

接口是真实的接缝：持久化在 `store.Data` 接口后面，队列在 `Enqueue`/`Running` 后面。换回 PostgreSQL 和 Asynq 各自只需改一个包。

**单一二进制，而非独立 CLI。**

独立 CLI 意味着两个产物要构建、版本管理、安装、保持同步 —— 而这种安排的经典故障就是 CLI 和服务端对 schema 或规则格式的理解不一致。分析器注册表、规则引擎、评分模型、报告渲染器两边都要用，共享严格优于复制。

实际好处：CI 里 `fizzra ci` 完成一次完整扫描和门禁评估，**不需要守护进程、不需要网络、不需要装第二个二进制**。

---

## 许可证

GNU General Public License v3.0。全文见 [`LICENSE`](LICENSE)。

```
Copyright (C) 2026 RoLingG

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU General Public License as published by
the Free Software Foundation, either version 3 of the License, or
(at your option) any later version.

This program is distributed in the hope that it will be useful,
but WITHOUT ANY WARRANTY; without even the implied warranty of
MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.  See the
GNU General Public License for more details.
```

这意味着修改后再分发必须开源。若要在闭源或商业产品中嵌入 Fizzra，
需要单独联系作者取得授权。
