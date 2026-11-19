// Platform-management pages use the same shadcn providers in their own document.
import {createApp} from '../shared/app.js'
import PlatformPage from './PlatformPage.vue'

import {initTheme} from '../shared/composables/theme.js'
import {installPlatformAuthGuard} from '../shared/platformAuth.js'
initTheme();installPlatformAuthGuard();createApp(PlatformPage).mount('#app')
