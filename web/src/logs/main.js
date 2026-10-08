import { createApp } from 'vue'
import { createRouter, createWebHashHistory } from 'vue-router'
import App from './App.vue'
import SearchView from './pages/SearchView.vue'
import LiveView from './pages/LiveView.vue'
import SubsView from './pages/SubsView.vue'
import StatusView from './pages/StatusView.vue'
import '../shared/theme.css'
import { initTheme } from '../shared/composables/theme.js'
initTheme()

const router = createRouter({
  history: createWebHashHistory(),
  routes: [
    { path: '/', redirect: '/search' },
    { path: '/search', component: SearchView },
    { path: '/live', component: LiveView },
    { path: '/subs', component: SubsView },
    { path: '/status', component: StatusView },
  ],
})
createApp(App).use(router).mount('#app')
