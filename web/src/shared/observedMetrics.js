// Missing telemetry is unknown. Keep actual zero readings distinct from missing values.
export function metricNumber(value){
 if(typeof value!=='number'&&typeof value!=='string')return null
 if(typeof value==='string'&&!value.trim())return null
 const number=Number(value)
 return Number.isFinite(number)&&number>=0?number:null
}
export function formatMetric(value,unit=''){
 const number=metricNumber(value)
 return number===null?'—':String(number)+unit
}
export function sumMetrics(values){
 if(!Array.isArray(values))return null
 const numbers=values.map(metricNumber)
 return numbers.some(n=>n===null)?null:numbers.reduce((sum,n)=>sum+n,0)
}
export function formatMetricBytes(value){
 const number=metricNumber(value)
 if(number===null)return '—'
 if(number===0)return '0 B'
 const units=['B','KB','MB','GB','TB','PB'],index=Math.max(0,Math.min(Math.floor(Math.log(number)/Math.log(1024)),units.length-1))
 return (number/1024**index).toFixed(index?1:0)+' '+units[index]
}
