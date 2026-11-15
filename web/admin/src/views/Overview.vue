<script setup lang="ts">
import { computed, onMounted, ref } from "vue";

type Instance = {
  service: string;
  id: string;
  addr: string;
};

type ServiceRow = {
  name: string;
  container: string;
  grpc: string;
  instances: Instance[] | null;
  image_id: string;
  last_release: string | null;
};

type Cell = {
  text: string;
  cls: string;
};

const rows = ref<ServiceRow[]>([]);
const failed = ref(false);
const settled = ref(false);

const containerCell: Record<string, Cell> = {
  running: { text: "运行中", cls: "status-ok" },
  exited: { text: "已停止", cls: "status-fault" },
  missing: { text: "不存在", cls: "status-fault" },
  unknown: { text: "未知", cls: "status-unknown" },
};

const grpcCell: Record<string, Cell> = {
  serving: { text: "正常", cls: "status-ok" },
  not_serving: { text: "故障", cls: "status-fault" },
  skipped: { text: "不适用", cls: "status-unknown" },
  unknown: { text: "未知", cls: "status-unknown" },
};

const etcdStatus = computed((): Cell => {
  if (!settled.value) {
    return { text: "未知", cls: "status-unknown" };
  }
  if (failed.value || rows.value.some((row) => row.instances == null)) {
    return { text: "故障", cls: "status-fault" };
  }
  return { text: "正常", cls: "status-ok" };
});

function labeled(map: Record<string, Cell>, value: string): Cell {
  return map[value] ?? { text: value, cls: "status-unknown" };
}

function instanceCell(row: ServiceRow): Cell {
  if (row.instances == null) {
    return { text: "故障", cls: "status-fault" };
  }
  if (row.instances.length === 0 && row.grpc === "skipped") {
    return { text: "不适用", cls: "status-unknown" };
  }
  return { text: `${row.instances.length} 个实例`, cls: "" };
}

function releaseCell(row: ServiceRow): Cell {
  if (row.last_release == null) {
    return { text: "尚无发布", cls: "status-unknown" };
  }
  return { text: row.last_release, cls: "" };
}

onMounted(async () => {
  try {
    const res = await fetch("/api/overview", { credentials: "include" });
    if (!res.ok) {
      failed.value = true;
      return;
    }
    const body = (await res.json()) as { services?: ServiceRow[] | null };
    rows.value = Array.isArray(body.services) ? body.services : [];
  } catch {
    failed.value = true;
  } finally {
    settled.value = true;
  }
});
</script>

<template>
  <a-card :bordered="false" class="panel">
    <p class="etcd-line">
      etcd
      <span :class="etcdStatus.cls">{{ etcdStatus.text }}</span>
    </p>
    <table v-if="!failed" class="sheet">
      <thead>
        <tr>
          <th>服务</th>
          <th>容器</th>
          <th>gRPC</th>
          <th>实例</th>
          <th>镜像</th>
          <th>最近发布</th>
        </tr>
      </thead>
      <tbody>
        <tr v-for="row in rows" :key="row.name">
          <td class="name">{{ row.name }}</td>
          <td :class="labeled(containerCell, row.container).cls">{{ labeled(containerCell, row.container).text }}</td>
          <td :class="labeled(grpcCell, row.grpc).cls">{{ labeled(grpcCell, row.grpc).text }}</td>
          <td :class="instanceCell(row).cls">{{ instanceCell(row).text }}</td>
          <td class="image" :title="row.image_id">{{ row.image_id }}</td>
          <td :class="releaseCell(row).cls">{{ releaseCell(row).text }}</td>
        </tr>
      </tbody>
    </table>
  </a-card>
</template>

<style>
.status-ok { color: #00b42a; }
.status-degraded { color: #ff7d00; }
.status-fault { color: #f53f3f; }
.status-unknown { color: #86909c; }

.panel {
  border-radius: 8px;
}

.etcd-line {
  margin: 0 0 16px;
  color: var(--color-text-2);
}

.sheet {
  width: 100%;
  border-collapse: collapse;
  table-layout: fixed;
}

.sheet th,
.sheet td {
  padding: 12px 16px;
  border-bottom: 1px solid var(--color-neutral-3);
  text-align: left;
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}

.sheet th {
  background: var(--color-fill-1);
  color: var(--color-text-2);
  font-weight: 500;
}

.sheet .name {
  font-weight: 600;
}

.sheet .image {
  font-family: ui-monospace, SFMono-Regular, Consolas, monospace;
  font-size: 12px;
  color: var(--color-text-2);
}
</style>
