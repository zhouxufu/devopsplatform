// Compare preparation inputs, not transient artifact pickers, approval tokens
// or object insertion order. Go serializes map keys in sorted order.
export function preparationIdentity(x={}) {
 const sources=Object.fromEntries(Object.entries(x.artifact_sources||{}).sort(([a],[b])=>a.localeCompare(b)))
 return JSON.stringify([x.template_id,x.source_type,x.version_id||0,x.work_item_ids||[],x.source_change_ids||null,x.selected_change_ids||null,x.environment,x.scene||'',sources])
}
