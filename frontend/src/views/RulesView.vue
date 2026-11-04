<script setup lang="ts">
import { computed, onMounted, reactive, ref } from 'vue'
import { ElMessage, ElMessageBox, type FormInstance, type FormRules } from 'element-plus'
import { useI18n } from 'vue-i18n'

import { reportError, ruleApi } from '@/api/client'
import type { Rule, Severity } from '@/api/types'
import { SEVERITY_ORDER, severityClass } from '@/api/format'
import { useFormat } from '@/composables/useFormat'
import { localizeRuleText } from '@/locales/ruleText'

const { t } = useI18n()

const { severityLabel } = useFormat()

const loading = ref(true)
const rules = ref<Rule[]>([])
const search = ref('')
const severityFilter = ref<Severity[]>([])
const sourceFilter = ref<'all' | 'builtin' | 'custom'>('all')

const dialogOpen = ref(false)
const saving = ref(false)
const editingId = ref<string | null>(null)
const formRef = ref<FormInstance>()

const patternError = ref('')

const form = reactive({
  name: '',
  severity: 'warning' as Severity,
  message: '',
  advice: '',
  patterns: '',
  paths: '',
  exclude: '',
  enabled: true,
})

// 用 computed 才能在切换语言后重建
const rules_ = computed<FormRules>(() => ({
  name: [{ required: true, message: t('rulesView.validateName'), trigger: 'blur' }],
  severity: [{ required: true, message: t('rulesView.validateSeverity'), trigger: 'change' }],
}))

// 自定义规则保留用户输入的原文，只有内置规则套用目录文案
const displayRules = computed(() =>
  rules.value.map((rule) => (rule.builtin ? localizeRuleText(rule) : rule)),
)

const filtered = computed(() => {
  const q = search.value.trim().toLowerCase()
  return displayRules.value.filter((rule) => {
    if (sourceFilter.value === 'builtin' && !rule.builtin) return false
    if (sourceFilter.value === 'custom' && rule.builtin) return false
    if (severityFilter.value.length && !severityFilter.value.includes(rule.severity)) return false
    if (!q) return true
    return (
      rule.name.toLowerCase().includes(q) ||
      rule.message.toLowerCase().includes(q) ||
      (rule.patterns ?? []).some((p) => p.toLowerCase().includes(q))
    )
  })
})

const counts = computed(() => ({
  total: rules.value.length,
  builtin: rules.value.filter((r) => r.builtin).length,
  custom: rules.value.filter((r) => !r.builtin).length,
  disabled: rules.value.filter((r) => !r.enabled).length,
}))

async function load() {
  loading.value = true
  try {
    rules.value = await ruleApi.list()
  } catch (error) {
    reportError(error, t('errors.loadRules'))
  } finally {
    loading.value = false
  }
}

function openCreate() {
  editingId.value = null
  Object.assign(form, {
    name: '',
    severity: 'warning',
    message: '',
    advice: '',
    patterns: '',
    paths: '**/*.go',
    exclude: '',
    enabled: true,
  })
  patternError.value = ''
  dialogOpen.value = true
  formRef.value?.clearValidate()
}

function openEdit(rule: Rule) {
  editingId.value = rule.id
  Object.assign(form, {
    name: rule.name,
    severity: rule.severity,
    message: rule.message,
    advice: rule.advice,
    patterns: (rule.patterns ?? []).join('\n'),
    paths: (rule.paths ?? []).join('\n'),
    exclude: (rule.exclude ?? []).join('\n'),
    enabled: rule.enabled,
  })
  patternError.value = ''
  dialogOpen.value = true
  formRef.value?.clearValidate()
}

function lines(value: string): string[] {
  return value
    .split('\n')
    .map((line) => line.trim())
    .filter((line) => line.length > 0)
}

// 前端预校验只为给出精确错误，服务端仍是权威
function validatePatterns(): boolean {
  const patterns = lines(form.patterns)
  if (!patterns.length) {
    patternError.value = t('rulesView.validatePatterns')
    return false
  }
  for (const pattern of patterns) {
    try {
      new RegExp(pattern)
    } catch (error) {
      patternError.value = t('rulesView.validatePatternInvalid', {
        pattern,
        message: (error as Error).message,
      })
      return false
    }
  }
  patternError.value = ''
  return true
}

async function save() {
  const valid = await formRef.value?.validate().catch(() => false)
  if (!valid) return
  if (!validatePatterns()) return

  saving.value = true
  try {
    const payload = {
      name: form.name.trim(),
      severity: form.severity,
      message: form.message.trim(),
      advice: form.advice.trim(),
      patterns: lines(form.patterns),
      paths: lines(form.paths),
      exclude: lines(form.exclude),
      enabled: form.enabled,
    }

    if (editingId.value) {
      await ruleApi.update(editingId.value, payload)
      ElMessage.success(t('rulesView.updated'))
    } else {
      await ruleApi.create(payload)
      ElMessage.success(t('rulesView.created'))
    }
    dialogOpen.value = false
    await load()
  } catch (error) {
    reportError(error, t('errors.saveRule'))
  } finally {
    saving.value = false
  }
}

async function toggle(rule: Rule) {
  try {
    await ruleApi.toggle(rule.id, !rule.enabled)
    rule.enabled = !rule.enabled
    ElMessage.success(
      t('rulesView.toggled', {
        name: rule.name,
        state: rule.enabled ? t('rulesView.enabled') : t('rulesView.disabled'),
      }),
    )
  } catch (error) {
    reportError(error, t('errors.toggleRule'))
  }
}

async function remove(rule: Rule) {
  try {
    await ElMessageBox.confirm(
      t('rulesView.deleteBody', { name: rule.name }),
      t('rulesView.deleteTitle'),
      {
        confirmButtonText: t('common.delete'),
        cancelButtonText: t('common.cancel'),
        type: 'warning',
      },
    )
  } catch {
    return
  }

  try {
    await ruleApi.remove(rule.id)
    ElMessage.success(t('rulesView.deleted'))
    await load()
  } catch (error) {
    reportError(error, t('errors.deleteRule'))
  }
}

onMounted(load)
</script>

<template>
  <div class="rules">
    <div class="head" v-reveal>
      <div class="page-head">
        <h1 class="page-title">{{ t('nav.rules') }}</h1>
        <p class="page-sub">{{ t('rulesView.intro') }}</p>
        <p class="page-sub stat-line">
          {{
            t('rulesView.counts', {
              total: counts.total,
              builtin: counts.builtin,
              custom: counts.custom,
              disabled: counts.disabled,
            })
          }}
        </p>
      </div>
      <el-button type="primary" :icon="'Plus'" @click="openCreate">
        {{ t('rulesView.newRule') }}
      </el-button>
    </div>

    <div class="toolbar" v-reveal>
      <el-input
        v-model="search"
        :placeholder="t('rulesView.searchPlaceholder')"
        clearable
        class="search"
        :prefix-icon="'Search'"
      />
      <el-select
        v-model="severityFilter"
        multiple
        collapse-tags
        :placeholder="t('rulesView.severityPlaceholder')"
        class="filter"
        clearable
      >
        <el-option v-for="sev in SEVERITY_ORDER" :key="sev" :label="severityLabel(sev)" :value="sev" />
      </el-select>
      <el-radio-group v-model="sourceFilter">
        <el-radio-button value="all">{{ t('rulesView.filterAll') }}</el-radio-button>
        <el-radio-button value="builtin">{{ t('rulesView.filterBuiltin') }}</el-radio-button>
        <el-radio-button value="custom">{{ t('rulesView.filterCustom') }}</el-radio-button>
      </el-radio-group>
    </div>

    <!-- v-reveal 套在外层，不能挂在 <el-table> 上，原因见 ProjectsView 同处 -->
    <div v-reveal>
      <el-table
        v-loading="loading"
        :data="filtered"
        row-key="id"
        size="small"
        class="table-card"
        :empty-text="t('rulesView.empty')"
      >
        <el-table-column :label="t('rulesView.colOn')" width="62">
        <template #default="{ row }">
          <el-switch :model-value="row.enabled" size="small" @change="toggle(row)" />
        </template>
      </el-table-column>

      <el-table-column :label="t('rulesView.colRule')" min-width="200">
        <template #default="{ row }">
          <div class="rule-name">
            <span>{{ row.name }}</span>
            <el-tag v-if="row.builtin" size="small" effect="plain" type="info">
              {{ t('rulesView.tagBuiltin') }}
            </el-tag>
            <el-tag v-if="row.requireLoop" size="small" effect="plain" class="scope-tag">
              {{ t('rulesView.tagLoopsOnly') }}
            </el-tag>
          </div>
          <div v-if="row.message" class="muted message">{{ row.message }}</div>
        </template>
      </el-table-column>

      <el-table-column :label="t('rulesView.colSeverity')" width="104">
        <template #default="{ row }">
          <span :class="severityClass(row.severity)">{{ severityLabel(row.severity) }}</span>
        </template>
      </el-table-column>

      <el-table-column :label="t('rulesView.colPatterns')" min-width="200">
        <template #default="{ row }">
          <div v-for="(pattern, index) in (row.patterns ?? []).slice(0, 2)" :key="index" class="mono pattern">
            {{ pattern }}
          </div>
          <div v-if="(row.patterns ?? []).length > 2" class="muted more">
            {{ t('rulesView.morePatterns', { count: (row.patterns ?? []).length - 2 }) }}
          </div>
        </template>
      </el-table-column>

      <el-table-column :label="t('rulesView.colScope')" min-width="130">
        <template #default="{ row }">
          <span class="muted mono scope">{{ (row.paths ?? ['**/*.go']).join(', ') }}</span>
        </template>
      </el-table-column>

      <el-table-column :label="t('issue.colActions')" width="64">
        <template #default="{ row }">
          <!-- 竖排而不是并排：两个图标按钮横放要占掉一整个列宽，
               竖排只占一个按钮的宽度，操作列因此从 104px 收到 64px。
               内置规则只有编辑一个按钮，加 `is-single` 让它垂直居中，
               不会因为放不下第二行而被顶到上面。 -->
          <div class="row-actions" :class="{ 'is-single': row.builtin }">
            <el-button
              size="small"
              text
              :icon="'Edit'"
              @click="openEdit(row)"
              :aria-label="t('rulesView.editRule')"
            />
            <el-button
              v-if="!row.builtin"
              size="small"
              text
              type="danger"
              :icon="'Delete'"
              @click="remove(row)"
              :aria-label="t('rulesView.deleteRule')"
            />
          </div>
        </template>
      </el-table-column>
    </el-table>
    </div>

    <el-dialog
      v-model="dialogOpen"
      :title="editingId ? t('rulesView.editTitle') : t('rulesView.newTitle')"
      width="640px"
      :close-on-click-modal="false"
    >
      <el-form ref="formRef" :model="form" :rules="rules_" label-position="top">
        <div class="grid-2">
          <el-form-item :label="t('rulesView.formName')" prop="name">
            <el-input
              v-model="form.name"
              :disabled="editingId !== null"
              :placeholder="t('rulesView.phName')"
            />
          </el-form-item>
          <el-form-item :label="t('rulesView.formSeverity')" prop="severity">
            <el-select v-model="form.severity" class="full">
              <el-option v-for="sev in SEVERITY_ORDER" :key="sev" :label="severityLabel(sev)" :value="sev" />
            </el-select>
          </el-form-item>
        </div>

        <el-form-item :label="t('rulesView.formMessage')">
          <el-input v-model="form.message" :placeholder="t('rulesView.phMessage')" />
        </el-form-item>

        <el-form-item :label="t('rulesView.formPatterns')" required>
          <el-input
            v-model="form.patterns"
            type="textarea"
            :rows="4"
            :placeholder="t('rulesView.phPatterns')"
            @blur="validatePatterns"
          />
          <div v-if="patternError" class="error">{{ patternError }}</div>
          <div v-else class="field-hint muted">{{ t('rulesView.hintPatterns') }}</div>
        </el-form-item>

        <el-form-item :label="t('rulesView.formAdvice')">
          <el-input
            v-model="form.advice"
            type="textarea"
            :rows="3"
            :placeholder="t('rulesView.phAdvice')"
          />
        </el-form-item>

        <div class="grid-2">
          <el-form-item :label="t('rulesView.formPaths')">
            <el-input v-model="form.paths" type="textarea" :rows="3" placeholder="**/*.go" />
            <div class="field-hint muted">{{ t('rulesView.hintPaths') }}</div>
          </el-form-item>
          <el-form-item :label="t('rulesView.formExclude')">
            <el-input v-model="form.exclude" type="textarea" :rows="3" placeholder="**/*_test.go" />
            <div class="field-hint muted">{{ t('rulesView.hintExclude') }}</div>
          </el-form-item>
        </div>

        <el-form-item :label="t('rulesView.formEnabled')">
          <el-switch v-model="form.enabled" />
        </el-form-item>
      </el-form>

      <template #footer>
        <el-button @click="dialogOpen = false">{{ t('common.cancel') }}</el-button>
        <el-button type="primary" :loading="saving" @click="save">
          {{ editingId ? t('common.save') : t('rulesView.newRule') }}
        </el-button>
      </template>
    </el-dialog>
  </div>
</template>

<style scoped>
.rules {
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

/* 页头自带下边距，这里不要再叠一层，外层 .rules 的 gap 负责间距 */
.head .page-head {
  margin-bottom: 0;
}

/* 说明和统计分两行：合并成一行时，说明本身要换行，
   统计就被挤到第二行的行尾，读起来像句子的一部分。
   分开之后统计单独成行，字号再小一档，形成「标题 → 说明 → 计数」的层次。 */
.stat-line {
  margin-top: 2px;
  font-size: 12px;
  color: var(--text-soft);
}

.toolbar {
  display: flex;
  flex-wrap: wrap;
  gap: var(--s-xs);
  align-items: center;
}

.search {
  flex: 1 1 280px;
  min-width: 200px;
}

.filter {
  width: 150px;
}

.rule-name {
  display: flex;
  align-items: center;
  gap: var(--s-xxs);
  flex-wrap: wrap;
}

/* 操作列竖排。两个按钮之间没有竖直方向的间距，
 * 只靠按钮自身的 24px 高度把上下界线分开，行高因此不会被撑大。
 *
 * Element Plus 给相邻按钮加了 `.el-button + .el-button { margin-left: 12px }`，
 * 那是横向排列用的。竖排时它会把第二个按钮往右推 12px，看起来和第一个错开，
 * 所以这里清零。 */
.row-actions {
  display: flex;
  flex-direction: column;
  align-items: flex-start;
  gap: 0;
  line-height: 1;
}

.row-actions .el-button + .el-button {
  margin-left: 0;
}

/* 内置规则只有一个按钮，独占一列会贴在最上面，居中后才和多按钮状态对齐 */
.row-actions.is-single {
  justify-content: center;
}

.row-actions .el-button {
  margin: 0;
  height: 24px;
  padding: 0 4px;
}

.scope-tag {
  font-size: 10px;
}

.message {
  font-size: 12px;
  margin-top: 2px;
}

.pattern {
  font-size: 11.5px;
  word-break: break-all;
  color: var(--text);
}

.more,
.scope {
  font-size: 11px;
}

/* 路径可能拼成一长串，窄列里必须允许断开，否则会撑宽单元格、把表格推出容器 */
.scope {
  display: block;
  word-break: break-all;
}

.grid-2 {
  display: grid;
  grid-template-columns: 1fr 1fr;
  gap: 0 14px;
}

.full {
  width: 100%;
}

:deep(.el-form-item__content) {
  column-gap: 8px;
}

.field-hint {
  font-size: 11px;
  line-height: 1.45;
}

.error {
  font-size: 11.5px;
  color: var(--sev-error);
  margin-top: 3px;
  line-height: 1.45;
}

@media (max-width: 760px) {
  .grid-2 {
    grid-template-columns: 1fr;
  }
  .head {
    flex-direction: column;
  }
}
</style>
