// Explicit selections are separate from derived changes. Removing a demand
// never removes a shared change or a change the user explicitly selected.
export function scopeChanges(changes, workIDs = [], manualIDs = []) {
  const work = new Set(workIDs || []), manual = new Set(manualIDs || []);
  return (changes || []).filter(c => manual.has(c.id) || (c.work_items || []).some(w => work.has(w.id)));
}
export function scopePayload(version, workIDs, manualIDs) {
  return {version:version.version, title:version.title, description:version.description || '',
	preparation_template_id:version.preparation_template_id||0,preparation_environment:version.preparation_environment||'',
    work_item_ids:[...new Set(workIDs || [])], manual_change_ids:[...new Set(manualIDs || [])]};
}
export function frozenChangeIDs(versions = []) {
  return new Set((versions || []).filter(v=>v.status!=='draft').flatMap(v=>(v.changes||[]).map(c=>c.id)));
}
