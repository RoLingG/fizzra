<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { ElMessage } from 'element-plus'
import { useI18n } from 'vue-i18n'

import { reportError, systemApi } from '@/api/client'
import type { AnalyzerOption, AnalyzerReadiness, SystemInfo } from '@/api/types'
import { localizeAnalyzerDescription, localizeAnalyzerOptions } from '@/locales/analyzerText'

const { t } = useI18n()

const loading = ref(true)
const savingOffline = ref(false)
const savingOption = ref('')
const info = ref<SystemInfo | null>(null)

const settingsAnalyzer = ref<AnalyzerReadiness | null>(null)
const settingsOpen = ref(false)

function openSettings(analyzer: AnalyzerReadiness) {
  // 打开抽屉时才做本地化
  settingsAnalyzer.value = localizeAnalyzerOptions(localizeAnalyzerDescription(analyzer))
  settingsOpen.value = true
}

function configuredCount(analyzer: AnalyzerReadiness): number {
  return (analyzer.options ?? []).filter((o) => optionValue(analyzer.name, o)).length
}

const ready = computed(() => (info.value?.analyzers ?? []).filter((a) => a.ready))
const unavailable = computed(() => (info.value?.analyzers ?? []).filter((a) => !a.ready))

const localizedAnalyzers = computed(() =>
  (info.value?.analyzers ?? []).map((a) => localizeAnalyzerDescription(a)),
)

const networkAnalyzers = ['cargo-audit', 'govulncheck']

// 用 computed 才能在切换语言后重建
const cacheRows = computed(() => [
  {
    tool: 'ruff',
    state: t('system.cacheDisabled'),
    tagType: 'success' as const,
    effect: t('system.cacheRuff'),
  },
  {
    tool: 'golangci-lint',
    state: t('system.cacheActive'),
    tagType: 'warning' as const,
    effect: t('system.cacheGolangci'),
  },
  {
    tool: 'staticcheck',
    state: t('system.cacheActive'),
    tagType: 'warning' as const,
    effect: t('system.cacheStaticcheck'),
  },
  {
    tool: 'govulncheck',
    state: t('system.cacheActive'),
    tagType: 'warning' as const,
    effect: t('system.cacheGovulncheck'),
  },
  {
    tool: 'go vet',
    state: t('system.cacheBuildOnly'),
    tagType: 'info' as const,
    effect: t('system.cacheGovet'),
  },
])

// 保存成功后回写服务端返回的状态
async function setOffline(enabled: boolean) {
  savingOffline.value = true
  try {
    await systemApi.setOffline(enabled)
    if (info.value) info.value.offline = enabled
    ElMessage.success(t('system.offlineSaved'))
  } catch (error) {
    reportError(error, t('errors.loadSystem'))
  } finally {
    savingOffline.value = false
  }
}

// 未设置时取分析器声明的默认值
function optionValue(analyzerName: string, option: AnalyzerOption): boolean {
  const raw = info.value?.analyzerOptions?.[analyzerName]?.[option.key]
  if (raw === undefined) return option.default
  return raw === 'true' || raw === 'yes' || raw === 'on' || raw === '1'
}

// 接口是替换非合并，须发整份映射
async function setOption(analyzerName: string, option: AnalyzerOption, enabled: boolean) {
  const name = `${analyzerName}.${option.key}`
  savingOption.value = name
  try {
    const next: Record<string, string> = {
      ...(info.value?.analyzerOptions?.[analyzerName] ?? {}),
      [option.key]: enabled ? 'true' : 'false',
    }
    await systemApi.setAnalyzerOptions(analyzerName, next)
    if (info.value) {
      info.value.analyzerOptions = {
        ...(info.value.analyzerOptions ?? {}),
        [analyzerName]: next,
      }
    }
    ElMessage.success(t('system.optionSaved'))
  } catch (error) {
    reportError(error, t('errors.loadSystem'))
  } finally {
    savingOption.value = ''
  }
}

const missingTools = computed(() => {
  const seen = new Map<string, { usedBy: string[]; hint: string }>()
  for (const analyzer of unavailable.value) {
    for (const tool of analyzer.missingTools ?? []) {
      const entry = seen.get(tool) ?? { usedBy: [], hint: '' }
      entry.usedBy.push(analyzer.name)
      // 优先用分析器自己声明的安装提示，按工具名猜测会出错（如 pmd、spotbugs 是解压式发行版）
      if (!entry.hint && analyzer.installHint) {
        entry.hint = analyzer.installHint
      }
      seen.set(tool, entry)
    }
  }
  return [...seen.entries()]
    .sort(([a], [b]) => a.localeCompare(b))
    .map(([tool, { usedBy, hint }]) => ({
      tool,
      // 分析器和工具同名时不重复一遍，「oxlint 供 oxlint 使用」不携带信息
      usedBy: usedBy.filter((name) => name !== tool),
      install: hint || installCommand(tool),
      steps: t(`system.toolHints.${tool}`, ''),
    }))
})

// 安装路径上游可能变化，集中在一处维护
function installCommand(tool: string): string {
  switch (tool) {
    case 'golangci-lint':
      return 'go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@latest'
    case 'staticcheck':
      return 'go install honnef.co/go/tools/cmd/staticcheck@latest'
    case 'gosec':
      return 'go install github.com/securego/gosec/v2/cmd/gosec@latest'
    case 'govulncheck':
      return 'go install golang.org/x/vuln/cmd/govulncheck@latest'
    case 'go':
      return 'Install Go from https://go.dev/dl/'
    case 'gofmt':
      return 'Ships with the Go toolchain'
    default:
      return `go install ${tool}@latest`
  }
}

// 先转义再替换反引号，顺序不能反
function renderInlineCode(text: string): string {
  const escaped = text
    .replace(/&/g, '&amp;')
    .replace(/</g, '&lt;')
    .replace(/>/g, '&gt;')
    .replace(/"/g, '&quot;')
  return escaped.replace(/`([^`]+)`/g, '<code>$1</code>')
}

async function copy(text: string) {
  try {
    await navigator.clipboard.writeText(text)
  } catch {
    // 剪贴板 API 仅在安全上下文可用，失败时静默，值本身仍可见
  }
}

onMounted(async () => {
  try {
    info.value = await systemApi.info()
  } catch (error) {
    reportError(error, t('errors.loadSystem'))
  } finally {
    loading.value = false
  }
})
</script>

<template>
  <div v-loading="loading" class="system">
    <div class="page-head" v-reveal>
      <h1 class="page-title">{{ t('nav.system') }}</h1>
      <p class="page-sub">{{ t('system.intro') }}</p>
    </div>
    <template v-if="info">
      <section class="card panel" v-reveal>
        <h2 class="section-title">{{ t('system.server') }}</h2>
        <dl class="kv">
          <dt>{{ t('system.version') }}</dt>
          <dd class="mono">{{ info.version }}</dd>
          <dt>{{ t('system.mode') }}</dt>
          <dd>{{ info.mode }}</dd>
          <dt>{{ t('system.database') }}</dt>
          <dd class="mono path">{{ info.database }}</dd>
          <dt>{{ t('system.workspace') }}</dt>
          <dd class="mono path">{{ info.workspace }}</dd>
          <dt>{{ t('system.configFile') }}</dt>
          <dd class="mono path">{{ info.configFile || t('system.builtinDefaults') }}</dd>
          <dt>{{ t('system.projects') }}</dt>
          <dd>{{ info.projectCount }}</dd>
          <dt>{{ t('system.rules') }}</dt>
          <dd>{{ info.ruleCount }}</dd>
          <dt>{{ t('system.runningTasks') }}</dt>
          <dd>{{ info.runningTasks }}</dd>
        </dl>
      </section>

      <section class="card panel" v-reveal>
        <h2 class="section-title">{{ t('system.networkHeading') }}</h2>
        <el-form label-position="top">
          <el-form-item :label="t('system.offlineLabel')">
            <el-switch
              :model-value="info.offline"
              :loading="savingOffline"
              @change="setOffline($event as boolean)"
            />
            <div class="offline-hint">
              <span class="muted">{{ t('system.offlineHint') }}</span>
              <span class="muted">
                {{ t('system.offlineAffects', { analyzers: networkAnalyzers.join(', ') }) }}
              </span>
            </div>
          </el-form-item>
        </el-form>
      </section>

      <section class="card panel" v-reveal>
        <h2 class="section-title">{{ t('system.cacheHeading') }}</h2>
        <p class="muted sub">{{ t('system.cacheIntro') }}</p>

        <el-table :data="cacheRows" size="small" class="cache-table">
          <el-table-column :label="t('system.cacheColTool')" width="150">
            <template #default="{ row }">
              <span class="mono cache-tool">{{ row.tool }}</span>
            </template>
          </el-table-column>
          <el-table-column :label="t('system.cacheColState')" width="130">
            <template #default="{ row }">
              <el-tag :type="row.tagType" size="small" effect="plain">{{ row.state }}</el-tag>
            </template>
          </el-table-column>
          <el-table-column :label="t('system.cacheColEffect')" min-width="280">
            <template #default="{ row }">
              <span class="cache-effect">{{ row.effect }}</span>
            </template>
          </el-table-column>
        </el-table>

        <p class="muted cache-footnote">{{ t('system.cacheFootnote') }}</p>
      </section>

      <section class="card panel" v-reveal>
        <h2 class="section-title">
          {{ t('system.analyzersHeading') }}
          <span class="muted count">
            {{ t('system.readyOf', { ready: ready.length, total: info.analyzers.length }) }}
          </span>
        </h2>

        <div class="analyzer-grid">
          <article
            v-for="analyzer in localizedAnalyzers"
            :key="analyzer.name"
            class="analyzer"
            :class="{ disabled: !analyzer.ready }"
          >
            <div class="analyzer-head">
              <span class="analyzer-title">
                <span class="mono analyzer-name">{{ analyzer.name }}</span>
                <el-tooltip
                  v-if="analyzer.options?.length"
                  placement="top"
                  :content="t('system.configure')"
                >
                  <el-button
                    text
                    size="small"
                    :icon="'Setting'"
                    :aria-label="t('system.configure')"
                    @click="openSettings(analyzer)"
                  />
                </el-tooltip>
              </span>
              <el-tag :type="analyzer.ready ? 'success' : 'warning'" size="small" effect="plain">
                {{ analyzer.ready ? t('system.ready') : t('system.unavailable') }}
              </el-tag>
            </div>
            <p class="muted analyzer-desc">{{ analyzer.description }}</p>

            <div class="analyzer-foot">
              <el-tag size="small" effect="plain" class="category">{{ analyzer.category }}</el-tag>
              <span v-if="analyzer.requires?.length" class="muted mono requires">
                {{ t('system.needs', { tools: analyzer.requires.join(', ') }) }}
              </span>
              <span v-if="analyzer.options?.length" class="muted options-hint">
                {{ t('system.optionCount', { count: configuredCount(analyzer) }, analyzer.options.length) }}
              </span>
            </div>
          </article>
        </div>
      </section>

      <el-drawer
        v-model="settingsOpen"
        size="560px"
        :with-header="false"
        direction="rtl"
      >
        <div v-if="settingsAnalyzer" class="settings">
          <div class="settings-head">
            <span class="mono analyzer-name">{{ settingsAnalyzer.name }}</span>
            <el-button text :icon="'Close'" :aria-label="t('common.close')" @click="settingsOpen = false" />
          </div>
          <p class="muted settings-desc">{{ settingsAnalyzer.description }}</p>

          <aside v-if="settingsAnalyzer.requires?.length" class="notice notice-info settings-note">
            <span class="notice-mark" aria-hidden="true">
              <svg viewBox="0 0 16 16" width="16" height="16">
                <circle cx="8" cy="8" r="7" fill="none" stroke="currentColor" stroke-width="1.6" />
                <path d="M8 4.4v4.4" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" />
                <circle cx="8" cy="11.6" r="0.95" fill="currentColor" />
              </svg>
            </span>
            <div class="notice-body">
              <p class="notice-title">
                {{ t('system.needs', { tools: settingsAnalyzer.requires.join(', ') }) }}
              </p>
            </div>
          </aside>

          <div class="label settings-heading">{{ t('system.optionsHeading') }}</div>
          <p class="muted settings-intro">{{ t('system.optionsIntro') }}</p>

          <div class="option-list">
            <div
              v-for="option in settingsAnalyzer.options ?? []"
              :key="option.key"
              class="option-item"
              :class="{ 'option-on': optionValue(settingsAnalyzer.name, option) }"
            >
              <div class="option-row">
                <el-switch
                  :model-value="optionValue(settingsAnalyzer.name, option)"
                  :loading="savingOption === `${settingsAnalyzer.name}.${option.key}`"
                  @change="setOption(settingsAnalyzer.name, option, $event as boolean)"
                />
                <span class="mono option-key">{{ option.key }}</span>
                <el-tag v-if="option.default" size="small" effect="plain" type="info">
                  {{ t('system.optionDefaultOn') }}
                </el-tag>
              </div>
              <p class="muted option-desc">{{ option.description }}</p>
            </div>
          </div>

          <p class="muted settings-footnote">{{ t('system.optionsFootnote') }}</p>
        </div>
      </el-drawer>

      <section v-if="missingTools.length" class="card panel">
        <h2 class="section-title">{{ t('system.unlockMore') }}</h2>
        <p class="muted sub">{{ t('system.detectsTools') }}</p>
        <p class="scope-note">{{ t('system.globalOnlyNote') }}</p>

        <div v-for="item in missingTools" :key="item.tool" class="install">
          <div class="install-head">
            <strong class="mono">{{ item.tool }}</strong>
            <span v-if="item.usedBy.length" class="muted">{{ t('system.usedBy', { analyzers: item.usedBy.join(', ') }) }}</span>
          </div>
          <div class="install-cmd">
            <code class="mono">{{ item.install }}</code>
            <el-button size="small" text :icon="'CopyDocument'" @click="copy(item.install)">
              {{ t('common.copy') }}
            </el-button>
          </div>
          <!-- steps 已由 renderInlineCode 转义，只能经 v-html 渲染，不要改插值 -->
          <p v-if="item.steps" class="install-steps" v-html="renderInlineCode(item.steps)" />
        </div>
      </section>

      <section v-else class="card panel">
        <el-result
          icon="success"
          :title="t('system.allAvailable')"
          :sub-title="t('system.allAvailableSub')"
        />
      </section>

      <section class="card panel" v-reveal>
        <h2 class="section-title">{{ t('system.ciIntegration') }}</h2>
        <p class="muted sub">{{ t('system.ciBody') }}</p>
        <pre class="mono snippet"># GitHub Actions
- name: Fizzra
  run: |
    go run ./backend/cmd/fizzra ci \
      --format sarif \
      --output fizzra.sarif

- name: Upload SARIF
  uses: github/codeql-action/upload-sarif@v3
  with:
    sarif_file: fizzra.sarif</pre>
        <p class="muted sub">{{ t('system.ciNote') }}</p>
      </section>
    </template>
  </div>
</template>

<style scoped>
.system {
  display: flex;
  flex-direction: column;
  gap: var(--s-md);
}

/* 外层 gap 负责间距，页头不要再叠一层下边距 */
.system .page-head {
  margin-bottom: 0;
}

.panel {
  padding: var(--s-lg);
}

.count {
  font: var(--t-body-sm);
  color: var(--text-muted);
  margin-left: var(--s-xs);
}

.kv {
  display: grid;
  grid-template-columns: 160px 1fr;
  gap: 7px 16px;
  margin: 0;
  font-size: 13px;
}

.kv dt {
  color: var(--text-muted);
}

.kv dd {
  margin: 0;
  word-break: break-all;
}

.path {
  font-size: 12px;
}

.sub {
  font-size: 12px;
  margin: -6px 0 12px;
  max-width: min(100%, 1200px);
  line-height: 1.6;
}

.offline-hint {
  display: flex;
  flex-direction: column;
  gap: 1px;
  margin-left: 14px;
  flex: 1 1 auto;
  min-width: 0;
  font-size: 12px;
  line-height: 1.5;
  max-width: min(100%, 1200px);
}

.analyzer-grid {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(258px, 1fr));
  gap: var(--s-sm);
}

.analyzer {
  border: 1px solid var(--border);
  border-radius: var(--r-md);
  padding: var(--s-sm) var(--s-md);
  display: flex;
  flex-direction: column;
  gap: 7px;
  background: var(--surface);
}

.analyzer.disabled {
  opacity: 0.62;
}

.analyzer-head {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: var(--s-xs);
}

.analyzer-title {
  display: flex;
  align-items: center;
  gap: 2px;
  min-width: 0;
}

.analyzer-name {
  font-weight: 600;
  font-size: 13px;
}

.analyzer-desc {
  font-size: 12px;
  line-height: 1.5;
  margin: 0;
  flex: 1;
}

.analyzer-foot {
  display: flex;
  align-items: center;
  gap: var(--s-xs);
  flex-wrap: wrap;
}

.options-hint {
  font-size: 11px;
}

.settings {
  display: flex;
  flex-direction: column;
  gap: var(--s-sm);
}

.settings-head {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: var(--s-sm);
}

.settings-desc {
  font-size: 13px;
  line-height: 1.55;
  margin: 0;
}

.settings-note {
  /* 提示条自带外边距，这里由所在栏的间距控制 */
  margin: 0;
}

.settings-heading {
  margin-top: var(--s-xs);
}

.settings-intro {
  font-size: 12px;
  line-height: 1.55;
  margin: 0;
}

.option-list {
  display: flex;
  flex-direction: column;
  gap: var(--s-xs);
}

.option-item {
  border: 1px solid var(--border);
  border-radius: var(--r-md);
  padding: var(--s-sm) var(--s-md);
  display: flex;
  flex-direction: column;
  gap: 4px;
  transition: border-color 0.15s ease, background 0.15s ease;
}

.option-on {
  border-color: color-mix(in srgb, var(--text) 35%, transparent);
  background: var(--surface-soft);
}

.option-row {
  display: flex;
  align-items: center;
  gap: var(--s-xs);
}

.option-key {
  font-size: 13px;
  font-weight: 600;
  color: var(--text);
}

.option-desc {
  font-size: 12px;
  line-height: 1.5;
  margin: 0;
}

.settings-footnote {
  font-size: 11px;
  line-height: 1.5;
  margin: var(--s-xs) 0 0;
}

.category {
  font-size: 10px;
}

.requires {
  font-size: 11px;
}

.scope-note {
  margin: 0 0 var(--s-md);
  padding: 10px 12px;
  border-radius: var(--r-md);
  background: var(--surface-soft);
  border: 1px solid var(--border);
  font-size: 12.5px;
  line-height: 1.6;
  color: var(--text-muted);
}

.install {
  border: 1px solid var(--border);
  border-radius: var(--r-md);
  padding: var(--s-sm) var(--s-md);
  margin-bottom: 9px;
}

.install-head {
  display: flex;
  align-items: baseline;
  gap: var(--s-xs);
  font-size: 12px;
  margin-bottom: 6px;
  flex-wrap: wrap;
}

.install-cmd {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: var(--s-xs);
  background: var(--bg);
  border-radius: var(--r-sm);
  padding: 7px 9px;
}

.install-cmd code {
  font-size: 12px;
  word-break: break-all;
  line-height: 1.6;
  flex: 1;
  min-width: 0;
  color: var(--text);
  background: var(--surface-card);
  border-radius: var(--r-xs);
  padding: 2px 6px;
}

/* 保留换行，分步说明才有先后顺序 */
.install-steps {
  margin: var(--s-xs) 0 0;
  font: var(--t-body-sm);
  font-size: 12.5px;
  line-height: 1.75;
  color: var(--text-muted);
  white-space: pre-line;
}

.install-steps :deep(code) {
  font-family: var(--font-mono);
  font-size: 12px;
  padding: 1px 5px;
  border-radius: var(--r-xs);
  background: var(--surface-card);
  color: var(--text-body);
}

.cache-table {
  --el-table-border-color: var(--border);
}

.cache-tool {
  font-size: 12px;
  font-weight: 600;
}

.cache-effect {
  font-size: 12px;
  line-height: 1.5;
  color: var(--text-body);
}

.cache-footnote {
  font-size: 12px;
  line-height: 1.55;
  margin: var(--s-sm) 0 0;
  max-width: min(100%, 1200px);
}

.snippet {
  margin: 0 0 10px;
  padding: 13px;
  background: var(--bg);
  border: 1px solid var(--border);
  border-radius: var(--r-md);
  font-size: 12px;
  line-height: 1.6;
  overflow-x: auto;
}
</style>
