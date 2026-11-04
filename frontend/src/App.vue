<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref, watchEffect } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { useI18n } from 'vue-i18n'
import { motion } from 'motion-v'
import en from 'element-plus/es/locale/lang/en'
import zhCn from 'element-plus/es/locale/lang/zh-cn'

import { reportError, systemApi } from '@/api/client'
import type { AnalyzerReadiness, SystemInfo } from '@/api/types'
import { pageVariants, reduceMotion } from '@/composables/useMotion'
import { useTheme, type ThemeChoice } from '@/composables/useTheme'
import { setLocale, type Locale } from '@/locales'

const { t, locale } = useI18n()

const route = useRoute()
const router = useRouter()

const { choice: themeChoice, setTheme } = useTheme()

const themeOptions: Array<{ value: ThemeChoice; icon: string; labelKey: string }> = [
  { value: 'light', icon: 'Sunny', labelKey: 'theme.light' },
  { value: 'dark', icon: 'Moon', labelKey: 'theme.dark' },
  { value: 'auto', icon: 'Monitor', labelKey: 'theme.auto' },
]

const languageOptions: Array<{ value: Locale; label: string }> = [
  { value: 'en', label: 'English' },
  { value: 'zh', label: '中文' },
]

// 通过 config provider 覆盖 Element Plus 的界面语言
const elementLocale = computed(() => (locale.value === 'zh' ? zhCn : en))

// 命令式 ElMessage 只读取 config provider 的默认值，app.use 的选项对它无效
const MESSAGE_DURATION = 1800

// 默认偏移会让消息压在吸顶导航下方
const MESSAGE_OFFSET = 76

const elementConfig = computed(() => ({
  locale: elementLocale.value,
  message: { duration: MESSAGE_DURATION, showClose: false, offset: MESSAGE_OFFSET },
}))

function onLanguageChange(next: Locale) {
  setLocale(next)
}

const info = ref<SystemInfo | null>(null)

// 顶栏阴影只在页面滚动后出现，静止时保持和 demo 一样的平面感
// passive + 只在跨越阈值时写一次 state，避免每个滚动像素都触发渲染
const scrolled = ref(false)

function onScroll() {
  const next = window.scrollY > 4
  if (next !== scrolled.value) scrolled.value = next
}

const navOrder = ['dashboard', 'projects', 'rules', 'system']

const navItems = computed(() =>
  navOrder
    .map((name) => {
      const record = router.getRoutes().find((r) => r.name === name)
      if (!record) return null
      return {
        name,
        path: record.path,
        titleKey: (record.meta.titleKey as string) ?? name,
      }
    })
    .filter((x): x is { name: string; path: string; titleKey: string } => x !== null),
)

// 详情页路由不属于导航项，需手动映射到父栏目才能高亮
const activeName = computed(() => {
  if (route.name === 'project-detail') return 'projects'
  if (route.name === 'issue-detail') return 'projects'
  return route.name as string
})

const unavailable = computed(() => (info.value?.analyzers ?? []).filter((a) => !a.ready))

function tooltipFor(items: AnalyzerReadiness[]): string {
  return items.map((a) => `${a.name}: ${(a.missingTools ?? []).join(', ')}`).join('\n')
}

// 标题跟随路由与语言变化
watchEffect(() => {
  const key = route.meta.titleKey as string | undefined
  document.title = key ? `${t(key)} · Fizzra` : 'Fizzra'
})

onMounted(async () => {
  window.addEventListener('scroll', onScroll, { passive: true })
  try {
    info.value = await systemApi.info()
  } catch (error) {
    reportError(error, t('errors.loadSystem'))
  }
})

onBeforeUnmount(() => {
  window.removeEventListener('scroll', onScroll)
})
</script>

<template>
  <el-config-provider v-bind="elementConfig">
    <div class="shell" :class="{ 'is-scrolled': scrolled }">
      <header class="topnav">
        <div class="nav-inner">
          <router-link to="/" class="brand">
            <span class="brand-mark" aria-hidden="true"></span>
            <span class="brand-text">
              <span class="brand-name">Fizzra</span>
              <span class="brand-sub">{{ t('app.brandSub') }}</span>
            </span>
          </router-link>

          <nav class="tabs" :aria-label="t('app.sections')">
            <router-link
              v-for="item in navItems"
              :key="item.name"
              :to="item.path"
              class="tab"
              :class="{ 'tab-active': activeName === item.name }"
            >
              {{ t(item.titleKey) }}
            </router-link>
          </nav>

          <div class="nav-right">
            <el-radio-group
              :model-value="themeChoice"
              size="small"
              :aria-label="t('theme.label')"
              @change="setTheme($event as ThemeChoice)"
            >
              <el-tooltip
                v-for="option in themeOptions"
                :key="option.value"
                placement="bottom"
                :content="t(option.labelKey)"
                :show-after="150"
                :hide-after="0"
                :enterable="false"
              >
                <el-radio-button :value="option.value" :aria-label="t(option.labelKey)">
                  <el-icon><component :is="option.icon" /></el-icon>
                </el-radio-button>
              </el-tooltip>
            </el-radio-group>

            <el-select
              :model-value="locale"
              size="small"
              class="lang-select"
              :aria-label="t('language.label')"
              @change="onLanguageChange($event as Locale)"
            >
              <el-option
                v-for="option in languageOptions"
                :key="option.value"
                :value="option.value"
                :label="option.label"
              />
            </el-select>

            <el-tooltip
              v-if="unavailable.length"
              placement="bottom"
              :content="tooltipFor(unavailable)"
            >
              <span class="status-chip status-warn">
                {{ t('app.analyzersUnavailable', unavailable.length) }}
              </span>
            </el-tooltip>

            <span v-if="info?.runningTasks" class="status-chip status-running">
              {{ t('app.tasksRunning', info.runningTasks) }}
            </span>

            <span v-if="info" class="version mono">{{ info.version }}</span>
          </div>
        </div>
      </header>

      <main class="page">
        <!--
          只做淡入：key 变化时 Vue 换掉整个元素，新页面淡入。
          位移交给页面内的 v-reveal，避免两层 transform 叠加。

          不用 AnimatePresence：它的 mode="wait" 要等退场动画的完成回调才挂载
          新页面。那条链路一旦不回调，页面就停在退场态，新内容永远不出现——
          路由切换不值得押上这种风险，入场动画已足够表达切换。
        -->
        <motion.div
          :key="route.path"
          :initial="reduceMotion ? false : 'enter'"
          animate="center"
          :variants="pageVariants"
        >
          <router-view v-slot="{ Component }">
            <component :is="Component" />
          </router-view>
        </motion.div>
      </main>

      <footer class="footer">
        <div class="footer-inner">
          <span>Fizzra {{ info?.version ?? '' }}</span>
          <span class="soft">
            {{ t('app.footerCounts', { projects: info?.projectCount ?? 0, rules: info?.ruleCount ?? 0 }) }}
          </span>
        </div>
      </footer>
    </div>
  </el-config-provider>
</template>

<style scoped>
.shell {
  min-height: 100vh;
  display: flex;
  flex-direction: column;
  background: var(--bg);
}

.topnav {
  position: sticky;
  top: 0;
  z-index: 20;
  background: var(--bg);
  border-bottom: 1px solid var(--border);
}

/* 吸顶栏下方的柔和阴影，说明见脚本里的 scrolled */
.topnav::after {
  content: '';
  position: absolute;
  inset: auto 0 -10px;
  height: 10px;
  pointer-events: none;
  background: linear-gradient(to bottom, rgba(0, 0, 0, 0.045), transparent);
  opacity: 0;
  transition: opacity 0.25s var(--ease-out);
}

.shell.is-scrolled .topnav::after {
  opacity: 1;
}

.nav-inner {
  max-width: 1440px;
  margin: 0 auto;
  height: 64px;
  padding: 0 var(--s-lg);
  display: flex;
  align-items: center;
  gap: var(--s-lg);
}

.brand {
  display: flex;
  align-items: center;
  gap: var(--s-xs);
  border-bottom: none;
  flex: 0 0 auto;
}

.brand:hover {
  border-bottom: none;
}

/* 品牌标记：墨色圆角方块，中间挖出纸色内芯。用 CSS 画，才能跟随主题 */
.brand-mark {
  width: 18px;
  height: 18px;
  flex: 0 0 auto;
  border-radius: 6px;
  background: var(--text);
  position: relative;
}

.brand-mark::after {
  content: '';
  position: absolute;
  inset: 6px;
  border-radius: 2px;
  background: var(--bg);
}

.brand-text {
  display: flex;
  flex-direction: column;
  line-height: 1.15;
}

.brand-name {
  font: var(--t-title-sm);
  color: var(--text);
}

.brand-sub {
  font: var(--t-caption);
  font-size: 11px;
  color: var(--text-soft);
}

.tabs {
  display: flex;
  align-items: center;
  gap: 2px;
  flex: 1 1 auto;
  justify-content: center;
}

.tab {
  padding: 6px 14px;
  border-radius: var(--r-sm);
  font: var(--t-nav);
  color: var(--text-muted);
  border-bottom: none;
  transition: color 0.18s var(--ease-out), background-color 0.18s var(--ease-out);
  white-space: nowrap;
}

.tab:hover {
  color: var(--text);
  background: var(--bg-sunken);
  border-bottom: none;
}

.tab-active,
.tab-active:hover {
  background: var(--surface-card);
  color: var(--text);
  font-weight: 600;
}

.nav-right {
  display: flex;
  align-items: center;
  gap: var(--s-sm);
  flex: 0 0 auto;
}

.nav-right :deep(.el-radio-group) {
  flex: 0 0 auto;
}

.nav-right :deep(.el-radio-button__inner) {
  display: flex;
  align-items: center;
  height: 28px;
  padding: 0 9px;
}

.lang-select {
  width: 104px;
  flex: 0 0 auto;
}

.status-chip {
  padding: 3px 10px;
  border-radius: var(--r-pill);
  font: var(--t-caption);
  font-size: 12px;
  white-space: nowrap;
}

.status-warn {
  background: var(--sev-warning-bg);
  color: var(--sev-warning);
}

.status-running {
  background: var(--sev-info-bg);
  color: var(--sev-info);
}

.version {
  font-size: 12px;
  color: var(--text-soft);
}

.page {
  flex: 1 1 auto;
  width: 100%;
  max-width: 1440px;
  margin: 0 auto;
  padding: var(--s-lg) var(--s-lg) var(--s-xxl);
}

.footer {
  border-top: 1px solid var(--border);
  background: var(--surface-soft);
}

.footer-inner {
  max-width: 1440px;
  margin: 0 auto;
  padding: var(--s-md) var(--s-lg);
  display: flex;
  justify-content: space-between;
  gap: var(--s-md);
  font: var(--t-body-sm);
  color: var(--text-muted);
}

@media (max-width: 900px) {
  .nav-inner {
    height: auto;
    padding: var(--s-sm) var(--s-md);
    flex-wrap: wrap;
    gap: var(--s-sm);
  }
  .tabs {
    order: 3;
    width: 100%;
    justify-content: flex-start;
    overflow-x: auto;
  }
  .brand-sub {
    display: none;
  }
  .page {
    padding: var(--s-md) var(--s-md) var(--s-xl);
  }
}

@media (max-width: 640px) {
  .version,
  .status-chip {
    display: none;
  }
}</style>
