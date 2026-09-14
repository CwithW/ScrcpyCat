import { createRouter, createWebHistory } from 'vue-router'

const routes = [
  {
    path: '/',
    name: 'Dashboard',
    components: {
      default: () => import('@/views/DeviceList.vue')
    }
  },
  {
    path: '/deploy',
    name: 'Deploy',
    components: {
      default: () => import('@/views/DeployPage.vue')
    }
  },
  {
    path: '/monitor',
    name: 'Monitor',
    components: {
      default: () => import('@/views/Dashboard.vue')
    }
  },
  {
    path: '/advanced',
    name: 'Advanced',
    components: {
      default: () => import('@/views/AdvancedPage.vue')
    }
  },
  { path: '/files', component: () => import('@/views/FileManagerPage.vue') },
  { path: '/batch', component: () => import('@/views/BatchControlPage.vue') },
  { path: '/shares', component: () => import('@/views/ShareAdminPage.vue') },
  { path: '/admin', component: () => import('@/views/UserAdminPage.vue') },
  { path: '/terminal', component: () => import('@/views/DeviceList.vue') },
  { path: '/login', component: () => import('@/views/Login.vue') },
  { path: '/:pathMatch(.*)*', redirect: '/' },
  {
    path: '/share',
    name: 'ShareDevice',
    components: {
      default: () => import('@/views/ShareView.vue')
    }
  }
]

const router = createRouter({
  history: createWebHistory(),
  routes
})

export default router
