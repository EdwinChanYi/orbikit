<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref, watch } from "vue";

type Task = {
  id: number;
  service: string;
  kind: string;
  git_ref: string;
  image_id: string;
  status: string;
  log: string;
  error: string;
  finished_at: string | null;
};

type Version = {
  tag: string;
  image_id: string;
  created_at: string;
};

type RemoteRef = {
  name: string;
  ref: string;
  kind: string;
  summary: string;
  committed_at: string;
};

const services = ["core", "chatroom", "gateway", "webhook", "admin", "builder"];
const current = ref("core");
const versions = ref<Version[]>([]);
const versionsReady = ref(false);
const imageID = ref("");
const remoteRefs = ref<RemoteRef[]>([]);
const refsReady = ref(false);
const buildRef = ref("");
const tasks = ref<Task[]>([]);
const active = ref(false);
const rollback = ref(false);
const message = ref("");
const messageBad = ref(false);
const sending = ref(false);

const busy = computed(
  () => sending.value || active.value || tasks.value.some((task) => task.status === "queued" || task.status === "running"),
);

const statusText: Record<string, string> = {
  queued: "排队",
  running: "进行中",
  succeeded: "成功",
  failed: "失败",
};

const kindText: Record<string, string> = {
  publish: "发布",
  rollback: "回滚",
  recreate: "重建",
};

const sha = /^[0-9a-fA-F]{40}$/;
const hex = /^[0-9a-fA-F]+$/;
const tag = /^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$/;

function refError(value: string): string {
  if (sha.test(value)) {
    return "";
  }
  if (hex.test(value) || !tag.test(value)) {
    return "引用无效";
  }
  return "";
}

function refLabel(item: RemoteRef): string {
  if (item.kind === "tag") {
    return item.name;
  }
  const when = (item.committed_at ?? "").slice(0, 16).replace("T", " ");
  return `${item.ref.slice(0, 12)} · ${item.summary} · ${when}`;
}

function versionLabel(version: Version): string {
  const id = version.image_id.replace(/^sha256:/, "").slice(0, 12);
  const when = (version.created_at ?? "").slice(0, 16).replace("T", " ");
  return `${version.tag} · ${id} · ${when}`;
}

function chooseImage(list: Version[], currentId: string): string {
  if (list.some((version) => version.image_id === currentId)) {
    return currentId;
  }
  return list.find((version) => version.tag === "latest")?.image_id ?? list[0]?.image_id ?? "";
}

function clock(value: string | null): string {
  return value ? value.slice(0, 16).replace("T", " ") : "";
}

function shortHex(value: string): string {
  const bare = value.replace(/^sha256:/, "");
  return bare.length > 12 ? bare.slice(0, 12) : bare;
}

function taskTitle(task: Task): string {
  return [kindText[task.kind] ?? task.kind, task.git_ref, task.image_id, task.finished_at ?? ""].filter(Boolean).join(" ");
}

async function load() {
  const res = await fetch(`/api/releases?service=${current.value}`, { credentials: "include" });
  if (!res.ok) {
    const body = (await res.json().catch(() => null)) as { error?: string } | null;
    message.value = body?.error ?? "发布请求失败";
    return;
  }
  const body = (await res.json()) as { releases?: Task[]; active?: boolean; rollback?: boolean };
  tasks.value = body.releases ?? [];
  active.value = body.active === true;
  rollback.value = body.rollback === true;
}

async function loadVersions() {
  const res = await fetch(`/api/releases/versions?service=${current.value}`, { credentials: "include" });
  if (!res.ok) {
    versions.value = [];
    imageID.value = "";
    versionsReady.value = true;
    const body = (await res.json().catch(() => null)) as { error?: string } | null;
    message.value = body?.error ?? "发布请求失败";
    return;
  }
  const body = (await res.json()) as { versions?: Version[] };
  versions.value = body.versions ?? [];
  imageID.value = chooseImage(versions.value, imageID.value);
  versionsReady.value = true;
}

async function loadRefs() {
  const res = await fetch("/api/releases/refs", { credentials: "include" });
  if (!res.ok) {
    remoteRefs.value = [];
    buildRef.value = "";
    refsReady.value = true;
    const body = (await res.json().catch(() => null)) as { error?: string } | null;
    message.value = body?.error ?? "发布请求失败";
    return;
  }
  const body = (await res.json()) as { refs?: RemoteRef[] };
  remoteRefs.value = body.refs ?? [];
  if (!remoteRefs.value.some((item) => item.ref === buildRef.value)) {
    buildRef.value = remoteRefs.value[0]?.ref ?? "";
  }
  refsReady.value = true;
}

async function deploy() {
  if (!imageID.value) {
    message.value = "没有已构建的版本";
    messageBad.value = true;
    return;
  }
  const version = versions.value.find((item) => item.image_id === imageID.value);
  const label = version ? versionLabel(version) : imageID.value;
  if (!window.confirm(`部署 ${current.value} 的 ${label}？这会替换正在运行的容器。`)) {
    return;
  }
  await send("/api/releases/deploy", { service: current.value, image_id: imageID.value });
}

async function build() {
  if (!buildRef.value) {
    message.value = "没有可构建的版本";
    messageBad.value = true;
    return;
  }
  const invalid = refError(buildRef.value);
  if (invalid) {
    message.value = invalid;
    messageBad.value = true;
    return;
  }
  const item = remoteRefs.value.find((ref) => ref.ref === buildRef.value);
  const label = item ? refLabel(item) : buildRef.value;
  if (!window.confirm(`构建 ${current.value} 的 ${label}？`)) {
    return;
  }
  await send("/api/releases", { service: current.value, ref: buildRef.value });
}

async function rollBack() {
  if (!window.confirm(`回滚 ${current.value}？这会替换正在运行的容器。`)) {
    return;
  }
  await send("/api/releases/rollback", { service: current.value });
}

async function recreate(id: number) {
  if (!window.confirm(`用失败任务 ${id} 的镜像重建 ${current.value}？这会替换正在运行的容器。`)) {
    return;
  }
  await send(`/api/releases/${id}/recreate`, {});
}

async function send(path: string, body: unknown) {
  message.value = "";
  messageBad.value = false;
  sending.value = true;
  const res = await fetch(path, {
    method: "POST",
    credentials: "include",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(body),
  });
  try {
    if (!res.ok) {
      const decoded = (await res.json().catch(() => null)) as { error?: string } | null;
      message.value = decoded?.error ?? "发布请求失败";
      messageBad.value = true;
    }
    await load();
    if (res.ok && !message.value) {
      message.value = "已提交";
      messageBad.value = false;
    }
  } finally {
    sending.value = false;
  }
}

let timer: ReturnType<typeof setInterval> | undefined;

function watchTask() {
  if (timer) {
    clearInterval(timer);
    timer = undefined;
  }
  if (!busy.value) {
    return;
  }
  timer = setInterval(() => {
    void load();
  }, 2000);
}

onMounted(async () => {
  await Promise.all([load(), loadVersions(), loadRefs()]);
  watchTask();
});

watch(busy, async (isBusy, wasBusy) => {
  watchTask();
  if (wasBusy && !isBusy) {
    await loadVersions();
  }
});
watch(current, async () => {
  message.value = "";
  imageID.value = "";
  await Promise.all([load(), loadVersions()]);
});

onUnmounted(() => {
  if (timer) {
    clearInterval(timer);
  }
});
</script>

<template>
  <div class="release">
    <a-card :bordered="false" class="services">
      <a-button
        v-for="service in services"
        :key="service"
        long
        :type="service === current ? 'primary' : 'secondary'"
        @click="current = service"
      >
        {{ service }}
      </a-button>
    </a-card>
    <a-card :bordered="false" class="detail">
      <p class="label">已构建版本</p>
      <div class="versions">
        <p v-if="!versionsReady">正在读取</p>
        <a-radio-group v-else-if="versions.length" v-model="imageID" direction="vertical">
          <a-radio v-for="version in versions" :key="version.tag + version.image_id" :value="version.image_id">
            {{ versionLabel(version) }}
          </a-radio>
        </a-radio-group>
        <p v-else>没有已构建的版本</p>
      </div>
      <div class="actions">
        <a-button :disabled="busy || !imageID" @click="deploy">部署</a-button>
        <a-button v-if="rollback" :disabled="busy" @click="rollBack">回滚</a-button>
      </div>
      <p class="label">构建新版本</p>
      <div class="versions">
        <p v-if="!refsReady">正在读取</p>
        <a-radio-group v-else-if="remoteRefs.length" v-model="buildRef" direction="vertical">
          <a-radio v-for="item in remoteRefs" :key="item.kind + item.ref" :value="item.ref" :disabled="busy">
            {{ refLabel(item) }}
          </a-radio>
        </a-radio-group>
        <p v-else>没有可构建的版本</p>
      </div>
      <div class="actions">
        <a-button type="primary" :disabled="busy || !buildRef" @click="build">构建</a-button>
      </div>
      <p v-if="message" :class="messageBad ? 'fail' : 'ok'">{{ message }}</p>
      <div v-for="task in tasks" :key="task.id" class="task">
        <p class="task-line" :title="taskTitle(task)">
          <span v-if="task.kind">{{ kindText[task.kind] ?? task.kind }}</span>
          {{ statusText[task.status] ?? task.status }}
          <span v-if="task.git_ref">{{ shortHex(task.git_ref) }}</span>
          <span v-if="task.image_id">{{ shortHex(task.image_id) }}</span>
          {{ clock(task.finished_at) }}
        </p>
        <p v-if="task.error">{{ task.error }}</p>
        <pre class="log">{{ task.log }}</pre>
        <a-button
          v-if="task.status === 'failed' && task.image_id"
          size="mini"
          :disabled="busy"
          @click="recreate(task.id)"
        >
          用该镜像重建
        </a-button>
      </div>
    </a-card>
  </div>
</template>

<style scoped>
.release {
  display: grid;
  grid-template-columns: 180px 1fr;
  gap: 16px;
}

.label {
  margin: 16px 0 8px;
  font-weight: 600;
}

.label:first-child {
  margin-top: 0;
}

.versions {
  display: flex;
  flex-direction: column;
  gap: 8px;
  margin-bottom: 12px;
}

.versions :deep(label) {
  height: auto;
  white-space: normal;
  align-items: flex-start;
  line-height: 1.5;
}

.actions {
  display: flex;
  gap: 8px;
  align-items: center;
}

pre {
  max-height: 240px;
  overflow: auto;
  margin: 8px 0;
  white-space: pre-wrap;
  font-family: ui-monospace, SFMono-Regular, Consolas, monospace;
}

.fail {
  color: #f53f3f;
}

.ok {
  color: #00b42a;
}

@media (max-width: 800px) {
  .release {
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
