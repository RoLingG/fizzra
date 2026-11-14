// 与 Go 接口模型手工同步，改 Go 侧 JSON 字段时须一并修改

export type Severity = 'critical' | 'error' | 'warning' | 'info'

export type AnalysisStatus = 'pending' | 'running' | 'success' | 'failed' | 'canceled'

export type GateStatus = 'passed' | 'failed' | 'skipped'

export type ProjectLanguage = 'go' | 'rust' | 'python' | 'javascript' | 'java' | 'csharp' | 'cpp' | 'php'

// 与语言正交（Wails 应用是 go + wails），多数项目为空
export type ProjectKind = 'wails' | ''

export type IssueStatus = 'open' | 'ignored' | 'false_positive'

export interface Envelope<T> {
  data?: T
  meta?: {
    total?: number
    limit?: number
    offset?: number
  }
  error?: {
    code: string
    message: string
  }
}

export interface Project {
  id: string
  name: string
  description: string
  repoUrl: string
  branch: string
  localPath: string
  language: ProjectLanguage | string
  kind: ProjectKind | string
  color: string
  defaultAnalyzers: string[] | null
  lastAnalysisId: string
  createdAt: string
  updatedAt: string
}

export interface AnalysisStats {
  runs: number
  avgScore: number
  bestScore: number
  worstScore: number
  passedRuns: number
  failedRuns: number
}

export interface ProjectSummary extends Project {
  latest?: Analysis
  inFlight?: Analysis
  stats: AnalysisStats
}

export interface ComplexFunction {
  file: string
  name: string
  line: number
  complexity: number
  length: number
  params: number
  receiver?: string
}

export interface DuplicateBlock {
  lines: number
  tokens: number
  files: string[] | null
  snippet: string
}

export interface FileSize {
  file: string
  lines: number
  code: number
  isTest: boolean
}

export interface Metrics {
  files: number
  lines: number
  codeLines: number
  commentLines: number
  blankLines: number
  functions: number
  avgComplexity: number
  maxComplexity: number
  complexFunctions: ComplexFunction[] | null
  longFunctions: ComplexFunction[] | null
  duplication: number
  duplicateBlocks: DuplicateBlock[] | null
  coverage: number
  testFiles: number
  testFunctions: number
  largestFiles: FileSize[] | null
}

export interface Analysis {
  id: string
  projectId: string
  status: AnalysisStatus
  trigger: string
  branch: string
  commit: string
  path: string
  analyzers: string[] | null
  progress: number
  startedAt: string
  finishedAt: string | null
  durationMs: number
  error: string
  totalIssues: number
  criticalIssues: number
  errorIssues: number
  warningIssues: number
  infoIssues: number
  score: number
  gateStatus: GateStatus
  gateReason: string
  metrics: Metrics
  createdAt: string

  // 有分析器没跑完时为 true：漏跑的分析器让发现的更少，分数反而虚高，界面要据此提醒
  incomplete: boolean
  /** 出错或超时的分析器 */
  failedAnalyzers: string[] | null
  /** 与 failedAnalyzers 一一对应，顺序相同 */
  failureReasons: string[] | null
  // 与 failedAnalyzers 分开：失败要改项目，跳过通常是装工具
  skippedAnalyzers: string[] | null
  /** 与 skippedAnalyzers 一一对应，顺序相同 */
  skipReasons: string[] | null
  /** 已去重，多个分析器可能缺同一个工具 */
  skippedTools: string[] | null
  // 所有选中的分析器都被跳过时为 true，即什么都没检查
  noAnalyzerRan: boolean
}

// before/after 为空的表示改法依赖上下文，无法给出示例
export interface FixExample {
  before: string
  after: string
  note?: string
  language?: string
}

export interface Issue {
  id: string
  analysisId: string
  projectId: string
  analyzer: string
  rule: string
  severity: Severity
  message: string
  advice: string
  fix?: FixExample | null
  file: string
  line: number
  column: number
  code: string
  docUrl: string
  fingerprint: string
  /** 上一次成功分析没有匹配的指纹时为 true */
  isNew: boolean
  status: IssueStatus
  createdAt: string
  // 'direct'/'transitive'（依赖通告）或空（源码发现），驱动标签和措辞
  origin: string
  // 最外层在前，直接依赖和源码发现时为空
  dependencyPath: string[] | null
}

export interface Rule {
  id: string
  name: string
  analyzer: string
  severity: Severity
  message: string
  advice: string
  fix?: FixExample | null
  patterns: string[] | null
  paths: string[] | null
  exclude: string[] | null
  requireLoop: boolean
  forbidInLoop: boolean
  enabled: boolean
  builtin: boolean
  createdAt: string
  updatedAt: string
}

export interface QualityGate {
  id: string
  projectId: string
  minScore: number
  maxCritical: number
  maxHigh: number
  maxComplexity: number
  minCoverage: number
  failOnWarning: boolean
  enabled: boolean
  updatedAt: string
}

export interface TrendPoint {
  analysisId: string
  at: string
  score: number
  total: number
  critical: number
  errors: number
  warnings: number
  gateStatus: GateStatus
}

export interface IssueStats {
  total: number
  bySeverity: Record<Severity, number>
  byAnalyzer: Record<string, number>
  topRules: Array<{ rule: string; analyzer: string; severity: string; count: number }>
}

export interface ProjectOverview {
  project: Project
  latest?: Analysis
  issueStats?: IssueStats
  history?: Analysis[]
  trend?: TrendPoint[]
  gate?: QualityGate
  stats?: AnalysisStats
}

export interface ScanPreflight {
  project: string
  language: string
  analyzers: string[]
  /** 为空即可直接开跑 */
  blocked: BlockedAnalyzer[]
}

export interface BlockedAnalyzer {
  name: string
  description: string
  /** 具体缺哪些可执行文件 */
  missingTools: string[] | null
  /** 由分析器声明。从工具名正推会给 npm 包塞出 `go install` 之类错命令 */
  installHint?: string
}

// 由分析器自己声明
export interface AnalyzerOption {
  key: string
  /** 未配置时的取值 */
  default: boolean
  description: string
}

export interface AnalyzerReadiness {
  name: string
  description: string
  category: string
  requires: string[] | null
  ready: boolean
  missingTools: string[] | null
  /** 适用的语言，空表示与语言无关 */
  languages: string[]
  /** 由分析器声明的安装方法 */
  installHint?: string
  /** 该分析器暴露的设置，没有时为空 */
  options?: AnalyzerOption[] | null
}

export interface ToolStatus {
  name: string
  available: boolean
  path?: string
  error?: string
}

export interface SystemInfo {
  version: string
  mode: string
  database: string
  workspace: string
  configFile: string
  runningTasks: number
  analyzers: AnalyzerReadiness[]
  tools: Record<string, ToolStatus>
  ruleCount: number
  projectCount: number
  /** 分析器被禁止联网时为 true */
  offline: boolean
  /** 缺的键表示用分析器声明的默认值 */
  analyzerOptions?: Record<string, Record<string, string>> | null
  /** 用于在图表上画门禁线 */
  minScore: number
}

export interface Progress {
  percent: number
  stage: string
}

export interface AnalysisDetail {
  analysis: Analysis
  progress?: Progress
}

export interface ScoreDimension {
  name: string
  score: number
  weight: number
  detail: string
}

export interface CreateProjectInput {
  name: string
  description?: string
  repoUrl?: string
  branch?: string
  localPath?: string
  kind?: ProjectKind
  color?: string
  analyzers?: string[]
}

export interface IssueQuery {
  severity?: string[]
  analyzer?: string[]
  rule?: string[]
  status?: string[]
  newOnly?: boolean
  file?: string
  search?: string
  orderBy?: string
  limit?: number
  offset?: number
}
