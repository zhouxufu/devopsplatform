import test from 'node:test'
import assert from 'node:assert/strict'
import {readFile} from 'node:fs/promises'
import {runInNewContext} from 'node:vm'
const source=(await readFile(new URL('./uuid.js',import.meta.url),'utf8')).replace('export function randomUUID','function randomUUID')
test('intranet HTTP capability set generates valid distinct IDs without crypto.randomUUID',()=>{
 let call=0
 const crypto={getRandomValues(bytes){for(let i=0;i<bytes.length;i++)bytes[i]=(i+call)%256;call++;return bytes}}
 const context={crypto,Uint8Array};runInNewContext(source+';this.generate=randomUUID',context)
 const a=context.generate(),b=context.generate()
 assert.match(a,/^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/)
 assert.notEqual(a,b)
})
test('secure contexts retain the native generator',()=>{
 const context={crypto:{randomUUID:()=> 'native-id'},Uint8Array};runInNewContext(source+';this.generate=randomUUID',context);assert.equal(context.generate(),'native-id')
})
