import { readFileSync } from "node:fs";
import { join } from "node:path";
import { flushPromises, mount } from "@vue/test-utils";
import ArcoVue from "@arco-design/web-vue";
import { describe, expect, it } from "vitest";
import { createMemoryHistory, createRouter } from "vue-router";
import App from "./App.vue";
import Config from "./views/Config.vue";

Object.defineProperty(window, "matchMedia", {
  writable: true,
  value: (query: string) => ({
    matches: false,
    media: query,
    onchange: null,
    addListener: () => {},
    removeListener: () => {},
    addEventListener: () => {},
    removeEventListener: () => {},
    dispatchEvent: () => false,
  }),
});

describe("Config", () => {
  it("keeps the empty state when Nacos is off", async () => {
    const original = globalThis.fetch;
    globalThis.fetch = async () =>
      new Response(JSON.stringify({ source: "local", enabled: false }), {
        status: 200,
        headers: { "Content-Type": "application/json" },
      });
    try {
      const wrapper = mount(Config, { global: { plugins: [ArcoVue] } });
      await flushPromises();
      expect(wrapper.text()).toContain("配置还没有功能");
      expect(wrapper.text()).not.toContain("灰度");
    } finally {
      globalThis.fetch = original;
    }
  });

  it("publishes the selected service when Nacos is on", async () => {
    const original = globalThis.fetch;
    const calls: { url: string; method?: string; credentials?: RequestCredentials }[] = [];
    globalThis.fetch = async (input, init) => {
      const url = String(input);
      calls.push({ url, method: init?.method, credentials: init?.credentials });
      if (url === "/api/config/source") {
        return new Response(JSON.stringify({ source: "nacos", enabled: true }), { status: 200 });
      }
      if (url === "/api/config/core" && init?.method === "PUT") {
        return new Response(null, { status: 204 });
      }
      if (url === "/api/config/core") {
        return new Response(JSON.stringify({ content: "log:\n  level: debug\n" }), { status: 200 });
      }
      return new Response(JSON.stringify({ revisions: [], listeners: [], ips: [] }), { status: 200 });
    };
    try {
      const wrapper = mount(Config, { global: { plugins: [ArcoVue] } });
      await flushPromises();
      await wrapper.get("button").trigger("click");
      await flushPromises();
      expect(wrapper.text()).toContain("log:");
      const publish = wrapper.findAll("button").find((button) => button.text() === "发布");
      expect(publish).toBeTruthy();
      await publish!.trigger("click");
      await flushPromises();
      expect(calls).toContainEqual({ url: "/api/config/core", method: "PUT", credentials: "include" });
      expect(wrapper.text()).toContain("已发布");
    } finally {
      globalThis.fetch = original;
    }
  });

  it("loads core and marks it selected when Nacos is on", async () => {
    const original = globalThis.fetch;
    globalThis.fetch = async (input) => {
      const url = String(input);
      if (url === "/api/config/source") {
        return new Response(JSON.stringify({ source: "nacos", enabled: true }), { status: 200 });
      }
      if (url === "/api/config/core") {
        return new Response(JSON.stringify({ content: "log:\n  level: info\n" }), { status: 200 });
      }
      return new Response(JSON.stringify({ revisions: [], listeners: [], ips: [] }), { status: 200 });
    };
    try {
      const wrapper = mount(Config, { global: { plugins: [ArcoVue] } });
      await flushPromises();
      expect(wrapper.get("textarea").element.value).toContain("level: info");
      const core = wrapper.findAll("button").find((button) => button.text() === "core");
      expect(core?.classes()).toContain("arco-btn-primary");
      expect(readFileSync(join(process.cwd(), "src/views/Config.vue"), "utf8")).toContain("@media (max-width: 800px)");
    } finally {
      globalThis.fetch = original;
    }
  });

  it("shows the API reason for a bad beta IP and keeps the editor", async () => {
    const original = globalThis.fetch;
    globalThis.fetch = async (input, init) => {
      const url = String(input);
      if (url === "/api/config/source") {
        return new Response(JSON.stringify({ enabled: true }), { status: 200 });
      }
      if (url === "/api/config/core/beta" && init?.method === "PUT") {
        return new Response(JSON.stringify({ error: "无效的灰度 IP" }), { status: 400 });
      }
      if (url === "/api/config/core") {
        return new Response(JSON.stringify({ content: "keep: true\n" }), { status: 200 });
      }
      return new Response(JSON.stringify({ revisions: [], listeners: [], ips: [] }), { status: 200 });
    };
    try {
      const wrapper = mount(Config, { global: { plugins: [ArcoVue] } });
      await flushPromises();
      await wrapper.get("input").setValue("not-an-ip");
      const beta = wrapper.findAll("button").find((button) => button.text() === "灰度");
      await beta?.trigger("click");
      await flushPromises();
      expect(wrapper.text()).toContain("无效的灰度 IP");
      expect(wrapper.get("textarea").element.value).toContain("keep: true");
    } finally {
      globalThis.fetch = original;
    }
  });

  it("does not pretend to stop a beta that is not running", async () => {
    const original = globalThis.fetch;
    const methods: string[] = [];
    globalThis.fetch = async (input, init) => {
      const url = String(input);
      if (url.includes("/beta")) {
        methods.push(init?.method ?? "GET");
      }
      if (url === "/api/config/source") {
        return new Response(JSON.stringify({ enabled: true }), { status: 200 });
      }
      if (url === "/api/config/core") {
        return new Response(JSON.stringify({ content: "a: 1\n" }), { status: 200 });
      }
      return new Response(JSON.stringify({ revisions: [], listeners: [], ips: [] }), { status: 200 });
    };
    try {
      const wrapper = mount(Config, { global: { plugins: [ArcoVue] } });
      await flushPromises();
      const stop = wrapper.findAll("button").find((button) => button.text() === "停止灰度");
      await stop?.trigger("click");
      await flushPromises();
      expect(wrapper.text()).toContain("当前没有灰度");
      expect(methods).not.toContain("DELETE");
    } finally {
      globalThis.fetch = original;
    }
  });

  it("keeps the editor and the error when another service fails to load", async () => {
    const original = globalThis.fetch;
    globalThis.fetch = async (input) => {
      const url = String(input);
      if (url === "/api/config/source") {
        return new Response(JSON.stringify({ enabled: true }), { status: 200 });
      }
      if (url === "/api/config/gateway") {
        return new Response(JSON.stringify({ error: "Nacos 请求失败" }), { status: 502 });
      }
      if (url === "/api/config/core") {
        return new Response(JSON.stringify({ content: "keep: 1\n" }), { status: 200 });
      }
      return new Response(JSON.stringify({ revisions: [], listeners: [{ ip: "10.0.0.8" }], ips: [] }), { status: 200 });
    };
    try {
      const wrapper = mount(Config, { global: { plugins: [ArcoVue] } });
      await flushPromises();
      const gateway = wrapper.findAll("button").find((button) => button.text() === "gateway");
      await gateway?.trigger("click");
      await flushPromises();
      expect(wrapper.get("textarea").element.value).toContain("keep: 1");
      expect(wrapper.text()).toContain("Nacos 请求失败");
      const core = wrapper.findAll("button").find((button) => button.text() === "core");
      expect(core?.classes()).toContain("arco-btn-primary");
    } finally {
      globalThis.fetch = original;
    }
  });

  it("asks before rolling a config back", async () => {
    const original = globalThis.fetch;
    const confirm = window.confirm;
    window.confirm = () => false;
    const methods: string[] = [];
    globalThis.fetch = async (input, init) => {
      const url = String(input);
      methods.push(`${init?.method ?? "GET"} ${url}`);
      if (url === "/api/config/source") {
        return new Response(JSON.stringify({ enabled: true }), { status: 200 });
      }
      if (url === "/api/config/core/history") {
        return new Response(JSON.stringify({ revisions: [{ id: "9", modified: "2026-11-15T13:40:07+08:00" }] }), { status: 200 });
      }
      if (url === "/api/config/core") {
        return new Response(JSON.stringify({ content: "a: 1\n" }), { status: 200 });
      }
      return new Response(JSON.stringify({ revisions: [], listeners: [], ips: [] }), { status: 200 });
    };
    try {
      const wrapper = mount(Config, { global: { plugins: [ArcoVue] } });
      await flushPromises();
      const rollback = wrapper.findAll("button").find((button) => button.text() === "回滚");
      await rollback?.trigger("click");
      await flushPromises();
      expect(methods.some((call) => call.startsWith("POST"))).toBe(false);
    } finally {
      window.confirm = confirm;
      globalThis.fetch = original;
    }
  });
});

describe("source label", () => {
  it("says 本地配置 until the source is nacos", async () => {
    const original = globalThis.fetch;
    let source = "local";
    globalThis.fetch = async (input) => {
      const url = String(input);
      if (url === "/api/me") {
        return new Response(JSON.stringify({ username: "admin" }), { status: 200 });
      }
      return new Response(JSON.stringify({ source, enabled: source === "nacos" }), { status: 200 });
    };
    const router = createRouter({
      history: createMemoryHistory(),
      routes: [{ path: "/", name: "总览", component: { template: "<div />" } }],
    });
    try {
      const wrapper = mount(App, { global: { plugins: [ArcoVue, router] } });
      await router.isReady();
      await flushPromises();
      expect(wrapper.text()).toContain("本地配置");
      expect(wrapper.text()).not.toContain("容器输出");
      expect(wrapper.text()).toContain("退出");
      const appSource = readFileSync(join(process.cwd(), "src/App.vue"), "utf8");
      expect(appSource).toContain("@media (max-width: 800px)");
      source = "nacos";
      const again = mount(App, { global: { plugins: [ArcoVue, router] } });
      await flushPromises();
      expect(again.text()).toContain("Nacos");
      expect(again.text()).not.toContain("本地配置");
      expect(again.text()).not.toContain("容器输出");
      expect(again.text()).toContain("退出");
    } finally {
      globalThis.fetch = original;
    }
  });

  it("shows Nacos after login without a reload", async () => {
    const original = globalThis.fetch;
    let authed = false;
    globalThis.fetch = async (input) => {
      const url = String(input);
      if (url === "/api/me") {
        return new Response(authed ? JSON.stringify({ username: "admin" }) : null, { status: authed ? 200 : 401 });
      }
      if (!authed) {
        return new Response(null, { status: 401 });
      }
      return new Response(JSON.stringify({ source: "nacos", enabled: true }), { status: 200 });
    };
    const router = createRouter({
      history: createMemoryHistory(),
      routes: [
        { path: "/login", name: "login", component: { template: "<div>login</div>" } },
        { path: "/", name: "总览", component: { template: "<div />" } },
      ],
    });
    try {
      await router.push("/login");
      const wrapper = mount(App, { global: { plugins: [ArcoVue, router] } });
      await router.isReady();
      await flushPromises();
      expect(wrapper.text()).not.toContain("Nacos");
      authed = true;
      await router.push("/");
      await flushPromises();
      expect(wrapper.text()).toContain("Nacos");
    } finally {
      globalThis.fetch = original;
    }
  });
});
