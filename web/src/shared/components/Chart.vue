// 通用 SVG 折线图（多系列 + Y 轴 + 简单悬停）
<script setup>
import { computed } from 'vue'
const props = defineProps({
  times: Array, series: Array, unit: String, yMax: Number, stacked: Boolean, height: { type: Number, default: 200 },
})
const W = 920, P = 46, PB = 20
const H = props.height
const n = computed(() => props.times?.length || 0)
const max = computed(() => {
  if (props.yMax) return props.yMax
  let m = 0
  if (props.stacked && n.value) {
    for (let i = 0; i < n.value; i++) { let t = 0; props.series.forEach(s => t += s.vals[i] || 0); if (t > m) m = t }
  } else props.series?.forEach(s => s.vals.forEach(v => { if (v > m) m = v }))
  return Math.max(m * 1.15, 1)
})
const X = i => P + (W - P * 1.2) * i / Math.max(n.value - 1, 1)
const Y = v => P + (H - P - PB) * (1 - Math.min(v / max.value, 1))
const fmtV = v => v >= 10000 ? (v / 1000).toFixed(0) + 'k' : v >= 1000 ? (v / 1000).toFixed(1) + 'k' : Math.round(v * 10) / 10
const fmtT = t => { const d = new Date(t); return String(d.getHours()).padStart(2, '0') + ':' + String(d.getMinutes()).padStart(2, '0') }
const geoms = computed(() => {
  const cum = new Array(n.value).fill(0)
  return props.series.map(s => {
    const topV = s.vals.map((v, i) => cum[i] + (v || 0))
    const baseV = cum.slice()
    if (props.stacked) cum.splice(0, cum.length, ...topV)
    return { s, pts: topV.map((v, i) => `${X(i)},${Y(v)}`).join(' '), base: baseV.map((v, i) => `${X(i)},${Y(v)}`).reverse().join(' ') }
  })
})
const gridLines = [0, 1, 2, 3, 4].map(k => ({ y: P + (H - P - PB) * k / 4, v: max.value * (1 - k / 4) }))
const xTicks = computed(() => {
  const out = [], step = Math.max(1, Math.round(n.value / 7))
  for (let i = 0; i < n.value; i += step) out.push({ x: X(i), t: fmtT(props.times[i]) })
  return out
})
</script>
<template>
  <svg :viewBox="`0 0 ${W} ${H}`" style="width:100%;height:auto">
    <line v-for="g in gridLines" :key="g.y" class="lp-grid" x1="46" :y1="g.y" :x2="W - 10" :y2="g.y" />
    <text v-for="g in gridLines" :key="'t' + g.y" class="lp-axis" :x="40" :y="g.y + 3" text-anchor="end" font-size="9">{{ fmtV(g.v) }}</text>
    <text v-for="(t, i) in xTicks" :key="'x' + i" class="lp-axis" :x="t.x" :y="H - 5" text-anchor="middle" font-size="9">{{ t.t }}</text>
    <template v-for="(g, i) in geoms" :key="i">
      <polygon v-if="stacked" :points="g.pts + ' ' + g.base" :fill="g.s.color" fill-opacity="0.13" />
      <polyline :points="g.pts" fill="none" :stroke="g.s.color" stroke-width="1.6" />
    </template>
  </svg>
</template>
