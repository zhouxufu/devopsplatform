// Keep the active query object (including its target, editor and results) when another tab closes.
export function closeQuerySession(sessions,closingID,activeID){
 const at=sessions.findIndex(s=>s.id===closingID)
 if(at<0||sessions.length<=1)return null
 const remaining=sessions.filter(s=>s.id!==closingID),kept=remaining.findIndex(s=>s.id===activeID)
 return {sessions:remaining,index:kept>=0?kept:Math.min(at,remaining.length-1)}
}
