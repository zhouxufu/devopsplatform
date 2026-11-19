import test from 'node:test'
import assert from 'node:assert/strict'
import {readFileSync} from 'node:fs'
test('every built-in document declares a device-width viewport for responsive layouts',()=>{
 for(const entry of ['shell','deploy','logs','monitor','research','workbench','api-center','devtools','delivery-editor']){
  const html=readFileSync(new URL('../../'+entry+'.html',import.meta.url),'utf8')
  assert.match(html,/<meta\s+name="viewport"\s+content="width=device-width,\s*initial-scale=1(?:\.0)?"\s*\/?\s*>/,entry)
 }
})
