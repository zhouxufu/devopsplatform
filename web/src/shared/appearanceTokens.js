import {normalizeAppearance} from './palette.js'
// Coordinated light/dark surfaces, navigation and accents. All entries are trusted constants.
const schemes={
 graphite:{
  light:['#ffffff','#252b36','#ffffff','#f3f4f6','#68717e','#e4e7ec','#2a3546','#ffffff','#edf1f6','#26354b','#1d2533','#c4cedb','#344155','#ffffff','#303a49',['#93a2b8','#71829c','#526783','#394e6b','#263b56']],
  dark:['#101720','#e6e9ef','#19222e','#263241','#aab5c4','#344255','#c4cedb','#172332','#2b3b51','#e1e8f4','#141d29','#c4cedb','#34465e','#f0f4fb','#344255',['#bccce3','#93accf','#7998c3','#6585b1','#52739d']],
 },
 steel:{
  light:['#f3f6fa','#25364a','#ffffff','#eef3f8','#596b83','#dce5ee','#285d8f','#ffffff','#e5eef8','#234f79','#172b40','#bfcee0','#284865','#ffffff','#2c435d',['#8bb9db','#6197c5','#467faf','#2d628e','#204c70']],
  dark:['#101a25','#e0eaf5','#192838','#23384b','#a7bad0','#314b64','#9dbfe1','#102738','#263f5b','#dae9fb','#132337','#c4d5e8','#304e6b','#eff6ff','#314b64',['#b1d0ed','#8fb6d9','#71a0c8','#598ab7','#4674a0']],
 },
 blue:{
  light:['#f3f5f9','#252f42','#ffffff','#f2f5fa','#637189','#e3e8f0','#315eb7','#ffffff','#eaf0fc','#23467f','#ffffff','#4e5d76','#eaf0fc','#23467f','#e3e8f0',['#a7c5ef','#7ca3de','#527bc7','#315eb7','#27498d']],
  dark:['#101722','#e4ebf7','#192332','#263348','#a5b4cb','#35455f','#9dbbff','#142342','#253957','#d4e3ff','#172130','#c0cde1','#2b4062','#e1ebff','#35455f',['#bdd1ff','#9dbbff','#7a9ae5','#6585ce','#5270b6']],
 },
 indigo:{
  light:['#f5f5fa','#303047','#ffffff','#f1f0f7','#6b6a81','#e4e3ef','#514fa1','#ffffff','#eeedf9','#45447d','#ffffff','#5b5a73','#eeedf9','#45447d','#e4e3ef',['#c1bfed','#a09dd8','#7e7bc0','#615eaa','#48468b']],
  dark:['#171722','#e9e8f4','#232331','#333247','#b5b3cd','#45435c','#bab8f6','#22213e','#373551','#e5e2ff','#20202f','#ccc9e2','#3b3859','#efecff','#45435c',['#d6d2ff','#bab8f6','#a29de4','#8983cc','#726bb5']],
 },
 sage:{
  light:['#f5f5f0','#303c36','#ffffff','#f0f2ec','#657062','#e0e4da','#3a594b','#ffffff','#e7ede2','#334b3d','#edf0e8','#52644f','#dce5d8','#304b36','#dce3d7',['#b1c6ac','#8da98b','#6e8b72','#52745c','#395b45']],
  dark:['#171e19','#e3e9df','#222d25','#303e32','#b0bead','#415043','#a9c7ad','#172a1f','#344b38','#deedda','#202a22','#c2d0bd','#354c38','#e2efdd','#415043',['#c3d9bc','#a9c7ad','#8fae94','#789880','#628169']],
 },
 sand:{
  light:['#f8f5ef','#40362c','#fffdf9','#f3eee5','#776958','#e6ded2','#76583d','#ffffff','#efe5d6','#64482d','#f0e9df','#71614e','#e8dccb','#604729','#e1d7c8',['#dac1a2','#bca07c','#a1845e','#866948','#6b5135']],
  dark:['#211d18','#efe6da','#2d2721','#40362c','#c3b49e','#564737','#dabb9d','#302115','#45382d','#f0d8be','#2b241d','#d6c5af','#4d3d2e','#f5dfc4','#564737',['#e6d1b4','#dabb9d','#bda080','#a68a67','#90734f']],
 },
}
export function appearanceTokens(value,dark=false){
 const {design,colorway}=normalizeAppearance(value)
 // The original theme is owned by shadcn.css, including its light/dark tokens.
 if(design==='default')return {}
 const [canvas,foreground,card,muted,dim,border,primary,primaryInk,accent,accentInk,sidebar,sidebarInk,sidebarAccent,sidebarAccentInk,sidebarBorder,charts]=schemes[colorway][dark?'dark':'light']
 const tokens={
  '--console-canvas':canvas,'--background':card,'--foreground':foreground,'--card':card,'--card-foreground':foreground,
  '--popover':card,'--popover-foreground':foreground,'--muted':muted,'--muted-foreground':dim,'--secondary':muted,'--secondary-foreground':foreground,
  '--border':border,'--input':border,'--primary':primary,'--primary-foreground':primaryInk,'--accent':accent,'--accent-foreground':accentInk,'--ring':primary,
  '--sidebar':sidebar,'--sidebar-foreground':sidebarInk,'--sidebar-primary':primary,'--sidebar-primary-foreground':primaryInk,
  '--sidebar-accent':sidebarAccent,'--sidebar-accent-foreground':sidebarAccentInk,'--sidebar-border':sidebarBorder,'--sidebar-ring':primary,
  '--destructive':dark?'#ff8b86':'#dc2626','--radius':design==='compact'?'.35rem':design==='layered'?'.6rem':'.9rem',
 }
 charts.forEach((color,i)=>tokens['--chart-'+(i+1)]=color)
 return tokens
}

// Remove every curated override when returning to the original CSS theme.
export const appearanceTokenKeys=Object.keys(appearanceTokens({design:'compact',colorway:'graphite'}))
