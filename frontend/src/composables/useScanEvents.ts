import { onBeforeUnmount, onMounted, ref } from 'vue'

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
export function useScanEvents(onEvent: (ev: ScanEvent) => void, onReady?: () => void) {
  const connected = ref(false)
  let source: EventSource | null = null

  function connect() {
    source = new EventSource('/api/v1/events')

    source.addEventListener('ready', () => {
      connected.value = true
      onReady?.()
    })

    source.addEventListener('scan', (e) => {
      try {
        onEvent(JSON.parse((e as MessageEvent).data) as ScanEvent)
      } catch {
        // 解析失败说明后端改了格式，忽略这一条而不是让连接崩掉
      }
    })

    source.onerror = () => {
      connected.value = false
    }
  }

  onMounted(connect)
  onBeforeUnmount(() => {
    source?.close()
    source = null
  })

  return { connected }
}
