# 常见问题

### Q：扫描说跳过了某些分析器？
有三种情况，界面会区分，不要一律当成「缺工具」：

| 提示 | 含义 | 怎么办 |
|---|---|---|
| 检查某个工具是否安装 | 工具不在 PATH 上 | 跑 `./fizzra.exe analyzers`，安装命令会直接列出来 |
| **未完成** | 分析器跑失败了（超时、崩溃、输出无法解析） | 看告警里列出的失败分析器和原因 |
| 没有任何分析器运行 | 所有分析器都被跳过 | 扫描结果不可信，门禁会**失败** |

前两种只影响检测面，属于**正常状态**；第三种意味着这次扫描什么都没检查，分数不能当真——所以门禁不会放行。

### Q：Rust 项目扫出一堆依赖 CVE，但我改不了？
`cargo-audit` 查的是 `Cargo.lock` 里的依赖漏洞，不是你写的代码。这类问题通常只能靠 `cargo update` 升级依赖解决，而依赖有时被上游钉死。

因此**这些结果默认不计入评分**，但**仍然显示**，并且仍会让 `maxCritical` / `maxHigh` 门禁失败。要恢复计入：

```yaml
scan:
  analyzerOptions:
    cargo-audit:
      scoredTransitive: "true"
```

每条依赖问题都带**来源徽标**（直接依赖 / 传递依赖），鼠标悬停能看到完整依赖链——13 条里有 13 条是间接依赖的情况并不罕见，那说明问题不在你的直接依赖选择上。

### Q：覆盖度一直是 n/a？
Fizzra **不运行你的测试**。要得到覆盖度，先自己生成：

```bash
go test -coverprofile=coverage.out ./...
```

然后重新扫描。

### Q：分数为什么比预期低？
打开项目详情的 **Overview** 标签，那里有每个维度的得分、权重和具体原因。

### Q：怎么调低误报？
三条路：① Rules 页面关闭该规则；② 编辑规则的 `exclude` 缩小作用范围；③ 项目 Settings 里放宽门禁阈值。另外 `fizzra.yaml` 里可以覆盖内置规则的作用范围。

### Q：端口被占用？
```bash
./fizzra.exe serve --port 9090
```

### Q：`go generate` 报 `EPERM: operation not permitted, unlink`？
有进程正占着 `node_modules` 里的原生模块（最常见的是 `@rolldown/binding-win32-x64-msvc`），而 `npm ci` 需要先删除整个目录。

先确认是谁占着：

```bash
# Windows：列出 node 进程及其命令行
wmic process where "name='node.exe'" get ProcessId,CommandLine
```

关掉那个 Vite dev server 再重跑就行。**不要去改文件权限、也不要用管理员权限**——文件是被占用，不是没权限，Windows 上两者共用 `EPERM`，所以报错信息会把人带偏。

### Q：想完全重置？
```bash
rm -rf ~/.fizzra      # Windows: rd /s /q %USERPROFILE%\.fizzra
```

### Q：能扫非 Go 项目吗？
能扫，而且不止结构层面。非 Go 走的是各生态的真实工具链——`ruff`（Python）、`clippy`（Rust）、`tsc` + `oxlint` + `biome`（JS/TS）——报出来的就是这些工具自己的规则，不是文本匹配出来的近似结果。

检查深度分三档，差别在**工具给了多少信息**，而不是我们做了多少工作：

| 能力 | 覆盖范围 | 说明 |
|---|---|---|
| 定位（文件:行:列） | 全部分析器 | 基础 |
| **问题源码片段** | 全部分析器 | 工具本身不给源码文本时，由 Fizzra 读回该行显示 |
| **修复示例（before/after）** | `ruff`、`clippy` | 仅当工具自身给出机器可读的替换文本时才有 |

所以「只有结构层面」这个说法已经不成立了。真正的边界是**修复示例**：`biome`、`oxlint`、`tsc` 不提供机器可读的替换文本（Biome 只在人类可读的 reporter 里打印 diff，JSON reporter 会返回空的 `advices`），因此这些分析器的发现**只显示问题源码，不显示修复示例**。这里不会去猜一个补丁出来——把工具的散文建议包装成 before/after，等于替工具断言一件它没说的事。

仍然成立的一点：Go/Wails 是这个工具的主场。Go 有内建分析器（`go/ast` 自研检查 + `go vet` + `golangci-lint` + `gosec` + `govulncheck` + 覆盖率 + 自研规则引擎），深度和 Wails 框架感知的检查（绑定面、事件、运行时 API 误用）是别处没有的。其他语言是**接入成熟工具**，Go 是**深度定制**。

### Q：数据存在哪？会不会外传？
全部在本地 `~/.fizzra/` 的 SQLite 里。除了 `govulncheck` 会查询 Go 官方漏洞数据库（`proxy.golang.org` / `vuln.go.dev`）之外，**没有任何网络请求**。

### Q：配置写错了会怎样？

键名拼错会被拒绝并提示最接近的正确写法，启动失败：

```
$ fizzra --config typo.yaml version
configuration error: config typo.yaml has 2 problem(s):
  - line 4: unknown configuration key "quality.maxCritcal" — did you mean "quality.maxCritical"?
  - line 5: unknown configuration key "quality.minScor" — did you mean "quality.minScore"?
```

YAML 没有必填字段，`maxCritcal` 少一个字母会作为完全合法的文档加载成功，但什么都没设置——门禁看起来生效了，实际用的是默认值。静默失败比启动失败危险，所以这里是硬错误。

值不合法则只警告并自动纠正，程序照常启动：

```
WARN msg="configuration adjusted" field=server.port detail="80800 is not a valid port; using 8080"
WARN msg="configuration adjusted" field=quality.minScore detail="150.0 is outside 0-100; using 70.0"
```

旧版键名（`coverage`、`criticalIssue`、`highSecurity`、`level`、单数 `pattern`）作为别名仍然可用。


### Q：扫描陌生来源的仓库安全吗？

不完全安全。部分分析器会构建、甚至执行被扫描项目的代码，最典型的是 `clippy`——`cargo clippy` 会完整构建 crate，`build.rs` 和 proc-macro 都在这个阶段运行。

`golangci-lint` 和 `go-vet` 在类型检查时也可能触发构建。其余分析器（`ruff`、`oxlint`、`biome`、`tsc`、`pmd`、`spotbugs`、`phpstan`、`cargo-audit`、`rustfmt`）只读源码或已编译产物，不执行代码。

扫描自己和团队的代码没有问题，这些工具本来就是开发者日常在用的。需要隔离时：

- 在容器里跑 `fizzra`，不要在有凭据的宿主机上直接扫。
- 或用 `--analyzers` 只启用不执行代码的分析器：

```bash
fizzra scan ./untrusted --analyzers custom,metrics,ruff,oxlint
```

`--offline` 只禁止联网，不阻止代码执行，两者不能互相替代。完整对照表见 README 的「安全须知」。
