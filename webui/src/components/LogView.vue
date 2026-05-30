<script setup>
import { computed, onMounted, onUnmounted, ref } from 'vue'
import { getJSON } from '../api'

const logs = ref([])
const error = ref('')
let timer = 0

const lines = computed(() => logs.value.map(entry => {
  const ts = entry.time ? new Date(entry.time).toLocaleString() : '-'
  return `${ts} #${entry.seq} ${entry.message}`
}).join('\n'))

async function refresh() {
  try {
    logs.value = await getJSON('/api/logs')
    error.value = ''
  } catch (err) {
    error.value = String(err.message || err)
  }
}

onMounted(() => {
  refresh()
  timer = window.setInterval(refresh, 1000)
})

onUnmounted(() => {
  window.clearInterval(timer)
})
</script>

<template>
  <v-card variant="outlined">
    <v-card-title class="d-flex align-center justify-space-between">
      <span>Recent Logs</span>
      <v-btn size="small" variant="tonal" @click="refresh">Refresh</v-btn>
    </v-card-title>
    <v-card-text>
      <v-alert v-if="error" type="error" variant="tonal" class="mb-4">
        {{ error }}
      </v-alert>
      <v-sheet color="grey-lighten-4" rounded class="pa-4 overflow-auto" min-height="360">
        <pre class="ma-0 text-body-2">{{ lines || 'No logs yet' }}</pre>
      </v-sheet>
    </v-card-text>
  </v-card>
</template>
