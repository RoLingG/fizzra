import { createApp } from 'vue'
import { createPinia } from 'pinia'
import ElementPlus from 'element-plus'
import * as ElementPlusIconsVue from '@element-plus/icons-vue'

// 顺序不能换：tokens → Element Plus → 自己的覆盖，靠级联让 main.css 不用 !important 也能改库样式
import '@/styles/tokens.css'
import 'element-plus/dist/index.css'
import '@/styles/main.css'

import App from './App.vue'
import router from './router'
import { i18n } from './locales'
import { installCardWaves } from './composables/useCardWave'
import { installSodaFill } from './composables/useSodaFill'
import { reveal } from './directives/reveal'

const app = createApp(App)

// 图标集合固定且不大，全量注册省得逐个 import
for (const [name, component] of Object.entries(ElementPlusIconsVue)) {
  app.component(name, component)
}

app.use(createPinia())
app.use(i18n)
app.use(router)
app.use(ElementPlus)

// 卡片海浪：指针进入任意 .card 时从指针处涌出三层灰浪 + 一层卡底色，见 useCardWave.ts
installCardWaves()

// 主按钮的碳酸汽水填充，液面推进、气泡翻涌、离开时退潮，见 useSodaFill.ts
installSodaFill()

// 滚动渐现：进入视口时从下方浮上来，从上方滚出去的回来时从上方掉下来
app.directive('reveal', reveal)

app.mount('#app')
