// 通用 SVG 折线图（多系列 + Y 轴 + 简单悬停）
<script setup>
import {computed,onMounted,onBeforeUnmount,ref} from 'vue'
const props = defineProps({
  times: Array, series: Array, unit: String, yMax: Number, stacked: Boolean, height: { type: Number, default: 200 },
})
const chart=ref(null),W=ref(920),P=58,PB=28
let observer
onMounted(()=>{
  observer=new ResizeObserver(entries=>{const width=Math.round(entries[0]?.contentRect.width||0);if(width>0)W.value=width})
  observer.observe(chart.value)
})
onBeforeUnmount(()=>observer?.disconnect())
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
const X = i => P + (W.value - P - 36) * i / Math.max(n.value - 1, 1)
const Y = v => P + (H - P - PB) * (1 - Math.min(v / max.value, 1))
const fmtV = v => v >= 10000 ? (v / 1000).toFixed(0) + 'k' : v >= 1000 ? (v / 1000).toFixed(1) + 'k' : Math.round(v * 10) / 10
const fmtT = t => { const d = new Date(t); return String(d.getHours()).padStart(2, '0') + ':' + String(d.getMinutes()).padStart(2, '0') }
const geoms = computed(() => {
  const cum = new Array(n.value).fill(0)
  return props.series.map(s => {
    const topV = s.vals.map((v, i) => cum[i] + (v || 0))
    const baseV = cum.slice()
    if (props.stacked) cum.splice(0, cum.length, ...topV)
    return { s, topV, pts: topV.map((v, i) => `${X(i)},${Y(v)}`).join(' '), base: baseV.map((v, i) => `${X(i)},${Y(v)}`).reverse().join(' ') }
  })
})
const gridLines = computed(()=>[0, 1, 2, 3, 4].map(k => ({ y: P + (H - P - PB) * k / 4, v: max.value * (1 - k / 4) })))
const xTicks = computed(() => {
  const ticks=Math.max(2,Math.min(8,Math.floor((W.value-P-36)/90)))
  const out = [], step = Math.max(1, Math.ceil(n.value / ticks))
  for (let i = 0; i < n.value; i += step) out.push({ x: X(i), t: fmtT(props.times[i]) })
  return out
})
</script>
<template>
 <figure class="m-0 min-w-0">
  <svg ref="chart" :viewBox="`0 0 ${W} ${H}`" :style="{width:'100%',height:H+'px'}" role="img" :aria-label="series?.map(s=>s.name).join('、')+'趋势图'">
    <line v-for="g in gridLines" :key="g.y" class="lp-grid" :x1="P" :y1="g.y" :x2="W - 36" :y2="g.y" />
    <text v-for="g in gridLines" :key="'t' + g.y" class="lp-axis" :x="P - 8" :y="g.y + 3" text-anchor="end" style="font-size:.75rem">{{ fmtV(g.v) }}</text>
    <text v-for="(t, i) in xTicks" :key="'x' + i" class="lp-axis" :x="t.x" :y="H - 5" text-anchor="middle" style="font-size:.75rem">{{ t.t }}</text>
    <template v-for="(g, i) in geoms" :key="i">
      <polygon v-if="stacked" :points="g.pts + ' ' + g.base" :fill="g.s.color" fill-opacity="0.13" />
      <polyline :points="g.pts" fill="none" :stroke="g.s.color" stroke-width="1.6" />
      <circle v-if="n===1&&typeof g.s.vals[0]==='number'&&Number.isFinite(g.s.vals[0])" :cx="X(0)" :cy="Y(g.topV[0])" r="3" :fill="g.s.color"><title>{{g.s.name}}：{{fmtV(g.s.vals[0])}}{{unit||''}}</title></circle>
    </template>
  </svg>
  <figcaption class="mt-2 flex flex-wrap items-center gap-x-4 gap-y-1 text-xs text-muted-foreground"><span v-for="(item,i) in series" :key="i" class="inline-flex items-center gap-2"><span class="h-0.5 w-4 shrink-0" :style="{background:item.color}" aria-hidden="true"/>{{item.name}}</span></figcaption>
 </figure>
</template>
