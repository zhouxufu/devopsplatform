import test from 'node:test'
import assert from 'node:assert/strict'
import {readFileSync} from 'node:fs'
const navigation=readFileSync(new URL('./ConsoleNavigation.vue',import.meta.url),'utf8')
const tokens=readFileSync(new URL('../shared/shadcn.css',import.meta.url),'utf8')
test('official neutral sidebar selection remains readable in light and dark modes',()=>{
 const light=tokens.slice(tokens.indexOf(':root {'),tokens.indexOf('.dark {')),dark=tokens.slice(tokens.indexOf('.dark {'))
 for(const css of [light,dark]){
  const l=Number(css.match(/--sidebar-accent-foreground: oklch\(([\d.]+) 0 0\)/)[1]),b=Number(css.match(/--sidebar-accent: oklch\(([\d.]+) 0 0\)/)[1])
  const ratio=(Math.max(l**3,b**3)+.05)/(Math.min(l**3,b**3)+.05)
  assert.ok(ratio>=4.5,`neutral sidebar contrast ${ratio}`)
 }
})
test('official sidebar owns active styling and exposes comfortable menu density',()=>{
 assert.match(navigation,/SidebarMenuButton/);assert.match(navigation,/:is-active=/)
 assert.match(navigation,/menu-button\]\]:h-10/);assert.match(navigation,/menu-sub-button\]\]:h-9/)
 assert.doesNotMatch(navigation,/@arco-design|arco-menu/)
 const variants=readFileSync(new URL('../components/ui/sidebar/index.ts',import.meta.url),'utf8')
 assert.match(variants,/data-active:font-medium/);assert.match(variants,/data-active:bg-sidebar-accent/)
 const shell=readFileSync(new URL('./App.vue',import.meta.url),'utf8')
 assert.match(shell,/'--sidebar-width':'var\(--console-sidebar-width,15rem\)'/)
})
