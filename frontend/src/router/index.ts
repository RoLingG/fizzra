import { createRouter, createWebHistory, type RouteRecordRaw } from 'vue-router'

// 懒加载让 Vite 拆成独立 chunk，初始包保持小
const routes: RouteRecordRaw[] = [
  {
    path: '/',
    name: 'dashboard',
    component: () => import('@/views/DashboardView.vue'),
    meta: { titleKey: 'nav.dashboard', icon: 'Odometer' },
  },
  {
    path: '/projects',
    name: 'projects',
    component: () => import('@/views/ProjectsView.vue'),
    meta: { titleKey: 'nav.projects', icon: 'Folder' },
  },
  {
    path: '/projects/:id',
    name: 'project-detail',
    component: () => import('@/views/ProjectDetailView.vue'),
    props: true,
    meta: { titleKey: 'nav.projects', hidden: true },
  },
  {
    // 单独成页是为了能贴链接进工单，抽屉无法被链接
    path: '/issues/:id',
    name: 'issue-detail',
    component: () => import('@/views/IssueDetailView.vue'),
    props: true,
    meta: { titleKey: 'nav.projects', hidden: true },
  },
  {
    path: '/rules',
    name: 'rules',
    component: () => import('@/views/RulesView.vue'),
    meta: { titleKey: 'nav.rules', icon: 'SetUp' },
  },
  {
    path: '/system',
    name: 'system',
    component: () => import('@/views/SystemView.vue'),
    meta: { titleKey: 'nav.system', icon: 'Monitor' },
  },
  {
    path: '/:pathMatch(.*)*',
    redirect: '/',
  },
]

const router = createRouter({
  history: createWebHistory(),
  routes,
  scrollBehavior(to, _from, saved) {
    if (saved) return saved
    if (to.hash) return { el: to.hash, behavior: 'smooth' }
    return { top: 0 }
  },
})

export default router
