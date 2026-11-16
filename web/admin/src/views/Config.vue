<script setup lang="ts">
import { onMounted, ref } from "vue";

const services = ["core", "chatroom", "gateway", "webhook", "admin", "builder"];
const enabled = ref(false);
const current = ref("");
const content = ref("");
const revisions = ref<{ id: string; modified: string }[]>([]);
const listeners = ref<{ ip: string }[]>([]);
const ips = ref("");
const notice = ref("");
const noticeBad = ref(false);

type Reply = {
  ok: boolean;
  body: {
    error?: string;
    enabled?: boolean;
    content?: string;
    revisions?: { id: string; modified: string }[];
    ips?: string[];
    listeners?: { ip: string }[];
  } | null;
};

async function request(path: string, init?: RequestInit): Promise<Reply> {
  try {
    const res = await fetch(path, { credentials: "include", ...init });
    const text = await res.text();
    let body: Reply["body"] = null;
    if (text) {
      try {
        body = JSON.parse(text) as Reply["body"];
      } catch {
        body = null;
      }
    }
    return { ok: res.ok, body };
  } catch {
    return { ok: false, body: null };
  }
}

function showError(reply: Reply) {
  notice.value = reply.body?.error || "Nacos 请求失败";
  noticeBad.value = true;
}

function clock(value: string): string {
  return value ? value.slice(0, 16).replace("T", " ") : "";
}

async function loadExtras(service: string): Promise<boolean> {
  const history = await request(`/api/config/${service}/history`);
  if (!history.ok) {
    showError(history);
    return false;
  }
  revisions.value = history.body?.revisions ?? [];
  const beta = await request(`/api/config/${service}/beta`);
  if (!beta.ok) {
    showError(beta);
    return false;
  }
  ips.value = (beta.body?.ips ?? []).join(",");
  const watching = await request(`/api/config/${service}/listeners`);
  if (!watching.ok) {
    showError(watching);
    return false;
  }
  listeners.value = watching.body?.listeners ?? [];
  return true;
}

async function select(service: string) {
  const got = await request(`/api/config/${service}`);
  if (!got.ok) {
    showError(got);
    return;
  }
  current.value = service;
  content.value = got.body?.content ?? "";
  if (await loadExtras(service)) {
    notice.value = "";
    noticeBad.value = false;
  }
}

async function publish() {
  const got = await request(`/api/config/${current.value}`, {
    method: "PUT",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ content: content.value }),
  });
  if (!got.ok) {
    showError(got);
    return;
  }
  if (await loadExtras(current.value)) {
    notice.value = "已发布";
    noticeBad.value = false;
  }
}

async function rollback(id: string, modified: string) {
  if (!window.confirm(`把 ${current.value} 回滚到 ${clock(modified)}？`)) {
    return;
  }
  const got = await request(`/api/config/${current.value}/rollback`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ id }),
  });
  if (!got.ok) {
    showError(got);
    return;
  }
  await select(current.value);
  if (!noticeBad.value) {
    notice.value = "已回滚";
    noticeBad.value = false;
  }
}

async function publishBeta() {
  const list = ips.value.split(",").map((ip) => ip.trim()).filter(Boolean);
  if (list.length === 0) {
    notice.value = "请填写灰度 IP";
    noticeBad.value = true;
    return;
  }
  const got = await request(`/api/config/${current.value}/beta`, {
    method: "PUT",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ content: content.value, ips: list }),
  });
  if (!got.ok) {
    showError(got);
    return;
  }
  notice.value = "灰度已发布";
  noticeBad.value = false;
}

async function stopBeta() {
  if (!ips.value.trim()) {
    notice.value = "当前没有灰度";
    noticeBad.value = true;
    return;
  }
  const got = await request(`/api/config/${current.value}/beta`, { method: "DELETE" });
  if (!got.ok) {
    showError(got);
    return;
  }
  ips.value = "";
  notice.value = "已停止灰度";
  noticeBad.value = false;
}

onMounted(async () => {
  const body = await request("/api/config/source");
  enabled.value = body.ok && body.body?.enabled === true;
  if (enabled.value) {
    await select("core");
  }
});
</script>

<template>
  <a-card v-if="!enabled" :bordered="false" class="panel">
    <a-empty description="配置还没有功能" />
  </a-card>
  <div v-else class="config">
    <a-card :bordered="false" class="services">
      <a-button
        v-for="service in services"
        :key="service"
        long
        :type="service === current ? 'primary' : 'secondary'"
        @click="select(service)"
      >
        {{ service }}
      </a-button>
    </a-card>
    <a-card :bordered="false" class="editor">
      <a-textarea v-model="content" class="yaml" :auto-size="{ minRows: 16 }" />
      <div class="actions">
        <a-button type="primary" @click="publish">发布</a-button>
        <a-button @click="publishBeta">灰度</a-button>
        <a-button @click="stopBeta">停止灰度</a-button>
      </div>
      <p v-if="notice" :class="noticeBad ? 'fail' : 'ok'">{{ notice }}</p>
      <p v-for="rev in revisions" :key="rev.id">
        {{ clock(rev.modified) }}
        <a-button size="mini" @click="rollback(rev.id, rev.modified)">回滚</a-button>
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
  flex-wrap: wrap;
  gap: 8px;
  margin-top: 12px;
}

.panel {
  min-height: 320px;
}

.fail {
  color: #f53f3f;
}

.ok {
  color: #00b42a;
}

@media (max-width: 800px) {
  .config {
    grid-template-columns: 1fr;
  }

  .services :deep(.arco-card-body) {
    display: flex;
    flex-direction: row;
    gap: 8px;
    overflow-x: auto;
  }

  .services :deep(.arco-btn) {
    width: auto;
    flex: none;
  }
}
</style>
