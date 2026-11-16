# Admin Shell Repair Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 让已打开的管理端在深色背景上能读、能登录、能退出，顶栏标明当前是本地配置和容器输出。

**Architecture:** 只改 `web/admin` 的主题、登录页和登录后的框架。登录、会话和端口收口保持现有 Go API。顶栏的两段文字写死为「本地配置」和「容器输出」，因为 Nacos 与 Elasticsearch 仍然关闭。配置、集群数据、日志和发布不在这份计划里。

**Tech Stack:** Vue 3、Vite、Arco Design Vue、vitest、`@vue/test-utils`、jsdom。

## Global Constraints

- 浏览器只访问管理后台 `8091`，以及网关 TCP `8000`、WebSocket `8001`。
- compose 不把 `core:9001`、`chatroom:9002`、网关 HTTP `8080`、etcd、MySQL、Redis、Kafka、webhook 映射到宿主机。这份计划不改 `deploy/docker-compose.yml`。
- 登录账号是平台操作员，用户名固定为 `admin`。失败响应正文都是 `登录失败`。
- 未设置 `ADMIN_PASSWORD` 时密码是 `123456`。设置了环境变量就用那个值。`operators` 表已有记录时，不覆盖已写入的密码哈希。
- cookie 名 `orbikit_session`，HttpOnly，SameSite=Lax，不标记 Secure。
- 页面背景色 `#17171a`，文字色 `#f2f3f5`。`document.body` 设置 `arco-theme="dark"`。
- 顶栏固定显示「本地配置」「容器输出」和按钮「退出」。
- 测试用 vitest。不引入新的前端依赖。
- 不在这一份计划里实现服务状态、etcd 页面、YAML 编辑、容器日志、构建或 Nacos。

浏览器测试已经通过、这份计划不重复修的项：未登录访问内页会到 `/login`；错误密码和空密码显示「登录失败」；`123456` 能进入总览；刷新保持登录；`POST /api/logout` 返回 204 后再次访问内页回到登录页；左侧五个菜单能切换路由；宿主机端口只有 `8091`、`8000`、`8001`。

后续计划（本文件不实现）：

1. `2026-11-14-admin-status.md`：builder，以及总览服务表和集群页。通过标准是总览有容器状态、gRPC 健康、etcd 实例、镜像 ID、最近发布；集群有 etcd 健康、主节点、库大小、告警和实例地址；etcd 不可达时显示故障。
2. `2026-11-14-admin-config.md`：本地 YAML 的编辑、未保存离开确认、diff、历史、重启和回滚。
3. `2026-11-14-admin-logs.md`：按服务跟随和暂停容器输出，等宽字体，不做关键词检索。
4. `2026-11-14-admin-release.md`：只允许 core、chatroom、gateway、webhook；版本只能是 tag 或完整 commit SHA；能看构建输出、替换和回滚上一镜像。
5. `2026-11-14-admin-optional.md`：Nacos 与 Elasticsearch 开关，默认关闭。打开后顶栏改为读真实来源，替换本计划写死的「本地配置」和「容器输出」。

---

### Task 1: 深色主题

**Files:**
- Create: `web/admin/src/theme.ts`
- Create: `web/admin/src/theme.test.ts`
- Modify: `web/admin/src/main.ts`

**Interfaces:**
- Consumes: 无
- Produces: `pageBackground` 为 `"#17171a"`，`pageText` 为 `"#f2f3f5"`，`applyTheme(root: HTMLElement): void`。它给 `root` 设置属性 `arco-theme="dark"`，并把 `root.style.background` 和 `root.style.color` 设成上面两个颜色。

- [ ] **Step 1: Write the failing test**

```ts
import { describe, expect, it } from "vitest";
import { applyTheme, pageBackground, pageText } from "./theme";

describe("applyTheme", () => {
  it("paints the root dark with light text", () => {
    const root = document.createElement("div");
    applyTheme(root);
    expect(pageBackground).toBe("#17171a");
    expect(pageText).toBe("#f2f3f5");
    expect(root.getAttribute("arco-theme")).toBe("dark");
    expect(root.style.background).toBe("#17171a");
    expect(root.style.color).toBe("#f2f3f5");
  });
});
```

- [ ] **Step 2: Run test to verify it fails**

Run: `npm test -- src/theme.test.ts`

工作目录：`web/admin`。

Expected: FAIL，找不到 `./theme`。

- [ ] **Step 3: Write minimal implementation**

`web/admin/src/theme.ts`：

```ts
export const pageBackground = "#17171a";
export const pageText = "#f2f3f5";

export function applyTheme(root: HTMLElement) {
  root.setAttribute("arco-theme", "dark");
  root.style.background = pageBackground;
  root.style.color = pageText;
}
```

`web/admin/src/main.ts` 在 `createApp` 之前调用它：

```ts
import { createApp } from "vue";
import ArcoVue from "@arco-design/web-vue";
import "@arco-design/web-vue/dist/arco.css";
import App from "./App.vue";
import router from "./router";
import { applyTheme } from "./theme";

applyTheme(document.body);
createApp(App).use(ArcoVue).use(router).mount("#app");
```

- [ ] **Step 4: Run test to verify it passes**

Run: `npm test -- src/theme.test.ts`

工作目录：`web/admin`。

Expected: PASS。

- [ ] **Step 5: Commit**

```bash
git add web/admin/src/theme.ts web/admin/src/theme.test.ts web/admin/src/main.ts
git commit -m "fix: make the admin page readable on a dark background"
```

---

### Task 2: 可辨认的登录页

**Files:**
- Modify: `web/admin/src/views/Login.vue`
- Modify: `web/admin/src/login.test.ts`

**Interfaces:**
- Consumes: `pageBackground`、`pageText`。`POST /api/login`，成功状态码 `204`，失败时页面文字包含 `登录失败`。
- Produces: 登录页有标签「密码」、密码框、按钮「登录」。整页使用 `pageBackground` 和 `pageText`。

- [ ] **Step 1: Write the failing test**

把 `web/admin/src/login.test.ts` 换成：

```ts
import { flushPromises, mount } from "@vue/test-utils";
import ArcoVue from "@arco-design/web-vue";
import { describe, expect, it } from "vitest";
import { createMemoryHistory, createRouter } from "vue-router";
import { pageBackground, pageText } from "./theme";
import Login from "./views/Login.vue";

function mountLogin() {
  const router = createRouter({
    history: createMemoryHistory(),
    routes: [
      { path: "/login", component: Login },
      { path: "/", component: { template: "<div />" } },
    ],
  });
  return mount(Login, { global: { plugins: [ArcoVue, router] } });
}

describe("Login", () => {
  it("shows a labeled password field on the dark page", () => {
    const wrapper = mountLogin();
    expect(wrapper.text()).toContain("密码");
    expect(wrapper.get("input").attributes("type")).toBe("password");
    expect(wrapper.text()).toContain("登录");
    const style = wrapper.get(".login").attributes("style") ?? "";
    expect(style).toContain(pageBackground);
    expect(style).toContain(pageText);
  });

  it("shows 登录失败 after the login button when the server returns 401", async () => {
    const original = globalThis.fetch;
    globalThis.fetch = async () => new Response(null, { status: 401 });
    try {
      const wrapper = mountLogin();
      await wrapper.get("button").trigger("click");
      await flushPromises();
      expect(wrapper.text()).toContain("登录失败");
    } finally {
      globalThis.fetch = original;
    }
  });
});
```

- [ ] **Step 2: Run test to verify it fails**

Run: `npm test -- src/login.test.ts`

工作目录：`web/admin`。

Expected: FAIL，页面文字里没有「密码」，或 `.login` 的样式不含 `#17171a`。

- [ ] **Step 3: Write minimal implementation**

`web/admin/src/views/Login.vue`：

```vue
<script setup lang="ts">
import { ref } from "vue";
import { useRouter } from "vue-router";
import { pageBackground, pageText } from "../theme";

const password = ref("");
const failed = ref(false);
const router = useRouter();
const pageStyle = {
  background: pageBackground,
  color: pageText,
  minHeight: "100%",
};

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
  <div class="login" :style="pageStyle">
    <a-card title="orbikit">
      <a-form :model="{ password }" layout="vertical" @submit.prevent="submit">
        <a-form-item label="密码" field="password">
          <a-input-password v-model="password" autocomplete="current-password" />
        </a-form-item>
        <a-button html-type="submit" type="primary" long>登录</a-button>
        <p v-if="failed">登录失败</p>
      </a-form>
    </a-card>
  </div>
</template>

<style scoped>
.login {
  display: flex;
  align-items: center;
  justify-content: center;
}
</style>
```

- [ ] **Step 4: Run test to verify it passes**

Run: `npm test -- src/login.test.ts`

工作目录：`web/admin`。

Expected: PASS，2 个测试通过。

- [ ] **Step 5: Commit**

```bash
git add web/admin/src/views/Login.vue web/admin/src/login.test.ts
git commit -m "fix: show a readable admin login form"
```

---

### Task 3: 顶栏、退出和可读的内容区

**Files:**
- Modify: `web/admin/src/App.vue`
- Create: `web/admin/src/shell.test.ts`

**Interfaces:**
- Consumes: `pageBackground`、`pageText`。`POST /api/logout` 成功时状态码 `204`。路由守卫继续请求 `GET /api/me`。
- Produces: 登录后的页面包含文字「本地配置」「容器输出」「退出」，以及当前路由名。点击「退出」发送 `POST /api/logout` 并进入 `/login`。外壳样式包含 `pageBackground` 和 `pageText`。

- [ ] **Step 1: Write the failing test**

`web/admin/src/shell.test.ts`：

```ts
import { flushPromises, mount } from "@vue/test-utils";
import ArcoVue from "@arco-design/web-vue";
import { describe, expect, it } from "vitest";
import { createMemoryHistory, createRouter } from "vue-router";
import App from "./App.vue";
import { pageBackground, pageText } from "./theme";
import Section from "./views/Section.vue";

function mountShell() {
  const router = createRouter({
    history: createMemoryHistory(),
    routes: [
      { path: "/login", component: { template: "<div>登录页</div>" } },
      { path: "/", name: "总览", component: Section },
      { path: "/config", name: "配置", component: Section },
      { path: "/cluster", name: "集群", component: Section },
      { path: "/logs", name: "日志", component: Section },
      { path: "/release", name: "发布", component: Section },
    ],
  });
  return { router, wrapper: mount(App, { global: { plugins: [ArcoVue, router] } }) };
}

describe("App shell", () => {
  it("shows the local mode header and the current section in light text", async () => {
    const original = globalThis.fetch;
    globalThis.fetch = async () => new Response('{"username":"admin"}', { status: 200 });
    try {
      const { router, wrapper } = mountShell();
      await router.push("/");
      await router.isReady();
      await flushPromises();
      expect(wrapper.text()).toContain("本地配置");
      expect(wrapper.text()).toContain("容器输出");
      expect(wrapper.text()).toContain("退出");
      expect(wrapper.text()).toContain("总览");
      const style = wrapper.get(".shell").attributes("style") ?? "";
      expect(style).toContain(pageBackground);
      expect(style).toContain(pageText);
    } finally {
      globalThis.fetch = original;
    }
  });

  it("logs out and returns to the login page", async () => {
    const original = globalThis.fetch;
    const calls: string[] = [];
    globalThis.fetch = async (input, init) => {
      const url = String(input);
      calls.push(`${init?.method ?? "GET"} ${url}`);
      return new Response(null, { status: 204 });
    };
    try {
      const { router, wrapper } = mountShell();
      await router.push("/");
      await router.isReady();
      await flushPromises();
      await wrapper.get("button").trigger("click");
      await flushPromises();
      expect(calls).toContain("POST /api/logout");
      expect(router.currentRoute.value.path).toBe("/login");
    } finally {
      globalThis.fetch = original;
    }
  });
});
```

- [ ] **Step 2: Run test to verify it fails**

Run: `npm test -- src/shell.test.ts`

工作目录：`web/admin`。

Expected: FAIL，页面文字里没有「本地配置」或「退出」。

- [ ] **Step 3: Write minimal implementation**

`web/admin/src/App.vue`：

```vue
<script setup lang="ts">
import { useRoute, useRouter } from "vue-router";
import { pageBackground, pageText } from "./theme";

const route = useRoute();
const router = useRouter();
const shellStyle = {
  background: pageBackground,
  color: pageText,
  minHeight: "100%",
};

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
  <a-layout v-else class="shell" :style="shellStyle">
    <a-layout-header>
      <span>本地配置</span>
      <span>容器输出</span>
      <a-button @click="logout">退出</a-button>
    </a-layout-header>
    <a-layout>
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
  </a-layout>
</template>
```

- [ ] **Step 4: Run test to verify it passes**

Run: `npm test`

工作目录：`web/admin`。

Expected: PASS。`theme.test.ts`、`login.test.ts`、`shell.test.ts` 全部通过。

- [ ] **Step 5: Commit**

```bash
git add web/admin/src/App.vue web/admin/src/shell.test.ts
git commit -m "fix: add the admin header and logout control"
```

---

### Task 4: 浏览器复核

**Files:**
- Modify: `internal/admin/static/dist/`（由 `npm run build` 生成，随管理端镜像一起更新）

**Interfaces:**
- Consumes: Task 1 到 Task 3 的页面。
- Produces: 重新构建后的静态资源。浏览器打开 `http://localhost:8091/login` 时，密码框和「登录」在深色底上可见。使用 `123456` 登录后，顶栏有「本地配置」「容器输出」「退出」，内容区的「总览」是浅色字。点击「退出」回到登录页。

- [ ] **Step 1: Build the embedded page**

Run: `npm run build`

工作目录：`web/admin`。

Expected: `internal/admin/static/dist/index.html` 被写好，命令退出码为 0。

- [ ] **Step 2: Rebuild and open the admin container**

在仓库根目录执行：

```bash
docker compose -f deploy/docker-compose.yml up -d --build admin
```

然后在浏览器打开 `http://localhost:8091/`。

Expected: 未登录时看到带「密码」标签的登录卡片。输入 `123456` 后看到深色内容区和浅色的「总览」。点击「退出」后地址变为 `/login`。配置、集群、日志、发布仍然只有标题，这是后续计划的范围。

- [ ] **Step 3: Commit the built assets**

```bash
git add internal/admin/static/dist
git commit -m "fix: publish the readable admin shell"
```
