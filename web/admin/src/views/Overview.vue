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

const etcdFault = computed(() => failed.value || rows.value.some((row) => row.instances == null));

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
  }
});
</script>

<template>
  <p>
    etcd
    <span :class="etcdFault ? 'status-fault' : 'status-ok'">{{ etcdFault ? "故障" : "正常" }}</span>
  </p>
  <table v-if="!failed">
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
        <td>{{ row.name }}</td>
        <td :class="labeled(containerCell, row.container).cls">{{ labeled(containerCell, row.container).text }}</td>
        <td :class="labeled(grpcCell, row.grpc).cls">{{ labeled(grpcCell, row.grpc).text }}</td>
        <td :class="instanceCell(row).cls">{{ instanceCell(row).text }}</td>
        <td>{{ row.image_id }}</td>
        <td :class="releaseCell(row).cls">{{ releaseCell(row).text }}</td>
      </tr>
    </tbody>
  </table>
</template>

<style>
.status-ok { color: #00b42a; }
.status-degraded { color: #ff7d00; }
.status-fault { color: #f53f3f; }
.status-unknown { color: #86909c; }
</style>
