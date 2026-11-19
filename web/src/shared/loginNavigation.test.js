import {test} from 'node:test'
import assert from 'node:assert/strict'
import {safeLoginReturn,loginPageURL,requestedLoginReturn} from './loginNavigation.js'
import {passwordError,MIN_PASSWORD_LENGTH} from './passwordPolicy.js'
const origin='https://console.example'
test('login return accepts only shell or same-origin plugin UI',()=>{
 for(const value of ['https://evil.example/','//evil.example/','/api/users','/login?return_to=/','javascript:alert(1)','/api/plugins/deploy/ui/extra'])assert.equal(safeLoginReturn(value,origin),'/',value)
 const target='/api/plugins/research/ui?embedded=0#/work/42'
 assert.equal(safeLoginReturn(target,origin),target)
 assert.equal(safeLoginReturn('/#/',origin),'/#/')
 assert.equal(loginPageURL('/',false,origin),'/login')
 const url=new URL(loginPageURL(target,true,origin),origin)
 assert.equal(url.pathname,'/login');assert.equal(url.searchParams.get('return_to'),target);assert.equal(url.searchParams.get('reason'),'expired')
})
test('standalone plugin redirect retains its hash without overwriting a frozen return fragment',()=>{
 assert.equal(requestedLoginReturn('?return_to=%2Fapi%2Fplugins%2Fresearch%2Fui','#/work/42',origin),'/api/plugins/research/ui#/work/42')
 assert.equal(requestedLoginReturn('?return_to='+encodeURIComponent('/api/plugins/research/ui#/work/7'),'#/work/42',origin),'/api/plugins/research/ui#/work/7')
 assert.equal(requestedLoginReturn('?return_to=https%3A%2F%2Fevil.example','#/work/42',origin),'/')
})
test('new/reset passwords require six characters; blank edit retains the existing password',()=>{
 assert.equal(MIN_PASSWORD_LENGTH,6)
 assert.equal(passwordError('',{creating:true}),'密码至少 6 位')
 assert.equal(passwordError('12345'),'密码至少 6 位')
 assert.equal(passwordError('123456'),'')
 assert.equal(passwordError('一二三四五'),'密码至少 6 位')
 assert.equal(passwordError('一二三四五六'),'')
 assert.equal(passwordError(''),'')
})
