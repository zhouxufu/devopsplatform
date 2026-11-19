import {createApp} from '../shared/app.js'
import {initTheme} from '../shared/composables/theme.js'
import {installPlatformAuthGuard} from '../shared/platformAuth.js'

import App from './App.vue'
initTheme();installPlatformAuthGuard();createApp(App).mount('#app')
