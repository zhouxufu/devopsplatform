import test from 'node:test'
import assert from 'node:assert/strict'
import {runtimeEnvironmentUpdate as update, editRuntimeVariable as edit} from './runtimeEnvironment.js'
const plain = value => JSON.parse(JSON.stringify(value))
test('only sends incremental changes and retains empty values, equals and whitespace', () => {
  assert.deepEqual(plain(update({runtime_env_text:'NEW=added\nTOKEN=a=b==\nEMPTY=\nSPACE= value '})), {runtime_env_patch:{NEW:'added',TOKEN:'a=b==',EMPTY:'',SPACE:' value '}})
  assert.deepEqual(update({runtime_env_text:'\n# keep saved variables'}), {})
})
test('deletion is explicit and clear is distinct from an empty patch', () => {
  assert.deepEqual(plain(update({runtime_env_delete_keys:['OLD'],runtime_env_text:'NEW=value'})), {runtime_env_patch:{OLD:null,NEW:'value'}})
  assert.deepEqual(update({runtime_env_clear:true}), {runtime_env:{}})
})
test('rejects contradictory edits and duplicate or malformed entries without echoing values', () => {
  for (const draft of [
    {runtime_env_clear:true,runtime_env_text:'NEW=private-value'},
    {runtime_env_clear:true,runtime_env_delete_keys:['OLD']},
    {runtime_env_text:'OLD=private-value',runtime_env_delete_keys:['OLD']},
    {runtime_env_text:'OLD=private-value\nOLD=other'},
    {runtime_env_text:'private-value'},
    {runtime_env_text:'BAD KEY=private-value'},
    {runtime_env_text:'KEY=private-value\0'}
  ]) assert.throws(() => update(draft), error => !error.message.includes('private-value'))
})
test('valid special object names remain ordinary variable keys', () => {
  assert.deepEqual(plain(update({runtime_env_text:'__proto__=value\nconstructor=another'})), {runtime_env_patch:JSON.parse('{"__proto__":"value","constructor":"another"}')})
})
test('modify key shortcut inserts a key once and selects only the current draft value', () => {
  assert.deepEqual(edit('', 'TOKEN'), {text:'TOKEN=',start:6,end:6})
  const added = edit('NEW=value', 'TOKEN')
  assert.equal(added.text, 'NEW=value\nTOKEN=')
  const changed = edit('NEW=value\nTOKEN= draft-value ', 'TOKEN')
  assert.equal(changed.text.slice(changed.start,changed.end), ' draft-value ')
  assert.equal(changed.text, 'NEW=value\nTOKEN= draft-value ')
})
