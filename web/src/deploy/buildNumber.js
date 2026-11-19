// Global IDs are route/control keys, never an application's build sequence.
export function buildRunLabel(run){
 const number=Number(run?.number)
 if(Number.isSafeInteger(number)&&number>0)return '#'+number
 const id=Number(run?.id)
 return Number.isSafeInteger(id)&&id>0?'记录 ID '+id:'—'
}
export function artifactBuildLabel(artifact){
 return buildRunLabel({number:artifact?.source_number,id:artifact?.source_id})
}

// Releases use their own persisted application-local sequence. The global ID
// remains the key for opening, approving, continuing and rolling back an order.
export const releaseOrderLabel=buildRunLabel
