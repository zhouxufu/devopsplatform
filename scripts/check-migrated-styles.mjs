// Verify the built static import graph, not just source-level import spelling.
import {readFile} from 'node:fs/promises'
import path from 'node:path'
const root=path.resolve(import.meta.dirname,'../web/dist'),manifest=JSON.parse(await readFile(path.join(root,'.vite/manifest.json'),'utf8'))
for(const entry of Object.keys(manifest).filter(key=>manifest[key].isEntry||['src/shell/shadcnMain.js','src/shell/legacyMain.js'].includes(key))){
 const visited=new Set(),styles=new Set()
 function visit(key){if(visited.has(key))return;visited.add(key);const chunk=manifest[key];if(!chunk)throw Error('Missing built entry: '+key);for(const css of chunk.css||[])styles.add(css);for(const dependency of [...chunk.imports||[],...chunk.dynamicImports||[]])visit(dependency)}
 visit(entry)
 let hasToasterStyle=false
 for(const file of styles){const css=await readFile(path.join(root,file),'utf8');hasToasterStyle ||= css.includes('[data-sonner-toaster]');if(/\.arco-|\.el-input|\.el-select|--primary-6/.test(css))throw Error('Legacy CSS leaked into '+entry+': '+file)}
 if(!styles.size)throw Error('No component CSS loaded for '+entry)
 if(!hasToasterStyle)throw Error('Official Sonner styles missing from '+entry)
 console.log(entry+': '+styles.size+' isolated stylesheets; no legacy selectors')
}
const html=await readFile(path.join(root,'assets/delivery-editor.html'),'utf8')
if(html.includes('./assets/'))throw Error('Editor HTML still points to the parent assets directory')
for(const match of html.matchAll(/(?:src|href)="\.\/([^"]+)"/g))await readFile(path.join(root,'assets',match[1]))
console.log('Isolated editor asset paths verified')
