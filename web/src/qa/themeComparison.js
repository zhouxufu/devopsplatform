// QA-only entry: not included in production Rollup inputs.
import {createApp,h,ref,computed} from 'vue'
import {ConfigProvider,Select,Option,RadioGroup,Radio,Button,Tag,Alert} from '@arco-design/web-vue'
import zhCN from '@arco-design/web-vue/es/locale/lang/zh-cn'
import '@arco-design/web-vue/dist/arco.css'
import './themeComparison.css'
import {themeReferences} from './themeReferences.js'
const packages=await fetch('/__theme_audit/report.json').then(r=>r.json())
const report=[...packages,...themeReferences]
const params=new URLSearchParams(location.search)
const notes={'gi-demo':'接入候选 · 简洁；表头、暗色链接需加强对比','digitforce':'暂不建议直接用 · 暗色白底、控件文字低对比','taolu-publishing-platform':'绿色风格 · 浅色链接对比不足，需加深','wst2':'接入候选 · 接近当前默认；暗色链接需加亮','0000':'接入候选 · 蓝色、较大圆角；浅色链接需加深'}
notes.mengtu='粉色主色 · 原始 Less + 当前 Vue 2.59；按钮和链接文字对比需关注'
createApp({setup(){
 const theme=ref(report.some(t=>t.id===params.get('theme'))?params.get('theme'):'gi-demo'),mode=ref(params.get('mode')==='dark'?'dark':'light')
 const selected=computed(()=>report.find(t=>t.id===theme.value))
 const src=computed(()=>'/__theme_preview/deploy?'+new URLSearchParams({theme:theme.value,mode:mode.value}))
 function update(key,value){(key==='theme'?theme:mode).value=value;const url='?'+new URLSearchParams({theme:theme.value,mode:mode.value});if(params.get('gallery')==='1')location.href=url;else history.replaceState(null,'',url)}
 return()=>h(ConfigProvider,{locale:zhCN},()=>[
  h('header',{class:'theme-preview-controls'},[
   h('div',[h('strong','DevOps · 在线主题兼容预览'),h('small',selected.value?.reference?'官方演示截图 · 非本平台接入效果':'真实应用列表组件 · 合成数据 · 所有业务写操作已拦截')]),
   h(Select,{modelValue:theme.value,'onUpdate:modelValue':v=>update('theme',v),style:{width:'245px'},'aria-label':'选择预览主题'},()=>report.map(t=>h(Option,{value:t.id,label:t.label+(t.reference?' · 官方参考':t.vue?'':' · React，不可直用')}))),
   h(RadioGroup,{type:'button',modelValue:mode.value,'onUpdate:modelValue':v=>update('mode',v),'aria-label':'预览明暗模式'},()=>[h(Radio,{value:'light'},()=> '浅色'),h(Radio,{value:'dark'},()=> '深色')]),
   h(Tag,{color:selected.value?.compiled?'green':'orange'},()=>selected.value?.reference?'官方参考 · 未接入':selected.value?.compiled?'Vue 2.59 编译通过':'需移植'),
   h(Button,{href:'/theme-comparison-qa.html?gallery=1'},()=> '主题效果总览'),
  ]),
  params.get('gallery')==='1'?h('main',{class:'theme-preview-gallery'},[
   h('h2',packages.filter(t=>t.compiled).length+' 套主题 · 当前平台真实组件对比'),h('p','同一应用列表、同一组只读合成数据。上排浅色，下排深色；点击图片进入大图交互预览。编译通过不代表全部业务页面适配完成。'),
   h('div',{class:'theme-gallery-grid'},report.filter(t=>t.compiled).map(t=>h('article',[
    h('h3',t.label),h('small',t.package+'@'+t.version),h('p',notes[t.id]),
    ...['light','dark'].map(mode=>h('a',{href:'?theme='+t.id+'&mode='+mode},[h('span',mode==='light'?'浅色':'深色'),h('img',{src:'/__theme_audit/shots/'+t.id+'-'+mode+'.png',alt:t.label+(mode==='light'?'浅色':'深色')+'真实应用组件截图'})]))
   ]))),
   h('h2','补充 · 官方原版参考（非平台接入效果）'),
   ...themeReferences.map(t=>h('article',[
    h('h3',t.label+' · '+t.package),h(Alert,{type:'warning'},()=>t.notice),h('p',t.note),
    h(Button,{href:'?theme='+t.id+'&mode=light'},()=> '查看官方浅色／深色效果')
   ])),h(Alert,{type:'warning'},()=> 'Merry Christmas 是 React 主题，尚不可直接用于 Vue。带圣诞装饰图及动画，并依赖外部 HTTP 图片；需要单独移植与资源本地化。'),
   h(Button,{href:'?theme=christmas&mode=light'},()=> '查看圣诞主题原版截图与检查结果')
  ]):selected.value?.compiled?h('iframe',{class:'theme-preview-frame',key:src.value,src:src.value,title:selected.value.label+' '+mode.value+' 实际应用预览'}):selected.value?.reference?h('section',{class:'theme-preview-unavailable'},[
   h('h2',selected.value.label+' · 官方 '+(mode.value==='dark'?'深色':'浅色')+'参考'),
   h(Alert,{type:'warning'},()=>selected.value.notice),h('p',selected.value.note),
   h('img',{class:'christmas-reference',src:'/__theme_audit/shots/'+selected.value.screenshots[mode.value],alt:selected.value.label+' 官方 '+mode.value+' 原版，非本平台接入效果'}),
   h(Button,{type:'primary',href:selected.value.source,target:'_blank',rel:'noopener'},()=> '打开官方互动预览')
  ]):h('section',{class:'theme-preview-unavailable'},[
   h('h2','Merry Christmas · React 主题'),h(Alert,{type:'warning'},()=> '不是 Vue 原生主题，不能直接替换当前平台样式。包内包含圣诞装饰图、动画及 HTTP 外部图片，需做 Vue 移植、资源本地化和暗色适配。'),
   h('p','下面是官方 React 原版截图，不是本平台接入效果。'),h('img',{class:'christmas-reference',src:'/__theme_audit/shots/christmas-official.png',alt:'Merry Christmas 官方 React 原版：积雪按钮与圣诞装饰，非本平台接入效果'}),h(Button,{type:'primary',href:'https://arco.design/themes/preview/1285',target:'_blank',rel:'noopener'},()=> '打开官方互动预览')
  ])
 ])
}}).mount('#app')
