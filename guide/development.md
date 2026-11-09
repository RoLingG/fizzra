# 开发与测试


## 一键验证

> 测试文件不随本仓库发布，下面这些命令只在完整源码树里可用。

```bash
go test -race ./backend/...
```

`-race` 不能省，数据竞态就是靠它发现的。九个包全绿说明后端正常。

## 分层测试

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

## 静态检查

```bash
go vet ./backend/...
gofmt -l backend/        # 有输出说明哪些文件需要格式化
```

## 自扫描 —— 最有说服力的一项

```bash
go build -o fizzra.exe ./backend/cmd/fizzra
./fizzra.exe ci ./backend
```

用工具检查它自己的代码。**预期：gate 通过，退出码 0。**

> 分数随代码增长会缓慢变化，不要把它当成固定值去对——它是这个仓库当前状态的快照，不是断言。重要的是 `gate passed` 与退出码 0。
>
> **必须在仓库根目录运行。** 配置查找用的是相对路径 `./fizzra.yaml`，在 `backend/` 里执行会找不到配置、退回内置默认值，于是 `fizzra.yaml` 里那些针对本仓库的豁免（比如 `no-panic` 对 `internal/analyzer/analyzer.go` 的排除）全部失效，结果会显示 gate failed。这不是代码问题，是运行位置问题——`ci` 的目标参数是 `./backend`，不是 `cd backend`。

规则有误报的话这里会立刻暴露——事实上确实暴露过：加入代码示例功能后，示例里的「反面代码」被自己的规则误报成 2 个 critical，因此实现了示例抑制逻辑。

## 手动验收清单

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

## 验证「假通过已被堵住」

这是多语言支持要解决的核心问题，也是最危险的一类故障。

```bash
# 一个只有 Rust 清单、没有任何分析器能处理的目录
./fizzra.exe scan ./some-rust-dir
```

**预期**：报 `INCOMPLETE`，列出跳过的分析器及原因，门禁**失败**。

**对照**：修复前它报 `94.4 / A / 门禁通过`，而实际上一个文件都没检查过。

同理，如果某个项目只配了 lint 分析器但工具没装（例如 clippy 的 cargo 不在 PATH 上），扫描也应失败而不是通过 —— 因为度量分析器（metrics）会跑，但它找不到任何 bug。

## 验证「不完整扫描不会假通过」

这是本项目修过的最严重的缺陷：9 个分析器里 8 个超时失败，却报告 `gate=passed` 且退出码 0。在 CI 里等于门禁失效——而且因为问题数变少，分数反而比真实扫描**更高**。

```bash
./fizzra.exe ci ./backend --timeout 1ns      # 强制超时，模拟分析器大面积失败
```

**预期**：输出以 `!! INCOMPLETE` 开头，列出失败的分析器，`gate failed`，**退出码 1**。

对照正常扫描（退出码 0）：

```bash
./fizzra.exe ci ./backend
```

## 验证「参数拼写错误会报错」

```bash
./fizzra.exe ci ./backend --fail-on zzz       # 报错：unknown severity "zzz"
./fizzra.exe ci ./backend --fail-on errors    # 接受（复数可以识别）
```

此前任何无法识别的值都会被静默当成 warning——`--fail-on errors` 这种复数笔误会**意外启用最严格的模式**。

---




## 日常命令

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

## 项目结构

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

## 更多细节

- [`cli/README.md`](../cli/README.md) —— 为什么 CLI 不是独立二进制

## 测试覆盖

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

## 新增一个分析器

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

## 新增一条内置规则（含中文文案）

1. 在 `backend/internal/rule/builtin.go` 的 `BuiltinRules()` 里加一条，`Name` 用 kebab-case slug，并写好英文的 `Message` / `Advice` / `Fix`。
2. 在 `frontend/src/locales/en.ts` 与 `zh.ts` 的 `rule` 块里，按同一个 slug 各加一个 `{ message, advice, fixNote }`，英文与 Go 侧保持一致。
3. 在 `frontend/scripts/check-locales.mjs` 的 `SLUGS` 数组里补上该 slug——这是防止漏译的守卫。

漏了第 2 步不会报错，界面会**回退显示英文**（这是刻意的：比显示裸 key 好）。漏了第 3 步则守卫不起作用，所以别省。规则名不能改——它同时是指纹的输入与翻译的 key。

## 新增一种界面语言

1. 复制 `frontend/src/locales/en.ts` 为 `locales/<lang>.ts`，翻好全部词条。
2. 在 `locales/index.ts` 的 `LOCALES` 与 `isLocale`、`locales/ruleText.ts` 以及 `App.vue` 的 `languageOptions` 里各加一项。
3. `index.html` 的首屏脚本里加一条语言判断。

`zh.ts` 用 `typeof en` 的形状做类型约束，新语言照做即可，`vue-tsc` 会逐键检查是否漏翻。

---

## 前端热更新开发

前后端分离启动，前端改动即时生效：

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
