<script setup lang="ts">
import { onMounted, ref, watch } from "vue";
import { useRoute, useRouter } from "vue-router";

const route = useRoute();
const router = useRouter();
const sourceLabel = ref("本地配置");

async function loadSource() {
  try {
    const res = await fetch("/api/config/source", { credentials: "include" });
    if (!res.ok) {
      return;
    }
    const body = (await res.json()) as { source?: string };
    sourceLabel.value = body.source === "nacos" ? "Nacos" : "本地配置";
  } catch {
    sourceLabel.value = "本地配置";
  }
}

onMounted(loadSource);
watch(
  () => route.path,
  (path) => {
    if (path !== "/login") {
      void loadSource();
    }
  },
);

function open(key: string) {
  router.push(key);
}

async function logout() {
  await fetch("/api/logout", { method: "POST", credentials: "include" });
  await router.push("/login");
}
</script>

<template>
  <router-view v-if="route.path === '/login'" />
  <a-layout v-else class="shell">
    <a-layout-sider class="sider" :width="216">
      <div class="brand">orbikit</div>
      <a-menu :selected-keys="[route.path]" @menu-item-click="open">
        <a-menu-item key="/">总览</a-menu-item>
        <a-menu-item key="/config">配置</a-menu-item>
        <a-menu-item key="/cluster">集群</a-menu-item>
        <a-menu-item key="/logs">日志</a-menu-item>
        <a-menu-item key="/release">发布</a-menu-item>
      </a-menu>
    </a-layout-sider>
    <a-layout>
      <a-layout-header class="top">
        <span class="title">{{ route.name }}</span>
        <a-space>
          <a-tag>{{ sourceLabel }}</a-tag>
          <a-tag>容器输出</a-tag>
          <a-button size="small" @click="logout">退出</a-button>
        </a-space>
      </a-layout-header>
      <a-layout-content class="content">
        <router-view />
      </a-layout-content>
    </a-layout>
  </a-layout>
</template>

<style>
html,
body,
#app {
  height: 100%;
  margin: 0;
}

.shell {
  min-height: 100%;
  background: var(--color-fill-2);
}

.sider {
  background: var(--color-bg-2);
  border-right: 1px solid var(--color-border-2);
}

.brand {
  height: 56px;
  display: flex;
  align-items: center;
  padding: 0 20px;
  font-size: 16px;
  font-weight: 600;
  letter-spacing: 0.02em;
}

.top {
  height: 56px;
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: 0 24px;
  background: var(--color-bg-2);
  border-bottom: 1px solid var(--color-border-2);
}

.title {
  font-size: 16px;
  font-weight: 600;
}

.content {
  padding: 20px 24px 32px;
}
</style>
