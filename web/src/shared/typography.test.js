import test from 'node:test'
import assert from 'node:assert/strict'
import {typographyPresets,normalizeTypography,typographyVariables,typographyComponentSize} from './typography.js'
test('type preferences are allowlisted and default to readable standard size',()=>{for(const v of [null,undefined,'',{},'large;display:none'])assert.equal(normalizeTypography(v),'standard');assert.equal(typographyVariables()['--font-size-body-3'],'16px')})
test('all presets scale type and icons together with official component size',()=>{assert.deepEqual(typographyPresets.map(p=>typographyComponentSize(p.id)),['small','medium','large']);const sizes=typographyPresets.map(p=>typographyVariables(p.id));for(const key of ['--font-size-body-1','--font-size-body-3','--font-size-title-2','--icon-size-medium']){assert.ok(parseFloat(sizes[0][key])<parseFloat(sizes[1][key]));assert.ok(parseFloat(sizes[1][key])<parseFloat(sizes[2][key]))}})
