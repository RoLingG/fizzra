// 英文文案是键的事实来源，`as const` 保证加 key 漏翻译会编译报错
// 插值一律写具名占位符，复数写 `单数 | 复数 | 复数`
const en = {
  nav: {
    dashboard: 'Dashboard',
    projects: 'Projects',
    rules: 'Rules',
    system: 'System',
  },

  app: {
    brandSub: 'Code quality review',
    sections: 'Sections',
    analyzersUnavailable: 'no analyzers unavailable | {count} analyzer unavailable | {count} analyzers unavailable',
    tasksRunning: 'no tasks running | {count} task running | {count} tasks running',
    footerCounts: '{projects} projects · {rules} rules',
  },

  common: {
    cancel: 'Cancel',
    save: 'Save',
    delete: 'Delete',
    close: 'Close',
    copy: 'Copy',
    copied: 'Copied',
    refresh: 'Refresh',
    back: 'Back',
    edit: 'Edit',
    scan: 'Scan',
    reset: 'Reset',
    never: 'never',
    justNow: 'just now',
    notAvailable: 'n/a',
    none: '—',
    all: 'All',
    projectWide: 'project-wide',
    loading: 'Loading…',
  },

  theme: {
    label: 'Theme',
    light: 'Light',
    dark: 'Dark',
    auto: 'System',
  },

  language: {
    label: 'Language',
    en: 'English',
    zh: '中文',
  },

  severity: {
    critical: 'Critical',
    error: 'Error',
    warning: 'Warning',
    info: 'Info',
  },

  status: {
    open: 'Open',
    ignored: 'Ignored',
    falsePositive: 'False positive',
    pending: 'Pending',
    running: 'Running',
    success: 'Success',
    failed: 'Failed',
    canceled: 'Canceled',
  },

  gate: {
    passed: 'Passed',
    failed: 'Failed',
    skipped: 'Skipped',
    violation: {
      minScore: 'Score is below the minimum',
      maxCritical: 'Too many critical issues',
      maxHigh: 'Too many high-severity issues',
      maxComplexity: 'Complexity exceeds the limit',
      minCoverage: 'Coverage is below the minimum',
      default: 'Quality gate condition failed',
    },
  },

  quality: {
    security: 'Security',
    bugs: 'Bug risk',
    maintainability: 'Maintainability',
    complexity: 'Complexity',
    coverage: 'Coverage',
    notMeasured: 'not measured',
  },

  // description 逐字抄自 Go 侧 Description()，缺译文回退时与这里一致
  analyzer: {
    custom: {
      name: 'Custom rules',
      summary: 'Built-in rules, including Go/Wails checks',
      description:
        "Evaluates Fizzra's own rule set: correctness, security hygiene and Go/Wails specific patterns defined in fizzra.yaml or the web UI.",
    },
    wails: {
      name: 'Wails',
      summary: 'Wails project structure and binding checks',
      description:
        'Wails project structure, runtime API usage (EventsEmit, Window, Dialog, BrowserOpenURL) and binding surface review.',
    },
    metrics: {
      name: 'Metrics',
      summary: 'Size, complexity and duplication metrics',
      description:
        'Computes code size, cyclomatic complexity, function length, parameter counts and duplication. Produces the metrics behind the quality score.',
    },
    gofmt: {
      name: 'gofmt',
      summary: 'Go formatting check',
      description: 'Reports Go files that are not gofmt-formatted. Uses the local Go toolchain.',
    },
    'go-vet': {
      name: 'go vet',
      summary: 'Go vet: bad Printf verbs, lost cancels, unreachable code',
      description:
        'Runs go vet: suspicious constructs the compiler accepts, such as bad Printf verbs, lost cancel funcs and unreachable code.',
    },
    golangci: {
      name: 'golangci-lint',
      summary: 'Broad Go static analysis: unused code, errors, style',
      description:
        'Runs golangci-lint: unused code, error handling, style and a broad set of static analysis linters in one pass.',
    },
    staticcheck: {
      name: 'staticcheck',
      summary: 'Deep Go static analysis for bugs and suspicious code',
      description:
        'Runs staticcheck standalone: deep static analysis for bugs, simplifications and questionable code.',
    },
    gosec: {
      name: 'gosec',
      summary: 'Go security scan: injection, crypto, credentials, file modes',
      description:
        'Runs gosec: SQL/command injection, insecure crypto, hard-coded credentials, permissive file permissions and other OWASP-relevant Go patterns.',
    },
    govulncheck: {
      name: 'govulncheck',
      summary: 'Go dependency vulnerabilities, checked against the official database',
      description:
        'Runs govulncheck against the Go vulnerability database to find reachable CVEs in module dependencies.',
      option: {
        offline: {
          description:
            'Skip this check, because govulncheck must reach the Go vulnerability database. It has no usable local-database mode, so offline means skipping rather than degrading — reporting Go vulnerabilities from a stale database is more dangerous than reporting none.',
        },
      },
    },
    ruff: {
      name: 'ruff',
      summary: 'Python lint and security rules',
      description:
        'Runs ruff: correctness, imports, style, complexity and the bandit-derived security rules for Python.',
      // 与后端 ruffRuleGroups 的 Key 一致
      option: {
        style: {
          description:
            "Line length, whitespace, blank lines and other pycodestyle formatting. A formatter's job; off by default because it dominates the issue list.",
        },
        docstrings: {
          description:
            'Missing or malformed docstrings (pydocstyle). A documentation policy rather than a defect, and the single largest source of noise on a real project.',
        },
        annotations: {
          description:
            'Missing type annotations (flake8-annotations). Useful as a ratchet on new code; noisy on a codebase that predates it.',
        },
        copyright: {
          description:
            'Missing copyright notice at the top of each file. An organisation policy, not a code quality signal.',
        },
        naming: {
          description: 'PEP 8 naming conventions (pep8-naming).',
        },
        pandas: {
          description: 'Pandas-specific anti-patterns (pandas-vet). Enable for data-science code.',
        },
        quotes: {
          description: 'Quote style consistency (flake8-quotes).',
        },
        todo: {
          description: 'Formatting of TODO comments (flake8-todos).',
        },
      },
    },
    oxlint: {
      name: 'oxlint',
      summary: 'JS/TS lint, including React, a11y and import checks',
      description:
        'Runs oxlint: correctness, suspicious patterns, TypeScript, React, accessibility and import checks for JavaScript and TypeScript.',
      option: {
        vuePlugin: {
          description:
            "Enable oxlint's Vue plugin (--vue-plugin), which adds the template-aware rules for .vue files. On by default: it adds the 31 Vue rules oxlint ships (measured: 111 → 142 rules) and no noise. Turning it off gives up those 31 checks; a .vue file's <script> block is linted either way.",
        },
      },
    },
    tsc: {
      name: 'tsc',
      summary: 'TypeScript type checking',
      description:
        "Runs the project's own TypeScript compiler in check-only mode: type errors, impossible values, unsafe null handling and contract violations. Requires a tsconfig.json.",
    },
    biome: {
      name: 'biome',
      summary: 'JS/TS lint and formatting',
      description:
        "Runs Biome's linter: correctness, suspicious patterns and style for JavaScript, TypeScript, JSX/TSX, JSON and CSS. It enables several correctness rules oxlint's default set leaves off.",
      option: {
        css: {
          description:
            'Also check .css: unknown properties, duplicate declarations, empty blocks and !important. Note that Biome cannot read .scss or .less — not a configuration problem, it simply has no parser for them.',
        },
      },
    },
    clippy: {
      name: 'clippy',
      summary: 'Rust lint: correctness, performance, idioms',
      description:
        'Runs cargo clippy: correctness, suspicious patterns, performance and idiomatic-Rust lints from the official Rust toolchain.',
    },
    pmd: {
      name: 'PMD',
      summary: 'Java lint: error-prone, performance, concurrency, design, security',
      description:
        'Runs PMD across its error-prone, best-practices, performance, concurrency, design and security categories — over three hundred rules. Style is excluded by default because it buries the real findings.',
    },
    spotbugs: {
      name: 'SpotBugs',
      summary: 'Java bytecode defects: credentials, SQL injection, resource leaks, null',
      description:
        'Runs SpotBugs against compiled bytecode: hard-coded credentials, SQL built from user input, unclosed resources and definite null dereferences. Requires a build output.',
    },
    rustfmt: {
      name: 'rustfmt',
      summary: 'Rust formatting check',
      description:
        'Reports Rust files that are not rustfmt-formatted. Uses the local Rust toolchain.',
    },
    cppcheck: {
      name: 'cppcheck',
      summary: 'C/C++ defect analysis (memory leaks, buffer overflows, dangling pointers)',
      description:
        'Runs cppcheck: memory leaks, buffer overflows, dangling pointers and similar defects. Needs no build and no compilation database.',
    },
    'clang-tidy': {
      name: 'clang-tidy',
      summary: 'Deep C/C++ analysis (command injection, dangling pointers, CERT C)',
      description:
        'Runs clang-tidy: command injection, dangling pointers, memory leaks and CERT C rules. Needs Python and a C/C++ standard library on the machine.',
    },
    roslynator: {
      name: 'Roslynator',
      summary: 'C# analysis (compiler diagnostics, .NET analyzers, code-quality rules)',
      description:
        'Runs Roslynator on the .csproj: compiler diagnostics, .NET built-in analyzers and code-quality rules. Adds no package to the project and does not touch the project file.',
    },
    phpstan: {
      name: 'PHPStan',
      summary: 'PHP type analysis (undefined methods, wrong argument types, unreachable branches)',
      description:
        'Runs PHPStan: undefined methods and properties, wrong argument types, unreachable branches and other type errors. Needs a PHP interpreter on the machine.',
    },
    'cargo-audit': {
      name: 'cargo-audit',
      summary: 'Rust dependency vulnerabilities, checked against RustSec',
      description:
        'Runs cargo audit: checks the packages locked in Cargo.lock against the RustSec advisory database (CVE/RUSTSEC ids). It examines dependencies, not this project’s code.',
      option: {
        offline: {
          description:
            'Run cargo-audit with --no-fetch, using the advisory snapshot already on disk. Unlike govulncheck this keeps working — it reports against a possibly stale database rather than skipping. The global `offline` setting has the same effect on every analyzer.',
        },
        scoredTransitive: {
          description:
            'Count vulnerabilities in transitive dependencies against the quality score. Off by default: the project cannot upgrade those directly, so their advisories track upstream release timing rather than the quality of this code. They are always reported; this only controls whether they affect the score.',
        },
      },
    },
  },

  progress: {
    starting: 'Starting…',
    preparingSources: 'Preparing sources…',
    indexingFiles: 'Indexing files…',
    savingResults: 'Saving results…',
    scoring: 'Scoring…',
    done: 'Done',
  },

  errors: {
    network: 'The request failed.',
    timeout: 'The request timed out. A large scan can take a while — try again or check the server logs.',
    generic: 'Something went wrong.',
    notFound: 'Not found.',
    conflict: 'That already exists.',
    invalidInput: 'The input was rejected.',
    internalError: 'The server hit an internal error.',
    queueFull: 'The scan queue is full. Try again shortly.',
    exportReport: 'Could not export the report.',
    loadSystem: 'Could not load server information.',
    loadProjects: 'Could not load projects.',
    loadProject: 'Could not load the project.',
    loadIssues: 'Could not load issues.',
    loadIssue: 'Could not load the issue.',
    loadRules: 'Could not load rules.',
    saveProject: 'Could not save the project.',
    deleteProject: 'Could not delete the project.',
    saveRule: 'Could not save the rule.',
    deleteRule: 'Could not delete the rule.',
    toggleRule: 'Could not change the rule state.',
    saveGate: 'Could not save the quality gate.',
    updateIssue: 'Could not update the issue.',
    startScan: 'Could not start the scan.',
    copyFailed: 'Select the text and copy it manually.',
  },

  issue: {
    searchPlaceholder: 'Filter by message, file or rule…',
    severityPlaceholder: 'Severity',
    analyzerPlaceholder: 'Analyzer',
    statusPlaceholder: 'Status',
    newOnly: 'New only',
    sortBySeverity: 'By severity',
    sortByFile: 'By file',
    sortByAnalyzer: 'By analyzer',
    sortNewest: 'Newest first',
    summary: 'no issues matching the current filters | {count} issue matching the current filters | {count} issues matching the current filters',
    empty: 'No issues match the current filters.',
    colSeverity: 'Severity',
    colAnalyzer: 'Analyzer',
    colRule: 'Rule',
    colMessage: 'Message',
    colLocation: 'Location',
    colActions: 'Actions',
    badgeNew: 'new',
    badgeNewHint: 'Not present in the previous successful scan',
    badgeFix: 'fix available',
    badgeFixHint: 'A worked before/after example is available',

    origin_direct: 'direct dep',
    origin_transitive: 'transitive',
    originHint_direct:
      'This package is declared in Cargo.toml, so the project can upgrade it directly.',
    originTransitiveHint:
      'The project does not depend on this package directly; {path} pulled it in. Upgrading it here will not work — the dependency above it in the chain needs to move.',
    dependencyChain: 'Dependency chain',
    dependencyNoteTitle: '{count} findings come from third-party dependencies',
    dependencyNoteBody:
      'These are not problems in this project’s code. They are packages locked in Cargo.lock that match an advisory in the RustSec database. The project does not depend on them directly — they arrive through something else.',
    dependencyNoteUnscored:
      '{count} of them are transitive and are excluded from the quality score: this project cannot upgrade them, so counting them would make the score track upstream release timing rather than the quality of this code. Change this under cargo-audit settings on the System page.',

    whyThisMatters: 'Why this matters',
    location: 'Location',
    source: 'Source',
    details: 'Details',
    toolOutput: 'Analyzer output',
    detected: 'Detected',
    statusLabel: 'Status',
    fingerprint: 'Fingerprint',
    fingerprintNote:
      'The fingerprint is stable across scans, so an unchanged finding is recognised even when its line number moves.',
    ruleDocs: 'Rule documentation ↗',
    openIssuePage: 'Open the issue page',
    issueActions: 'Issue actions',
    ignore: 'Ignore',
    falsePositive: 'False positive',
    reopen: 'Reopen',
    ignoreConfirmTitle: 'Ignore this issue?',
    ignoreConfirmBody: 'The issue will be hidden from the default view but kept in the database for audit.',
    copiedLabel: 'Copied {label}',
    markedAs: 'Marked as {status}.',
    reopened: 'Issue reopened.',
    copyLocation: 'location',
    copyFingerprint: 'fingerprint',
  },

  dashboard: {
    emptyTitle: 'No projects yet',
    emptyBody: 'Register a repository to start reviewing it. The CLI can also add one automatically:',
    addProject: 'Add a project',
    overallQuality: 'Overall quality',
    headlineGood: 'In good shape overall',
    headlineFair: 'Reasonable, with room to improve',
    headlinePoor: 'Needs attention',
    headlineNone: 'Nothing to grade yet',
    sideGateTitle: 'Gates passing',
    sideLowest: 'Lowest scores',
    sideAll: 'All',
    gradeLabel: 'Grade',
    scoreUnavailable: 'Score unavailable',
    grade: {
      a: 'Excellent',
      b: 'Good',
      c: 'Fair',
      d: 'Pass',
      e: 'Weak',
      f: 'At risk',
    },
    noAnalyses: 'No completed analyses yet. Run a scan to get a score.',
    heroSummary:
      'Mean across {count} analysed project. {passing} of {count} passing the quality gate. | Mean across {count} analysed projects. {passing} of {count} passing the quality gate.',
    gaugeWorst: 'Worst',
    gaugeNow: 'Now',
    gaugeLine: 'Gate',
    gaugeBest: 'Best',
    bandRuns: 'Scans',
    bandPassed: 'Gate passed',
    bandFailed: 'Gate failed',
    affectedProjects: 'Projects',
    criticalAlertTitle: 'Critical findings need attention',
    security: {
      highLabel: 'High risk',
      highHeadline: '{count} critical',
      highDetail:
        '{count} critical and {errors} high-severity finding need attention before the next release. | {count} critical and {errors} high-severity findings need attention before the next release.',
      elevatedLabel: 'Elevated',
      elevatedHeadline: '{count} high-severity',
      elevatedDetail:
        'No critical findings, but {count} high-severity issue was reported. | No critical findings, but {count} high-severity issues were reported.',
      unknownLabel: 'Unknown',
      unknownHeadline: 'Not scanned',
      unknownDetail: 'Run a scan to get a security verdict.',
      lowLabel: 'Low risk',
      lowHeadline: 'No critical findings',
      lowDetail: 'No critical or high-severity issues in the latest analyses.',
    },
    coverageNotAvailable: 'n/a',
    coverageHint: 'No coverage profile — run go test -coverprofile',
    coverageMeasured: '{measured} of {total} project measured | {measured} of {total} projects measured',
    criticalSummary: '{count} critical issue across: {projects}. | {count} critical issues across: {projects}.',
    statOpenIssues: 'Open issues',
    statCritical: 'Critical',
    statHigh: 'High severity',
    statProjects: 'Projects',
    statCoverage: 'Coverage',
    statAcross: 'across {count} projects',
    statRequiresAction: 'Requires action',
    statErrorLevel: 'Error level',
    statAnalysed: '{count} analysed',
    sectionIssueMix: 'Issue mix',
    sectionIssueMixSub: 'Latest analysis of each project, combined.',
    sectionScoreHistory: 'Score history',
    sectionScoreHistorySub: 'Mean score per analysis run, oldest to newest.',
    sectionNeedsAttention: 'Needs attention',
    sectionNeedsAttentionSub: 'Lowest scoring projects first.',
    sectionRecent: 'Recent activity',
    sectionRecentSub: 'Most recent completed analyses.',
    issuesCount: '{count} issue | {count} issues',
  },

  projectsView: {
    intro:
      'A project points at a local directory or a Git repository. Registering the same directory twice is detected automatically.',
    addProject: 'Add project',
    empty: 'No projects registered yet.',
    colName: 'Name',
    colLanguage: 'Language',
    languageShort_javascript: 'JS / TS',
    colSource: 'Source',
    colScore: 'Score',
    colGate: 'Gate',
    colIssues: 'Issues',
    colLastScan: 'Last scan',
    scanning: 'Scanning',
    scanQueued: 'Scan queued for {name}.',
    editTitle: 'Edit project',
    addTitle: 'Add project',
    formName: 'Name',
    formDescription: 'Description',
    formLocalPath: 'Local path',
    formRepoUrl: 'Repository URL',
    formSource: 'Code source',
    sourceLocal: 'Local directory',
    sourceRepo: 'Git repository',
    formBranch: 'Branch',
    formBranchOptional: 'Branch (optional)',
    formKind: 'Framework',
    formLanguage: 'Language',
    languageHint: 'Selects which analyzers run. A language with no analyzers yet reports that nothing was checked rather than passing.',
    language_go: 'Go',
    language_rust: 'Rust',
    language_python: 'Python',
    language_javascript: 'JavaScript / TypeScript',
    language_java: 'Java',
    language_csharp: 'C#',
    language_cpp: 'C / C++',
    language_php: 'PHP',
    formAnalyzers: 'Analyzers',
    formAccent: 'Accent colour',
    phName: 'my-wails-app',
    phOptional: 'Optional',
    phLocalPath: 'D:\\code\\my-wails-app or /home/me/my-app',
    phRepoUrl: 'https://github.com/you/my-wails-app.git',
    phBranch: 'main',
    phBranchOptional: 'Leave empty to use the default branch',
    phAnalyzers: 'All available',
    hintLocalPath: 'Scanned in place. Leave empty when using a repository URL instead.',
    hintRepoUrl: 'Cloned into the workspace on first scan and fetched on later ones.',
    hintAnalyzers: 'Leave empty to run every available analyzer.',
    kindGo: 'Go',
    kindWails: 'Wails',
    wailsNote: 'Wails projects additionally run the runtime API and binding checks.',
    needsTools: 'needs {tools}',
    updated: 'Project updated.',
    registered: 'Project registered.',
    deleted: 'Project deleted.',
    deleteTitle: 'Delete this project?',
    deleteBody:
      'This deletes "{name}" along with its analysis history and issues. The repository itself is not touched.',
    selectedCount: '{count} projects selected',
    clearSelection: 'Clear selection',
    deleteSelected: 'Delete selected',
    deleteManyTitle: 'Delete these {count} projects?',
    deleteManyBody:
      'This deletes "{names}" along with their analysis history and issues. The repositories themselves are not touched.',
    deleteManyBodyMore:
      'This deletes "{names}" and {count} projects in total, along with their analysis history and issues. The repositories themselves are not touched.',
    deleteManyConfirmError: 'Type {word} to confirm',
    deletedMany: 'Deleted {count} projects.',
    deletedManyPartial: 'Deleted {done} of {total} — the rest may already have been removed.',
    validateNameRequired: 'A name is required.',
    validateNameLength: 'Use at least 2 characters.',
    validateSource: 'Provide either a local path or a repository URL.',
  },

  project: {
    scanning: 'scanning…',
    backToProjects: 'Back to projects',
    branch: 'branch',
    scannedAt: 'scanned {when}',
    queuing: 'Queuing…',
    scanNow: 'Scan now',
    gateFailed: 'Quality gate failed',
    notScanned: 'not scanned',

    incompleteTag: 'Incomplete',
    incomplete: 'This scan was incomplete ({count} analyzers did not finish)',
    incompleteSub:
      'The analyzers below did not run to completion, so they contributed no findings. Fewer checks than normal means the score is inflated — treat it as unreliable rather than as a result.',
    incompleteTooltip: 'Did not finish: {analyzers}',
    noAnalyzerRan: 'No analyzer ran on this scan',
    noAnalyzerRanSub:
      'Every selected analyzer skipped, so no code was examined at all. The score and the issue counts are meaningless in this state.',
    skipped: 'This scan skipped {count} analyzers',
    skippedSub:
      'The analyzers below did not run, so they contributed no results. A skip usually means a missing tool or config file rather than a problem with the project — fix that and scan again.',
    skippedTools: 'Missing tools: {tools}',
    skipsShow: 'Show skipped analyzers',
    skipsHide: 'Hide',
    statScore: 'Score',
    statIssues: 'Issues',
    statCode: 'Code',
    statComplexity: 'Complexity',
    statDuplication: 'Duplication',
    statCoverage: 'Coverage',
    unitOutOfHundred: '/100',
    hintGate: 'gate {status}',
    hintCriticalError: '{critical} critical · {errors} error',
    hintFilesFunctions: '{files} files · {functions} functions',
    hintWorst: 'worst {value}',
    hintCloneGroups: '{count} clone groups',
    hintNoClones: 'no clones found',
    hintTestFiles: '{count} test files',
    tabIssues: 'Issues',
    tabOverview: 'Overview',
    tabMetrics: 'Metrics',
    tabHistory: 'History',
    tabSettings: 'Settings',
    runScanToSeeIssues: 'Run a scan to see issues.',
    scoreContribution: 'Score contribution',
    scoreContributionSub:
      'The total score is a weighted sum. Each dimension below shows what it contributed.',
    colDimension: 'Dimension',
    colWeight: 'Weight',
    colDetail: 'Detail',
    issueDistribution: 'Issue distribution',
    byAnalyzer: 'By analyzer',
    mostFrequentRules: 'Most frequent rules',
    colCount: 'Count',
    noMetrics: 'No metrics recorded yet.',
    size: 'Size',
    mFiles: 'Files',
    mTotalLines: 'Total lines',
    mCodeLines: 'Code lines',
    mCommentLines: 'Comment lines',
    mBlankLines: 'Blank lines',
    mFunctions: 'Functions',
    mTestFiles: 'Test files',
    mTestFunctions: 'Test functions',
    mComplexity: 'Complexity',
    mAverage: 'Average',
    mWorst: 'Worst',
    mDuplication: 'Duplication',
    mCoverage: 'Coverage',
    coverageNote:
      'Fizzra never runs your tests. Generate the profile yourself with go test -coverprofile=coverage.out ./... and scan again.',
    largestFiles: 'Largest files',
    largestFilesSub: 'The biggest files by code line count.',
    colFile: 'File',
    colLines: 'Lines',
    colCode: 'Code',
    colKind: 'Kind',
    kindTest: 'test',
    kindSource: 'source',
    mostComplex: 'Most complex functions',
    colFunction: 'Function',
    colLocation: 'Location',
    colComplexity: 'Complexity',
    colParams: 'Params',
    longestFunctions: 'Longest functions',
    duplicatedBlocks: 'Duplicated blocks',
    duplicatedBlocksSub: 'Windows of {lines} normalised lines appearing more than once.',
    linesCount: '{count} lines',
    historyEmpty: 'No analyses yet.',
    colStarted: 'Started',
    colStatus: 'Status',
    colTrigger: 'Trigger',
    colScore: 'Score',
    colIssues: 'Issues',
    colGate: 'Gate',
    colDuration: 'Duration',
    colAnalyzers: 'Analyzers',
    colExport: 'Export',
    exportReport: 'Export',
    exportRunning: 'This scan is still running. Wait for it to finish to export.',
    exportHtml: 'HTML report (to read)',
    exportJson: 'JSON data (to process)',
    exportSarif: 'SARIF (code scanning platforms)',
    analyzersNone: 'No analyzers',
    gateHeading: 'Quality gate',
    gateSub:
      'The gate fails a scan when any enabled condition below is breached. Every failing condition is listed, not just the first.',
    gateEnabled: 'Enabled',
    gateMinScore: 'Minimum score',
    gateMaxCritical: 'Maximum critical issues',
    gateMaxHigh: 'Maximum high-severity issues',
    gateMaxComplexity: 'Maximum cyclomatic complexity',
    gateMinCoverage: 'Minimum coverage (%)',
    gateFailOnWarnings: 'Fail on warnings',
    gateHintMinScore: 'Fail the gate when the score is below this.',
    gateHintZeroDisables: 'Zero disables the check.',
    gateHintCoverage: 'Only applies when a coverage profile is available. Zero disables the check.',
    gateHintFailOnWarnings: 'Strict mode: any warning fails the gate. Useful for a mature codebase.',
    gateSave: 'Save gate',
    gateSaved: 'Quality gate saved.',
    scanQueued: 'Scan queued. Results appear here as soon as it finishes.',
    preflightTitle: 'Before scanning',
    preflightLead:
      '{count} analyzer(s) cannot run. What they check will be skipped, so the score may read higher than it should.',
    preflightScanAnyway: 'Scan anyway',
    preflightCancel: 'Cancel',
    notFound: 'Project not found.',
    triggerManual: 'manual',
    triggerCli: 'cli',
    triggerCi: 'ci',
    triggerSchedule: 'schedule',
  },

  rulesView: {
    intro:
      'Rules are Go regular expressions matched against source lines. Comments and string literals are never matched, so a rule cannot fire on its own definition.',
    counts: '{total} rules · {builtin} built-in · {custom} custom · {disabled} disabled',
    searchPlaceholder: 'Search by name, message or pattern…',
    severityPlaceholder: 'Severity',
    filterAll: 'All',
    filterBuiltin: 'Built-in',
    filterCustom: 'Custom',
    newRule: 'New rule',
    empty: 'No rules match the filter.',
    colOn: 'On',
    colRule: 'Rule',
    colSeverity: 'Severity',
    colPatterns: 'Patterns',
    colScope: 'Scope',
    tagBuiltin: 'built-in',
    tagLoopsOnly: 'loops only',
    morePatterns: '+{count} more',
    editRule: 'Edit rule',
    deleteRule: 'Delete rule',
    editTitle: 'Edit rule',
    newTitle: 'New rule',
    formName: 'Name',
    formSeverity: 'Severity',
    formMessage: 'Message',
    formPatterns: 'Patterns',
    formAdvice: 'Remediation advice',
    formPaths: 'Applies to (globs)',
    formExclude: 'Exclude (globs)',
    formEnabled: 'Enabled',
    phName: 'no-unsafe-html',
    phMessage: 'Shown in the issue list.',
    phPatterns: '^\\s*fmt\\.Print(ln|f)\\s*\\(',
    phAdvice: 'Explain the fix. Shown in the issue drawer and in reports.',
    hintPatterns:
      'One regular expression per line. Comments and string literals are never matched, so you do not need to exclude rule definitions or documentation.',
    hintPaths: 'One glob per line. ** crosses directories.',
    hintExclude: 'Skip matching files, e.g. tests or generated code.',
    updated: 'Rule updated. The next scan will use it.',
    created: 'Rule created. The next scan will use it.',
    deleted: 'Rule deleted.',
    toggled: '{name} {state}.',
    enabled: 'enabled',
    disabled: 'disabled',
    deleteTitle: 'Delete rule',
    deleteBody: 'Delete "{name}"? This cannot be undone.',
    validateName: 'A rule name is required.',
    validateSeverity: 'Pick a severity.',
    validatePatterns: 'At least one pattern is required.',
    validatePatternInvalid: 'Invalid regular expression "{pattern}": {message}',
  },

  system: {
    server: 'Server',
    intro: 'Server status, analyzers and toolchain.',
    analyzersHeading: 'Analyzers',
    unlockMore: 'Unlock more checks',
    ciIntegration: 'CI integration',
    version: 'Version',
    mode: 'Mode',
    database: 'Database',
    workspace: 'Workspace',
    configFile: 'Config file',
    projects: 'Projects',
    rules: 'Rules',
    runningTasks: 'Running tasks',
    networkHeading: 'Network',
    offlineLabel: 'Offline mode',
    offlineHint:
      'Stops analyzers from reaching the network. Enable it on a machine without outbound access: the vulnerability scanners fetch an advisory database, and on a blocked network that fetch hangs until the socket times out rather than failing quickly.',
    offlineAffects: 'Affects: {analyzers}.',
    offlineSaved: 'Network setting saved.',

    cacheHeading: 'Scan caching',
    cacheIntro:
      'Some analyzers cache their results and reuse them on the next run. A cache hit is faster, but it also means the same code and the same configuration can produce a slightly different result than last time — when two scores disagree, this is the first thing to rule out. The table below states what each tool actually supports.',
    cacheColTool: 'Tool',
    cacheColState: 'Cache',
    cacheColEffect: 'Effect on this scan',
    cacheDisabled: 'Disabled',
    cacheActive: 'Active',
    cacheBuildOnly: 'Build only',
    cacheRuff:
      'Fizzra passes --no-cache on every scan, so results are not affected by cache state.',
    cacheGolangci:
      'Uses a built-in result cache (in your user directory by default). golangci-lint v2 removed the per-run flag that skips it, so a scan cannot opt out; run golangci-lint cache clean by hand to force a full recompute.',
    cacheStaticcheck:
      'Reuses the Go build cache to speed up analysis. The tool exposes no cache flag, so it cannot be disabled from a scan.',
    cacheGovulncheck:
      'Reuses the Go build cache. -db can point at a different advisory database, but there is no option to skip the cache.',
    cacheGovet:
      'Caches no results of its own, though it relies on the Go build cache. That affects speed only, not the findings.',
    cacheFootnote:
      'Note: there are no switches here because no tool other than ruff offers a way to turn caching off — a switch that does nothing when flipped is more misleading about reliability than no switch at all. If two scans disagree, compare their findings rather than only the totals.',

    optionsHeading: 'Check groups',
    optionDefaultOn: 'on by default',
    optionSaved: 'Setting saved.',
    configure: 'Configure checks',
    optionCount: '{count} of {total} check groups enabled',
    optionsIntro:
      'These groups are off by default because whether their findings count as problems is a team decision, not a fact. Turning one on adds findings without changing the quality gate: they are reported at info level.',
    optionsFootnote:
      'Changes apply to the next scan.',
    builtinDefaults: 'built-in defaults',
    readyOf: '{ready} of {total} ready',
    ready: 'ready',
    unavailable: 'unavailable',
    needs: 'needs {tools}',
    notProbed: '(not probed)',
    detectsTools:
      'Fizzra detects these tools on PATH at startup. Installing one needs no configuration — restart Fizzra and it becomes available.',
    globalOnlyNote:
      'These results are global. Tools a project ships itself (biome, tsc and friends under node_modules/.bin) do not appear here — open the project and press Scan to get the list for that project specifically.',
    usedBy: 'used by {analyzers}',
    toolHints: {
      oxlint:
        'Installed globally through npm; needs Node.js on the machine:\n' +
        '1. Run the command above (or `brew install oxlint`);\n' +
        '2. Make sure npm\'s global install directory is on the system PATH environment variable (`npm config get prefix` shows it; on Windows it is usually `%APPDATA%\\npm`);\n' +
        '3. Restart Fizzra so it probes again.\n' +
        'oxlint is not a Go module, so `go install` will not work. It checks out of the box with no configuration in the project.',
      biome:
        'Installed globally through npm; needs Node.js on the machine:\n' +
        '1. Run the command above;\n' +
        '2. Make sure npm\'s global install directory is on the system PATH environment variable (`npm config get prefix` shows it; on Windows it is usually `%APPDATA%\\npm`);\n' +
        '3. Restart Fizzra so it probes again.\n' +
        'The package name is scoped: the unscoped `biome` on npm is an unrelated, long-abandoned 0.3.x release.',
      tsc:
        'This analyzer runs the compiler of the project being scanned, not a globally installed one:\n' +
        '1. Run the command above inside that project;\n' +
        '2. If the project has `.vue` files, install `vue-tsc` as well (`npm install --save-dev vue-tsc`);\n' +
        '3. The project needs a `tsconfig.json` (or `jsconfig.json`); type checking follows its strictness settings.\n' +
        'It is not installed globally on purpose: a different compiler would report something other than what that project sees at build time.',
      pmd:
        'Ships as a zip, so nothing installs it for you:\n' +
        '1. Download `pmd-dist-*.zip` from the address above;\n' +
        '2. Unzip it anywhere;\n' +
        '3. Add the unzipped `pmd-bin-*/bin` to the system PATH environment variable, or point `FIZZRA_PMD_HOME` at the `pmd-bin-*` folder.\n' +
        'Requires a JDK 17 or newer on PATH.',
      spotbugs:
        'Ships as a zip, so nothing installs it for you:\n' +
        '1. Download `spotbugs-*.zip` from the address above;\n' +
        '2. Unzip it anywhere;\n' +
        '3. Add the unzipped `spotbugs-*/bin` to the system PATH environment variable, or point `FIZZRA_SPOTBUGS_HOME` at the `spotbugs-*` folder.\n' +
        'Requires a JDK 17 or newer on PATH. It analyses compiled output, so build the project before scanning.',
      cppcheck:
        'Download the latest `.msi` from the Releases page at the address above and run it:\n' +
        'The installer adds cppcheck to the system PATH environment variable; restart Fizzra afterwards so it probes again.\n' +
        'Needs no compilation database and no build step.',
      'clang-tidy':
        'Installed through pip, so the machine needs Python first:\n' +
        '1. Run the command above;\n' +
        '2. Make sure Python\'s Scripts folder is on the system PATH environment variable;\n' +
        '3. Restart Fizzra so it probes again.\n' +
        'Analysing C++ also needs a C/C++ standard library on the machine (Visual Studio or MinGW provides one); plain C does not.',
      roslynator:
        'Installed as a .NET global tool; needs the .NET SDK 8 or newer first:\n' +
        '1. Run the command above;\n' +
        '2. Make sure `%USERPROFILE%\\.dotnet\\tools` is on the system PATH environment variable (`dotnet tool install --global` normally adds it);\n' +
        '3. Restart Fizzra so it probes again.\n' +
        'Adds no NuGet package to the project and does not modify the `.csproj`.',
      phpstan:
        'Download `phpstan.phar` from the Releases page at the address above; needs PHP 7.4 or newer on the machine:\n' +
        '1. Put `phpstan.phar` in any directory and add that directory to the system PATH environment variable so it can be called globally;\n' +
        '2. Make sure the PHP interpreter is also on the system PATH environment variable (`php -v` must report 7.4 or newer);\n' +
        '3. Restart Fizzra so it probes again.\n' +
        'Composer works too: `composer global require phpstan/phpstan`.',
    },
    allAvailable: 'External tools on PATH are ready',
    allAvailableSub:
      'That does not mean every project can run every check: tools installed per project are supplied by the project.',
    ciBody:
      'The CI command runs the same analyzers and applies the same quality gate, so a green build means the same thing locally and in the pipeline.',
    ciNote: 'The GitLab template also ships a non-blocking merge-request advisory job.',
  },

  issueDetail: {
    backToProject: 'Back to project',
    projectNav: 'Project',
    whyThisMatters: 'Why this matters',
    location: 'Location',
    source: 'Source',
    details: 'Details',
    toolOutput: 'Analyzer output',
    detected: 'Detected',
    firstSeen: 'First seen',
    analyzer: 'Analyzer',
    rule: 'Rule',
    fingerprint: 'Fingerprint',
    newInScan: 'new in this scan',
    knownFromBefore: 'present in the previous scan too',
    fingerprintNote:
      'The fingerprint is stable across scans, so an unchanged finding is recognised even when its line number moves.',
    ruleDocs: 'Rule documentation ↗',
    ignore: 'Ignore',
    falsePositive: 'False positive',
    reopen: 'Reopen',
    notFound: 'Issue not found.',
    copyLocation: 'location',
    copyFingerprint: 'fingerprint',
    copiedLabel: 'Copied {label}',
    markedAs: 'Marked as {status}.',
    reopened: 'Issue reopened.',
  },

  fix: {
    heading: 'Suggested change',
    before: 'Before',
    after: 'After',
  },

  chart: {
    scoreHistory: 'Score history',
    completedAnalyses: '{count} completed analysis, oldest to newest. | {count} completed analyses, oldest to newest.',
    issueMix: 'Issue mix over time',
    issueMixSub: 'Stacked counts per analysis.',
    noAnalyses: 'No completed analyses yet. Run a scan to see the trend.',
    seriesScore: 'Score',
    tooltipScore: 'Score: <strong>{score}</strong> / 100',
    tooltipIssues: 'Issues: {total} ({critical} critical, {errors} high)',
    tooltipGate: 'Gate: {status}',
    gateMarkLine: 'gate {score}',
    severityByIssues: 'Issues by severity',
    noIssues: 'No issues',
    countBySeverity: '{count} {severity}',
  },

  // 内置规则文案按 model.Rule.Name 索引，不能放 Go 侧：Message 参与指纹计算，改语言会破坏新旧问题对比
  // 未收录的规则回退服务端英文。fix 代码示例保持英文
  rule: {
    'no-panic': {
      message: 'panic() called outside initialisation',
      advice: 'Return an error instead of panicking; a library has no right to take the process down.',
      fixNote: 'The caller decides how to handle the failure; panicking removes that choice.',
    },
    'defer-in-loop': {
      message: 'defer inside a loop body',
      advice: 'The deferred call runs when the enclosing function returns, not each iteration. Extract the body into a function, or close explicitly.',
      fixNote: 'Moving the body into its own function makes the defer run once per call.',
    },
    'context-background-in-request-path': {
      message: 'context.Background() used on a request path',
      advice: 'Derive the context from the incoming request so cancellation and deadlines propagate.',
      fixNote: 'The request context carries the client deadline and cancels with the connection.',
    },
    'goroutine-captures-loopvar': {
      message: 'goroutine closes over a loop variable',
      advice: 'Pass the value as an argument or shadow it inside the loop; otherwise every goroutine may read the final value.',
      fixNote: 'The parameter is evaluated per iteration, so each goroutine sees its own value.',
    },
    'mutex-by-value': {
      message: 'sync.Mutex copied by value',
      advice: 'Pass a pointer. Copying a mutex silently duplicates its state and the lock stops protecting anything.',
      fixNote: 'Taking a pointer keeps one mutex; the copied version locks an independent lock.',
    },
    'sleep-in-loop': {
      message: 'time.Sleep inside a loop',
      advice: 'Use a ticker or back off with a context-aware timer so the loop can be cancelled.',
      fixNote: 'A ticker with a context select remains interruptible; Sleep does not.',
    },
    'discarded-error': {
      message: 'error return value discarded',
      advice: 'Handle the error, return it, or log it with context. Silent failure is the hardest kind to debug.',
      fixNote: 'Wrapping the error preserves the chain while making the failure visible.',
    },
    'error-shadowed-by-nil': {
      message: 'error variable shadowed by nil',
      advice: 'Declare the error in the outer scope and assign with =, not :=, or the outer error stays unset.',
      fixNote: 'The outer `err` is assigned rather than shadowed, so the check sees the real error.',
    },
    'command-injection': {
      message: 'user input passed into a shell command',
      advice: 'Pass discrete arguments and never route input through sh -c.',
      fixNote: 'Arguments are passed as a list; no shell is involved, so metacharacters are inert.',
    },
    'weak-random': {
      message: 'math/rand used for a security-sensitive value',
      advice: 'Use crypto/rand. math/rand is seeded predictably and is unsuitable for tokens, keys or nonces.',
      fixNote: 'crypto/rand reads from the OS entropy source.',
    },
    'broken-hash': {
      message: 'cryptographically broken hash function',
      advice: 'Use SHA-256 or stronger for integrity, and bcrypt/argon2 for passwords.',
      fixNote: 'SHA-256 is collision-resistant; MD5 and SHA-1 are not.',
    },
    'insecure-tls': {
      message: 'TLS verification disabled',
      advice: 'Remove InsecureSkipVerify. If a self-signed certificate is the reason, pin the CA instead.',
      fixNote: 'Verifying against a pinned CA pool keeps the check without trusting everything.',
    },
    'hardcoded-credential': {
      message: 'credential or secret hard-coded in source',
      advice: 'Read secrets from the environment or a secret manager; a committed secret must be rotated.',
      fixNote: 'The value comes from the environment, so the repository holds no secret.',
    },
    'sql-injection': {
      message: 'SQL built by string concatenation',
      advice: 'Use a parameterised query; never interpolate values into SQL text.',
      fixNote: 'The placeholder keeps the value out of the SQL grammar entirely.',
    },
    'unsafe-file-permissions': {
      message: 'file written with overly permissive mode',
      advice: 'Use 0600 for files that may hold sensitive data, and 0750 or tighter for directories.',
      fixNote: '0600 is readable only by the owner.',
    },
    'unsafe-pointer': {
      message: 'use of unsafe.Pointer',
      advice: 'Avoid unsafe unless there is no alternative; document the invariant that makes the conversion sound.',
      fixNote: 'A typed conversion avoids bypassing the type system.',
    },
    'wails-events-emit-in-loop': {
      message: 'EventsEmit called inside a loop',
      advice: 'Batch the payloads and emit once; per-iteration emits flood the JS bridge and stall the UI.',
      fixNote: 'One emit with the whole slice keeps the bridge traffic constant.',
    },
    'wails-dialog-unchecked': {
      message: 'dialog result discarded',
      advice: 'dialog returns (result, error). Treating a cancelled dialog as confirmation deletes data the user meant to keep.',
      fixNote: 'The result is checked, so a cancelled dialog does nothing.',
    },
    'wails-browser-open-url': {
      message: 'BrowserOpenURL called with an unvalidated URL',
      advice: 'Validate the scheme against an allow-list. file:// and custom schemes reach local handlers, so an attacker-controlled link is local code execution.',
      fixNote: 'Rejecting anything that is not http(s) keeps the URL out of local handlers.',
    },
    'wails-frontend-hardcoded-path': {
      message: 'frontend path hard-coded in Go source',
      advice: 'Resolve the path relative to the executable or the embedded assets so it survives packaging.',
      fixNote: 'The asset is embedded, so the path is valid in the packaged binary too.',
    },
    'wails-wailsjson-legacy-fields': {
      message: 'wails.json contains fields from an older format',
      advice: 'Remove the deprecated keys and use the v2 schema; stale fields are ignored silently at build time.',
      fixNote: 'The v2 schema is the one the toolchain actually reads.',
    },
    'unresolved-marker': {
      message: 'unresolved marker left in source',
      advice: 'Resolve the marker or turn it into a tracked issue; unowned markers accumulate indefinitely.',
      fixNote: 'A reference to a real issue keeps the follow-up visible.',
    },
    'commented-out-code': {
      message: 'commented-out code',
      advice: 'Delete it. Version control remembers the old version, and commented code drifts out of date silently.',
      fixNote: 'The history is in the repository; the file stays readable.',
    },
    'debug-output-left': {
      message: 'debug output left in library code',
      advice: 'Use the project logger so output can be filtered by level, or remove the call.',
      fixNote: 'A leveled logger keeps diagnostics available without writing to stdout.',
    },
  },
} as const

export default en
