import {createApp as createVueApp,h} from 'vue'
import {TooltipProvider} from '@/components/ui/tooltip'
import {SidebarProvider} from '@/components/ui/sidebar'
import FeedbackHost from './components/FeedbackHost.vue'
import './shadcn.css'
import './business.css'
import {installWorkspaceNavigation,installWorkspaceRouterSync} from './workspaceNavigation.js'
installWorkspaceNavigation()
export function createApp(App){
 const app=createVueApp({render:()=>h(TooltipProvider,null,{default:()=>h(SidebarProvider,{class:'h-svh min-h-0',style:{'--sidebar-width':'15rem'}},{default:()=>[h(App,{class:'min-w-0 w-full flex-1'}),h(FeedbackHost)]})})})
 installWorkspaceRouterSync(app)
 return app
}
