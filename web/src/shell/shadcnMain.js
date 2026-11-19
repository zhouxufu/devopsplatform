import {createApp} from 'vue'
import App from './App.vue'
import '../shared/shadcn.css'
import './shell.css'
import {installWorkspaceNavigation,installWorkspaceRouterSync} from '../shared/workspaceNavigation.js'
import {initTheme} from '../shared/composables/theme.js'
import {installPlatformAuthGuard} from '../shared/platformAuth.js'
initTheme({designSystem:'shadcn'});installPlatformAuthGuard({shell:true});installWorkspaceNavigation()
const app=createApp(App);installWorkspaceRouterSync(app);app.mount('#app')
