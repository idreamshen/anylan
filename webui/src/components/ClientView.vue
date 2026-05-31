<script setup>
import { computed, onMounted, reactive, ref, watch } from 'vue'
import { getJSON, postJSON } from '../api'
import CaptureView from './CaptureView.vue'
import LogView from './LogView.vue'
import MetricCard from './MetricCard.vue'

const props = defineProps({
  status: { type: Object, required: true },
})
const emit = defineEmits(['refresh'])

const devices = ref([])
const error = ref('')
const activeTab = ref('status')
const previous = ref(null)
const previousAt = ref(Date.now())
const touched = reactive({})
const form = reactive({
  server_host: '',
  server_port: '4433',
  room: '',
  display_name: '',
  device_name: '',
  prioritize_virtual_adapter: true,
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
  if (!touched.server_host && !touched.server_port) setServerFields(status.server || '')
  if (!touched.room) form.room = status.room || ''
  if (!touched.display_name) form.display_name = status.display_name || ''
  if (!touched.device_name) form.device_name = status.device_name || ''
  if (!touched.prioritize_virtual_adapter) form.prioritize_virtual_adapter = !!status.prioritize_virtual_adapter
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
    const server = serverAddress()
    if (!server) throw new Error('server host and port are required')
    await postJSON('/api/join', {
      server,
      room: form.room,
      display_name: form.display_name,
      device_name: form.device_name,
      insecure_skip_verify: true,
      prioritize_virtual_adapter: form.prioritize_virtual_adapter,
    })
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

function setServerFields(server) {
  const trimmed = String(server || '').trim()
  if (!trimmed) {
    form.server_host = ''
    form.server_port = form.server_port || '4433'
    return
  }
  try {
    const parsed = new URL(`anylan://${trimmed}`)
    form.server_host = parsed.hostname
    form.server_port = parsed.port || '4433'
  } catch {
    const idx = trimmed.lastIndexOf(':')
    if (idx > 0 && !trimmed.slice(0, idx).includes(':')) {
      form.server_host = trimmed.slice(0, idx)
      form.server_port = trimmed.slice(idx + 1) || '4433'
      return
    }
    form.server_host = trimmed
    form.server_port = form.server_port || '4433'
  }
}

function serverAddress() {
  const host = form.server_host.trim()
  const port = form.server_port.trim()
  if (!host || !port) return ''
  if (host.includes(':') && !host.startsWith('[')) return `[${host}]:${port}`
  return `${host}:${port}`
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
    <v-tabs v-model="activeTab" color="primary">
      <v-tab value="status">Status</v-tab>
      <v-tab value="peers">Peers</v-tab>
      <v-tab value="connect">Connect</v-tab>
      <v-tab value="capture">Capture</v-tab>
      <v-tab value="log">Log</v-tab>
    </v-tabs>

    <v-window v-model="activeTab">
      <v-window-item value="status">
        <v-card-text>
          <v-row>
            <v-col cols="12" sm="6" lg="3"><MetricCard label="State" :value="status.state || 'unknown'" /></v-col>
            <v-col cols="12" sm="6" lg="3"><MetricCard label="Room" :value="status.room || '-'" /></v-col>
            <v-col cols="12" sm="6" lg="3"><MetricCard label="Virtual IP" :value="status.ipv4 || '-'" /></v-col>
            <v-col cols="12" sm="6" lg="3"><MetricCard label="Peers" :value="peers.length" /></v-col>
            <v-col cols="12" sm="6" lg="3"><MetricCard label="RX rate" :value="`${bytes(rate('rx_bytes'))}/s`" /></v-col>
            <v-col cols="12" sm="6" lg="3"><MetricCard label="TX rate" :value="`${bytes(rate('tx_bytes'))}/s`" /></v-col>
            <v-col cols="12" sm="6" lg="3"><MetricCard label="Reconnects" :value="status.reconnects || 0" /></v-col>
          </v-row>

          <v-card class="mt-4" variant="outlined">
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
        </v-card-text>
      </v-window-item>

      <v-window-item value="peers">
        <v-card-text>
          <v-data-table :headers="peerHeaders" :items="peers" item-value="id">
            <template #item.name="{ item }">{{ item.display_name || item.id }}</template>
            <template #no-data>No peers</template>
          </v-data-table>
        </v-card-text>
      </v-window-item>

      <v-window-item value="connect">
        <v-card-text>
          <v-alert v-if="busy" type="info" variant="tonal" class="mb-4">
            Leave the current room before changing connection settings.
          </v-alert>
          <v-form @submit.prevent="join">
            <v-row>
              <v-col cols="12" md="6" lg="4">
                <v-text-field v-model="form.server_host" label="Server host" placeholder="your-server" :disabled="busy" @update:model-value="markTouched('server_host')" />
              </v-col>
              <v-col cols="12" md="6" lg="4">
                <v-text-field v-model="form.server_port" label="Server port" placeholder="4433" :disabled="busy" @update:model-value="markTouched('server_port')" />
              </v-col>
              <v-col cols="12" md="6" lg="4">
                <v-text-field v-model="form.room" label="Room" placeholder="room code" :disabled="busy" @update:model-value="markTouched('room')" />
              </v-col>
              <v-col cols="12" md="6" lg="4">
                <v-text-field v-model="form.display_name" label="Name" placeholder="shown to peers" :disabled="busy" @update:model-value="markTouched('display_name')" />
              </v-col>
              <v-col cols="12" md="6" lg="4">
                <v-select v-model="form.device_name" :items="deviceItems" label="Virtual adapter" :disabled="busy" @update:model-value="markTouched('device_name')" />
              </v-col>
              <v-col cols="12" md="6" lg="4">
                <v-switch
                  v-model="form.prioritize_virtual_adapter"
                  color="primary"
                  density="comfortable"
                  :disabled="busy"
                  hide-details="auto"
                  label="Prioritize virtual adapter"
                  hint="Set metric to 1 on macOS/Windows so room traffic prefers anylan."
                  persistent-hint
                  @update:model-value="markTouched('prioritize_virtual_adapter')"
                />
              </v-col>
            </v-row>
            <v-card-actions class="px-0">
              <v-btn color="primary" type="submit" variant="flat" :disabled="busy">Join</v-btn>
              <v-btn color="error" variant="tonal" :disabled="!busy" @click="leave">Leave</v-btn>
            </v-card-actions>
          </v-form>
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
