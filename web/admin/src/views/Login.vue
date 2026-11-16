<script setup lang="ts">
import { ref } from "vue";
import { useRoute, useRouter } from "vue-router";
import { landingPath } from "../router";

const password = ref("");
const failed = ref(false);
const pending = ref(false);
const route = useRoute();
const router = useRouter();

async function submit() {
  if (pending.value) {
    return;
  }
  if (!password.value.trim()) {
    failed.value = true;
    return;
  }
  pending.value = true;
  try {
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
    await router.push(landingPath(route.query.redirect));
  } finally {
    pending.value = false;
  }
}
</script>

<template>
  <div class="login">
    <a-card class="card" title="orbikit">
      <a-form layout="vertical" :model="{ password }" @submit.prevent="submit">
        <a-form-item label="密码" field="password">
          <a-input-password
            v-model="password"
            autocomplete="current-password"
            :input-attrs="{ 'aria-label': '密码', id: 'admin-password', autocomplete: 'current-password' }"
          />
        </a-form-item>
        <a-button type="primary" html-type="submit" long :loading="pending" :disabled="pending" @click.prevent="submit">登录</a-button>
        <p v-if="failed" class="fail">登录失败</p>
      </a-form>
    </a-card>
  </div>
</template>

<style scoped>
.login {
  min-height: 100%;
  display: flex;
  align-items: center;
  justify-content: center;
  background: var(--color-fill-2);
}

.card {
  width: min(360px, calc(100% - 32px));
  border-radius: 8px;
}

.fail {
  margin: 12px 0 0;
  color: #f53f3f;
}
</style>
