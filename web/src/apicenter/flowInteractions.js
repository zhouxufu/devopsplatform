export const clampZoom=value=>Math.max(.4,Math.min(1.6,Math.round(value*100)/100))
export function canvasPoint(point,view){return {x:(point.x-view.pan.x)/view.zoom,y:(point.y-view.pan.y)/view.zoom}}
export function zoomAt(view,value,anchor){const zoom=clampZoom(value),world=canvasPoint(anchor,view);return {zoom,pan:{x:anchor.x-world.x*zoom,y:anchor.y-world.y*zoom}}}
export function fitCanvas(positions,width,height){
 const points=Object.values(positions);if(!points.length)return {zoom:1,pan:{x:20,y:28}}
 const left=Math.min(...points.map(p=>p.x)),top=Math.min(...points.map(p=>p.y)),right=Math.max(...points.map(p=>p.x+230)),bottom=Math.max(...points.map(p=>p.y+150))
 const zoom=clampZoom(Math.min((width-48)/(right-left),(height-48)/(bottom-top)))
 return {zoom,pan:{x:(width-(right-left)*zoom)/2-left*zoom,y:(height-(bottom-top)*zoom)/2-top*zoom}}
}
export function canvasShortcut(event,{hasNode=false,hasEdge=false,editable=true}={}){
 if(event.defaultPrevented||event.isComposing||event.ctrlKey||event.metaKey||event.altKey)return null
 const key=event.key
 if(key==='Escape')return 'clear'
 if(key===' '||key==='Spacebar')return 'pan'
 if(key==='Enter'&&hasNode)return 'inspect'
 if(['Delete','Backspace'].includes(key)&&editable&&(hasNode||hasEdge))return hasEdge?'disconnect':'remove'
 if(key==='f'||key==='F')return 'fit'
 if(key==='0')return 'reset'
 if(key==='+'||key==='=')return 'zoom-in'
 if(key==='-')return 'zoom-out'
 return null
}
