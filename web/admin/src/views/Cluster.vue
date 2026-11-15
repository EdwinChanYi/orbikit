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
  <a-card v-if="failed || cluster?.status === 'fault'" :bordered="false" class="panel">
    <p class="status-fault">故障</p>
    <p v-if="cluster?.error" class="error">{{ cluster.error }}</p>
  </a-card>
  <div v-else-if="cluster" class="stack">
    <a-card :bordered="false" class="panel">
      <div class="stats">
        <div>
          <span class="k">状态</span>
          <span :class="statusCell[cluster.status]?.cls">{{ statusCell[cluster.status]?.text }}</span>
        </div>
        <div>
          <span class="k">主节点</span>
          {{ cluster.leader }}
        </div>
        <div>
          <span class="k">库大小</span>
          {{ cluster.db_size }}
        </div>
      </div>
      <p v-for="alarm in cluster.alarms ?? []" :key="alarm" class="alarm">{{ alarm }}</p>
    </a-card>
    <a-card :bordered="false" class="panel" title="实例">
      <table class="sheet">
        <thead>
          <tr>
            <th>服务</th>
            <th>地址</th>
          </tr>
        </thead>
        <tbody>
          <tr v-for="inst in cluster.instances ?? []" :key="`${inst.service}/${inst.id}`">
            <td>{{ inst.service }}</td>
            <td class="addr">{{ inst.addr }}</td>
          </tr>
        </tbody>
      </table>
    </a-card>
  </div>
</template>

<style>
.status-ok { color: #00b42a; }
.status-degraded { color: #ff7d00; }
.status-fault { color: #f53f3f; }
.status-unknown { color: #86909c; }

.stack {
  display: flex;
  flex-direction: column;
  gap: 16px;
}

.panel {
  border-radius: 8px;
}

.stats {
  display: grid;
  grid-template-columns: repeat(3, minmax(0, 1fr));
  gap: 16px;
}

.k {
  display: block;
  margin-bottom: 4px;
  color: var(--color-text-3);
  font-size: 12px;
}

.error,
.alarm {
  margin: 12px 0 0;
  color: var(--color-text-2);
}

.sheet {
  width: 100%;
  border-collapse: collapse;
}

.sheet th,
.sheet td {
  padding: 12px 16px;
  border-bottom: 1px solid var(--color-neutral-3);
  text-align: left;
}

.sheet th {
  background: var(--color-fill-1);
  color: var(--color-text-2);
  font-weight: 500;
}

.addr {
  font-family: ui-monospace, SFMono-Regular, Consolas, monospace;
}
</style>
