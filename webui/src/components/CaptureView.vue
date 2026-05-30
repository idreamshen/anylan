<script setup>
import { computed, onMounted, onUnmounted, ref } from 'vue'
import { getJSON } from '../api'

const error = ref('')
const snapshot = ref({ events: [], count: 0, limit: 0 })
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
    Showing the latest {{ snapshot.count || 0 }} packet metadata events. Payload bytes are not stored.
  </v-alert>

  <v-data-table :headers="headers" :items="rows" item-value="seq" density="compact">
    <template #item.time="{ item }">{{ time(item.time) }}</template>
    <template #item.size="{ item }">{{ item.size }} B</template>
    <template #item.type="{ item }">{{ typeLabel(item) }}</template>
    <template #item.room="{ item }">{{ item.room || '-' }}</template>
    <template #item.peer="{ item }">{{ peerLabel(item) }}</template>
    <template #no-data>No packets captured yet</template>
  </v-data-table>
</template>
