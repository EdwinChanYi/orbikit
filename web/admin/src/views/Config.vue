<script setup lang="ts">
import { onMounted, ref } from "vue";

const services = ["core", "chatroom", "gateway", "webhook", "admin", "builder"];
const enabled = ref(false);
const current = ref("core");
const content = ref("");
const revisions = ref<{ id: string; modified: string }[]>([]);
const listeners = ref<{ ip: string }[]>([]);
const ips = ref("");
const failed = ref(false);

async function load(path: string, init?: RequestInit) {
  const res = await fetch(path, { credentials: "include", ...init });
  if (!res.ok) {
    failed.value = true;
    return null;
  }
  failed.value = false;
  if (res.status === 204) {
    return null;
  }
  return res.json();
}

async function select(service: string) {
  current.value = service;
  const body = (await load(`/api/config/${service}`)) as { content?: string } | null;
  content.value = body?.content ?? "";
  const history = (await load(`/api/config/${service}/history`)) as { revisions?: { id: string; modified: string }[] } | null;
  revisions.value = history?.revisions ?? [];
  const beta = (await load(`/api/config/${service}/beta`)) as { ips?: string[] } | null;
  ips.value = (beta?.ips ?? []).join(",");
  const watching = (await load(`/api/config/${service}/listeners`)) as { listeners?: { ip: string }[] } | null;
  listeners.value = watching?.listeners ?? [];
}

async function publish() {
  await load(`/api/config/${current.value}`, {
    method: "PUT",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ content: content.value }),
  });
}

async function rollback(id: string) {
  await load(`/api/config/${current.value}/rollback`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ id }),
  });
  await select(current.value);
}

async function publishBeta() {
  const list = ips.value.split(",").map((ip) => ip.trim()).filter(Boolean);
  await load(`/api/config/${current.value}/beta`, {
    method: "PUT",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ content: content.value, ips: list }),
  });
}

async function stopBeta() {
  await load(`/api/config/${current.value}/beta`, { method: "DELETE" });
}

onMounted(async () => {
  const body = (await load("/api/config/source")) as { enabled?: boolean } | null;
  enabled.value = body?.enabled === true;
});
</script>

<template>
  <a-card v-if="!enabled" :bordered="false" class="panel">
    <a-empty description="配置还没有功能" />
  </a-card>
  <div v-else class="config">
    <a-card :bordered="false" class="services">
      <a-button v-for="service in services" :key="service" long @click="select(service)">{{ service }}</a-button>
    </a-card>
    <a-card :bordered="false" class="editor">
      <a-textarea v-model="content" class="yaml" :auto-size="{ minRows: 16 }" />
      <div class="actions">
        <a-button type="primary" @click="publish">发布</a-button>
        <a-button @click="publishBeta">灰度</a-button>
        <a-button @click="stopBeta">停止灰度</a-button>
      </div>
      <p v-if="failed">Nacos 请求失败</p>
      <p v-for="rev in revisions" :key="rev.id">
        {{ rev.modified }}
        <a-button size="mini" @click="rollback(rev.id)">回滚</a-button>
      </p>
      <a-input v-model="ips" placeholder="10.0.0.8,10.0.0.9" />
      <p v-if="listeners.length === 0">没有监听者</p>
      <p v-for="item in listeners" :key="item.ip">{{ item.ip }}</p>
    </a-card>
  </div>
</template>

<style scoped>
.config {
  display: grid;
  grid-template-columns: 180px 1fr;
  gap: 16px;
}

.services {
  display: flex;
  flex-direction: column;
  gap: 8px;
}

.yaml :deep(textarea) {
  font-family: ui-monospace, SFMono-Regular, Consolas, monospace;
}

.actions {
  display: flex;
  gap: 8px;
  margin-top: 12px;
}

.panel {
  min-height: 320px;
}
</style>
