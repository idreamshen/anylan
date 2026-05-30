<script setup>
defineProps({
  label: { type: String, required: true },
  value: { type: [String, Number], required: true },
})
</script>

<template>
  <v-card variant="tonal">
    <v-card-text>
      <div class="text-caption text-medium-emphasis text-uppercase">{{ label }}</div>
      <div class="text-h6 text-truncate">{{ value }}</div>
    </v-card-text>
  </v-card>
</template>
