import { flushPromises, mount } from "@vue/test-utils";
import ArcoVue from "@arco-design/web-vue";
import { describe, expect, it } from "vitest";
import { createMemoryHistory, createRouter } from "vue-router";
import Login from "./views/Login.vue";

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

describe("Login", () => {
  it("shows 登录失败 after the login button when the server returns 401", async () => {
    const original = globalThis.fetch;
    globalThis.fetch = async () => new Response(null, { status: 401 });
    const router = createRouter({
      history: createMemoryHistory(),
      routes: [
        { path: "/login", component: Login },
        { path: "/", component: { template: "<div />" } },
      ],
    });
    try {
      const wrapper = mount(Login, { global: { plugins: [ArcoVue, router] } });
      await wrapper.get("button").trigger("click");
      await flushPromises();
      expect(wrapper.text()).toContain("登录失败");
    } finally {
      globalThis.fetch = original;
    }
  });
});
