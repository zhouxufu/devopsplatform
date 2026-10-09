import {createApp} from 'vue'
import App from './App.vue'
import '../shared/theme.css'
import {initTheme} from '../shared/composables/theme.js'
initTheme()
createApp(App).mount('#app')
