// Arco calls onBeforeUpload once per file. Batch one picker selection so the
// existing attachment validators see the total count/bytes before any upload.
export function uploadSelection(upload){
 let files=[],queued=false
 return file=>{
  files.push(file)
  if(!queued){queued=true;queueMicrotask(()=>{const selection=files;files=[];queued=false;upload(selection)})}
  return false // The business API owns attachment storage, never Arco's action.
 }
}
