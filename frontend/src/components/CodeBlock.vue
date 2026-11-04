<script setup lang="ts">
/**
 * 带语法着色的代码片段
 *
 * 着色只在词法层面做（注释、字符串、数字、关键字、被调用的函数），配色取设计 token，
 * 深浅主题自动跟随。片段本身按 white-space: pre 保留缩进。
 */
import { computed } from 'vue'

import { languageOf, tokenizeCode } from '@/composables/useCodeHighlight'

const props = defineProps<{
  code: string
  /** 文件名或路径，用来推断语言；缺省时退化为通用着色 */
  path?: string
}>()

const tokens = computed(() => tokenizeCode(props.code ?? '', languageOf(props.path)))
</script>

<template>
  <pre class="code-hl"><code><span
    v-for="(tok, index) in tokens"
    :key="index"
    :class="tok.cls || undefined"
  >{{ tok.text }}</span></code></pre>
</template>

<style scoped>
.code-hl {
  margin: 0;
  font-family: var(--font-mono);
  font-size: 11.5px;
  line-height: 1.55;
  overflow-x: auto;
  white-space: pre;
  tab-size: 4;
  color: var(--text-body);
}

.code-hl code {
  font-family: inherit;
}

/* 单色方案下不引入新配色，只用墨色阶区分层次：
   关键字最重，字符串轻度压低，注释最淡，数字与函数保持正文 */
.code-hl .cm {
  color: var(--text-soft);
  font-style: italic;
}

.code-hl .st {
  color: var(--text-muted);
}

.code-hl .kw {
  color: var(--text);
  font-weight: 600;
}

.code-hl .nu {
  color: var(--text-muted);
}

.code-hl .fn {
  color: var(--text);
  font-weight: 500;
}
</style>
