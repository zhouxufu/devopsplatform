import { installPlatformAuthGuard } from '../shared/platformAuth.js'
import {createApp} from '../shared/app.js'
import App from './App.vue'

import {initTheme} from '../shared/composables/theme.js'
initTheme()
installPlatformAuthGuard()
createApp(App).mount('#app')
