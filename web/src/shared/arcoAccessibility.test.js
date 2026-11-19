import {test} from 'node:test'
import assert from 'node:assert/strict'
import {collapseKeydown} from './arcoAccessibility.js'
function event(key,{header=true,disabled=false}={}){let clicks=0,prevented=false;return {key,target:{classList:{contains:()=>header},getAttribute:()=>String(disabled),click:()=>clicks++},preventDefault:()=>prevented=true,get clicks(){return clicks},get prevented(){return prevented}}}
test('official collapse header supports Enter and Space without handling form input keys',()=>{
 for(const key of ['Enter',' ']){const e=event(key);collapseKeydown(e);assert.equal(e.clicks,1);assert.equal(e.prevented,true)}
 for(const e of [event('Enter',{header:false}),event('Enter',{disabled:true}),event('ArrowDown')]){collapseKeydown(e);assert.equal(e.clicks,0);assert.equal(e.prevented,false)}
})
