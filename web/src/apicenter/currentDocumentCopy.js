// Read the rendered document section, so hidden tabs, collapsed children and
// other endpoints can never be accidentally included by a model-wide export.
const omitTags=new Set(['SCRIPT','STYLE','TEMPLATE','NOSCRIPT','SVG','INPUT','TEXTAREA','SELECT'])
const blockTags=new Set(['DIV','SECTION','P','ARTICLE','HEADER','FOOTER','DL','DT','DD','H1','H2','H3','H4','H5','H6'])
function hidden(element){
 if(element.hidden||element.getAttribute('aria-hidden')==='true'||element.hasAttribute('data-document-copy-ignore')||element.classList?.contains('sr-only'))return true
 if(element.getAttribute('data-state')==='closed'&&element.getAttribute('data-slot')?.endsWith('content'))return true
 const style=element.ownerDocument?.defaultView?.getComputedStyle?.(element)
 return style?.display==='none'||style?.visibility==='hidden'||style?.visibility==='collapse'
}
function children(element){return [...(element.childNodes||[])].map(render).join(' ')}
function tableText(table){
 const rows=[]
 const visit=element=>{
  if(element.nodeType!==1||hidden(element))return
  if(element.tagName==='TR'){
   const cells=[...(element.childNodes||[])].filter(n=>n.nodeType===1&&['TD','TH'].includes(n.tagName)&&!hidden(n)).map(cell=>children(cell).replace(/\s+/g,' ').trim())
   if(cells.length){const depth=Math.min(32,Math.max(0,Number(element.getAttribute('data-document-field-depth'))||0));cells[0]='  '.repeat(depth)+cells[0];rows.push(cells.join('\t'))}
   return
  }
  for(const node of element.childNodes||[])if(node.nodeType===1&&node.tagName!=='TABLE')visit(node)
 }
 for(const node of table.childNodes||[])visit(node)
 return rows.join('\n')
}
function render(node){
 if(node.nodeType===3)return node.textContent.replace(/\s+/g,' ')
 if(node.nodeType!==1||hidden(node)||omitTags.has(node.tagName))return ''
 const explicit=node.getAttribute('data-document-copy-value')
 if(explicit!==null)return explicit
 if(node.getAttribute('role')==='tablist'||node.tagName==='BUTTON'&&!node.hasAttribute('data-document-copy-include'))return ''
 if(node.tagName==='PRE')return '\n\n'+node.textContent+'\n\n'
 if(node.tagName==='BR')return '\n'
 if(node.tagName==='TABLE')return '\n\n'+tableText(node)+'\n\n'
 let heading=node.getAttribute('data-document-copy-heading')||''
 if(node.getAttribute('role')==='tabpanel'){
  heading=node.ownerDocument?.getElementById?.(node.getAttribute('aria-labelledby'))?.textContent?.trim()||heading
 }
 const content=children(node)
 return (heading?'\n\n'+heading+'\n\n':'')+(blockTags.has(node.tagName)?'\n\n'+content+'\n\n':content)
}
export function currentDocumentText(root,{title=''}={}){
 if(!root)return ''
 const content=render(root).replace(/[ \t]+\n/g,'\n').replace(/\n[ \t]+\n/g,'\n\n').replace(/\n{3,}/g,'\n\n').trim()
 return content?[title.trim(),content].filter(Boolean).join('\n\n'):''
}
