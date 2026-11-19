<script setup>
import {computed} from 'vue'
import {MoonIcon,SunIcon,MonitorIcon,CaseSensitiveIcon,Rows3Icon} from '@lucide/vue'
import {themeMode,themeDark,themeTypography,themeDesign,themeColorway,themeDensity,setTheme} from '../shared/composables/theme.js'
import {typographyPresets} from '../shared/typography.js'
import {appearancePresets,densityPresets} from '../shared/palette.js'
import {DropdownMenuRadioGroup,DropdownMenuRadioItem,DropdownMenuSub,DropdownMenuSubTrigger,DropdownMenuSubContent} from '@/components/ui/dropdown-menu'
import {Button,DropdownMenu,DropdownMenuContent,DropdownMenuLabel,DropdownMenuSeparator,DropdownMenuTrigger} from '../shared/shadcn-ui.js'
const current=computed(()=>appearancePresets.find(p=>p.id===themeDesign.value))
const modes=[{id:'light',label:'浅色',icon:SunIcon},{id:'dark',label:'深色',icon:MoonIcon},{id:'system',label:'跟随系统',icon:MonitorIcon}]
</script>
<template>
  <DropdownMenu>
    <DropdownMenuTrigger as-child>
      <Button variant="ghost" size="icon" aria-label="外观与主题"><MoonIcon v-if="themeDark" class="size-4"/><SunIcon v-else class="size-4"/></Button>
    </DropdownMenuTrigger>
    <DropdownMenuContent align="end" class="w-72 max-w-[calc(100vw-1rem)]" aria-label="外观设置">
      <DropdownMenuLabel>视觉方案</DropdownMenuLabel>
      <DropdownMenuRadioGroup :model-value="themeDesign" aria-label="视觉方案" @update:model-value="setTheme({design:$event})">
        <DropdownMenuRadioItem v-for="preset in appearancePresets" :key="preset.id" :value="preset.id" class="items-start py-2">
          <span class="flex min-w-0 flex-col gap-1"><span class="font-medium">{{preset.label}}</span><span class="text-xs text-muted-foreground">{{preset.description}}</span></span>
        </DropdownMenuRadioItem>
      </DropdownMenuRadioGroup>
      <DropdownMenuSeparator/>
      <DropdownMenuLabel>{{current.label}}配色</DropdownMenuLabel>
      <DropdownMenuRadioGroup :model-value="themeColorway" aria-label="方案配色" @update:model-value="setTheme({colorway:$event})">
        <DropdownMenuRadioItem v-for="colorway in current.colorways" :key="colorway.id" :value="colorway.id" class="py-2">
          <span class="mr-1 flex overflow-hidden rounded-sm border" aria-hidden="true"><span v-for="(color,index) in colorway.swatches" :key="index" class="h-4 w-3" :style="{backgroundColor:color}"/></span>{{colorway.label}}
        </DropdownMenuRadioItem>
      </DropdownMenuRadioGroup>
      <DropdownMenuSeparator/>
      <DropdownMenuSub><DropdownMenuSubTrigger><component :is="modes.find(m=>m.id===themeMode).icon" class="size-4"/>显示模式<span class="ml-auto text-xs text-muted-foreground">{{modes.find(m=>m.id===themeMode).label}}</span></DropdownMenuSubTrigger><DropdownMenuSubContent><DropdownMenuRadioGroup :model-value="themeMode" @update:model-value="setTheme({mode:$event})"><DropdownMenuRadioItem v-for="mode in modes" :key="mode.id" :value="mode.id"><component :is="mode.icon" class="size-4"/>{{mode.label}}</DropdownMenuRadioItem></DropdownMenuRadioGroup></DropdownMenuSubContent></DropdownMenuSub>
      <DropdownMenuSub><DropdownMenuSubTrigger><CaseSensitiveIcon class="size-4"/>文字大小<span class="ml-auto text-xs text-muted-foreground">{{typographyPresets.find(p=>p.id===themeTypography).label}}</span></DropdownMenuSubTrigger><DropdownMenuSubContent><DropdownMenuRadioGroup :model-value="themeTypography" @update:model-value="setTheme({typography:$event})"><DropdownMenuRadioItem v-for="preset in typographyPresets" :key="preset.id" :value="preset.id">{{preset.label}}</DropdownMenuRadioItem></DropdownMenuRadioGroup></DropdownMenuSubContent></DropdownMenuSub>
      <DropdownMenuSub><DropdownMenuSubTrigger><Rows3Icon class="size-4"/>表格密度<span class="ml-auto text-xs text-muted-foreground">{{densityPresets.find(p=>p.id===themeDensity).label}}</span></DropdownMenuSubTrigger><DropdownMenuSubContent><DropdownMenuRadioGroup :model-value="themeDensity" @update:model-value="setTheme({density:$event})"><DropdownMenuRadioItem v-for="preset in densityPresets" :key="preset.id" :value="preset.id">{{preset.label}}</DropdownMenuRadioItem></DropdownMenuRadioGroup></DropdownMenuSubContent></DropdownMenuSub>
    </DropdownMenuContent>
  </DropdownMenu>
</template>
