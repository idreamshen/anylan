<script setup>
import { computed, onMounted, onUnmounted, ref } from 'vue'
import { getJSON } from './api'
import ClientView from './components/ClientView.vue'
import ServerView from './components/ServerView.vue'

const status = ref(null)
const error = ref('')
const updatedAt = ref(null)
let timer = 0

const title = computed(() => Array.isArray(status.value?.rooms) ? 'anylan server' : 'anylan client')
const isServer = computed(() => Array.isArray(status.value?.rooms))

async function refresh() {
  try {
    status.value = await getJSON('/api/status')
    updatedAt.value = new Date()
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
  <v-app>
    <v-app-bar :title="title">
      <template #append>
        <span class="text-caption text-medium-emphasis mr-4">
          {{ updatedAt ? `Updated ${updatedAt.toLocaleTimeString()}` : '' }}
        </span>
      </template>
    </v-app-bar>

    <v-main>
      <v-container class="py-6" fluid>
        <v-alert v-if="error" type="error" variant="tonal" class="mb-4">
          {{ error }}
        </v-alert>
        <ServerView v-if="isServer && status" :status="status" />
        <ClientView v-else-if="status" :status="status" @refresh="refresh" />
        <v-progress-linear v-else indeterminate />
      </v-container>
    </v-main>
  </v-app>
</template>
