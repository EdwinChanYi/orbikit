<script setup lang="ts">
import { onMounted, ref } from "vue";

type Instance = {
  service: string;
  id: string;
  addr: string;
};

type ClusterBody = {
  status: string;
  leader?: string;
  db_size?: number;
  alarms?: string[] | null;
  instances?: Instance[] | null;
  error?: string;
};

const failed = ref(false);
const cluster = ref<ClusterBody | null>(null);

const statusCell: Record<string, { text: string; cls: string }> = {
  ok: { text: "正常", cls: "status-ok" },
  degraded: { text: "降级", cls: "status-degraded" },
  fault: { text: "故障", cls: "status-fault" },
};

onMounted(async () => {
  try {
    const res = await fetch("/api/cluster", { credentials: "include" });
    if (!res.ok) {
      failed.value = true;
      return;
    }
    cluster.value = (await res.json()) as ClusterBody;
  } catch {
    failed.value = true;
  }
});
</script>

<template>
  <template v-if="failed || cluster?.status === 'fault'">
    <p class="status-fault">故障</p>
    <p v-if="cluster?.error">{{ cluster.error }}</p>
  </template>
  <template v-else-if="cluster">
    <p><span :class="statusCell[cluster.status]?.cls">{{ statusCell[cluster.status]?.text }}</span></p>
    <p>主节点 {{ cluster.leader }}</p>
    <p>库大小 {{ cluster.db_size }}</p>
    <ul>
      <li v-for="alarm in cluster.alarms ?? []" :key="alarm">{{ alarm }}</li>
    </ul>
    <ul>
      <li v-for="inst in cluster.instances ?? []" :key="`${inst.service}/${inst.id}`">{{ inst.addr }}</li>
    </ul>
  </template>
</template>

<style>
.status-ok { color: #00b42a; }
.status-degraded { color: #ff7d00; }
.status-fault { color: #f53f3f; }
.status-unknown { color: #86909c; }
</style>
