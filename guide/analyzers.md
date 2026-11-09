# 分析器与外部工具

Fizzra 内置 22 个分析器，其中 19 个依赖外部工具，其余 3 个（`custom`、`metrics`、`wails`）是内置实现。**缺失的工具会被跳过，不会导致扫描失败** —— 但装上它们能显著提升检测能力。

查看当前状态：

```bash
fizzra analyzers
```

`STATUS` 为 `ready` 表示所需工具都在 PATH 上，`missing` 表示没有。`REQUIRES` 是它需要的二进制，`—` 表示无需外部工具。

```
  ANALYZER       CATEGORY       STATUS   REQUIRES
  ──────────────────────────────────────────────────────
  biome          lint           ready    biome
  cargo-audit    vulnerability  ready    cargo-audit
  clippy         lint           ready    cargo
  clang-tidy     bugrisk        missing  clang-tidy
  cppcheck       bugrisk        ready    cppcheck
  custom         custom         ready    —
  go-vet         lint           ready    go
  gofmt          lint           ready    gofmt
  golangci       lint           ready    golangci-lint
  gosec          security       ready    gosec
  govulncheck    vulnerability  ready    govulncheck
  metrics        metrics        ready    —
  oxlint         lint           ready    oxlint
  phpstan        bugrisk        missing  phpstan
  pmd            lint           missing  pmd
  roslynator     lint           ready    roslynator
  ruff           lint           ready    ruff
  rustfmt        lint           ready    cargo
  spotbugs       security       missing  spotbugs
  staticcheck    lint           ready    staticcheck
  tsc            correctness    ready    npx
  wails          wails          ready    —
```

列表之后逐个打印分析器说明，最后汇总缺失的工具及安装方法。

安装缺失的工具：

```bash
# Go（go vet 和 gofmt 随工具链提供，装好 Go 并加入 PATH 即可）
go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@latest
go install honnef.co/go/tools/cmd/staticcheck@latest
go install github.com/securego/gosec/v2/cmd/gosec@latest
go install golang.org/x/vuln/cmd/govulncheck@latest

# Python
pip install ruff

# JavaScript / TypeScript（装进项目，不是全局）
npm install --save-dev typescript vue-tsc oxlint @biomejs/biome

# Rust（clippy 和 rustfmt 随工具链，用 rustup 添加）
rustup component add clippy
rustup component add rustfmt
cargo install cargo-audit --locked

# Java（PMD 和 SpotBugs 是解压即用的发行包）
# 下载后把 bin/ 加入 PATH，或设 GUARDIAN_PMD_HOME / GUARDIAN_SPOTBUGS_HOME

# C#（.NET 全局工具）
dotnet tool install --global Roslynator.DotNet.Cli

# C / C++
apt install cppcheck clang-tidy        # Debian / Ubuntu
# clang-tidy 也可 pip install clang-tidy

# PHP
# 下载 phpstan.phar 放入 PATH，需要 PHP 7.4+ 解释器
```

> **JS/TS 的工具装在项目里，不是全局。** `tsc` 分析器用的是**被扫描项目自己**的 `node_modules` 里的编译器（通过 `npx --no-install` 解析），不是 PATH 上的全局副本，否则检查的会是另一个 TypeScript 版本。Biome 的 npm 包名是 **`@biomejs/biome`**：npm 上不带作用域的 `biome` 是另一个已废弃的 0.3.x 包，装错了分析器会一直报 missing。`./fizzra.exe analyzers` 里的安装提示会带上这些区别。

装完后**重启 Fizzra**（可用性在启动时探测一次），`./fizzra.exe analyzers` 就会显示 `ready`。

> **`pip install` 装的 ruff 常常不在 PATH 上**（Windows 上会落到 `%APPDATA%\Python\Python3xx\Scripts`）。Fizzra 会回退到 `python -m ruff`，所以 `analyzers` 仍会显示 `ready`，扫描也能正常工作。

### 需要联网的分析器

`cargo-audit` 和 `govulncheck` 要拉取漏洞数据库。**网络不通时它们不是快速失败，而是会一直等到连接超时**， 一个占用 2 秒的扫描会变成一分钟，然后什么都没报。

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

> `govulncheck` 之所以选择跳过而不是降级，是因为它没有本地数据库模式，而对着一份过时的数据库报 Go 漏洞会更危险。另外跳过也不是静默的，它会计入跳过原因，在扫描结果和 Web 界面上都能看到。

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

多出的 227 个是格式问题，映射为 `info` 等级，但**不会影响质量门禁**。默认结论仍然只关于缺陷。

也可以在配置文件里设：

```yaml
scan:
  analyzerOptions:
    ruff:
      style: "true"
      docstrings: "true"
```

**JS/TS 的两个开关**（同样默认值如下）：

```yaml
scan:
  analyzerOptions:
    oxlint:
      vuePlugin: "true"    # 默认开。见下
    biome:
      css: "true"          # 默认开
```

> **`vuePlugin` 默认开，与 ruff 那些默认关的开关相反**，因为两者性质不同：ruff 的组开启的是"团队规范"类检查（docstring、版权头），会淹没问题列表；`vuePlugin` 加的是 oxlint 自己的 31 条 Vue 规则，不加噪声。
>
> 实测（`--print-config`）：不开 111 条规则，开了 142 条，多出的 31 条全部是 `vue/*`。
>
> 实测一个"computed 里有副作用"的组件：不开报 0 条，开了报 `vue(no-side-effects-in-computed-properties)`。
>
> 关掉它就等于放弃这 31 条检查——`.vue` 的 `<script>` 块两种情况下都会检查，差别只在模板相关的规则。

> **`css` 只覆盖 `.css`。** Biome 不读 `.scss` / `.less` / `.sass`——实测同时喂三个文件给它，只产出一条诊断，且指向 `.css` 那个。所以这个开关不会假装覆盖 Sass/Less；如果用户样式是预处理器写的，Biome 这条路是空的，需要另配 stylelint（见下）。

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

> **检查深度不止是「报了个问题」。** 每条问题都会显示**所在行的源码**——工具本身不在 JSON 里给源码文本的（`ruff`/`biome`/`oxlint`/`clippy` 都不给），由 Fizzra 读回该行。**修复示例（before/after）**则只在工具自己提供了机器可读的替换文本时才有：目前是 `ruff` 和 `clippy`。`biome`/`oxlint`/`tsc` 不提供，所以它们只显示源码、不显示修复示例——这里不会替工具猜一个补丁出来。详见 [常见问题](faq.md#q能扫非-go-项目吗)。

> **自定义规则适用于所有语言。** 规则引擎会按项目语言选择合适的注释符、字符串形式与作用域模型——Python 的 `#` 注释、三引号 docstring、缩进块，Rust 的嵌套块注释、生命周期，JavaScript 的模板字符串，PHP 的 `#` 注释与反引号，都能正确识别。所以一条 `paths: ['**/*.py']` 的规则在 Python 项目上会生效，且不会误报在注释或 docstring 里。

### 样式检查的边界

**CSS 有检测，Sass/Less 没有。** Biome 能读 `.css`，读不了预处理器方言，Biome 没有那些解析器。当前覆盖：

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

