<script setup lang="ts">
import { ref } from "vue";
import { useRouter } from "vue-router";

const password = ref("");
const failed = ref(false);
const router = useRouter();

async function submit() {
  const res = await fetch("/api/login", {
    method: "POST",
    credentials: "include",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ password: password.value }),
  });
  if (res.status !== 204) {
    failed.value = true;
    return;
  }
  failed.value = false;
  await router.push("/");
}
</script>

<template>
  <form @submit.prevent="submit">
    <input v-model="password" type="password" autocomplete="current-password" />
    <button type="submit" @click.prevent="submit">登录</button>
    <p v-if="failed">登录失败</p>
  </form>
</template>
