// Compile the installed Arco Less tokens. Emit only declarations that differ
// from the official stylesheet; related padding/position arithmetic stays Arco's.
import fs from 'node:fs/promises'
import path from 'node:path'
import {createRequire} from 'node:module'
import {fileURLToPath} from 'node:url'
import {typographyPresets,typographyVariables} from '../web/src/shared/typography.js'
const root=path.resolve(path.dirname(fileURLToPath(import.meta.url)),'..')
const require=createRequire(path.join(root,'web/package.json')),less=require('less'),postcss=require('postcss')
const filename=require.resolve('@arco-design/web-vue/dist/arco.less'),source=await fs.readFile(filename,'utf8')
const baseline=postcss.parse((await less.render(source,{filename})).css)
function entries(ast){const counts=new Map(),out=new Map();ast.walkRules(rule=>{const parents=[];for(let p=rule.parent;p?.type==='atrule';p=p.parent)parents.unshift(p);const scope=parents.map(p=>p.name+' '+p.params).join('|')+'|'+rule.selector;const n=counts.get(scope)||0;counts.set(scope,n+1);out.set(scope+'#'+n,{rule,parents})});return out}
const base=entries(baseline),result=postcss.root()
const defaults=postcss.rule({selector:':root'});for(const [prop,value] of Object.entries(typographyVariables('standard')))defaults.append({prop,value});result.append(defaults)
result.append(postcss.comment({text:'Generated from installed Arco Less typography tokens. Edit typography.js / generator, not component overrides.'}))
for(const p of typographyPresets){
 const values={'font-size-body':p.body,'font-size-body-1':p.caption,'font-size-body-2':p.body-1,'font-size-body-3':p.body,'font-size-caption':p.caption,'font-size-title-1':p.body+2,'font-size-title-2':p.heading,'font-size-title-3':p.heading+4,'font-size-display-1':p.heading+12,'font-size-display-2':p.heading+24,'font-size-display-3':p.heading+32}
 const css=(await less.render(source,{filename,modifyVars:Object.fromEntries(Object.entries(values).map(([k,v])=>[k,v+'px']))})).css
 for(const [key,{rule,parents}] of entries(postcss.parse(css))){
  const original=base.get(key)?.rule;if(!original)throw Error('Arco theme rule shape changed: '+key)
  const declarations=rule.nodes.filter((d,i)=>d.type==='decl'&&d.value!==original.nodes[i]?.value)
  if(!declarations.length)continue
  const output=rule.clone({nodes:[]});output.selectors=rule.selectors.map(selector=>`html[data-console-typography="${p.id}"] ${selector}`);declarations.forEach(d=>output.append(d.clone()))
  let node=output;for(const parent of [...parents].reverse()){const wrapper=parent.clone({nodes:[]});wrapper.append(node);node=wrapper}result.append(node)
 }
}
await fs.writeFile(path.join(root,'web/src/shared/arcoTypography.css'),result.toString()+'\n')
console.log('Arco typography compiled: small / standard / large; official geometry preserved')
