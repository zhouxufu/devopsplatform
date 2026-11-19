import {ref} from 'vue'
import {normalizeTypography,typographyVariables} from '../typography.js'
import {appearanceTokens,appearanceTokenKeys} from '../appearanceTokens.js'
import {normalizeAppearance,migratePalette,normalizeDensity} from '../palette.js'
const KEY='lp-theme',TYPOGRAPHY_KEY='lp-theme-typography',APPEARANCE_KEY='lp-theme-appearance',DENSITY_KEY='lp-theme-density',LEGACY_PALETTE_KEY='lp-theme-palette',modes=['light','dark','system']
const preference=(key,fallback)=>{try{return localStorage.getItem(key)||fallback}catch{return fallback}}
const readMode=()=>modes.includes(preference(KEY,''))?preference(KEY,'light'):'light'
function readAppearance(){
 const stored=preference(APPEARANCE_KEY,'')
 if(stored){try{return normalizeAppearance(JSON.parse(stored))}catch{return normalizeAppearance()}}
 return migratePalette(preference(LEGACY_PALETTE_KEY,'neutral'))
}
const system=window.matchMedia('(prefers-color-scheme: dark)'),initial=readAppearance()
export const themeMode=ref(readMode()),themeTypography=ref(normalizeTypography(preference(TYPOGRAPHY_KEY,'standard')))
export const themeDesign=ref(initial.design),themeColorway=ref(initial.colorway),themeDensity=ref(normalizeDensity(preference(DENSITY_KEY,'standard')))
export const themeDark=ref(themeMode.value==='dark'||themeMode.value==='system'&&system.matches)
export const isDark=()=>themeDark.value
export function themeSyncPayload(){return {type:'theme.sync',dark:themeDark.value,mode:themeMode.value,typography:themeTypography.value,appearance:{design:themeDesign.value,colorway:themeColorway.value},density:themeDensity.value}}
function broadcast(){for(const frame of document.querySelectorAll('iframe'))try{frame.contentWindow?.postMessage(themeSyncPayload(),location.origin)}catch{}}
function applyTheme(){
 const dark=themeMode.value==='dark'||themeMode.value==='system'&&system.matches
 themeDark.value=dark
 document.documentElement.classList.toggle('dark',dark)
 document.documentElement.style.colorScheme=dark?'dark':'light'
 const tokens=appearanceTokens({design:themeDesign.value,colorway:themeColorway.value},dark)
 for(const key of appearanceTokenKeys){
  if(key in tokens)document.documentElement.style.setProperty(key,tokens[key])
  else document.documentElement.style.removeProperty(key)
 }
 document.documentElement.dataset.consoleTheme='shadcn'
 document.documentElement.dataset.consoleDesign=themeDesign.value
 document.documentElement.dataset.consoleColorway=themeColorway.value
 document.documentElement.dataset.consoleDensity=themeDensity.value
 document.documentElement.dataset.consoleTypography=themeTypography.value
 for(const el of [document.documentElement,document.body])for(const [key,value] of Object.entries(typographyVariables(themeTypography.value)))el.style.setProperty(key,value)
 if(window.parent!==window)document.body.dataset.consoleEmbedded=''
 broadcast()
}
function assignAppearance(value){const selected=normalizeAppearance(value);themeDesign.value=selected.design;themeColorway.value=selected.colorway}
let initialized=false
export function initTheme(){
 applyTheme();if(initialized)return;initialized=true
 window.addEventListener('message',event=>{
  if(window.parent===window||event.source!==window.parent||event.origin!==location.origin||event.data?.type!=='theme.sync')return
  if(modes.includes(event.data.mode))themeMode.value=event.data.mode
  else if(typeof event.data.dark==='boolean')themeMode.value=event.data.dark?'dark':'light'
  if(event.data.typography!==undefined)themeTypography.value=normalizeTypography(event.data.typography)
  if(event.data.appearance!==undefined)assignAppearance(event.data.appearance)
  if(event.data.density!==undefined)themeDensity.value=normalizeDensity(event.data.density)
  applyTheme()
 })
 window.addEventListener('storage',event=>{
  if(![KEY,TYPOGRAPHY_KEY,APPEARANCE_KEY,DENSITY_KEY,LEGACY_PALETTE_KEY,null].includes(event.key))return
  try{if(event.storageArea!==localStorage)return}catch{return}
  themeMode.value=readMode();themeTypography.value=normalizeTypography(preference(TYPOGRAPHY_KEY,'standard'));assignAppearance(readAppearance());themeDensity.value=normalizeDensity(preference(DENSITY_KEY,'standard'));applyTheme()
 })
 system.addEventListener('change',()=>{if(themeMode.value==='system')applyTheme()})
}
export function setTheme({mode=themeMode.value,typography=themeTypography.value,design=themeDesign.value,colorway,density=themeDensity.value}={}){
 if(!modes.includes(mode))return
 // Changing design picks its curated default; display settings never replace the colorway.
 assignAppearance({design,colorway:colorway??(design===themeDesign.value?themeColorway.value:undefined)})
 themeMode.value=mode;themeTypography.value=normalizeTypography(typography);themeDensity.value=normalizeDensity(density)
 try{localStorage.setItem(KEY,mode);localStorage.setItem(TYPOGRAPHY_KEY,themeTypography.value);localStorage.setItem(APPEARANCE_KEY,JSON.stringify({design:themeDesign.value,colorway:themeColorway.value}));localStorage.setItem(DENSITY_KEY,themeDensity.value)}catch{}
 applyTheme()
}
export function toggleTheme(){setTheme({mode:themeDark.value?'light':'dark'});return themeDark.value}
