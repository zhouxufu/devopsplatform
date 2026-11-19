// Arco Vue 2.59 exposes focusable Collapse headers but only handles clicks.
// Preserve the Enter/Space behavior of the native disclosures being replaced;
// all state changes still go through the official component's click handler.
export function collapseKeydown(event) {
  const header=event.target
  if(event.defaultPrevented||event.altKey||event.ctrlKey||event.metaKey||event.shiftKey||
    !header?.classList?.contains('arco-collapse-item-header')||header.getAttribute('aria-disabled')==='true')return
  if(event.key==='Enter'||event.key===' '){event.preventDefault();header.click()}
}
let installed=false
export function installArcoAccessibility(){if(!installed){document.addEventListener('keydown',collapseKeydown);installed=true}}
