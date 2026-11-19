// Keep this operation in the click handler: HTTP pages need synchronous selection
// copying, and a dialog's focus trap requires the temporary field inside it.
function selectionCopy(text,doc){
 if(!doc?.body||typeof doc.execCommand!=='function')return false
 const active=doc.activeElement
 let selection,ranges=[],inputSelection,temp
 try{
  selection=doc.defaultView?.getSelection?.()
  for(let i=0;i<(selection?.rangeCount||0);i++)ranges.push(selection.getRangeAt(i).cloneRange())
  if(active&&typeof active.selectionStart==='number')inputSelection=[active.selectionStart,active.selectionEnd,active.selectionDirection]
 }catch{/* Selection restoration is best effort; it must not prevent copying. */}
 try{
  const owner=active?.closest?.('[role="dialog"],[role="alertdialog"],dialog[open]')||doc.body
  temp=doc.createElement('textarea')
  temp.value=text
  temp.setAttribute('readonly','')
  temp.setAttribute('tabindex','-1')
  temp.setAttribute('aria-label','临时复制内容')
  temp.style.cssText='position:fixed;top:0;left:0;width:1px;height:1px;opacity:0;pointer-events:none;'
  owner.appendChild(temp)
  temp.focus({preventScroll:true})
  temp.select()
  temp.setSelectionRange(0,text.length)
  return doc.execCommand('copy')===true
 }catch{return false}
 finally{
  temp?.remove()
  try{
   if(active?.isConnected!==false)active?.focus?.({preventScroll:true})
   if(inputSelection)active?.setSelectionRange?.(...inputSelection)
   else if(selection){selection.removeAllRanges();for(const range of ranges)selection.addRange(range)}
  }catch{/* A closed dialog or unsupported input type must not mask copy success. */}
 }
}

export async function writeClipboardText(value,{navigator:nav=globalThis.navigator,document:doc=globalThis.document}={}){
 const text=String(value??'')
 try{
  if(typeof nav?.clipboard?.writeText==='function'){
   await nav.clipboard.writeText(text)
   return true
  }
 }catch{/* Permission denial or embedded-page restrictions may still allow selection copy. */}
 return selectionCopy(text,doc)
}
