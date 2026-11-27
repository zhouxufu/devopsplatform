import {createApp} from 'vue'
import App from './App.vue'
import '../shared/shadcn.css'
import './shell.css'
import {installWorkspaceNavigation,installWorkspaceRouterSync} from '../shared/workspaceNavigation.js'
import {initTheme} from '../shared/composables/theme.js'
import {installPlatformAuthGuard} from '../shared/platformAuth.js'
initTheme({designSystem:'shadcn'})
const auth=installPlatformAuthGuard({shell:true})
// A plugin document can be redirected here after expiry. Only the owning
// console may render login; mounting another shell would nest the entire UI.
if(window.parent!==window)auth.requireLogin()
else{
 installWorkspaceNavigation()
 const app=createApp(App);installWorkspaceRouterSync(app);app.mount('#app')
}
