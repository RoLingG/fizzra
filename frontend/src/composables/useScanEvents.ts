import { onBeforeUnmount, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'

import { reportError } from '@/api/client'
import type { AnalysisStatus } from '@/api/types'

/** 后端推来的扫描状态事件 */
export interface ScanEvent {
  type: 'progress' | 'finished'
  analysisId: string
  projectId: string
  status: AnalysisStatus
  progress: number
  stage: string
}

/**
 * 订阅扫描事件。
 *
 * 浏览器自带重连，但重连期间可能漏掉结束事件，所以每次（重新）连上时调用
 * onReady，让调用方重新拉一次数据兜底。
 */
/** 断线多久后放弃浏览器的自动重连，改为自己重建连接 */
const RECONNECT_AFTER = 30_000
export function useScanEvents(onEvent: (ev: ScanEvent) => void, onReady?: () => void) {
  const { t } = useI18n()
  const connected = ref(false)
  let source: EventSource | null = null
  let reconnectTimer: ReturnType<typeof setTimeout> | null = null
  let forced = false
  // 事件流是持续的，格式一旦不对每条都会失败，只在第一条时提示，避免刷屏
  let reportedMalformed = false

  function clearReconnect() {
    if (reconnectTimer) {
      clearTimeout(reconnectTimer)
      reconnectTimer = null
    }
  }

  // 主动关掉当前连接再建一条。浏览器的自动重连在网络恢复后可能长时间不再尝试，
  // 也可能一直连着一个半死的连接，这里到点就自己重建
  function rebuild() {
    clearReconnect()
    source?.close()
    source = null
    forced = true
    connect()
  }

  function connect() {
    const es = new EventSource('/api/v1/events')
    source = es

    es.addEventListener('ready', () => {
      connected.value = true
      clearReconnect()
      // 重新连上后允许再提示一次，否则第一轮失败之后就永远静默了
      reportedMalformed = false
      onReady?.()
    })

    es.addEventListener('scan', (e) => {
      let event: ScanEvent
      try {
        event = JSON.parse((e as MessageEvent).data) as ScanEvent
      } catch (error) {
        if (!reportedMalformed) {
          reportedMalformed = true
          reportError(error, t('errors.scanEventMalformed'))
        }
        return
      }
      onEvent(event)
    })

    es.onerror = () => {
      // 被自己关掉的旧连接不算断线
      if (es !== source) return
      connected.value = false
      if (forced) {
        forced = false
        reportError(null, t('errors.scanEventsLost'))
        return
      }
      // 已经排好了就不再重复计时
      if (reconnectTimer) return
      reconnectTimer = setTimeout(rebuild, RECONNECT_AFTER)
    }
  }

  onMounted(connect)
  onBeforeUnmount(() => {
    clearReconnect()
    source?.close()
    source = null
  })

  return { connected }
}
