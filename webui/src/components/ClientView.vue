<script setup>
import { computed, onMounted, reactive, ref, watch } from 'vue'
import { getJSON, postJSON } from '../api'
import MetricCard from './MetricCard.vue'

const props = defineProps({
  status: { type: Object, required: true },
})
const emit = defineEmits(['refresh'])

const devices = ref([])
const error = ref('')
const previous = ref(null)
const previousAt = ref(Date.now())
const touched = reactive({})
const form = reactive({
  server: '',
  room: '',
  room_key: '',
  display_name: '',
  device_name: '',
  insecure_skip_verify: false,
})

const busy = computed(() => ['starting', 'connecting', 'connected', 'reconnecting', 'leaving'].includes(props.status.state || ''))
const peers = computed(() => props.status.peers || [])
const deviceItems = computed(() => devices.value.map(device => ({
  title: device.display || device.name || 'Auto-detect',
  value: device.name || '',
  props: {
    subtitle: [device.default ? 'default' : '', device.virtual ? 'virtual' : '', !device.selectable ? 'detected' : ''].filter(Boolean).join(', '),
  },
})))

const peerHeaders = [
  { title: 'Name / ID', key: 'name' },
  { title: 'Virtual IP', key: 'ipv4' },
  { title: 'MAC', key: 'mac' },
]

watch(() => props.status, status => {
  if (!touched.server) form.server = status.server || ''
  if (!touched.room) form.room = status.room || ''
  if (!touched.display_name) form.display_name = status.display_name || ''
  if (!touched.device_name) form.device_name = status.device_name || ''
  if (!touched.insecure_skip_verify) form.insecure_skip_verify = !!status.insecure_skip_verify
}, { immediate: true })

watch(() => props.status, (status, oldStatus) => {
  previous.value = oldStatus || status
  previousAt.value = Date.now()
})

onMounted(async () => {
  try {
    devices.value = await getJSON('/api/devices')
  } catch (err) {
    error.value = String(err.message || err)
  }
})

async function join() {
  try {
    error.value = ''
    await postJSON('/api/join', { ...form })
    clearTouched()
    emit('refresh')
  } catch (err) {
    error.value = String(err.message || err)
  }
}

async function leave() {
  try {
    error.value = ''
    await postJSON('/api/leave', {})
    clearTouched()
    emit('refresh')
  } catch (err) {
    error.value = String(err.message || err)
  }
}

function markTouched(name) {
  touched[name] = true
}

function clearTouched() {
  for (const key of Object.keys(touched)) delete touched[key]
}

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

function rate(key) {
  const now = Date.now()
  const currentValue = props.status[key] || 0
  const previousValue = previous.value ? previous.value[key] || 0 : currentValue
  const seconds = Math.max((now - previousAt.value) / 1000, 1)
  return Math.max(0, (currentValue - previousValue) / seconds)
}

function time(value) {
  if (!value || value.startsWith?.('0001-')) return '-'
  return new Date(value).toLocaleString()
}
</script>

<template>
  <v-alert v-if="error" type="error" variant="tonal" class="mb-4">
    {{ error }}
  </v-alert>

  <v-card>
    <v-card-title>Join Room</v-card-title>
    <v-card-text>
      <v-form @submit.prevent="join">
        <v-row>
          <v-col cols="12" md="6" lg="4">
            <v-text-field v-model="form.server" label="Server" placeholder="your-server:4433" @update:model-value="markTouched('server')" />
          </v-col>
          <v-col cols="12" md="6" lg="4">
            <v-text-field v-model="form.room" label="Room" placeholder="room code" @update:model-value="markTouched('room')" />
          </v-col>
          <v-col cols="12" md="6" lg="4">
            <v-text-field v-model="form.display_name" label="Name" placeholder="shown to peers" @update:model-value="markTouched('display_name')" />
          </v-col>
          <v-col cols="12" md="6" lg="4">
            <v-text-field v-model="form.room_key" label="Room key" placeholder="optional" @update:model-value="markTouched('room_key')" />
          </v-col>
          <v-col cols="12" md="6" lg="4">
            <v-select v-model="form.device_name" :items="deviceItems" label="Virtual adapter" @update:model-value="markTouched('device_name')" />
          </v-col>
          <v-col cols="12" md="6" lg="4">
            <v-text-field v-model="form.device_name" label="Adapter name" placeholder="anylan0" @update:model-value="markTouched('device_name')" />
          </v-col>
          <v-col cols="12" md="6" lg="4">
            <v-checkbox v-model="form.insecure_skip_verify" label="Skip TLS verification" @update:model-value="markTouched('insecure_skip_verify')" />
          </v-col>
        </v-row>
        <v-card-actions class="px-0">
          <v-btn color="primary" type="submit" variant="flat" :disabled="busy">Join</v-btn>
          <v-btn color="error" variant="tonal" :disabled="!busy" @click="leave">Leave</v-btn>
        </v-card-actions>
      </v-form>
    </v-card-text>
  </v-card>

  <v-row class="mt-4">
    <v-col cols="12" sm="6" lg="3"><MetricCard label="State" :value="status.state || 'unknown'" /></v-col>
    <v-col cols="12" sm="6" lg="3"><MetricCard label="Room" :value="status.room || '-'" /></v-col>
    <v-col cols="12" sm="6" lg="3"><MetricCard label="Virtual IP" :value="status.ipv4 || '-'" /></v-col>
    <v-col cols="12" sm="6" lg="3"><MetricCard label="Peers" :value="peers.length" /></v-col>
    <v-col cols="12" sm="6" lg="3"><MetricCard label="RX rate" :value="`${bytes(rate('rx_bytes'))}/s`" /></v-col>
    <v-col cols="12" sm="6" lg="3"><MetricCard label="TX rate" :value="`${bytes(rate('tx_bytes'))}/s`" /></v-col>
    <v-col cols="12" sm="6" lg="3"><MetricCard label="Reconnects" :value="status.reconnects || 0" /></v-col>
  </v-row>

  <v-card class="mt-4">
    <v-card-title>Session</v-card-title>
    <v-table>
      <tbody>
        <tr><th>Server</th><td>{{ status.server || '-' }}</td></tr>
        <tr><th>Peer ID</th><td>{{ status.peer_id || '-' }}</td></tr>
        <tr><th>CIDR</th><td>{{ status.cidr || '-' }}</td></tr>
        <tr><th>MAC</th><td>{{ status.mac || '-' }}</td></tr>
        <tr><th>MTU</th><td>{{ status.mtu || '-' }}</td></tr>
        <tr><th>Room created</th><td>{{ time(status.room_created_at) }}</td></tr>
        <tr><th>RX total</th><td>{{ bytes(status.rx_bytes || 0) }}</td></tr>
        <tr><th>TX total</th><td>{{ bytes(status.tx_bytes || 0) }}</td></tr>
        <tr><th>Last error</th><td>{{ status.last_error || '' }}</td></tr>
      </tbody>
    </v-table>
  </v-card>

  <v-card class="mt-4">
    <v-card-title>Peers</v-card-title>
    <v-data-table :headers="peerHeaders" :items="peers" item-value="id">
      <template #item.name="{ item }">{{ item.display_name || item.id }}</template>
      <template #no-data>No peers</template>
    </v-data-table>
  </v-card>
</template>
