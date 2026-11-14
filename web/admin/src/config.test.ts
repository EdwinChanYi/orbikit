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
    } finally {
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
      expect(wrapper.text()).toContain("容器输出");
      expect(wrapper.text()).toContain("退出");
      source = "nacos";
      const again = mount(App, { global: { plugins: [ArcoVue, router] } });
      await flushPromises();
      expect(again.text()).toContain("Nacos");
      expect(again.text()).not.toContain("本地配置");
      expect(again.text()).toContain("容器输出");
      expect(again.text()).toContain("退出");
    } finally {
      globalThis.fetch = original;
    }
  });
});
