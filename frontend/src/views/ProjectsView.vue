<script setup lang="ts">
import { computed, onMounted, reactive, ref } from 'vue'
import { useRouter } from 'vue-router'
import { ElMessage, type FormInstance, type FormRules } from 'element-plus'
import { useI18n } from 'vue-i18n'

import { projectApi, reportError, systemApi } from '@/api/client'
import type {
  Analysis,
  AnalyzerReadiness,
  ProjectKind,
  ProjectLanguage,
  ProjectSummary,
} from '@/api/types'
import { scoreClass, projectFramework, statusTagType } from '@/api/format'
import { useFormat } from '@/composables/useFormat'
import { useScanEvents } from '@/composables/useScanEvents'

const router = useRouter()
const { t } = useI18n()

const { formatRelative, languageLabel } = useFormat()

// 语言决定运行哪些分析器
const languageOptions: ProjectLanguage[] = ['go', 'rust', 'python', 'javascript', 'java', 'csharp', 'cpp', 'php']

const kindOptions = [
  { value: '' as ProjectKind, title: 'kindGo', note: 'kindGoNote' },
  { value: 'wails' as ProjectKind, title: 'kindWails', note: 'wailsNote' },
]

const loading = ref(true)
const projects = ref<ProjectSummary[]>([])
const analyzers = ref<AnalyzerReadiness[]>([])

const dialogOpen = ref(false)
const saving = ref(false)
const editingId = ref<string | null>(null)
const formRef = ref<FormInstance>()
const step = ref(1)
const kindGroup = ref<HTMLElement>()

const confirmOpen = ref(false)
const confirmTitle = ref('')
const confirmBody = ref('')
const confirmWord = ref('')
const confirmTyped = ref('')
let confirmResolve: ((ok: boolean) => void) | null = null

function askConfirm(title: string, body: string, word = ''): Promise<boolean> {
  confirmTitle.value = title
  confirmBody.value = body
  confirmWord.value = word
  confirmTyped.value = ''
  confirmOpen.value = true
  return new Promise((resolve) => {
    confirmResolve = resolve
  })
}

function settleConfirm(ok: boolean) {
  confirmOpen.value = false
  confirmResolve?.(ok)
  confirmResolve = null
}

const form = reactive({
  name: '',
  description: '',
  // 两个来源二选一，sourceType 决定提交哪一个
  sourceType: 'local' as 'local' | 'repo',
  repoUrl: '',
  branch: '',
  localPath: '',
  language: 'go' as ProjectLanguage,
  kind: '' as ProjectKind,
  color: '#3b82f6',
  analyzers: [] as string[],
})

// 切换来源时清空另一侧，避免提交残留的路径或地址
function onSourceChange(type: 'local' | 'repo') {
  if (type === 'local') {
    form.repoUrl = ''
    form.branch = ''
  } else {
    form.localPath = ''
  }
}

// 只保留适用于当前语言的分析器，空 languages 表示与语言无关
const languageAnalyzers = computed(() =>
  analyzers.value.filter(
    (a) => a.languages.length === 0 || a.languages.includes(form.language),
  ),
)

// 换语言后清掉不再适用分析器，否则会静默跑出一份零问题报告
function onLanguageChange(lang: string) {
  form.analyzers = form.analyzers.filter((name) => {
    const a = analyzers.value.find((x) => x.name === name)
    return !a || a.languages.length === 0 || a.languages.includes(lang)
  })
  if (lang !== 'go') form.kind = ''
}

// 用 computed 才能在切换语言后重建
const rules = computed<FormRules>(() => ({
  name: [
    { required: true, message: t('projectsView.validateNameRequired'), trigger: 'blur' },
    { min: 2, message: t('projectsView.validateNameLength'), trigger: 'blur' },
  ],
  localPath: [
    {
      validator: (_rule, value, callback) => {
        if (form.sourceType === 'local' && !value) {
          callback(new Error(t('projectsView.validateSource')))
          return
        }
        callback()
      },
      trigger: 'blur',
    },
  ],
  repoUrl: [
    {
      validator: (_rule, value, callback) => {
        if (form.sourceType === 'repo' && !value) {
          callback(new Error(t('projectsView.validateSource')))
          return
        }
        callback()
      },
      trigger: 'blur',
    },
  ],
}))

async function load() {
  loading.value = true
  try {
    const [list, analyzerList] = await Promise.all([projectApi.list(), systemApi.analyzers()])
    projects.value = list
    analyzers.value = analyzerList
  } catch (error) {
    reportError(error, t('errors.loadProjects'))
  } finally {
    loading.value = false
  }
}

function openDialog() {
  step.value = 1
  dialogOpen.value = true
  formRef.value?.clearValidate()
}

function openCreate() {
  editingId.value = null
  Object.assign(form, {
    name: '',
    description: '',
    sourceType: 'local',
    repoUrl: '',
    branch: '',
    localPath: '',
    language: 'go',
    kind: '',
    color: '#3b82f6',
    analyzers: [],
  })
  openDialog()
}

function openEdit(project: ProjectSummary) {
  editingId.value = project.id
  Object.assign(form, {
    name: project.name,
    description: project.description,
    sourceType: project.repoUrl ? 'repo' : 'local',
    repoUrl: project.repoUrl,
    branch: project.branch,
    localPath: project.localPath,
    language: (project.language || 'go') as ProjectLanguage,
    // 历史数据的 kind 字段里存的是语言，经 projectFramework 归一后视为无框架
    kind: projectFramework(project.kind),
    color: project.color || '#3b82f6',
    analyzers: project.defaultAnalyzers ?? [],
  })
  openDialog()
}

function stepKind(delta: number) {
  const index = kindOptions.findIndex((option) => option.value === form.kind)
  const nextIndex = (index + delta + kindOptions.length) % kindOptions.length
  form.kind = kindOptions[nextIndex].value
  kindGroup.value?.querySelectorAll<HTMLElement>('.choice')[nextIndex]?.focus()
}

async function goStep2() {  const field = form.sourceType === 'repo' ? 'repoUrl' : 'localPath'
  try {
    await formRef.value?.validateField(['name', field])
  } catch {
    return
  }
  step.value = 2
}

async function save() {
  const valid = await formRef.value?.validate().catch(() => false)
  if (!valid) return

  saving.value = true
  try {
    const isRepo = form.sourceType === 'repo'
    const payload = {
      name: form.name.trim(),
      description: form.description.trim(),
      // 只提交选中那一侧的来源，另一侧发空串以清掉旧值
      repoUrl: isRepo ? form.repoUrl.trim() : '',
      branch: isRepo ? form.branch.trim() || 'main' : '',
      localPath: isRepo ? '' : form.localPath.trim(),
      language: form.language,
      kind: form.kind,
      color: form.color,
      analyzers: form.analyzers,
    }

    if (editingId.value) {
      await projectApi.update(editingId.value, payload)
      ElMessage.success(t('projectsView.updated'))
    } else {
      await projectApi.create(payload)
      ElMessage.success(t('projectsView.registered'))
    }
    dialogOpen.value = false
    await load()
  } catch (error) {
    reportError(error, t('errors.saveProject'))
  } finally {
    saving.value = false
  }
}

async function remove(project: ProjectSummary) {
  const ok = await askConfirm(
    t('projectsView.deleteTitle'),
    t('projectsView.deleteBody', { name: project.name }),
  )
  if (!ok) return

  try {
    await projectApi.remove(project.id)
    ElMessage.success(t('projectsView.deleted'))
    await load()
  } catch (error) {
    reportError(error, t('errors.deleteProject'))
  }
}

// 正在进行中的分析，列表在这一列显示扫描中而不是时间
function isScanning(p: ProjectSummary): boolean {
  const st = p.inFlight?.status
  return st === 'pending' || st === 'running'
}

// 缺失值不计入排序方向，用 sortable="custom" 自行排序
type SortField = 'name' | 'language' | 'source' | 'score' | 'issues' | 'lastScan' | 'gate'

function sourceOf(p: ProjectSummary): string {
  return p.localPath || p.repoUrl || ''
}

function scanTime(p: ProjectSummary): number | null {
  if (!p.latest?.createdAt) return null
  const t = new Date(p.latest.createdAt).getTime()
  return Number.isNaN(t) ? null : t
}

// 失败在前、通过在后，未扫描与未跑门禁同权
function gateRank(latest: Analysis | undefined): number {
  switch (latest?.gateStatus) {
    case 'failed':
      return 0
    case 'passed':
      return 2
    default:
      return 1
  }
}

const sortKeys: Record<SortField, (p: ProjectSummary) => string | number | null> = {
  name: (p) => p.name,
  language: (p) => languageLabel(p.language),
  source: sourceOf,
  score: (p) => p.latest?.score ?? null,
  issues: (p) => p.latest?.totalIssues ?? null,
  lastScan: scanTime,
  gate: (p) => gateRank(p.latest),
}

const nullableFields: SortField[] = ['score', 'issues', 'lastScan']

function compareValues(a: string | number | null, b: string | number | null, field: SortField): number {
  if (nullableFields.includes(field)) {
    return (a as number) - (b as number)
  }
  return String(a).localeCompare(String(b))
}

function isMissing(value: string | number | null, field: SortField): boolean {
  return value === null && nullableFields.includes(field)
}

const sortField = ref<SortField | null>(null)
const sortOrder = ref<'ascending' | 'descending' | null>(null)

// 相等时用 name 兜底，否则重排时行会跳位
const sortedProjects = computed(() => {
  const field = sortField.value
  const order = sortOrder.value
  if (!field || !order) return projects.value

  const dir = order === 'ascending' ? 1 : -1
  const key = sortKeys[field]

  return [...projects.value].sort((a, b) => {
    const ka = key(a)
    const kb = key(b)

    // 缺失值固定沉底，不随 dir 翻转
    const aMissing = isMissing(ka, field)
    const bMissing = isMissing(kb, field)
    if (aMissing || bMissing) {
      if (aMissing && bMissing) return a.name.localeCompare(b.name)
      return aMissing ? 1 : -1
    }

    const result = compareValues(ka, kb, field)
    return result !== 0 ? result * dir : a.name.localeCompare(b.name)
  })
})

function onSortChange({ prop, order }: { prop: string | null; order: string | null }) {
  sortField.value = (prop as SortField) ?? null
  sortOrder.value = (order as 'ascending' | 'descending' | null) ?? null
}

// 正则和提示文案必须引用同一个常量
const DELETE_CONFIRM_WORD = 'DELETE'

const selected = ref<ProjectSummary[]>([])
const deleting = ref(false)

function onSelectionChange(rows: ProjectSummary[]) {
  selected.value = rows
}

// 批量删除不可撤销，会连同分析历史和问题记录一起删掉
// 接口返回的 deleted 可能少于请求的 ID 数，需按实际数量报数
async function removeSelected() {
  const rows = selected.value
  if (!rows.length) return

  const names = rows.map((p) => p.name)
  const preview = names.slice(0, 5).join('、')
  const body = names.length > 5
    ? t('projectsView.deleteManyBodyMore', { names: preview, count: names.length })
    : t('projectsView.deleteManyBody', { names: preview })

  const ok = await askConfirm(
    t('projectsView.deleteManyTitle', { count: names.length }),
    body,
    DELETE_CONFIRM_WORD,
  )
  if (!ok) return

  deleting.value = true
  try {
    const deleted = await projectApi.removeMany(rows.map((p) => p.id))
    if (deleted.length === rows.length) {
      ElMessage.success(t('projectsView.deletedMany', { count: deleted.length }))
    } else {
      ElMessage.warning(
        t('projectsView.deletedManyPartial', { done: deleted.length, total: rows.length }),
      )
    }
    selected.value = []
    await load()
  } catch (error) {
    reportError(error, t('errors.deleteProject'))
    // 请求可能已部分生效，失败后也要刷新
    await load()
  } finally {
    deleting.value = false
  }
}

async function scan(project: ProjectSummary) {
  try {
    const { analysisApi } = await import('@/api/client')
    await analysisApi.start(project.id)
    ElMessage.success(t('projectsView.scanQueued', { name: project.name }))
    router.push({ name: 'project-detail', params: { id: project.id } })
  } catch (error) {
    reportError(error, t('errors.startScan'))
  }
}

// 扫描状态变化时只刷新受影响的项目，避免整表重载闪烁
useScanEvents(
  (ev) => {
    if (ev.type === 'finished') {
      void load()
      return
    }
    const target = projects.value.find((p) => p.id === ev.projectId)
    if (!target) return
    if (!target.inFlight) {
      target.inFlight = { ...target.latest, status: ev.status, progress: ev.progress } as Analysis
    } else {
      target.inFlight.status = ev.status
      target.inFlight.progress = ev.progress
    }
  },
  () => {
    if (!loading.value) void load()
  },
)

onMounted(load)
</script>

<template>
  <div class="projects">
    <div class="head" v-reveal>
      <div class="page-head">
        <h1 class="page-title">{{ t('nav.projects') }}</h1>
        <p class="page-sub intro">{{ t('projectsView.intro') }}</p>
      </div>
      <el-button type="primary" class="soda" :icon="'Plus'" @click="openCreate">
        {{ t('projectsView.addProject') }}
      </el-button>
    </div>

    <div v-if="selected.length" class="bulk-bar">
      <span class="muted">{{ t('projectsView.selectedCount', { count: selected.length }) }}</span>
      <el-button size="small" text @click="selected = []">
        {{ t('projectsView.clearSelection') }}
      </el-button>
      <el-button
        type="danger"
        size="small"
        :icon="'Delete'"
        :loading="deleting"
        @click="removeSelected"
      >
        {{ t('projectsView.deleteSelected') }}
      </el-button>
    </div>

    <div v-reveal>
      <el-table
        v-loading="loading"
        :data="sortedProjects"
        row-key="id"
        class="table-card"
        :empty-text="t('projectsView.empty')"
        @selection-change="onSelectionChange"
        @sort-change="onSortChange"
      >
      <el-table-column type="selection" width="40" :selectable="() => true" />
      <el-table-column
        :label="t('projectsView.colName')"
        prop="name"
        min-width="180"
        sortable="custom"
      >
        <template #default="{ row }">
          <div class="name-cell">
            <span class="dot" :style="{ background: row.color || '#3b82f6' }" />
            <div class="name-text">
              <router-link :to="{ name: 'project-detail', params: { id: row.id } }" class="name">
                {{ row.name }}
              </router-link>
              <div v-if="row.description" class="muted desc">{{ row.description }}</div>
            </div>
          </div>
        </template>
      </el-table-column>

      <el-table-column
        :label="t('projectsView.colLanguage')"
        prop="language"
        width="84"
        sortable="custom"
      >
        <template #default="{ row }">
          <span class="lang-name">{{ languageLabel(row.language) }}</span>
        </template>
      </el-table-column>

      <el-table-column
        :label="t('projectsView.colSource')"
        prop="source"
        min-width="160"
        sortable="custom"
      >
        <template #default="{ row }">
          <span class="mono source" :title="row.localPath || row.repoUrl">
            {{ row.localPath || row.repoUrl || t('common.none') }}
          </span>
        </template>
      </el-table-column>

      <el-table-column
        :label="t('projectsView.colScore')"
        prop="score"
        width="80"
        sortable="custom"
      >
        <template #default="{ row }">
          <span v-if="row.latest" :class="scoreClass(row.latest.score)" class="score">
            {{ row.latest.score.toFixed(1) }}
          </span>
          <span v-else class="muted">{{ t('common.none') }}</span>
        </template>
      </el-table-column>

      <el-table-column
        :label="t('projectsView.colGate')"
        prop="gate"
        width="88"
        sortable="custom"
      >
        <template #default="{ row }">
          <el-tag
            v-if="row.latest?.gateStatus"
            :type="statusTagType(row.latest.gateStatus)"
            size="small"
            effect="plain"
          >
            {{ t(`gate.${row.latest.gateStatus}`) }}
          </el-tag>
          <el-tag
            v-else-if="row.latest"
            :type="statusTagType(row.latest.status)"
            size="small"
            effect="plain"
          >
            {{ t(`status.${row.latest.status}`) }}
          </el-tag>
          <span v-else class="muted">{{ t('common.none') }}</span>
        </template>
      </el-table-column>

      <el-table-column
        :label="t('projectsView.colIssues')"
        prop="issues"
        width="72"
        sortable="custom"
      >
        <template #default="{ row }">
          <span v-if="row.latest">{{ row.latest.totalIssues }}</span>
          <span v-else class="muted">{{ t('common.none') }}</span>
        </template>
      </el-table-column>

      <el-table-column
        :label="t('projectsView.colLastScan')"
        prop="lastScan"
        width="110"
        sortable="custom"
      >
        <template #default="{ row }">
          <span v-if="isScanning(row)" class="scanning">
            <span class="scanning-dot" />
            {{ t('projectsView.scanning') }}
          </span>
          <span v-else-if="row.lastFailed && !row.latest" class="scan-failed">
            {{ t('projectsView.scanFailed') }}
          </span>
          <span v-else class="muted">
            {{ row.latest ? formatRelative(row.latest.createdAt) : t('common.never') }}
          </span>
        </template>
      </el-table-column>

      <el-table-column :label="t('issue.colActions')" width="104">
        <template #default="{ row }">
          <div class="actions">
            <el-button
              size="small"
              text
              :icon="'Refresh'"
              class="scan-btn"
              @click.stop="scan(row)"
            >
              {{ t('common.scan') }}
            </el-button>
            <div class="actions-secondary">
              <el-button
                size="small"
                text
                :icon="'Edit'"
                @click.stop="openEdit(row)"
                :aria-label="t('common.edit')"
              />
              <el-button
                size="small"
                text
                type="danger"
                :icon="'Delete'"
                @click.stop="remove(row)"
                :aria-label="t('common.delete')"
              />
            </div>
          </div>
        </template>
      </el-table-column>
    </el-table>
    </div>

    <el-dialog
      v-model="dialogOpen"
      :title="editingId ? t('projectsView.editTitle') : t('projectsView.addTitle')"
      width="560px"
      :close-on-click-modal="false"
    >
      <template #header="{ titleId, titleClass }">
        <div class="dialog-head">
          <div class="dialog-head-main">
            <span :id="titleId" :class="titleClass" class="dialog-title">
              {{ editingId ? t('projectsView.editTitle') : t('projectsView.addTitle') }}
            </span>
            <el-color-picker
              v-model="form.color"
              class="accent-picker"
              :aria-label="t('projectsView.formAccentLabel')"
            />
          </div>
          <button
            type="button"
            class="dialog-close"
            :aria-label="t('common.close')"
            @click="dialogOpen = false"
          >
            <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round">
              <path d="M18 6 6 18M6 6l12 12" />
            </svg>
          </button>
        </div>
      </template>

      <el-form ref="formRef" :model="form" :rules="rules" label-position="top">
        <div class="steps">
          <div class="step" :class="{ on: step === 1, done: step > 1 }">
            <span class="step-num">1</span>{{ t('projectsView.stepSource') }}
          </div>
          <div class="step" :class="{ on: step === 2 }">
            <span class="step-num">2</span>{{ t('projectsView.stepStack') }}
          </div>
        </div>

        <div v-show="step === 1" class="pane" :key="'a' + step">
          <div class="section-head">
            <span class="section-title">{{ t('projectsView.sectionBasic') }}</span>
            <span class="section-note">{{ t('projectsView.sectionBasicNote') }}</span>
          </div>

          <el-form-item :label="t('projectsView.formName')" prop="name">
            <el-input v-model="form.name" :placeholder="t('projectsView.phName')" />
          </el-form-item>

          <el-form-item :label="t('projectsView.formDescription')">
            <el-input
              v-model="form.description"
              type="textarea"
              :rows="2"
              :placeholder="t('projectsView.phOptional')"
            />
          </el-form-item>

          <div class="section-head">
            <span class="section-title">{{ t('projectsView.stepSource') }}</span>
            <span class="section-note">{{ t('projectsView.sectionSourceNote') }}</span>
          </div>

          <el-form-item>
            <el-radio-group v-model="form.sourceType" @change="onSourceChange">
              <el-radio-button value="local">{{ t('projectsView.sourceLocal') }}</el-radio-button>
              <el-radio-button value="repo">{{ t('projectsView.sourceRepo') }}</el-radio-button>
            </el-radio-group>
          </el-form-item>

          <el-form-item
            v-if="form.sourceType === 'local'"
            :label="t('projectsView.formLocalPath')"
            prop="localPath"
          >
            <el-input v-model="form.localPath" :placeholder="t('projectsView.phLocalPath')" />
            <div class="field-hint muted">{{ t('projectsView.hintLocalPath') }}</div>
          </el-form-item>

          <template v-else>
            <el-form-item :label="t('projectsView.formRepoUrl')" prop="repoUrl">
              <el-input v-model="form.repoUrl" :placeholder="t('projectsView.phRepoUrl')" />
              <div class="field-hint muted">{{ t('projectsView.hintRepoUrl') }}</div>
            </el-form-item>

            <el-form-item :label="t('projectsView.formBranchOptional')">
              <el-input
                v-model="form.branch"
                :placeholder="t('projectsView.phBranchOptional')"
              />
            </el-form-item>
          </template>
        </div>

        <div v-show="step === 2" class="pane" :key="'b' + step">
          <div class="section-head">
            <span class="section-title">{{ t('projectsView.formLanguage') }}</span>
            <span class="section-note">{{ t('projectsView.sectionLanguageNote') }}</span>
          </div>

          <el-form-item>
            <el-select v-model="form.language" class="full" @change="onLanguageChange">
              <el-option
                v-for="lang in languageOptions"
                :key="lang"
                :value="lang"
                :label="t(`projectsView.language_${lang}`)"
              />
            </el-select>
          </el-form-item>

          <transition name="collapse">
            <el-form-item v-if="form.language === 'go'" :label="t('projectsView.formKind')">
              <div
                ref="kindGroup"
                class="choice-group"
                role="radiogroup"
                @keydown.up.prevent="stepKind(-1)"
                @keydown.down.prevent="stepKind(1)"
              >
                <div
                  v-for="option in kindOptions"
                  :key="option.value"
                  class="choice"
                  :class="{ on: form.kind === option.value }"
                  role="radio"
                  :aria-checked="form.kind === option.value"
                  :tabindex="form.kind === option.value ? 0 : -1"
                  @click="form.kind = option.value"
                  @keydown.enter="form.kind = option.value"
                  @keydown.space.prevent="form.kind = option.value"
                >
                  <span class="choice-radio" />
                  <div class="choice-body">
                    <div class="choice-title">{{ t(`projectsView.${option.title}`) }}</div>
                    <div class="choice-sub">{{ t(`projectsView.${option.note}`) }}</div>
                  </div>
                </div>
              </div>
            </el-form-item>
          </transition>

          <div class="section-head">
            <span class="section-title">{{ t('projectsView.formAnalyzers') }}</span>
            <span class="section-note">{{ t('projectsView.hintAnalyzers') }}</span>
          </div>

          <el-form-item>
            <el-select
              v-model="form.analyzers"
              multiple
              :placeholder="t('projectsView.phAnalyzers')"
              class="full"
            >
              <el-option
                v-for="analyzer in languageAnalyzers"
                :key="analyzer.name"
                :value="analyzer.name"
                :label="analyzer.name"
                :disabled="!analyzer.ready"
              >
                <span class="option">
                  <span>{{ analyzer.name }}</span>
                  <span v-if="!analyzer.ready" class="muted option-note">
                    {{ t('projectsView.needsTools', { tools: (analyzer.missingTools ?? []).join(', ') }) }}
                  </span>
                  <span v-else class="muted option-note">{{ analyzer.category }}</span>
                </span>
              </el-option>
            </el-select>
          </el-form-item>
        </div>
      </el-form>

      <template #footer>
        <el-button v-if="step === 1" @click="dialogOpen = false">{{ t('common.cancel') }}</el-button>
        <el-button v-else @click="step = 1">{{ t('projectsView.prevStep') }}</el-button>
        <el-button v-if="step === 1" type="primary" @click="goStep2">
          {{ t('projectsView.nextStep') }}
        </el-button>
        <el-button v-else type="primary" :loading="saving" @click="save">
          {{ editingId ? t('common.save') : t('projectsView.addProject') }}
        </el-button>
      </template>
    </el-dialog>

    <el-dialog
      v-model="confirmOpen"
      width="420px"
      class="confirm-dialog"
      :close-on-click-modal="false"
      @closed="settleConfirm(false)"
    >
      <template #header="{ titleId, titleClass }">
        <div class="dialog-head">
          <span :id="titleId" :class="titleClass" class="dialog-title">{{ confirmTitle }}</span>
          <button
            type="button"
            class="dialog-close"
            :aria-label="t('common.close')"
            @click="settleConfirm(false)"
          >
            <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round">
              <path d="M18 6 6 18M6 6l12 12" />
            </svg>
          </button>
        </div>
      </template>

      <aside class="confirm-notice">
        <span class="confirm-notice-mark" aria-hidden="true">
          <svg viewBox="0 0 16 16" width="15" height="15">
            <path d="M8 1.8 15 14H1z" fill="none" stroke="currentColor" stroke-width="1.6" stroke-linejoin="round" />
            <path d="M8 6.2v3.4" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" />
            <circle cx="8" cy="11.7" r="0.95" fill="currentColor" />
          </svg>
        </span>
        <span class="confirm-notice-text">{{ t('projectsView.deleteWarning') }}</span>
      </aside>

      <p class="confirm-body">{{ confirmBody }}</p>

      <div v-if="confirmWord" class="confirm-word">
        <el-input
          v-model="confirmTyped"
          :placeholder="confirmWord"
          @keyup.enter="confirmTyped === confirmWord && settleConfirm(true)"
        />
        <div class="field-hint muted">
          {{ t('projectsView.deleteManyConfirmError', { word: confirmWord }) }}
        </div>
      </div>

      <template #footer>
        <el-button @click="settleConfirm(false)">{{ t('common.cancel') }}</el-button>
        <el-button
          type="primary"
          :disabled="!!confirmWord && confirmTyped !== confirmWord"
          @click="settleConfirm(true)"
        >
          {{ t('common.delete') }}
        </el-button>
      </template>
    </el-dialog>
  </div>
</template>

<style scoped>
.projects {
  display: flex;
  flex-direction: column;
  gap: var(--s-md);
}

.head {
  display: flex;
  align-items: flex-end;
  justify-content: space-between;
  gap: var(--s-md);
}

.head .page-head {
  margin-bottom: 0;
}

.bulk-bar {
  display: flex;
  align-items: center;
  gap: var(--s-xs);
  padding: var(--s-xs) var(--s-sm);
  border: 1px solid var(--border);
  border-radius: var(--r-md);
  background: var(--bg);
}

.bulk-bar .el-button:last-child {
  margin-left: auto;
}

.intro {
  margin: 0;
  max-width: 72ch;
  font: var(--t-body-sm);
  color: var(--text-muted);
}

.name-cell {
  display: flex;
  align-items: center;
  gap: var(--s-xs);
  min-width: 0;
}

.dot {
  width: 9px;
  height: 9px;
  border-radius: 50%;
  flex: 0 0 auto;
}

.name {
  font-weight: 600;
}

.name-text {
  min-width: 0;
}

.desc {
  font-size: 12px;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.source {
  font-size: 12px;
  word-break: break-all;
}

.score {
  font-variant-numeric: tabular-nums;
  font-weight: 600;
}

.full {
  width: 100%;
}

:deep(.el-form-item__content) {
  column-gap: 8px;
}

:deep(.el-color-picker__color-inner) {
  border-radius: 6px;
}

.field-hint {
  font-size: 12px;
  line-height: 1.5;
  margin-top: 7px;
}

.option {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: var(--s-sm);
}

.option-note {
  font-size: 11px;
}

.section-head {
  display: flex;
  align-items: baseline;
  gap: 10px;
  margin-bottom: 18px;
}

.section-title {
  flex: none;
  white-space: nowrap;
  font-size: 15px;
  font-weight: 640;
  color: var(--text);
}

.section-note {
  font-size: 12px;
  line-height: 1.5;
  color: var(--text-muted);
}

.pane {
  animation: pane-in 0.2s cubic-bezier(0.4, 0, 0.2, 1);
}

@keyframes pane-in {
  from {
    opacity: 0;
    transform: translateY(6px);
  }
  to {
    opacity: 1;
    transform: none;
  }
}

.steps {
  display: flex;
  gap: 28px;
  margin-bottom: 20px;
  padding-bottom: 22px;
  border-bottom: 1px solid var(--border);
}

.collapse-enter-active,
.collapse-leave-active {
  overflow: hidden;
  transition:
    max-height 0.22s cubic-bezier(0.4, 0, 0.2, 1),
    margin 0.22s cubic-bezier(0.4, 0, 0.2, 1),
    opacity 0.16s var(--ease-out);
}

.collapse-enter-from,
.collapse-leave-to {
  max-height: 0 !important;
  margin-bottom: 0 !important;
  opacity: 0;
}

.collapse-enter-to,
.collapse-leave-from {
  max-height: 260px;
}

.step {
  display: flex;
  align-items: center;
  gap: 10px;
  font-size: 13px;
  color: var(--text-soft);
  transition: color 0.15s var(--ease-out);
}

.step.on {
  color: var(--text);
  font-weight: 600;
}

.step.done {
  color: var(--text-muted);
}

.step-num {
  width: 26px;
  height: 26px;
  border-radius: 50%;
  border: 1px solid var(--line-strong);
  display: grid;
  place-items: center;
  font-size: 12px;
  font-weight: 600;
  transition:
    background 0.15s var(--ease-out),
    border-color 0.15s var(--ease-out),
    color 0.15s var(--ease-out);
}

.step.on .step-num,
.step.done .step-num {
  background: var(--text);
  color: var(--bg);
  border-color: var(--text);
}

.dialog-head {
  display: flex;
  align-items: center;
  gap: 12px;
}

.dialog-head-main {
  display: flex;
  align-items: center;
  gap: 10px;
  flex: 1;
  min-width: 0;
}

.dialog-title {
  line-height: 1.2;
}

.dialog-close {
  display: grid;
  place-items: center;
  width: 32px;
  height: 32px;
  border: 0;
  border-radius: 50%;
  background: none;
  cursor: pointer;
  color: var(--text-muted);
  padding: 0;
  margin-left: auto;
  transition:
    background 0.15s var(--ease-out),
    color 0.15s var(--ease-out);
}

.dialog-close:hover {
  background: var(--bg-sunken);
  color: var(--text);
}

.dialog-close svg {
  width: 18px;
  height: 18px;
}

.confirm-notice {
  display: flex;
  gap: 10px;
  padding: 12px 14px;
  margin-bottom: 16px;
  border-radius: var(--r-md);
  background: var(--danger-bg);
}

.confirm-notice-mark {
  flex: none;
  margin-top: 1px;
  color: var(--danger);
}

.confirm-notice-text {
  font-size: 12px;
  line-height: 1.6;
  color: var(--text-body);
}

.confirm-body {
  margin: 0;
  font-size: 13px;
  line-height: 1.7;
  color: var(--text-body);
}

.confirm-word {
  margin-top: 16px;
}

.confirm-dialog :deep(.el-dialog__body) {
  padding: 20px 28px 0;
}

.confirm-dialog :deep(.el-dialog__footer) {
  padding: 20px 28px 26px;
}

:deep(.el-dialog__headerbtn) {
  display: none;
}

:deep(.el-dialog__header) {
  padding: 26px 28px 0;
  margin-right: 0;
}

:deep(.el-dialog__body) {
  padding: 26px 28px;
}

:deep(.el-dialog__footer) {
  padding: 20px 28px;
}

:deep(.el-overlay-dialog) {
  display: flex;
  align-items: center;
  justify-content: center;
}

:deep(.el-dialog) {
  margin-top: 0;
  transition:
    height 0.22s cubic-bezier(0.4, 0, 0.2, 1),
    margin 0.22s cubic-bezier(0.4, 0, 0.2, 1);
}

:deep(.el-dialog .el-form-item) {
  --el-form-item-margin-bottom: 16px;
}

:deep(.el-dialog .el-form-item__label) {
  margin-bottom: 8px;
  line-height: 1.4;
  height: auto;
}

:deep(.el-dialog .el-input__wrapper),
:deep(.el-dialog .el-select__wrapper) {
  min-height: 42px;
  padding: 0 13px;
  border-radius: var(--r-sm);
}

:deep(.el-dialog .el-input__inner) {
  font-size: 14px;
}

:deep(.el-dialog .el-textarea__inner) {
  min-height: 72px;
  padding: 11px 13px;
  border-radius: var(--r-sm);
  font-size: 14px;
}

:deep(.el-dialog .el-button) {
  height: 40px;
  padding: 0 24px;
  border-radius: var(--r-pill);
}

.accent-picker {
  width: 20px;
  height: 20px;
  padding: 0;
  align-self: center;
}

:deep(.accent-picker .el-color-picker__trigger) {
  width: 20px;
  height: 20px;
  padding: 0;
  border-radius: 50%;
  border-color: var(--card-line);
}

:deep(.accent-picker .el-color-picker__color) {
  border-radius: 50%;
  border: 0;
}

:deep(.accent-picker .el-color-picker__color-inner) {
  border-radius: 50%;
}

.choice-group {
  display: flex;
  flex-direction: column;
  width: 100%;
}

.choice {
  display: flex;
  align-items: center;
  gap: 12px;
  width: 100%;
  padding: 14px 16px;
  border: 1px solid var(--card-line);
  border-radius: var(--r-md);
  cursor: pointer;
  margin-bottom: 10px;
  transition:
    border-color 0.15s var(--ease-out),
    background 0.15s var(--ease-out);
}

.choice-group .choice:last-child {
  margin-bottom: 0;
}

.choice:hover {
  border-color: var(--text);
}

.choice.on {
  border-color: var(--text);
  background: var(--bg-sunken);
}

.choice-radio {
  width: 16px;
  height: 16px;
  border-radius: 50%;
  border: 1.5px solid var(--line-strong);
  flex: none;
  transition: border-color 0.15s var(--ease-out);
}

.choice.on .choice-radio {
  border-color: var(--text);
  border-width: 4px;
}

.choice-body {
  flex: 1;
  min-width: 0;
}

.choice-title {
  font-size: 13px;
  font-weight: 600;
}

.choice-sub {
  font-size: 12px;
  color: var(--text-muted);
  margin-top: 2px;
}

.actions {
  display: flex;
  flex-direction: column;
  align-items: center;
  gap: 2px;
}

.scan-btn {
  width: 100%;
}

.actions-secondary {
  display: flex;
  align-items: center;
  gap: 10px;
}

/* Element Plus 给相邻按钮的 margin-left 会破坏 gap 布局 */
.actions .el-button + .el-button {
  margin-left: 0;
}

/* padding 不清零会压过 width 把图标挤没 */
.actions-secondary .el-button {
  width: 10px;
  padding: 0;
  min-height: 0;
}

.lang-name {
  font-size: 13px;
}

/* 扫描中：一个呼吸的圆点加文字，比纯文字更能表达「正在进行」 */
.scanning {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  font-size: 12px;
  color: var(--sev-warning);
}

.scan-failed {
  font-size: 12px;
  color: var(--sev-critical);
  cursor: help;
}

.scanning-dot {
  width: 6px;
  height: 6px;
  border-radius: 50%;
  background: currentColor;
  animation: pulse 1.4s ease-in-out infinite;
}

@keyframes pulse {
  0%,
  100% {
    opacity: 1;
  }
  50% {
    opacity: 0.25;
  }
}

@media (prefers-reduced-motion: reduce) {
  .scanning-dot {
    animation: none;
  }
}
</style>
