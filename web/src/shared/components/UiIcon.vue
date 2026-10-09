<script setup>
// Original, local outline icons: no remote font or image dependency.
defineProps({ name: { type: String, default: 'apps' }, size: { type: [Number, String], default: 18 } })
const paths = {
  deploy: ['M14 4c3-2 6-2 7-1 1 1 1 4-1 7l-6 6-6-6z', 'M8 10 3 11l-1 5 6-1M14 16l-1 6 5-1 1-5', 'M17 6h.01M6 18l-3 3'],
  sync: ['M3 10a9 9 0 0 1 15-6l3 3M21 3v4h-4', 'M21 14A9 9 0 0 1 6 20l-3-3M3 21v-4h4'],
  history: ['M3 11a9 9 0 1 1 2 7', 'M3 4v7h7M12 7v5l3 2'],
  settings: ['M9 3h6l1 4 4 1v8l-4 1-1 4H9l-1-4-4-1V8l4-1z', 'M12 9a3 3 0 1 0 0 6 3 3 0 0 0 0-6Z'],
  build: ['m14 6 4-4 4 4-4 4', 'm18 6-9 9', 'm6 12-4 4 6 6 4-4z'],
  changes: ['M8 3H5v18h14V3h-3', 'M8 2h8v4H8z', 'M8 11h8M8 15h5'],
  orchestration: ['M3 3h6v6H3zM15 15h6v6h-6zM15 3h6v6h-6z', 'M9 6h6M6 9v9h9'],
  apps: ['M3 3h7v7H3zM14 3h7v7h-7zM3 14h7v7H3zM14 14h7v7h-7z'],
  resources: ['M3 3h18v7H3zM3 14h18v7H3z', 'M6 6.5h.01M6 17.5h.01M10 6.5h7M10 17.5h7'],
  plugins: ['M8 3h8v4h5v10h-5v4H8v-4H3V7h5z'],
  dbms: ['M20 6c0 2-3.6 3-8 3S4 8 4 6s3.6-3 8-3 8 1 8 3Z', 'M4 6v12c0 2 3.6 3 8 3s8-1 8-3V6M4 12c0 2 3.6 3 8 3s8-1 8-3'],
  redis: ['m13 2-9 12h7l-1 8 10-13h-7z'],
  logs: ['M5 3h14v18H5z', 'M8 7h8M8 11h8M8 15h5'],
  monitor: ['M3 3v18h18', 'm5 14 5-6 4 4 6-7'],
  devtools: ['m8 5-6 7 6 7M16 5l6 7-6 7M14 3l-4 18'],
  search: ['M10 17a7 7 0 1 0 0-14 7 7 0 0 0 0 14Z', 'm15 15 6 6'],
  live: ['M8 8a6 6 0 0 0 0 8M16 8a6 6 0 0 1 0 8M4 4a12 12 0 0 0 0 16M20 4a12 12 0 0 1 0 16', 'M12 11h.01'],
  bell: ['M18 8a6 6 0 0 0-12 0v7l-2 3h16l-2-3z', 'M10 21h4'],
  status: ['M12 3a9 9 0 1 0 0 18 9 9 0 0 0 0-18Z', 'm8 12 3 3 5-6'],
  moon: ['M20 15a9 9 0 0 1-11-11 9 9 0 1 0 11 11Z'],
  sun: ['M12 8a4 4 0 1 0 0 8 4 4 0 0 0 0-8Z', 'M12 2v2M12 20v2M2 12h2M20 12h2M5 5l1.5 1.5M17.5 17.5 19 19M5 19l1.5-1.5M17.5 6.5 19 5'],
  user: ['M12 3a4 4 0 1 0 0 8 4 4 0 0 0 0-8Z', 'M4 21v-3a6 6 0 0 1 6-6h4a6 6 0 0 1 6 6v3'],
  java: ['M4 9h12v5a5 5 0 0 1-5 5H9a5 5 0 0 1-5-5z', 'M16 10h2a3 3 0 0 1 0 6h-2M3 22h15M8 2v3M12 1v4'],
  static: ['M12 2a10 10 0 1 0 0 20 10 10 0 0 0 0-20Z', 'M2 12h20M12 2a20 20 0 0 1 0 20 20 20 0 0 1 0-20Z'],
  image: ['m12 2 9 5v10l-9 5-9-5V7z', 'm3 7 9 5 9-5M12 12v10'],
}
</script>
<template>
  <svg :width="size" :height="size" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.7" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true" class="ui-icon">
    <path v-for="(d, index) in paths[name] || paths.apps" :key="index" :d="d" />
  </svg>
</template>
<style scoped>.ui-icon{display:inline-block;vertical-align:middle;flex-shrink:0}</style>
