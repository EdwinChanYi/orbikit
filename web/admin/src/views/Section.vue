<script setup lang="ts">
import { useRoute } from "vue-router";

const route = useRoute();
</script>

<template>
  <a-card :bordered="false" class="panel">
    <a-empty :description="`${route.name}还没有功能`" />
  </a-card>
</template>

<style scoped>
.panel {
  min-height: 320px;
  border-radius: 8px;
  display: flex;
  align-items: center;
  justify-content: center;
}
</style>
