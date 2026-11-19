import {MySQL} from '@codemirror/lang-sql'
import {highlightTree,tagHighlighter,tags} from '@lezer/highlight'

export const sqlTokenHighlighter=tagHighlighter([
 {tag:[tags.keyword,tags.operatorKeyword,tags.modifier],class:'sql-token-keyword'},
 {tag:[tags.string,tags.special(tags.string)],class:'sql-token-string'},
 {tag:[tags.number,tags.bool,tags.null],class:'sql-token-number'},
 {tag:tags.comment,class:'sql-token-comment'},
 {tag:[tags.typeName,tags.standard(tags.name)],class:'sql-token-type'},
 {tag:tags.operator,class:'sql-token-operator'},
])
const escapeHTML=value=>String(value).replace(/[&<>"']/g,char=>({'&':'&amp;','<':'&lt;','>':'&gt;','"':'&quot;',"'":'&#39;'}[char]))

// Syntax changes only the presentation. Preserve every original character and
// escape source text before inserting the parser's fixed, trusted token classes.
export function highlightSQL(value='') {
 const source=String(value??'');let html='',position=0
 highlightTree(MySQL.language.parser.parse(source),sqlTokenHighlighter,(from,to,classes)=>{
  html+=escapeHTML(source.slice(position,from))+`<span class="${classes}">${escapeHTML(source.slice(from,to))}</span>`
  position=to
 })
 return html+escapeHTML(source.slice(position))
}
