import test from 'node:test'
import assert from 'node:assert/strict'
import {appearancePresets,normalizeAppearance,migratePalette,normalizeDensity} from './palette.js'
import {appearanceTokens} from './appearanceTokens.js'
const luminance=hex=>hex.slice(1).match(/../g).map(v=>parseInt(v,16)/255).map(c=>c<=.04045?c/12.92:((c+.055)/1.055)**2.4).reduce((n,v,i)=>n+v*[.2126,.7152,.0722][i],0)
const contrast=(a,b)=>{const x=luminance(a),y=luminance(b);return (Math.max(x,y)+.05)/(Math.min(x,y)+.05)}
test('curated combinations reject a foreign colorway and migrate existing preferences',()=>{
 assert.deepEqual(appearancePresets.map(p=>p.id),['default','compact','layered','soft'])
 assert.deepEqual(normalizeAppearance({design:'soft',colorway:'violet'}),{design:'soft',colorway:'sage'})
 assert.deepEqual(normalizeAppearance({design:'soft',colorway:'blue'}),{design:'soft',colorway:'sage'})
 for(const value of [undefined,null,'x',{}, {design:'url(evil)'}])assert.deepEqual(normalizeAppearance(value),{design:'default',colorway:'neutral'})
 assert.deepEqual(migratePalette('blue'),{design:'layered',colorway:'blue'})
 assert.deepEqual(migratePalette('violet'),{design:'layered',colorway:'indigo'})
 assert.equal(normalizeDensity('comfortable;display:none'),'standard')
})
test('all six curated schemes provide readable light and dark semantic pairs',()=>{
 for(const design of appearancePresets.filter(p=>p.id!=='default'))for(const colorway of design.colorways)for(const dark of [false,true]){
  const t=appearanceTokens({design:design.id,colorway:colorway.id},dark)
  for(const [a,b] of [['--foreground','--background'],['--muted-foreground','--console-canvas'],['--primary-foreground','--primary'],['--accent-foreground','--accent'],['--sidebar-foreground','--sidebar'],['--sidebar-accent-foreground','--sidebar-accent']]){
   assert.ok(contrast(t[a],t[b])>=4.5,`${design.id}/${colorway.id}/${dark}: ${a} on ${b} = ${contrast(t[a],t[b])}`)
  }
 }
})

test('the original theme delegates both modes to official CSS without curated overrides',()=>{
 assert.deepEqual(migratePalette('neutral'),{design:'default',colorway:'neutral'})
 for(const dark of [false,true])assert.deepEqual(appearanceTokens({design:'default',colorway:'neutral'},dark),{})
})
