<script setup>
import { Button, Dialog, DialogContent, DialogDescription, DialogHeader, DialogTitle } from '../shared/shadcn-ui.js'
import { DialogTrigger } from '@/components/ui/dialog'
import OperationUpdates from './OperationUpdates.vue'
import DocumentProvenance from './DocumentProvenance.vue'

defineProps({ document: Object, operation: Object, service: String, environment: String, protocol: String })
</script>

<template>
  <Dialog>
    <DialogTrigger as-child><Button variant="outline">变更记录</Button></DialogTrigger>
    <DialogContent class="sm:max-w-xl">
      <DialogHeader class="pr-8">
        <DialogTitle>变更记录</DialogTitle>
        <DialogDescription class="break-words">{{ operation.summary || operation.path || operation.id }}</DialogDescription>
      </DialogHeader>
      <div class="min-w-0">
        <OperationUpdates :info="document.update_info" :service="service" :environment="environment"
          :protocol="protocol" :snapshot="document.id" :operation="operation.id" />
        <DocumentProvenance :lineage="document.lineage" :error="document.lineage_error" :operation="operation.id" />
      </div>
    </DialogContent>
  </Dialog>
</template>
