import test from 'node:test'
import assert from 'node:assert/strict'
import {highlightSQL} from './sqlHighlight.js'
const decode=value=>value.replace(/<span class="sql-token-[a-z]+">|<\/span>/g,'').replaceAll('&lt;','<').replaceAll('&gt;','>').replaceAll('&quot;','"').replaceAll('&#39;',"'").replaceAll('&amp;','&')
test('SQL highlighting retains exact source including Unicode, whitespace, comments and quoted HTML',()=>{
 const sql="-- 保留注释\r\n\tUPDATE `租赁` SET note='<img src=x onerror=alert(1)>', amount=12.50 WHERE id=7;\n"
 const html=highlightSQL(sql)
 assert.equal(decode(html),sql)
 for(const category of ['keyword','string','number','comment'])assert.match(html,new RegExp('sql-token-'+category))
 assert.doesNotMatch(html,/<img/)
 assert.match(html,/&lt;img/)
})
test('empty, incomplete and non-ASCII SQL remain copyable without rewriting',()=>{
 for(const sql of ['', 'SELECT', "SELECT '未闭合", ' \n\t', 'SELECT 1 < 2 && 2 > 1;'])assert.equal(decode(highlightSQL(sql)),sql)
 assert.equal(highlightSQL(null),'')
})
