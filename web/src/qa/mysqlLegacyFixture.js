// Synthetic compatibility fixture only; no real resource endpoints.
import {h,ref,provide} from 'vue';
import {createApp} from '/src/shared/app.js';
import Resources from '/src/deploy/pages/ResourcesView.vue';
import Editor from '/src/deploy/components/ApplicationResourcesEditor.vue';
import {Button} from '/src/shared/shadcn-ui.js';
import {initTheme,setTheme} from '/src/shared/composables/theme.js';
initTheme();setTheme({mode:new URLSearchParams(location.search).get('theme')||'light'});
window.addEventListener('error',e=>fetch('/__fixture/error',{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify({message:e.message})}));
const request=async(url,opt)=>{const r=await fetch('/'+url,opt);const b=await r.json();if(!r.ok)throw Error(b.error);return b};
createApp({setup(){provide('api',request);provide('resourcePlugin','dbms');const show=ref(false);return()=>h('main',{style:'padding:24px;min-width:0'},[h(Button,{onClick:()=>show.value=true},()=> '兼容绑定验收'),h(Resources,{plugin:'dbms'}),h(Editor,{show:show.value,initialService:'orders',services:[{name:'orders'}],onClose:()=>show.value=false})])}}).mount('#app');
