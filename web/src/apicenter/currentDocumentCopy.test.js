import test from 'node:test'
import assert from 'node:assert/strict'
import {currentDocumentText} from './currentDocumentCopy.js'
// Minimal DOM contract for deterministic content extraction tests. Browser
// geometry, focus and clipboard permissions are intentionally not simulated.
function el(tag,attributes={},...children){
 const attrs={...attributes},node={nodeType:1,tagName:tag.toUpperCase(),childNodes:children.flat().map(v=>typeof v==='string'?{nodeType:3,textContent:v}:v),hidden:Object.hasOwn(attrs,'hidden'),getAttribute:key=>Object.hasOwn(attrs,key)?String(attrs[key]):null,hasAttribute:key=>Object.hasOwn(attrs,key),classList:{contains:value=>String(attrs.class||'').split(/\s+/).includes(value)}}
 Object.defineProperty(node,'textContent',{get:()=>node.childNodes.map(v=>v.textContent||'').join('')})
 node.style={display:attrs.display||'',visibility:attrs.visibility||''}
 return node
}
function mount(root,...others){
 const ids=new Map(),doc={defaultView:{getComputedStyle:node=>node.style},getElementById:id=>ids.get(id)}
 const walk=node=>{node.ownerDocument=doc;if(node.nodeType===1){if(node.getAttribute('id'))ids.set(node.getAttribute('id'),node);node.childNodes.forEach(walk)}}
 walk(root);others.forEach(walk);return root
}
test('copy includes only current visible panel and excludes actions, other endpoints and closed definitions',()=>{
 const root=mount(el('div',{},el('div',{},el('span',{},'订单服务 · 测试'),el('button',{'data-document-copy-include':''},'/orders/{id}')),el('div',{'data-document-copy-ignore':''},el('button',{},'复制当前内容'),el('button',{},'生成用例')),el('div',{role:'tablist'},el('button',{id:'request'},'请求参数'),el('button',{id:'response'},'响应定义')),el('section',{role:'tabpanel','aria-labelledby':'request',display:'none'},'HIDDEN_REQUEST'),el('section',{role:'tabpanel','aria-labelledby':'response'},el('p',{},'HTTP 状态码：200'),el('p',{},'当前响应说明'),el('div',{'data-slot':'accordion-content','data-state':'closed'},'HIDDEN_SCHEMA'))),el('main',{},'OTHER_ENDPOINT'))
 const value=currentDocumentText(root,{title:'查询订单'})
 for(const expected of ['查询订单','订单服务 · 测试','/orders/{id}','响应定义','HTTP 状态码：200','当前响应说明'])assert.ok(value.includes(expected),expected)
 for(const excluded of ['请求参数','HIDDEN_REQUEST','HIDDEN_SCHEMA','OTHER_ENDPOINT','生成用例','复制当前内容'])assert.ok(!value.includes(excluded),excluded)
})
test('table copy preserves rendered child indentation and requirement labels without expand controls',()=>{
 const cell=(value,attributes={})=>el('td',attributes,value)
 const table=el('table',{},el('thead',{},el('tr',{},el('th',{},'字段'),el('th',{},'类型'),el('th',{},'关联需求'))),el('tbody',{},el('tr',{'data-document-field-depth':'0'},cell(el('div',{},el('button',{},'展开 /buyer'),el('button',{'data-document-copy-include':''},'buyer'))),cell('object'),cell('—')),el('tr',{'data-document-field-depth':'1'},cell(el('button',{'data-document-copy-include':''},'name')),cell('string'),cell(el('button',{'data-document-copy-include':''},'#12 新增购房人姓名'))),el('tr',{hidden:''},cell('COLLAPSED_CHILD'))))
 const value=currentDocumentText(mount(el('section',{},table)))
 assert.ok(value.includes('字段\t类型\t关联需求'));assert.ok(value.includes('buyer\tobject\t—'));assert.ok(value.includes('  name\tstring\t#12 新增购房人姓名'))
 assert.ok(!value.includes('展开'));assert.ok(!value.includes('COLLAPSED_CHILD'))
})
test('JSON view is copied verbatim with indentation and large integer precision',()=>{
 const json='{\n  "id": 9223372036854775807,\n  "name": "张三",\n  "note": "```"\n}'
 const root=mount(el('section',{'data-document-copy-heading':'响应体 · JSON'},el('div',{role:'tablist'},el('button',{},'缩进表格'),el('button',{},'JSON'),el('button',{},'上下对照')),el('p',{},'文档声明的示例'),el('pre',{},json),el('table',{display:'none'},el('tr',{},el('td',{},'HIDDEN_TABLE')))))
 const value=currentDocumentText(root)
 assert.ok(value.includes('响应体 · JSON'));assert.ok(value.includes(json));assert.ok(!value.includes('HIDDEN_TABLE'));assert.ok(!value.includes('上下对照'))
})
test('hidden roots and accessibility-only labels do not leak into copied document',()=>{
 assert.equal(currentDocumentText(mount(el('div',{display:'none'},'HIDDEN')),{title:'标题'}),'')
 const root=mount(el('section',{},el('span',{class:'sr-only'},'ACCESSIBILITY_LABEL'),el('span',{'aria-hidden':'true'},'ICON'),el('p',{},'可见说明')))
 assert.equal(currentDocumentText(root),'可见说明')
})
