<script setup lang="ts">
import { useRoute, useRouter } from "vue-router";

const route = useRoute();
const router = useRouter();

function open(key: string) {
  router.push(key);
}
</script>

<template>
  <router-view v-if="route.path === '/login'" />
  <a-layout v-else>
    <a-layout-sider>
      <a-menu :selected-keys="[route.path]" @menu-item-click="open">
        <a-menu-item key="/">总览</a-menu-item>
        <a-menu-item key="/config">配置</a-menu-item>
        <a-menu-item key="/cluster">集群</a-menu-item>
        <a-menu-item key="/logs">日志</a-menu-item>
        <a-menu-item key="/release">发布</a-menu-item>
      </a-menu>
    </a-layout-sider>
    <a-layout-content>
      <router-view />
    </a-layout-content>
  </a-layout>
</template>
