import axios, { AxiosError, type AxiosInstance } from 'axios'
import { ElMessage } from 'element-plus'

import { i18n } from '@/locales'

import type {
  Analysis,
  AnalysisDetail,
  AnalyzerReadiness,
  CreateProjectInput,
  Envelope,
  Issue,
  IssueQuery,
  IssueStats,
  Project,
  ProjectOverview,
  ProjectSummary,
  QualityGate,
  Rule,
  ScanPreflight,
  SystemInfo,
  TrendPoint,
} from './types'

// 调用方只拿数据或 ApiError，永远不碰信封
const http: AxiosInstance = axios.create({
  baseURL: '/api/v1',
  timeout: 120_000,
  headers: { 'Content-Type': 'application/json' },
})

export class ApiError extends Error {
  code: string
  status: number

  constructor(message: string, code: string, status: number) {
    super(message)
    this.name = 'ApiError'
    this.code = code
    this.status = status
  }
}

function unwrap<T>(payload: Envelope<T>): T {
  if (payload.error) {
    throw new ApiError(payload.error.message, payload.error.code, 0)
  }
  return payload.data as T
}

http.interceptors.response.use(
  (response) => response,
  async (error: AxiosError<Envelope<unknown>>) => {
    // blob 下载出错时 data 是 Blob 不是信封，直接读 .error 只会得到一句泛化的网络错误
    let body = error.response?.data as Envelope<unknown> | Blob | undefined
    if (body instanceof Blob) {
      try {
        body = JSON.parse(await body.text()) as Envelope<unknown>
      } catch {
        body = undefined
      }
    }
    // 优先用服务端的结构化错误，没有则回退传输层
    const envelope = body as Envelope<unknown> | undefined
    if (envelope?.error) {
      return Promise.reject(new ApiError(envelope.error.message, envelope.error.code, error.response?.status ?? 0))
    }
    const message =
      error.code === 'ECONNABORTED'
        ? i18n.global.t('errors.timeout')
        : error.message || i18n.global.t('errors.network')
    return Promise.reject(new ApiError(message, 'network_error', error.response?.status ?? 0))
  },
)

/** 按 error.code 查翻译，服务端英文文本只作回退 */
export function reportError(error: unknown, fallback?: string) {
  const message =
    error instanceof ApiError
      ? messageForCode(error.code) ?? error.message
      : (fallback ?? i18n.global.t('errors.generic'))
  ElMessage({ type: 'error', message, duration: 6000, showClose: true })
}

function messageForCode(code: string): string | undefined {
  const map: Record<string, string> = {
    not_found: 'errors.notFound',
    conflict: 'errors.conflict',
    invalid_input: 'errors.invalidInput',
    internal_error: 'errors.internalError',
    queue_full: 'errors.queueFull',
  }
  const key = map[code]
  return key ? i18n.global.t(key) : undefined
}

export const systemApi = {
  async info(): Promise<SystemInfo> {
    const { data } = await http.get<Envelope<SystemInfo>>('/system/info')
    return unwrap(data)
  },
  async analyzers(): Promise<AnalyzerReadiness[]> {
    const { data } = await http.get<Envelope<AnalyzerReadiness[]>>('/analyzers')
    return unwrap(data) ?? []
  },
  /** 联网开关是服务端设置，漏洞通告库由跑扫描的机器去取 */
  async setOffline(enabled: boolean): Promise<void> {
    await http.put('/system/offline', { enabled })
  },
  /** 提交整份配置映射 */
  async setAnalyzerOptions(name: string, options: Record<string, string>): Promise<void> {
    await http.put(`/system/analyzers/${encodeURIComponent(name)}/options`, options)
  },
}

export const projectApi = {
  async list(): Promise<ProjectSummary[]> {
    const { data } = await http.get<Envelope<ProjectSummary[]>>('/projects')
    return unwrap(data) ?? []
  },

  async get(id: string): Promise<Project> {
    const { data } = await http.get<Envelope<Project>>(`/projects/${encodeURIComponent(id)}`)
    return unwrap(data)
  },

  async overview(id: string): Promise<ProjectOverview> {
    const { data } = await http.get<Envelope<ProjectOverview>>(`/projects/${encodeURIComponent(id)}/overview`)
    return unwrap(data)
  },

  async create(input: CreateProjectInput): Promise<Project> {
    const { data } = await http.post<Envelope<Project>>('/projects', input)
    return unwrap(data)
  },

  async update(id: string, input: Partial<CreateProjectInput>): Promise<Project> {
    const { data } = await http.patch<Envelope<Project>>(`/projects/${encodeURIComponent(id)}`, input)
    return unwrap(data)
  },

  async remove(id: string): Promise<void> {
    await http.delete(`/projects/${encodeURIComponent(id)}`)
  },

  /** 返回实际删掉的 ID。允许部分成功，某个 ID 可能已被别的标签页删掉 */
  async removeMany(ids: string[]): Promise<string[]> {
    const { data } = await http.delete<Envelope<{ deleted: string[] }>>('/projects', {
      data: { ids },
    })
    return unwrap(data).deleted ?? []
  },

  async trend(id: string, limit = 30): Promise<TrendPoint[]> {
    const { data } = await http.get<Envelope<TrendPoint[]>>(`/projects/${encodeURIComponent(id)}/trend`, {
      params: { limit },
    })
    return unwrap(data) ?? []
  },

  async getGate(id: string): Promise<QualityGate> {
    const { data } = await http.get<Envelope<QualityGate>>(`/projects/${encodeURIComponent(id)}/gate`)
    return unwrap(data)
  },

  async saveGate(id: string, gate: Partial<QualityGate>): Promise<QualityGate> {
    const { data } = await http.put<Envelope<QualityGate>>(`/projects/${encodeURIComponent(id)}/gate`, gate)
    return unwrap(data)
  },

  /** 入队前调用，blocked 为空即可直接开跑 */
  async preflight(id: string): Promise<ScanPreflight> {
    const { data } = await http.get<Envelope<ScanPreflight>>(
      `/projects/${encodeURIComponent(id)}/preflight`,
    )
    return unwrap(data)
  },
}

export const analysisApi = {
  async start(projectId: string, analyzers?: string[]): Promise<Analysis> {
    const { data } = await http.post<Envelope<Analysis>>('/analyses', {
      projectId,
      analyzers: analyzers?.length ? analyzers : undefined,
      trigger: 'manual',
    })
    return unwrap(data)
  },

  async get(id: string): Promise<AnalysisDetail> {
    const { data } = await http.get<Envelope<AnalysisDetail>>(`/analyses/${encodeURIComponent(id)}`)
    return unwrap(data)
  },

  async listForProject(projectId: string, limit = 50): Promise<Analysis[]> {
    const { data } = await http.get<Envelope<Analysis[]>>(`/projects/${encodeURIComponent(projectId)}/analyses`, {
      params: { limit },
    })
    return unwrap(data) ?? []
  },

  async stats(id: string): Promise<IssueStats> {
    const { data } = await http.get<Envelope<IssueStats>>(`/analyses/${encodeURIComponent(id)}/stats`)
    return unwrap(data)
  },

  async issues(id: string, query: IssueQuery = {}): Promise<{ issues: Issue[]; total: number }> {
    // 数组以逗号分隔发送，与 Go 侧 handler 的 queryList 解析一致
    const params: Record<string, string | number> = {}
    if (query.severity?.length) params.severity = query.severity.join(',')
    if (query.analyzer?.length) params.analyzer = query.analyzer.join(',')
    if (query.rule?.length) params.rule = query.rule.join(',')
    if (query.status?.length) params.status = query.status.join(',')
    if (query.newOnly) params.newOnly = 'true'
    if (query.file) params.file = query.file
    if (query.search) params.search = query.search
    if (query.orderBy) params.orderBy = query.orderBy
    params.limit = query.limit ?? 50
    params.offset = query.offset ?? 0

    const { data } = await http.get<Envelope<Issue[]>>(`/analyses/${encodeURIComponent(id)}/issues`, { params })
    return { issues: unwrap(data) ?? [], total: data.meta?.total ?? 0 }
  },

  /** 走 blob 下载而非 window.open。Accept-Language 显式发，用面板语言而非系统语言 */
  async downloadReport(projectId: string, analysisId: string, format: ReportFormat = 'html'): Promise<void> {
    const response = await http.get(
      `/projects/${encodeURIComponent(projectId)}/report/${encodeURIComponent(analysisId)}`,
      {
        params: { format },
        responseType: 'blob',
        headers: { 'Accept-Language': i18n.global.locale.value },
      },
    )
    saveBlob(
      response.data,
      filenameFrom(response.headers['content-disposition'], `fizzra-report.${format}`),
    )
  },
}

/** 与后端 report.Format 一一对应 */
export type ReportFormat = 'html' | 'json' | 'sarif'

/** 认 filename*=UTF-8''…（中文项目名走这条）和 filename="…" 两种写法，缺失时用 fallback */
function filenameFrom(header: string | undefined, fallback: string): string {
  if (!header) return fallback

  const extended = /filename\*=UTF-8''([^;]+)/i.exec(header)
  if (extended) {
    try {
      return decodeURIComponent(extended[1])
    } catch {
      // 编码坏了就退回普通形式，不值得为此让下载失败
    }
  }

  const plain = /filename="?([^";]+)"?/i.exec(header)
  return plain ? plain[1] : fallback
}

/** 触发下载并释放临时地址 */
function saveBlob(blob: Blob, filename: string) {
  const url = URL.createObjectURL(blob)
  const link = document.createElement('a')
  link.href = url
  link.download = filename
  document.body.appendChild(link)
  link.click()
  link.remove()
  setTimeout(() => URL.revokeObjectURL(url), 0)
}

export const issueApi = {
  async get(id: string): Promise<Issue> {
    const { data } = await http.get<Envelope<Issue>>(`/issues/${encodeURIComponent(id)}`)
    return unwrap(data)
  },

  async setStatus(id: string, status: string): Promise<Issue> {
    const { data } = await http.patch<Envelope<Issue>>(`/issues/${encodeURIComponent(id)}`, { status })
    return unwrap(data)
  },
}

export const ruleApi = {
  async list(): Promise<Rule[]> {
    const { data } = await http.get<Envelope<Rule[]>>('/rules')
    return unwrap(data) ?? []
  },

  async create(input: Partial<Rule>): Promise<Rule> {
    const { data } = await http.post<Envelope<Rule>>('/rules', input)
    return unwrap(data)
  },

  async update(id: string, input: Partial<Rule>): Promise<Rule> {
    const { data } = await http.put<Envelope<Rule>>(`/rules/${encodeURIComponent(id)}`, input)
    return unwrap(data)
  },

  async remove(id: string): Promise<void> {
    await http.delete(`/rules/${encodeURIComponent(id)}`)
  },

  async toggle(id: string, enabled: boolean): Promise<Rule> {
    const { data } = await http.post<Envelope<Rule>>(`/rules/${encodeURIComponent(id)}/toggle`, { enabled })
    return unwrap(data)
  },
}

export default http
