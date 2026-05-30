<script setup>
import { computed, ref } from 'vue'
import CaptureView from './CaptureView.vue'
import LogView from './LogView.vue'
import MetricCard from './MetricCard.vue'

const props = defineProps({
  status: { type: Object, required: true },
})

const rooms = computed(() => props.status.rooms || [])
const peers = computed(() => rooms.value.flatMap(room => (room.peers || []).map(peer => ({ ...peer, room: room.name }))))
const rxTotal = computed(() => peers.value.reduce((sum, peer) => sum + (peer.rx_bytes || 0), 0))
const txTotal = computed(() => peers.value.reduce((sum, peer) => sum + (peer.tx_bytes || 0), 0))

const roomHeaders = [
  { title: 'Name', key: 'name' },
  { title: 'Peers', key: 'peer_count' },
  { title: 'Created', key: 'created_at' },
  { title: 'RX', key: 'rx' },
  { title: 'TX', key: 'tx' },
]

const peerHeaders = [
  { title: 'Name / ID', key: 'name' },
  { title: 'Room', key: 'room' },
  { title: 'IP', key: 'ip' },
  { title: 'MAC', key: 'mac' },
  { title: 'RX', key: 'rx_bytes' },
  { title: 'TX', key: 'tx_bytes' },
  { title: 'Connected', key: 'connected_at' },
]

const roomRows = computed(() => rooms.value.map(room => ({
  ...room,
  peer_count: (room.peers || []).length,
  rx: (room.peers || []).reduce((sum, peer) => sum + (peer.rx_bytes || 0), 0),
  tx: (room.peers || []).reduce((sum, peer) => sum + (peer.tx_bytes || 0), 0),
})))

const activeTab = ref('status')

function bytes(value) {
  if (!Number.isFinite(value)) return '0 B'
  const units = ['B', 'KB', 'MB', 'GB', 'TB']
  let next = value
  let unit = 0
  while (next >= 1024 && unit < units.length - 1) {
    next /= 1024
    unit += 1
  }
  return `${next.toFixed(unit === 0 ? 0 : 1)} ${units[unit]}`
}

function time(value) {
  if (!value) return '-'
  return new Date(value).toLocaleString()
}
</script>

<template>
  <v-card>
    <v-tabs v-model="activeTab" color="primary">
      <v-tab value="status">Status</v-tab>
      <v-tab value="rooms">Rooms</v-tab>
      <v-tab value="peers">Peers</v-tab>
      <v-tab value="capture">Capture</v-tab>
      <v-tab value="log">Log</v-tab>
    </v-tabs>

    <v-window v-model="activeTab">
      <v-window-item value="status">
        <v-card-text>
          <v-row>
            <v-col cols="12" sm="6" lg="3"><MetricCard label="Rooms" :value="rooms.length" /></v-col>
            <v-col cols="12" sm="6" lg="3"><MetricCard label="Peers" :value="peers.length" /></v-col>
            <v-col cols="12" sm="6" lg="3"><MetricCard label="RX total" :value="bytes(rxTotal)" /></v-col>
            <v-col cols="12" sm="6" lg="3"><MetricCard label="TX total" :value="bytes(txTotal)" /></v-col>
          </v-row>
        </v-card-text>
      </v-window-item>

      <v-window-item value="rooms">
        <v-card-text>
          <v-data-table :headers="roomHeaders" :items="roomRows" item-value="name">
            <template #item.created_at="{ item }">{{ time(item.created_at) }}</template>
            <template #item.rx="{ item }">{{ bytes(item.rx) }}</template>
            <template #item.tx="{ item }">{{ bytes(item.tx) }}</template>
            <template #no-data>No active rooms</template>
          </v-data-table>
        </v-card-text>
      </v-window-item>

      <v-window-item value="peers">
        <v-card-text>
          <v-data-table :headers="peerHeaders" :items="peers" item-value="id">
            <template #item.name="{ item }">{{ item.display_name || item.id }}</template>
            <template #item.rx_bytes="{ item }">{{ bytes(item.rx_bytes || 0) }}</template>
            <template #item.tx_bytes="{ item }">{{ bytes(item.tx_bytes || 0) }}</template>
            <template #item.connected_at="{ item }">{{ time(item.connected_at) }}</template>
            <template #no-data>No active peers</template>
          </v-data-table>
        </v-card-text>
      </v-window-item>

      <v-window-item value="log">
        <v-card-text>
          <LogView />
        </v-card-text>
      </v-window-item>

      <v-window-item value="capture">
        <v-card-text>
          <CaptureView />
        </v-card-text>
      </v-window-item>
    </v-window>
  </v-card>
</template>
