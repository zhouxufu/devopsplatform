import {test} from 'node:test'
import assert from 'node:assert/strict'
import {personalView} from './viewMemory.js'
const storage=()=>{const values=new Map();return {getItem:key=>values.get(key),setItem:(key,value)=>values.set(key,value)}}
test('view preferences isolate actors/workspaces and seed only new workspaces',()=>{
 const local=storage(),session=storage(),defaults={q:'',status:'',page:1,mine:false}
 const view=(actor,workspace)=>personalView({view:'work',actor,workspace,local,session})
 view('alice','one').write({q:'我的需求',status:'closed',page:2,mine:true,sql:'secret',token:'secret'},defaults)
 assert.deepEqual(view('bob','one').read(defaults),defaults)
 assert.equal(view('alice','two').read(defaults).q,'我的需求')
 view('alice','two').write({q:'另一窗口',page:1},defaults)
 assert.equal(view('alice','one').read(defaults).q,'我的需求')
 assert.equal(view('alice','three').read(defaults).q,'另一窗口')
 view('alice','untouched').read(defaults)
 view('alice','two').write({q:'再次修改',page:1},defaults)
 assert.equal(view('alice','untouched').read(defaults).q,'另一窗口','a restored workspace must not follow later defaults')
 assert(!JSON.stringify(view('alice','one').read(defaults)).includes('secret'))
})
test('corrupt/blocked storage and malformed preferences do not break the UI',()=>{
 const broken={getItem(){throw Error('blocked')},setItem(){throw Error('quota')}}
 const defaults={q:'',page:1,size:20,mine:false}
 const view=personalView({view:'work',actor:'alice',local:broken,session:broken})
 assert.deepEqual(view.read(defaults),defaults);assert.doesNotThrow(()=>view.write({},defaults))
 const local=storage(),session=storage(),good=personalView({view:'work',actor:'alice',local,session})
 good.write({q:'x'.repeat(501),page:-2,size:'100',mine:'true'},defaults)
 assert.deepEqual(good.read(defaults),defaults)
})
test('array filters are bounded, cloned and record kinds remain independent',()=>{
 const local=storage(),session=storage(),defaults={q:'',tags:[],page_size:20}
 const view=name=>personalView({view:name,actor:'alice',workspace:'one',local,session})
 view('apps').write({q:'orders',tags:['java','api'],page_size:100},defaults)
 const result=view('apps').read(defaults);result.tags.push('local-edit')
 assert.deepEqual(view('apps').read(defaults).tags,['java','api'])
 assert.deepEqual(view('versions').read(defaults),defaults)
 view('apps').write({tags:[{token:'never-persist'}],page_size:999},defaults)
 assert.deepEqual(view('apps').read(defaults),defaults)
})
