// Check actual template compilation as well as component imports. Missing slot
// scopes and dynamic components otherwise become runtime-only failures.
const fs=require('node:fs'),path=require('node:path')
const {parse,compileScript,compileTemplate,registerTS}=require('../web/node_modules/@vue/compiler-sfc')
const {baseParse}=require('../web/node_modules/@vue/compiler-dom')
registerTS(()=>require('../web/node_modules/typescript'))
let count=0
const errors=[],builtins=new Set(['RouterView','RouterLink','KeepAlive','Transition','TransitionGroup','Teleport','Suspense'])
const compilerFS={fileExists:file=>{try{return fs.statSync(file).isFile()}catch{return false}},readFile:file=>fs.readFileSync(file,'utf8')}
function check(file){
 const source=fs.readFileSync(file,'utf8'),parsed=parse(source,{filename:file}),d=parsed.descriptor
 for(const error of parsed.errors)errors.push(file+': '+(error.message||error))
 if(!d.template||!d.scriptSetup)return
 count++
 let bindings
 try{bindings=compileScript(d,{id:file,fs:compilerFS}).bindings}catch(error){errors.push(file+': '+error.message);return}
 try{const result=compileTemplate({source:d.template.content,filename:file,id:file,compilerOptions:{expressionPlugins:['typescript']}});for(const error of result.errors)errors.push(file+': '+(error.message||error))}catch(error){errors.push(file+': '+error.message)}
 function walk(n,inLabel=false){
  if(n.type===1){
   for(const prop of n.props.filter(p=>p.name==='on')){
    const event=prop.arg?.content||'',handler=prop.exp?.content||''
    if(event.startsWith('update:')&&/\$event\.target\b/.test(handler))errors.push(file+': model update emits a value, not a native DOM event')
    if(/\(\s*\w+\(\$event[^)]*\)\s*\)\s*\(/.test(handler))errors.push(file+': migrated menu handler calls the result of an event handler')
   }
   if(n.tag==='SheetContent'&&n.props.some(p=>p.name==='bind'&&p.arg?.content==='style'&&p.exp?.content?.includes('--editor-width'))&&!n.props.some(p=>p.name==='class'&&p.value?.content?.includes('data-[side=right]:sm:max-w-[var(--editor-width)]')))errors.push(file+': custom Sheet width must override the matching side variant')
   if(inLabel&&n.tag==='OptionPicker')errors.push(file+': use Field/FieldLabel around Combobox instead of a native label')
   if(['details','summary'].includes(n.tag))errors.push(file+': use official Accordion, DropdownMenu or Popover instead of '+n.tag)
   if(n.tag==='FieldLabel'&&n.children.some(c=>c.tag==='Checkbox'&&c.props.some(p=>p.name==='for')))errors.push(file+': put the checkbox loop on FieldLabel so its label stays in scope')
   if(n.tag==='TabsTrigger'&&n.children.some(c=>c.props?.some(p=>p.name==='slot'&&p.arg?.content==='radio')))errors.push(file+': TabsTrigger uses its default slot, not an old Radio slot')
   if(n.tag==='table'&&!file.replaceAll('\\','/').includes('/components/ui/'))errors.push(file+': use official Table')
   if(/^[A-Z]/.test(n.tag)&&!bindings[n.tag]&&!builtins.has(n.tag))errors.push(file+': unknown component '+n.tag)
   if(['Tabs','Accordion','Collapsible'].includes(n.tag)){
    const model=n.props.find(p=>p.name==='bind'&&p.arg?.content==='model-value')?.exp?.content, update=n.props.find(p=>p.name==='on'&&p.arg?.content==='update:model-value')?.exp?.content
    const local=model?.match(/\b_view\d+\b/)?.[0]
    if(local&&update&&!new RegExp('\\b'+local+'\\s*=').test(update))errors.push(file+': controlled primitive must update its local expansion state '+local)
   }
   if(['TabsContent','AccordionContent','CollapsibleContent'].includes(n.tag)&&n.props.some(p=>p.name==='bind'&&p.arg?.content==='hidden'))errors.push(file+': retained content must use v-show; the primitive manages its own hidden attribute')
   for(const prop of n.props)if(n.tag==='component'&&prop.name==='bind'&&prop.arg?.content==='is')for(const name of prop.exp.content.match(/\b[A-Z][a-zA-Z]+\b/g)||[])if(!bindings[name]&&name!=='Component')errors.push(file+': missing dynamic component '+name)
  }
  for(const child of n.children||[])walk(child,inLabel||n.tag==='label')
 }
 try{walk(baseParse(d.template.content,{isVoidTag:tag=>['area','base','br','col','embed','hr','img','input','link','meta','param','source','track','wbr'].includes(tag)}))}catch(error){errors.push(file+': '+error.message)}
}
function discover(dir){for(const entry of fs.readdirSync(dir,{withFileTypes:true})){const file=path.join(dir,entry.name);if(entry.isDirectory())discover(file);else if(file.endsWith('.vue'))check(file)}}
discover(path.resolve(__dirname,'../web/src'))
if(errors.length){console.error(errors.join('\n'));process.exitCode=1}else console.log('Vue imports and templates checked: '+count+' files')
