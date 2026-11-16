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

function loginRouter() {
  return createRouter({
    history: createMemoryHistory(),
    routes: [
      { path: "/login", component: Login },
      { path: "/", component: { template: "<div>home</div>" } },
      { path: "/config", component: { template: "<div>config</div>" } },
    ],
  });
}

describe("Login", () => {
  it("shows 登录失败 after the login button when the server returns 401", async () => {
    const original = globalThis.fetch;
    globalThis.fetch = async () => new Response(null, { status: 401 });
    const router = loginRouter();
    try {
      const wrapper = mount(Login, { global: { plugins: [ArcoVue, router] } });
      await wrapper.get("input").setValue("secret");
      await wrapper.get("button").trigger("click");
      await flushPromises();
      expect(wrapper.text()).toContain("登录失败");
      expect(wrapper.get("input").attributes("aria-label")).toBe("密码");
    } finally {
      globalThis.fetch = original;
    }
  });

  it("does not call the server when the password is empty", async () => {
    const original = globalThis.fetch;
    let calls = 0;
    globalThis.fetch = async () => {
      calls += 1;
      return new Response(null, { status: 401 });
    };
    const router = loginRouter();
    try {
      await router.push("/login");
      const wrapper = mount(Login, { global: { plugins: [ArcoVue, router] } });
      await wrapper.get("button").trigger("click");
      await flushPromises();
      expect(wrapper.text()).toContain("登录失败");
      expect(router.currentRoute.value.path).not.toBe("/");
      expect(calls).toBe(0);
    } finally {
      globalThis.fetch = original;
    }
  });

  it("posts a password once while the first request is still open", async () => {
    const original = globalThis.fetch;
    let calls = 0;
    globalThis.fetch = () => {
      calls += 1;
      return new Promise(() => {});
    };
    const router = loginRouter();
    try {
      const wrapper = mount(Login, { global: { plugins: [ArcoVue, router] } });
      await wrapper.get("input").setValue("secret");
      await wrapper.get("button").trigger("click");
      await wrapper.get("button").trigger("click");
      await flushPromises();
      expect(calls).toBe(1);
    } finally {
      globalThis.fetch = original;
    }
  });

  it("returns to the page that was requested before login", async () => {
    const original = globalThis.fetch;
    globalThis.fetch = async () => new Response(null, { status: 204 });
    const router = loginRouter();
    try {
      await router.push({ path: "/login", query: { redirect: "/config" } });
      const wrapper = mount(Login, { global: { plugins: [ArcoVue, router] } });
      await wrapper.get("input").setValue("secret");
      await wrapper.get("button").trigger("click");
      await flushPromises();
      expect(router.currentRoute.value.path).toBe("/config");
    } finally {
      globalThis.fetch = original;
    }
  });
});
