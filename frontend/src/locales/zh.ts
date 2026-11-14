import type en from './en'

// 只约束键、放开值，直接标 typeof en 会要求值相等
type Catalogue<T> = {
  [K in keyof T]: T[K] extends string ? string : Catalogue<T[K]>
}

// 按 en.ts 的形状标注，漏翻的键会在 typecheck 报错
const zh: Catalogue<typeof en> = {
  nav: {
    dashboard: '总览',
    projects: '项目',
    rules: '规则',
    system: '系统',
  },

  app: {
    brandSub: '代码质量审查',
    sections: '导航',
    analyzersUnavailable: '没有不可用的分析器 | {count} 个分析器不可用',
    tasksRunning: '没有运行中的任务 | {count} 个任务运行中',
    footerCounts: '{projects} 个项目 · {rules} 条规则',
  },

  common: {
    cancel: '取消',
    save: '保存',
    delete: '删除',
    close: '关闭',
    copy: '复制',
    copied: '已复制',
    refresh: '刷新',
    back: '返回',
    edit: '编辑',
    scan: '扫描',
    reset: '重置',
    never: '从未',
    justNow: '刚刚',
    notAvailable: '不适用',
    none: '—',
    all: '全部',
    projectWide: '整个项目',
    loading: '加载中…',
  },

  theme: {
    label: '主题',
    light: '浅色',
    dark: '深色',
    auto: '跟随系统',
  },

  language: {
    label: '语言',
    en: 'English',
    zh: '中文',
  },

  severity: {
    critical: '严重',
    error: '错误',
    warning: '警告',
    info: '提示',
  },

  status: {
    open: '待处理',
    ignored: '已忽略',
    falsePositive: '误报',
    pending: '等待中',
    running: '运行中',
    success: '成功',
    failed: '失败',
    canceled: '已取消',
  },

  gate: {
    passed: '通过',
    failed: '未通过',
    skipped: '已跳过',
    violation: {
      minScore: '分数低于下限',
      maxCritical: '严重问题过多',
      maxHigh: '高危问题过多',
      maxComplexity: '复杂度超出上限',
      minCoverage: '覆盖率低于下限',
      default: '质量门禁条件未满足',
    },
  },

  quality: {
    security: '安全性',
    bugs: '缺陷风险',
    maintainability: '可维护性',
    complexity: '复杂度',
    coverage: '覆盖率',
    notMeasured: '未测量',
  },

  analyzer: {
    custom: {
      name: '内置规则',
      summary: '内置规则集，含 Go/Wails 专项检查',
      description:
        '运行 Fizzra 自带的规则集：正确性、安全卫生，以及定义在 fizzra.yaml 或 Web 界面中的 Go/Wails 专项模式。',
    },
    wails: {
      name: 'Wails',
      summary: 'Wails 项目结构与绑定检查',
      description:
        '检查 Wails 项目结构、运行时 API 用法（EventsEmit、Window、Dialog、BrowserOpenURL）以及绑定接口设计。',
    },
    metrics: {
      name: '度量',
      summary: '代码规模、复杂度与重复度度量',
      description:
        '统计代码规模、圈复杂度、函数长度、参数个数与重复度，并产出质量评分所依赖的各项度量。',
    },
    gofmt: {
      name: 'gofmt',
      summary: 'Go 代码格式检查',
      description: '报告未经 gofmt 格式化的 Go 文件。使用本地 Go 工具链。',
    },
    'go-vet': {
      name: 'go vet',
      summary: 'Go 可疑写法检查（Printf 占位符、cancel 函数、不可达代码）',
      description:
        '运行 go vet：找出编译器能通过但可疑的写法，例如错误的 Printf 占位符、丢失的 cancel 函数与不可达代码。',
    },
    golangci: {
      name: 'golangci-lint',
      summary: 'Go 综合静态检查（未使用代码、错误处理、风格）',
      description:
        '运行 golangci-lint：一次执行即可覆盖未使用代码、错误处理、风格，以及大量静态分析检查。',
    },
    staticcheck: {
      name: 'staticcheck',
      summary: 'Go 深度静态分析，排查缺陷与可疑写法',
      description: '独立运行 staticcheck：针对缺陷、可简化写法与可疑代码的深度静态分析。',
    },
    gosec: {
      name: 'gosec',
      summary: 'Go 安全扫描（注入、加密、硬编码凭据、文件权限）',
      description:
        '运行 gosec：检查 SQL/命令注入、不安全的加密用法、硬编码凭据、过于宽松的文件权限，以及其他与 OWASP 相关的 Go 写法。',
    },
    govulncheck: {
      name: 'govulncheck',
      summary: 'Go 依赖漏洞检查（对照官方漏洞库）',
      description:
        '运行 govulncheck，比对 Go 官方漏洞库，找出模块依赖中可被实际触达的 CVE。',
      option: {
        offline: {
          description:
            '跳过本次检查，因为 govulncheck 必须联网访问 Go 漏洞库。它没有可用的本地数据库模式，所以离线时是跳过而不是降级——对着一份过时的数据库报 Go 漏洞，比不报更危险。',
        },
      },
    },
    ruff: {
      name: 'ruff',
      summary: 'Python 代码检查与安全规则',
      description:
        '运行 ruff：Python 的正确性、导入、风格、复杂度，以及来自 bandit 的安全规则族。',
      // 与后端 ruffRuleGroups 的 Key 一致
      option: {
        style: {
          description:
            '行长度、空白、空行等 pycodestyle 格式问题。这属于格式化工具的职责；默认关闭，因为它在实际项目里会淹没问题列表。',
        },
        docstrings: {
          description:
            '缺失或格式错误的文档字符串（pydocstyle）。这是文档规范而非代码缺陷，也是实际项目中最主要的噪音来源。',
        },
        annotations: {
          description:
            '缺失类型标注（flake8-annotations）。对新代码做增量约束很有用；对历史项目会很吵。',
        },
        copyright: {
          description: '文件头缺少版权声明。这是组织规范，不是代码质量的信号。',
        },
        naming: {
          description: 'PEP 8 命名规范（pep8-naming）。',
        },
        pandas: {
          description: 'Pandas 专用反模式（pandas-vet）。数据科学类代码建议开启。',
        },
        quotes: {
          description: '引号风格一致性（flake8-quotes）。',
        },
        todo: {
          description: 'TODO 注释的格式规范（flake8-todos）。',
        },
      },
    },
    oxlint: {
      name: 'oxlint',
      summary: 'JS/TS 代码检查（含 React、可访问性、导入）',
      description:
        '运行 oxlint：JavaScript 与 TypeScript 的正确性、可疑写法、TypeScript、React、可访问性与导入检查。',
      option: {
        vuePlugin: {
          description:
            '启用 oxlint 的 Vue 插件（--vue-plugin），覆盖 .vue 模板相关规则。默认开启：它加的是 oxlint 自带的 31 条 Vue 规则（实测规则总数 111 → 142），不增加噪声。关闭它就等于放弃这 31 条检查；.vue 的 <script> 块两种情况下都会检查。',
        },
      },
    },
    tsc: {
      name: 'tsc',
      summary: 'TypeScript 类型检查',
      description:
        '以仅检查模式运行项目自带的 TypeScript 编译器：类型错误、不可能的值、不安全的 null 处理与契约违反。需要 tsconfig.json。',
    },
    biome: {
      name: 'biome',
      summary: 'JS/TS 的 lint 与格式检查',
      description:
        '运行 Biome 的 linter：JavaScript、TypeScript、JSX/TSX、JSON 与 CSS 的正确性、可疑写法与风格。它启用了 oxlint 默认规则集未启用的几条错误规则。',
      option: {
        css: {
          description:
            '同时检查 .css：属性名拼写错误、重复声明、空规则块、!important。注意 Biome 读不了 .scss 和 .less——不是配置问题，是它没有那些解析器。',
        },
      },
    },
    clippy: {
      name: 'clippy',
      summary: 'Rust 代码检查（正确性、性能、惯用法）',
      description:
        '运行 cargo clippy：来自 Rust 官方工具链的正确性、可疑写法、性能与惯用写法检查。',
    },
    pmd: {
      name: 'PMD',
      summary: 'Java 代码检查（错误倾向、性能、并发、设计、安全）',
      description:
        '运行 PMD：错误倾向、最佳实践、性能、并发、设计与安全六个规则类别，共三百余条规则。默认只查其中六类——风格类会把真实缺陷淹掉。',
    },
    spotbugs: {
      name: 'SpotBugs',
      summary: 'Java 字节码缺陷检查（硬编码凭据、SQL 注入、资源泄漏、空指针）',
      description:
        '对编译后的字节码运行 SpotBugs：硬编码凭据、由用户输入拼接的 SQL、未关闭的资源、确定的空指针解引用。需要先构建项目。',
    },
    rustfmt: {
      name: 'rustfmt',
      summary: 'Rust 代码格式检查',
      description: '报告未按 rustfmt 格式化的 Rust 文件。使用本机的 Rust 工具链。',
    },
    cppcheck: {
      name: 'cppcheck',
      summary: 'C/C++ 缺陷检查（内存泄漏、缓冲区越界、悬垂指针）',
      description:
        '运行 cppcheck：内存泄漏、缓冲区越界、返回栈地址等缺陷。不需要构建，也不需要编译数据库。',
    },
    'clang-tidy': {
      name: 'clang-tidy',
      summary: 'C/C++ 深度检查（命令注入、悬垂指针、CERT C 规则）',
      description:
        '运行 clang-tidy：命令注入、悬垂指针、内存泄漏与 CERT C 规则。需要机器上有 Python 和 C/C++ 标准库。',
    },
    roslynator: {
      name: 'Roslynator',
      summary: 'C# 代码检查（编译器诊断、.NET 内置分析器、代码质量规则）',
      description:
        '对 .csproj 运行 Roslynator：编译器诊断、.NET 内置分析器与代码质量规则。不需要往项目里加任何包，也不会改动工程文件。',
    },
    phpstan: {
      name: 'PHPStan',
      summary: 'PHP 类型检查（未定义方法、传参类型不符、不可达分支）',
      description:
        '运行 PHPStan：未定义的方法与属性、传参类型不符、不可达分支等类型错误。需要机器上已有 PHP 解释器。',
    },
    'cargo-audit': {
      name: 'cargo-audit',
      summary: 'Rust 依赖漏洞检查（对照 RustSec 漏洞库）',
      description:
        '运行 cargo audit：比对 Cargo.lock 中锁定的依赖与 RustSec 漏洞库（CVE/RUSTSEC 编号）。它检查的是依赖包，不是本项目代码。',
      option: {
        offline: {
          description:
            '以 --no-fetch 运行 cargo-audit，使用本地已有的漏洞库快照。与 govulncheck 不同，它仍然会工作——依据一份可能过时的数据库报问题，而不是跳过。全局的 `offline` 设置对所有分析器效果相同。',
        },
        scoredTransitive: {
          description:
            '把传递依赖中的漏洞计入质量评分。默认关闭：项目无法直接升级这些包，它们的告警反映的是上游的发版节奏，而不是本项目代码的质量。这类问题始终会被报告，此开关只影响是否计入评分。',
        },
      },
    },
  },

  progress: {
    starting: '开始…',
    preparingSources: '准备源码…',
    indexingFiles: '索引文件…',
    savingResults: '保存结果…',
    scoring: '评分…',
    done: '完成',
  },

  errors: {
    network: '请求失败。',
    timeout: '请求超时。大项目扫描较慢，请重试或查看服务端日志。',
    generic: '出错了。',
    notFound: '未找到。',
    conflict: '该内容已存在。',
    invalidInput: '输入不合法。',
    internalError: '服务端内部错误。',
    queueFull: '扫描队列已满，请稍后重试。',
    exportReport: '报告导出失败。',
    loadSystem: '无法加载服务端信息。',
    loadProjects: '无法加载项目列表。',
    loadProject: '无法加载该项目。',
    loadIssues: '无法加载问题列表。',
    loadIssue: '无法加载该问题。',
    loadRules: '无法加载规则列表。',
    saveProject: '保存项目失败。',
    deleteProject: '删除项目失败。',
    saveRule: '保存规则失败。',
    deleteRule: '删除规则失败。',
    toggleRule: '切换规则状态失败。',
    saveGate: '保存质量门禁失败。',
    updateIssue: '更新问题失败。',
    startScan: '启动扫描失败。',
    copyFailed: '请手动选中文本后复制。',
  },

  issue: {
    searchPlaceholder: '按描述、文件或规则筛选…',
    severityPlaceholder: '等级',
    analyzerPlaceholder: '分析器',
    statusPlaceholder: '状态',
    newOnly: '仅新增',
    sortBySeverity: '按等级',
    sortByFile: '按文件',
    sortByAnalyzer: '按分析器',
    sortNewest: '最新优先',
    summary: '没有符合当前筛选条件的问题 | {count} 条问题符合当前筛选条件',
    empty: '没有符合当前筛选条件的问题。',
    colSeverity: '等级',
    colAnalyzer: '分析器',
    colRule: '规则',
    colMessage: '描述',
    colLocation: '位置',
    colActions: '操作',
    badgeNew: '新增',
    badgeNewHint: '上一次成功扫描中不存在',
    badgeFix: '含修复建议',
    badgeFixHint: '有对应的修改前后代码对比',

    origin_direct: '直接依赖',
    origin_transitive: '传递依赖',
    originHint_direct: '这是项目在 Cargo.toml 中直接声明的依赖，可以直接升级它。',
    originTransitiveHint: '项目并未直接依赖此包，它由 {path} 引入。直接升级它无法生效，需要升级链路上游的那个依赖。',
    dependencyChain: '依赖链路',
    dependencyNoteTitle: '本次扫描有 {count} 个问题来自第三方依赖',
    dependencyNoteBody:
      '这些不是本项目代码中的问题，而是 Cargo.lock 里锁定的依赖包命中了 RustSec 漏洞库。项目并未直接依赖它们，而是通过其他依赖间接引入。',
    dependencyNoteUnscored:
      '其中 {count} 个是传递依赖，已从质量评分中排除——它们的修复权不在本项目手中，计入评分会让分数随上游发版而波动。可在系统页的 cargo-audit 设置中改为计入。',

    whyThisMatters: '为什么重要',
    location: '位置',
    source: '源码',
    details: '详情',
    toolOutput: '分析器输出',
    detected: '发现时间',
    statusLabel: '状态',
    fingerprint: '指纹',
    fingerprintNote: '指纹在多次扫描间保持稳定，因此即使行号变动，同一个问题仍能被识别。',
    ruleDocs: '规则文档 ↗',
    openIssuePage: '打开问题详情页',
    issueActions: '问题操作',
    ignore: '忽略',
    falsePositive: '标记误报',
    reopen: '重新打开',
    ignoreConfirmTitle: '忽略这个问题？',
    ignoreConfirmBody: '该问题将从默认视图中隐藏，但仍保留在数据库中以备审计。',
    copiedLabel: '已复制{label}',
    markedAs: '已标记为{status}。',
    reopened: '问题已重新打开。',
    copyLocation: '位置',
    copyFingerprint: '指纹',
  },

  dashboard: {
    emptyTitle: '还没有项目',
    emptyBody: '注册一个仓库即可开始审查。也可以用命令行自动添加：',
    addProject: '添加项目',
    overallQuality: '整体质量',
    headlineGood: '整体状况良好',
    headlineFair: '整体尚可，仍有提升空间',
    headlinePoor: '整体需要关注',
    headlineNone: '还没有可评价的数据',
    sideGateTitle: '门禁通过',
    sideLowest: '得分最低',
    sideAll: '全部',
    gradeLabel: '评级',
    scoreUnavailable: '评分已不可用',
    grade: {
      a: '优秀',
      b: '良好',
      c: '一般',
      d: '及格',
      e: '较差',
      f: '危险',
    },
    noAnalyses: '还没有完成的扫描。运行一次扫描即可获得评分。',
    heroSummary: '{count} 个已分析项目的平均分。{passing} / {count} 通过质量门禁。',
    gaugeWorst: '最差',
    gaugeNow: '当前',
    gaugeLine: '门禁线',
    gaugeBest: '最好',
    bandRuns: '累计扫描',
    bandPassed: '门禁通过',
    bandFailed: '门禁失败',
    affectedProjects: '涉及项目',
    criticalAlertTitle: '严重问题需要处理',
    security: {
      highLabel: '高风险',
      highHeadline: '{count} 个严重',
      highDetail: '有 {count} 个严重问题和 {errors} 个高危问题需要在下次发布前处理。',
      elevatedLabel: '偏高',
      elevatedHeadline: '{count} 个高危',
      elevatedDetail: '没有严重问题，但报告了 {count} 个高危问题。',
      unknownLabel: '未知',
      unknownHeadline: '尚未扫描',
      unknownDetail: '运行一次扫描以获得安全评估。',
      lowLabel: '低风险',
      lowHeadline: '无严重问题',
      lowDetail: '最近的分析中没有严重或高危问题。',
    },
    coverageNotAvailable: '不适用',
    coverageHint: '没有覆盖率文件 —— 请先生成 profile',
    coverageMeasured: '{total} 个项目中有 {measured} 个已测量',
    criticalSummary: '{count} 个严重问题，分布于：{projects}。',
    statOpenIssues: '待处理问题',
    statCritical: '严重',
    statHigh: '高危',
    statProjects: '项目',
    statCoverage: '覆盖率',
    statAcross: '共 {count} 个项目',
    statRequiresAction: '需要处理',
    statErrorLevel: '错误等级',
    statAnalysed: '已分析 {count} 个',
    sectionIssueMix: '问题构成',
    sectionIssueMixSub: '各项目最近一次分析的合并结果。',
    sectionScoreHistory: '评分趋势',
    sectionScoreHistorySub: '每次分析的平均分，由旧到新。',
    sectionNeedsAttention: '需要关注',
    sectionNeedsAttentionSub: '分数最低的项目排在前面。',
    sectionRecent: '最近活动',
    sectionRecentSub: '最近完成的几次分析。',
    issuesCount: '{count} 条问题',
  },

  projectsView: {
    intro: '项目指向一个本地目录或 Git 仓库。重复注册同一目录会被自动识别。',
    addProject: '添加项目',
    empty: '还没有注册任何项目。',
    colName: '名称',
    // 这里显示语言，kind 只有 wails 一个值
    colLanguage: '语言',
    // 缩写只用于这列，表单下拉框有宽度放全称
    languageShort_javascript: 'JS / TS',
    colSource: '来源',
    colScore: '评分',
    colGate: '门禁',
    colIssues: '问题',
    colLastScan: '上次扫描',
    scanning: '扫描中',
    scanFailed: '扫描失败',
    scanQueued: '已为 {name} 排队扫描。',
    editTitle: '编辑项目',
    addTitle: '添加项目',
    formName: '名称',
    formDescription: '描述',
    formLocalPath: '本地路径',
    formRepoUrl: '仓库地址',
    formSource: '代码来源',
    sourceLocal: '本地目录',
    sourceRepo: 'Git 仓库',
    formBranch: '分支',
    formBranchOptional: '分支（选填）',
    formKind: '框架',
    formLanguage: '语言',
    languageHint: '决定运行哪些分析器。尚未支持的语言会报告「没有执行任何检查」，而不是直接通过。',
    language_go: 'Go',
    language_rust: 'Rust',
    language_python: 'Python',
    language_javascript: 'JavaScript / TypeScript',
    language_java: 'Java',
    language_csharp: 'C#',
    language_cpp: 'C / C++',
    language_php: 'PHP',
    formAnalyzers: '分析器',
    formAccent: '强调色',
    phName: 'my-wails-app',
    phOptional: '选填',
    phLocalPath: 'D:\\code\\my-wails-app 或 /home/me/my-app',
    phRepoUrl: 'https://github.com/you/my-wails-app.git',
    phBranch: 'main',
    phBranchOptional: '留空则使用仓库默认分支',
    phAnalyzers: '全部可用项',
    hintLocalPath: '就地扫描。若改用仓库地址，此项留空。',
    hintRepoUrl: '首次扫描时克隆到工作区，之后扫描会拉取更新。',
    hintAnalyzers: '留空表示运行所有可用的分析器。',
    kindGo: 'Go',
    kindWails: 'Wails',
    wailsNote: 'Wails 项目会额外运行运行时 API 与绑定检查。',
    needsTools: '需要 {tools}',
    updated: '项目已更新。',
    registered: '项目已注册。',
    deleted: '项目已删除。',
    deleteTitle: '删除这个项目？',
    deleteBody: '将删除「{name}」及其分析历史与问题记录。仓库本身不受影响。',
    selectedCount: '已选 {count} 个项目',
    clearSelection: '取消选择',
    deleteSelected: '删除所选',
    deleteManyTitle: '删除这 {count} 个项目？',
    deleteManyBody: '将删除「{names}」及其分析历史与问题记录。仓库本身不受影响。',
    deleteManyBodyMore: '将删除「{names}」等共 {count} 个项目及其分析历史与问题记录。仓库本身不受影响。',
    deleteManyConfirmError: '请输入 {word} 以确认',
    deletedMany: '已删除 {count} 个项目。',
    deletedManyPartial: '删除了 {done} 个，共请求 {total} 个；其余的可能已被删除。',
    validateNameRequired: '请填写名称。',
    validateNameLength: '至少 2 个字符。',
    validateSource: '请填写本地路径或仓库地址。',
  },

  project: {
    scanning: '扫描中…',
    backToProjects: '返回项目列表',
    branch: '分支',
    scannedAt: '{when}扫描',
    queuing: '排队中…',
    scanNow: '立即扫描',
    gateFailed: '质量门禁未通过',
    notScanned: '尚未扫描',

    incompleteTag: '不完整',
    scanFailedTitle: '上次扫描失败',
    scanFailedHint: '修好上面的问题后，重新点「立即扫描」即可。',
    incomplete: '本次扫描不完整（{count} 个分析器未完成）',
    incompleteSub:
      '以下分析器未能运行到结束，它们没有产生任何结果。由于检查得比正常情况少，本次评分会偏高，不能当作有效结果看待。',
    incompleteTooltip: '未完成的分析器：{analyzers}',
    noAnalyzerRan: '本次扫描没有运行任何分析器',
    noAnalyzerRanSub: '所有选中的分析器都被跳过，没有任何代码被检查。评分和问题数在此情况下没有意义。',
    skipped: '本次扫描跳过了 {count} 个分析器',
    skippedSub:
      '以下分析器没有运行，因此没有任何结果。跳过通常表示缺少工具或缺少配置文件，而不是项目有问题——修好之后重新扫描即可。',
    skippedTools: '缺失的工具：{tools}',
    skipsShow: '查看跳过的分析器',
    skipsHide: '收起',
    statScore: '评分',
    statIssues: '问题',
    statCode: '代码',
    statComplexity: '复杂度',
    statDuplication: '重复度',
    statCoverage: '覆盖率',
    unitOutOfHundred: '/100',
    hintGate: '门禁 {status}',
    hintCriticalError: '{critical} 严重 · {errors} 错误',
    hintFilesFunctions: '{files} 个文件 · {functions} 个函数',
    hintWorst: '最差 {value}',
    hintCloneGroups: '{count} 组重复块',
    hintNoClones: '未发现重复块',
    hintTestFiles: '{count} 个测试文件',
    tabIssues: '问题',
    tabOverview: '概览',
    tabMetrics: '度量',
    tabHistory: '历史',
    tabSettings: '设置',
    runScanToSeeIssues: '运行一次扫描即可查看问题。',
    scoreContribution: '分数构成',
    scoreContributionSub: '总分是各维度的加权和。下表列出每个维度贡献了多少。',
    colDimension: '维度',
    colWeight: '权重',
    colDetail: '说明',
    issueDistribution: '问题分布',
    byAnalyzer: '按分析器',
    mostFrequentRules: '高频规则',
    colCount: '数量',
    noMetrics: '还没有度量数据。',
    size: '规模',
    mFiles: '文件数',
    mTotalLines: '总行数',
    mCodeLines: '代码行',
    mCommentLines: '注释行',
    mBlankLines: '空行',
    mFunctions: '函数数',
    mTestFiles: '测试文件',
    mTestFunctions: '测试函数',
    mComplexity: '复杂度',
    mAverage: '平均',
    mWorst: '最差',
    mDuplication: '重复度',
    mCoverage: '覆盖率',
    coverageNote:
      'Fizzra 不会运行你的测试。请先用 go test -coverprofile=coverage.out ./... 自行生成覆盖率文件，然后重新扫描。',
    largestFiles: '最大的文件',
    largestFilesSub: '按代码行数排序的最大文件。',
    colFile: '文件',
    colLines: '行数',
    colCode: '代码',
    colKind: '类型',
    kindTest: '测试',
    kindSource: '源码',
    mostComplex: '最复杂的函数',
    colFunction: '函数',
    colLocation: '位置',
    colComplexity: '复杂度',
    colParams: '参数',
    longestFunctions: '最长的函数',
    duplicatedBlocks: '重复代码块',
    duplicatedBlocksSub: '出现次数超过一次的 {lines} 行归一化窗口。',
    linesCount: '{count} 行',
    historyEmpty: '还没有分析记录。',
    colStarted: '开始时间',
    colStatus: '状态',
    colTrigger: '触发方式',
    colScore: '评分',
    colIssues: '问题',
    colGate: '门禁',
    colDuration: '耗时',
    colAnalyzers: '分析器',
    colExport: '导出',
    exportReport: '导出',
    exportRunning: '扫描还在进行，跑完才能导出报告。',
    exportHtml: 'HTML 报告（阅读用）',
    exportJson: 'JSON 数据（进一步处理）',
    exportSarif: 'SARIF（代码扫描平台）',
    analyzersNone: '无分析器',
    gateHeading: '质量门禁',
    gateSub: '任一项已启用的条件被触及时，门禁判定为失败。所有未满足的条件都会列出，而不只是第一条。',
    gateEnabled: '启用',
    gateMinScore: '最低分',
    gateMaxCritical: '严重问题上限',
    gateMaxHigh: '高危问题上限',
    gateMaxComplexity: '圈复杂度上限',
    gateMinCoverage: '最低覆盖率 (%)',
    gateFailOnWarnings: '警告即失败',
    gateHintMinScore: '评分低于此值时门禁失败。',
    gateHintZeroDisables: '填 0 表示关闭该项检查。',
    gateHintCoverage: '仅在存在覆盖率文件时生效。填 0 表示关闭该项检查。',
    gateHintFailOnWarnings: '严格模式：任何警告都会导致门禁失败。适合成熟的代码库。',
    gateSave: '保存门禁',
    gateSaved: '质量门禁已保存。',
    scanQueued: '已排队扫描，完成后结果会显示在这里。',
    preflightTitle: '扫描前确认',
    preflightLead: '{count} 个分析器无法运行，它们负责的检查会被跳过，本次评分可能偏高。',
    preflightScanAnyway: '仍然扫描',
    preflightCancel: '取消',
    notFound: '未找到该项目。',
    triggerManual: '手动',
    triggerCli: '命令行',
    triggerCi: 'CI',
    triggerSchedule: '定时',
  },

  rulesView: {
    intro:
      '规则是逐行匹配源码的 Go 正则表达式。注释与字符串字面量永不参与匹配，因此规则不会在自身的定义上触发。',
    counts: '{total} 条规则 · {builtin} 条内置 · {custom} 条自定义 · {disabled} 条已停用',
    searchPlaceholder: '按名称、描述或表达式搜索…',
    severityPlaceholder: '等级',
    filterAll: '全部',
    filterBuiltin: '内置',
    filterCustom: '自定义',
    newRule: '新建规则',
    empty: '没有符合筛选条件的规则。',
    colOn: '启用',
    colRule: '规则',
    colSeverity: '等级',
    colPatterns: '表达式',
    colScope: '作用范围',
    tagBuiltin: '内置',
    tagLoopsOnly: '仅循环内',
    morePatterns: '还有 {count} 条',
    editRule: '编辑规则',
    deleteRule: '删除规则',
    editTitle: '编辑规则',
    newTitle: '新建规则',
    formName: '名称',
    formSeverity: '等级',
    formMessage: '描述',
    formPatterns: '表达式',
    formAdvice: '修复建议',
    formPaths: '适用范围（glob）',
    formExclude: '排除（glob）',
    formEnabled: '启用',
    phName: 'no-unsafe-html',
    phMessage: '显示在问题列表中。',
    phPatterns: '^\\s*fmt\\.Print(ln|f)\\s*\\(',
    phAdvice: '说明如何修复。会显示在问题抽屉与报告中。',
    hintPatterns:
      '每行一个正则表达式。注释与字符串字面量永不参与匹配，因此无需排除规则定义或文档。',
    hintPaths: '每行一个 glob。** 可跨目录匹配。',
    hintExclude: '跳过匹配的文件，例如测试或生成代码。',
    updated: '规则已更新，下次扫描生效。',
    created: '规则已创建，下次扫描生效。',
    deleted: '规则已删除。',
    toggled: '{name} 已{state}。',
    enabled: '启用',
    disabled: '停用',
    deleteTitle: '删除规则',
    deleteBody: '确定删除「{name}」？此操作无法撤销。',
    validateName: '请填写规则名称。',
    validateSeverity: '请选择等级。',
    validatePatterns: '至少需要一条表达式。',
    validatePatternInvalid: '正则表达式「{pattern}」无效：{message}',
  },

  system: {
    server: '服务端',
    intro: '服务端运行状态、分析器与工具链。',
    analyzersHeading: '分析器',
    unlockMore: '解锁更多检查',
    ciIntegration: 'CI 集成',
    version: '版本',
    mode: '模式',
    database: '数据库',
    workspace: '工作区',
    configFile: '配置文件',
    projects: '项目数',
    rules: '规则数',
    runningTasks: '运行中任务',
    networkHeading: '网络',
    offlineLabel: '离线模式',
    offlineHint:
      '禁止分析器访问网络。在无法访问外网的机器上建议开启：漏洞扫描器需要拉取漏洞数据库，网络不通时它不是快速失败，而是会一直等到连接超时。',
    offlineAffects: '影响的分析器：{analyzers}。',
    offlineSaved: '网络设置已保存。',

    cacheHeading: '扫描缓存',
    cacheIntro:
      '部分分析器会把结果缓存起来，下次扫描直接复用。缓存命中时更快，但也意味着「同样的代码、同样的配置」可能给出与上次略有差异的结果——分数不一致时，这是首先要排除的原因。下表列出各工具实际支持的缓存控制能力。',
    cacheColTool: '工具',
    cacheColState: '缓存',
    cacheColEffect: '对本次扫描的影响',
    cacheDisabled: '已禁用',
    cacheActive: '启用中',
    cacheBuildOnly: '仅构建缓存',
    cacheRuff: 'Fizzra 每次扫描都传入 --no-cache，因此结果不受缓存影响。',
    cacheGolangci:
      '使用内置结果缓存（默认位于用户目录）。golangci-lint v2 已移除单次运行跳过缓存的参数，无法从扫描中关闭；如需强制重算，需手动执行 golangci-lint cache clean。',
    cacheStaticcheck:
      '复用 Go 构建缓存加速分析。该工具没有提供任何缓存开关，无法从扫描中禁用。',
    cacheGovulncheck:
      '复用 Go 构建缓存。可用 -db 指定漏洞数据库地址，但没有跳过缓存的选项。',
    cacheGovet: '本身不缓存结果，但依赖 Go 构建缓存，仅影响速度，不影响结论。',
    cacheFootnote:
      '说明：此处不提供开关，是因为除 ruff 之外的工具都未提供关闭缓存的参数——给出一个点了不生效的开关，比不给更容易让人误解结果的可靠性。若两次扫描分数不同，建议先对比两次的问题明细，而非只看总分。',

    optionsHeading: '检查组',
    optionDefaultOn: '默认开启',
    optionSaved: '设置已保存。',
    configure: '配置检查项',
    optionCount: '已启用 {count} / {total} 个检查组',
    optionsIntro:
      '这些组默认关闭，因为「检查结果算不算问题」是团队规定而非客观事实。打开后只会增加问题条目，不影响质量门禁——它们按 info 级别报告。',
    optionsFootnote: '修改在下次扫描时生效。',
    builtinDefaults: '内置默认值',
    readyOf: '{total} 个中 {ready} 个就绪',
    ready: '就绪',
    unavailable: '不可用',
    needs: '需要 {tools}',
    notProbed: '（未探测）',
    detectsTools: 'Fizzra 在启动时从 PATH 探测这些工具。安装后无需任何配置 —— 重启 Fizzra 即可生效。',
    globalOnlyNote:
      '这里是全局探测的结果。项目自带的工具（node_modules/.bin 下的 biome、tsc 等）不在这里显示 —— 打开具体项目，「立即扫描」时会按那个项目实际需要的工具给出一份清单。',
    usedBy: '供 {analyzers} 使用',
    toolHints: {
      oxlint:
        '经 npm 全局安装，需要机器上已有 Node.js：\n' +
        '1. 执行上面那条命令（或 `brew install oxlint`）；\n' +
        '2. 确认 npm 的全局安装目录在系统环境变量 PATH 上（`npm config get prefix` 可查；Windows 上通常是 `%APPDATA%\\npm`）；\n' +
        '3. 重启 Fizzra 使其重新探测。\n' +
        'oxlint 不是 Go 模块，所以 `go install` 用不了。它开箱即可检查，不需要项目里有任何配置文件。',
      biome:
        '经 npm 全局安装，需要机器上已有 Node.js：\n' +
        '1. 执行上面那条命令；\n' +
        '2. 确认 npm 的全局安装目录在系统环境变量 PATH 上（`npm config get prefix` 可查；Windows 上通常是 `%APPDATA%\\npm`）；\n' +
        '3. 重启 Fizzra 使其重新探测。\n' +
        '注意包名带作用域 `@biomejs/`：npm 上不带作用域的 `biome` 是一个早已废弃、毫不相干的 0.3.x 版本。',
      tsc:
        '这个分析器用的是「被扫描项目自己的」TypeScript 编译器，不是全局装的那份：\n' +
        '1. 在要被扫描的项目里执行上面那条命令；\n' +
        '2. 项目里有 `.vue` 文件的话，再装一个 `vue-tsc`（`npm install --save-dev vue-tsc`）；\n' +
        '3. 项目里需要有 `tsconfig.json`（或 `jsconfig.json`），类型检查按它的严格度设置进行。\n' +
        '之所以不全局安装：换一份编译器，检查结果就不再是这个项目构建时看到的那些。',
      pmd:
        '解压式发行版，没有包管理器会代劳：\n' +
        '1. 从上面的地址下载 `pmd-dist-*.zip`；\n' +
        '2. 解压到任意目录；\n' +
        '3. 把解压出来的 `pmd-bin-*/bin` 加入系统环境变量 PATH，或把 `FIZZRA_PMD_HOME` 指向 `pmd-bin-*` 目录。\n' +
        '需要 PATH 上有 JDK 17 或更高版本。',
      spotbugs:
        '解压式发行版，没有包管理器会代劳：\n' +
        '1. 从上面的地址下载 `spotbugs-*.zip`；\n' +
        '2. 解压到任意目录；\n' +
        '3. 把解压出来的 `spotbugs-*/bin` 加入系统环境变量 PATH，或把 `FIZZRA_SPOTBUGS_HOME` 指向 `spotbugs-*` 目录。\n' +
        '需要 PATH 上有 JDK 17 或更高版本。它分析的是编译产物，所以先构建项目再扫描。',
      cppcheck:
        '从上面地址的 Releases 页下载最新的 `.msi` 安装包，双击安装即可：\n' +
        '安装程序会自动把 cppcheck 加入系统环境变量 PATH；装好后重启 Fizzra 使其重新探测。\n' +
        '不需要编译数据库，也不需要先构建项目。',
      'clang-tidy':
        '经 pip 安装，需要机器上已有 Python：\n' +
        '1. 执行上面那条命令；\n' +
        '2. 确认 Python 的 Scripts 目录在系统环境变量 PATH 上；\n' +
        '3. 重启 Fizzra 使其重新探测。\n' +
        '分析 C++ 还需要机器上有 C/C++ 标准库（装过 VS 或 MinGW 即可），C 项目不受此限。',
      roslynator:
        '通过 .NET 全局工具安装，需要先装 .NET SDK 8 或更高版本：\n' +
        '1. 执行上面那条命令；\n' +
        '2. 确认 `%USERPROFILE%\\.dotnet\\tools` 已加入系统环境变量 PATH（`dotnet tool install --global` 通常会自己加好）；\n' +
        '3. 重启 Fizzra 使其重新探测。\n' +
        '不需要往项目里加任何 NuGet 包，也不会改动 `.csproj`。',
      phpstan:
        '从上面地址的 Releases 页下载 `phpstan.phar`，需要机器上已有 PHP 7.4 或更高版本：\n' +
        '1. 把 `phpstan.phar` 放进任意目录，并把该目录加入系统环境变量 PATH，使其能全局调用；\n' +
        '2. 确认 PHP 解释器同样在系统环境变量 PATH 上（`php -v` 输出的版本需不低于 7.4）；\n' +
        '3. 重启 Fizzra 使其重新探测。\n' +
        '也可以用 composer 全局安装：`composer global require phpstan/phpstan`。',
    },
    allAvailable: 'PATH 上的外部工具均已就绪',
    allAvailableSub: '这不代表每个项目都能跑全部检查：装在各项目里的工具由项目自己提供。',
    ciBody: 'CI 命令运行相同的分析器并应用相同的质量门禁，因此本地绿和流水线绿含义一致。',
    ciNote: 'GitLab 模板还附带一个不阻断合并的咨询任务。',
  },

  issueDetail: {
    backToProject: '返回项目',
    projectNav: '项目',
    whyThisMatters: '为什么重要',
    location: '位置',
    source: '源码',
    details: '详情',
    toolOutput: '分析器输出',
    detected: '发现时间',
    firstSeen: '首次出现',
    analyzer: '分析器',
    rule: '规则',
    fingerprint: '指纹',
    newInScan: '本次扫描新增',
    knownFromBefore: '上次扫描中已存在',
    fingerprintNote: '指纹在多次扫描间保持稳定，因此即使行号变动，同一个问题仍能被识别。',
    ruleDocs: '规则文档 ↗',
    ignore: '忽略',
    falsePositive: '标记误报',
    reopen: '重新打开',
    notFound: '未找到该问题。',
    copyLocation: '位置',
    copyFingerprint: '指纹',
    copiedLabel: '已复制{label}',
    markedAs: '已标记为{status}。',
    reopened: '问题已重新打开。',
  },

  fix: {
    heading: '建议修改',
    before: '修改前',
    after: '修改后',
  },

  chart: {
    scoreHistory: '评分趋势',
    completedAnalyses: '{count} 次已完成的分析，由旧到新。',
    issueMix: '问题构成变化',
    issueMixSub: '每次分析的堆叠数量。',
    noAnalyses: '还没有完成的分析。运行一次扫描即可查看趋势。',
    seriesScore: '评分',
    tooltipScore: '评分：<strong>{score}</strong> / 100',
    tooltipIssues: '问题：{total}（{critical} 严重，{errors} 错误）',
    tooltipGate: '门禁：{status}',
    gateMarkLine: '门禁 {score}',
    severityByIssues: '按等级统计的问题',
    noIssues: '无问题',
    countBySeverity: '{count} 个{severity}',
  },

  // 内置规则文案按 model.Rule.Name 索引，不能放 Go 侧：Message 参与指纹计算，改语言会破坏新旧问题对比
  // 未收录的规则回退服务端英文
  rule: {
    'no-panic': {
      message: '在初始化之外调用了 panic()',
      advice: '应返回 error 而非 panic；库没有权力让整个进程崩溃。',
      fixNote: '由调用方决定如何处理失败；panic 剥夺了这个选择。',
    },
    'defer-in-loop': {
      message: '在循环体内使用 defer',
      advice: 'defer 在所在函数返回时才执行，而不是每次迭代。把循环体抽成函数，或显式关闭。',
      fixNote: '把循环体移入独立函数后，defer 每次调用只执行一次。',
    },
    'context-background-in-request-path': {
      message: '在请求路径上使用了 context.Background()',
      advice: '应基于传入的请求派生 context，让取消信号和超时能够传递下去。',
      fixNote: '请求自带的 context 会携带客户端超时，并随连接断开而取消。',
    },
    'goroutine-captures-loopvar': {
      message: 'goroutine 闭包捕获了循环变量',
      advice: '把值作为参数传入，或在循环内遮蔽它；否则所有 goroutine 可能读到最终值。',
      fixNote: '参数在每次迭代时求值，因此每个 goroutine 拿到各自的值。',
    },
    'mutex-by-value': {
      message: '按值复制了 sync.Mutex',
      advice: '应传递指针。复制互斥锁会静默复制其状态，锁将不再保护任何东西。',
      fixNote: '使用指针只保留一个互斥锁；复制出的版本锁的是另一个锁。',
    },
    'sleep-in-loop': {
      message: '在循环中使用 time.Sleep',
      advice: '改用 ticker，或使用可响应 context 的退避定时器，让循环能够被取消。',
      fixNote: '配合 context select 的 ticker 可以被中断，Sleep 不能。',
    },
    'discarded-error': {
      message: '丢弃了 error 返回值',
      advice: '应处理、向上返回，或带上上下文记录日志。静默失败是最难排查的一类问题。',
      fixNote: '包装错误既保留了错误链，又让失败可见。',
    },
    'error-shadowed-by-nil': {
      message: 'error 变量被 nil 遮蔽',
      advice: '在外层作用域声明 error，用 = 而非 := 赋值，否则外层的 error 始终是零值。',
      fixNote: '外层 `err` 被赋值而非遮蔽，因此判断读到的是真实的错误。',
    },
    'command-injection': {
      message: '用户输入被拼入了 shell 命令',
      advice: '应传递独立的参数，绝不要把输入交给 sh -c。',
      fixNote: '参数以列表形式传入，不经过 shell，因此元字符不起作用。',
    },
    'weak-random': {
      message: '安全敏感场景使用了 math/rand',
      advice: '应使用 crypto/rand。math/rand 的种子可预测，不适合生成令牌、密钥或随机数。',
      fixNote: 'crypto/rand 从操作系统的熵源读取。',
    },
    'broken-hash': {
      message: '使用了已被攻破的哈希算法',
      advice: '完整性校验应使用 SHA-256 或更强算法；口令应使用 bcrypt/argon2。',
      fixNote: 'SHA-256 具备抗碰撞性，MD5 与 SHA-1 不具备。',
    },
    'insecure-tls': {
      message: '关闭了 TLS 校验',
      advice: '应移除 InsecureSkipVerify。若原因是自签证书，改为固定信任的 CA。',
      fixNote: '针对固定的 CA 池校验，既保留检查又不必信任所有证书。',
    },
    'hardcoded-credential': {
      message: '源码中硬编码了凭据或密钥',
      advice: '应从环境变量或密钥管理服务读取；已提交的密钥必须轮换。',
      fixNote: '取值来自环境变量，仓库中不再包含密钥。',
    },
    'sql-injection': {
      message: '以字符串拼接构造 SQL',
      advice: '应使用参数化查询，绝不把值插进 SQL 文本。',
      fixNote: '占位符让值完全不进入 SQL 语法。',
    },
    'unsafe-file-permissions': {
      message: '写入文件时权限过于宽松',
      advice: '可能含敏感数据的文件应使用 0600，目录应使用 0750 或更严格。',
      fixNote: '0600 表示仅所有者可读。',
    },
    'unsafe-pointer': {
      message: '使用了 unsafe.Pointer',
      advice: '除非别无选择，否则应避免 unsafe；并记录使该转换成立的前提条件。',
      fixNote: '带类型的转换避免绕过类型系统。',
    },
    'wails-events-emit-in-loop': {
      message: '在循环中调用 EventsEmit',
      advice: '应批量收集后一次性发送；逐次发送会淹没 JS 桥，导致界面卡死。',
      fixNote: '一次发送整个切片，桥的通信量保持恒定。',
    },
    'wails-dialog-unchecked': {
      message: '丢弃了 dialog 的返回值',
      advice: 'dialog 返回 (result, error)。把用户取消当成确认，会删除用户本想保留的数据。',
      fixNote: '检查返回值后，用户取消时不会执行任何操作。',
    },
    'wails-browser-open-url': {
      message: 'BrowserOpenURL 使用了未校验的 URL',
      advice: '应按白名单校验 scheme。file:// 与自定义 scheme 会触达本地处理器，攻击者可控的链接等于本地代码执行。',
      fixNote: '拒绝任何非 http(s) 的 URL，使其无法触达本地处理器。',
    },
    'wails-frontend-hardcoded-path': {
      message: 'Go 源码中硬编码了前端路径',
      advice: '应相对于可执行文件或内嵌资源解析路径，以便打包后仍然有效。',
      fixNote: '资源已内嵌，因此打包后的二进制中路径同样有效。',
    },
    'wails-wailsjson-legacy-fields': {
      message: 'wails.json 中包含旧格式字段',
      advice: '应移除废弃字段并改用 v2 schema；陈旧字段会在构建时被静默忽略。',
      fixNote: 'v2 schema 才是工具链真正读取的那一份。',
    },
    'unresolved-marker': {
      message: '源码中遗留了未处理的标记',
      advice: '应处理该标记或将其转为可跟踪的 issue；无人认领的标记会无限期堆积。',
      fixNote: '引用真实的 issue 编号，让后续处理保持可见。',
    },
    'commented-out-code': {
      message: '被注释掉的代码',
      advice: '应直接删除。版本控制记录了旧版本，而注释掉的代码会静默失效。',
      fixNote: '历史留在仓库里，文件保持可读。',
    },
    'debug-output-left': {
      message: '库代码中遗留了调试输出',
      advice: '应改用项目日志组件以便按等级过滤，或直接删除该调用。',
      fixNote: '分级日志在不写标准输出的前提下保留了诊断能力。',
    },
  },
}

export default zh
