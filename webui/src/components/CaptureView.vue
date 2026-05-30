<script setup>
import { computed, onMounted, onUnmounted, ref } from 'vue'
import { getJSON, postJSON } from '../api'

const error = ref('')
const busy = ref(false)
const snapshot = ref({ enabled: false, events: [], count: 0, limit: 0 })
let timer = 0

const headers = [
  { title: 'Time', key: 'time' },
  { title: 'Direction', key: 'direction' },
  { title: 'Size', key: 'size' },
  { title: 'Type', key: 'type' },
  { title: 'Source MAC', key: 'src_mac' },
  { title: 'Destination MAC', key: 'dst_mac' },
  { title: 'Room', key: 'room' },
  { title: 'Peer', key: 'peer' },
]

const rows = computed(() => [...(snapshot.value.events || [])].reverse())

onMounted(() => {
  refresh()
  timer = window.setInterval(refresh, 1000)
})

onUnmounted(() => {
  window.clearInterval(timer)
})

async function refresh() {
  try {
    snapshot.value = await getJSON('/api/capture')
    error.value = ''
  } catch (err) {
    error.value = String(err.message || err)
  }
}

async function setEnabled(enabled) {
  try {
    busy.value = true
    error.value = ''
    snapshot.value = await postJSON(enabled ? '/api/capture/enable' : '/api/capture/disable', {})
  } catch (err) {
    error.value = String(err.message || err)
  } finally {
    busy.value = false
  }
}

function time(value) {
  if (!value) return '-'
  return new Date(value).toLocaleTimeString()
}

function typeLabel(item) {
  if (item.summary) return item.summary
  const base = item.type_name || 'Unknown'
  const etherType = item.ether_type || ''
  const vlan = item.vlan ? ' VLAN' : ''
  if (!item.valid) return item.reason || 'Invalid'
  return `${base}${vlan}${etherType ? ` (${etherType})` : ''}`
}

function peerLabel(item) {
  return item.peer_name || item.peer_id || '-'
}
</script>

<template>
  <v-alert v-if="error" type="error" variant="tonal" class="mb-4">
    {{ error }}
  </v-alert>

  <v-alert type="info" variant="tonal" class="mb-4">
    Packet metadata capture is {{ snapshot.enabled ? 'enabled' : 'disabled' }}. Payload bytes are not stored.
  </v-alert>

  <div class="d-flex flex-wrap align-center ga-3 mb-4">
    <v-chip :color="snapshot.enabled ? 'success' : 'default'" variant="tonal">
      {{ snapshot.enabled ? 'Enabled' : 'Disabled' }}
    </v-chip>
    <v-btn color="primary" variant="flat" :loading="busy" :disabled="snapshot.enabled" @click="setEnabled(true)">
      Enable Capture
    </v-btn>
    <v-btn color="error" variant="tonal" :loading="busy" :disabled="!snapshot.enabled" @click="setEnabled(false)">
      Disable Capture
    </v-btn>
    <span class="text-caption text-medium-emphasis">
      Showing latest {{ snapshot.count || 0 }} of {{ snapshot.limit || 0 }} metadata events.
    </span>
  </div>

  <v-data-table :headers="headers" :items="rows" item-value="seq" density="compact">
    <template #item.time="{ item }">{{ time(item.time) }}</template>
    <template #item.size="{ item }">{{ item.size }} B</template>
    <template #item.type="{ item }">{{ typeLabel(item) }}</template>
    <template #item.room="{ item }">{{ item.room || '-' }}</template>
    <template #item.peer="{ item }">{{ peerLabel(item) }}</template>
    <template #no-data>No packets captured yet</template>
  </v-data-table>
</template>
