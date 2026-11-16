import { readFileSync } from "node:fs";
import { join } from "node:path";
import { flushPromises, mount } from "@vue/test-utils";
import ArcoVue from "@arco-design/web-vue";
import { describe, expect, it } from "vitest";
import Release from "./views/Release.vue";

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

function mountRelease(
  body: unknown,
  versions: { tag: string; image_id: string; created_at: string }[] = [],
  refs: { name: string; ref: string; kind: string; summary: string; committed_at: string }[] = [],
) {
  const calls: { url: string; method?: string; body?: BodyInit | null }[] = [];
  globalThis.fetch = async (input, init) => {
    const url = String(input);
    calls.push({ url, method: init?.method, body: init?.body });
    if (url.includes("/versions")) {
      return new Response(JSON.stringify({ versions }), { status: 200, headers: { "Content-Type": "application/json" } });
    }
    if (url.includes("/refs")) {
      return new Response(JSON.stringify({ refs }), { status: 200, headers: { "Content-Type": "application/json" } });
    }
    return new Response(JSON.stringify(body), { status: 200, headers: { "Content-Type": "application/json" } });
  };
  window.confirm = () => true;
  const wrapper = mount(Release, { global: { plugins: [ArcoVue] } });
  return { wrapper, calls };
}

describe("Release", () => {
  it("lists six services and keeps rollback unavailable without a success", async () => {
    const { wrapper } = mountRelease({ releases: [], active: false, rollback: false });
    await flushPromises();
    for (const service of ["core", "chatroom", "gateway", "webhook", "admin", "builder"]) {
      expect(wrapper.text()).toContain(service);
    }
    const rollback = wrapper.findAll("button").find((button) => button.text() === "回滚");
    expect(rollback).toBeUndefined();
    expect(wrapper.text()).toContain("没有已构建的版本");
    expect(wrapper.text()).toContain("没有可构建的版本");
  });

  it("lists built versions and deploys the selected image", async () => {
    const latest = "sha256:" + "ab".repeat(32);
    const task5 = "sha256:" + "cd".repeat(32);
    const { wrapper, calls } = mountRelease({ releases: [], active: false, rollback: false }, [
      { tag: "latest", image_id: latest, created_at: "2026-11-15T08:00:00Z" },
      { tag: "task5", image_id: task5, created_at: "2026-11-14T08:00:00Z" },
    ]);
    await flushPromises();
    expect(wrapper.text()).toContain("latest");
    expect(wrapper.text()).toContain("task5");
    expect(wrapper.text()).not.toContain("没有已构建的版本");

    const radios = wrapper.findAll('input[type="radio"]');
    expect(radios).toHaveLength(2);
    await radios[1].setValue(true);
    const deploy = wrapper.findAll("button").find((button) => button.text() === "部署");
    await deploy?.trigger("click");
    await flushPromises();
    const posted = calls.find((call) => call.method === "POST");
    expect(posted?.url).toContain("/api/releases/deploy");
    expect(String(posted?.body)).toContain(task5);
  });

  it("lists remote versions and builds the selected ref", async () => {
    const sha = "a".repeat(40);
    const { wrapper, calls } = mountRelease({ releases: [], active: false, rollback: false }, [], [
      { name: "v0.0.2", ref: "v0.0.2", kind: "tag", summary: "", committed_at: "" },
      { name: sha.slice(0, 12), ref: sha, kind: "commit", summary: "fix login", committed_at: "2026-11-15T14:52:00Z" },
    ]);
    await flushPromises();
    expect(wrapper.text()).toContain("v0.0.2");
    expect(wrapper.text()).toContain("fix login");
    expect(wrapper.find("input.arco-input").exists()).toBe(false);

    const radios = wrapper.findAll('input[type="radio"]');
    expect(radios).toHaveLength(2);
    await radios[1].setValue(true);
    const build = wrapper.findAll("button").find((button) => button.text() === "构建");
    await build?.trigger("click");
    await flushPromises();
    const posted = calls.find((call) => call.method === "POST");
    expect(posted?.url).toContain("/api/releases");
    expect(posted?.url).not.toContain("/deploy");
    expect(String(posted?.body)).toContain('"service":"core"');
    expect(String(posted?.body)).toContain(`"ref":"${sha}"`);
  });

  it("disables publish and rollback while a task is running", async () => {
    const { wrapper } = mountRelease({
      releases: [{ id: 1, service: "core", status: "running", git_ref: "v1", image_id: "", log: "building", error: "" }],
      active: true,
      rollback: true,
    });
    await flushPromises();
    const publish = wrapper.findAll("button").find((button) => button.text() === "部署");
    const build = wrapper.findAll("button").find((button) => button.text() === "构建");
    const rollback = wrapper.findAll("button").find((button) => button.text() === "回滚");
    expect(publish?.attributes("disabled")).toBeDefined();
    expect(build?.attributes("disabled")).toBeDefined();
    expect(rollback?.attributes("disabled")).toBeDefined();
    expect(wrapper.text()).toContain("building");
  });

  it("does not deploy when the confirmation is declined", async () => {
    const latest = "sha256:" + "ab".repeat(32);
    const { wrapper, calls } = mountRelease({ releases: [], active: false, rollback: false }, [
      { tag: "latest", image_id: latest, created_at: "2026-11-15T08:00:00Z" },
    ]);
    window.confirm = () => false;
    await flushPromises();
    const deploy = wrapper.findAll("button").find((button) => button.text() === "部署");
    await deploy?.trigger("click");
    await flushPromises();
    expect(calls.some((call) => call.method === "POST")).toBe(false);
  });

  it("selects the latest tag and marks the current service", async () => {
    const latest = "sha256:" + "ab".repeat(32);
    const older = "sha256:" + "cd".repeat(32);
    const { wrapper } = mountRelease({ releases: [], active: false, rollback: false }, [
      { tag: older.slice(7, 19), image_id: older, created_at: "2026-11-15T17:11:00Z" },
      { tag: "latest", image_id: latest, created_at: "2026-11-15T01:21:00Z" },
    ]);
    await flushPromises();
    const checked = wrapper.findAll('input[type="radio"]').filter((radio) => (radio.element as HTMLInputElement).checked);
    expect(checked.map((radio) => (radio.element as HTMLInputElement).value)).toContain(latest);
    const core = wrapper.findAll("button").find((button) => button.text() === "core");
    expect(core?.classes()).toContain("arco-btn-primary");
    expect(readFileSync(join(process.cwd(), "src/views/Release.vue"), "utf8")).toContain("@media (max-width: 800px)");
  });

  it("names the task kind, shortens the time, and limits the log", async () => {
    const { wrapper } = mountRelease({
      releases: [
        {
          id: 6,
          service: "webhook",
          kind: "publish",
          status: "succeeded",
          git_ref: "b".repeat(40),
          image_id: "sha256:" + "ab".repeat(32),
          log: "line\n".repeat(80),
          error: "",
          finished_at: "2026-11-15T17:11:45.724583Z",
        },
        {
          id: 4,
          service: "webhook",
          kind: "rollback",
          status: "succeeded",
          git_ref: "",
          image_id: "sha256:" + "cd".repeat(32),
          log: "",
          error: "",
          finished_at: "2026-11-15T16:00:00Z",
        },
        {
          id: 3,
          service: "webhook",
          kind: "recreate",
          status: "failed",
          git_ref: "",
          image_id: "sha256:" + "ef".repeat(32),
          log: "",
          error: "重建失败",
          finished_at: "2026-11-15T15:00:00Z",
        },
      ],
      active: false,
      rollback: false,
    });
    await flushPromises();
    expect(wrapper.text()).toContain("发布");
    expect(wrapper.text()).toContain("回滚");
    expect(wrapper.text()).toContain("重建");
    expect(wrapper.text()).toContain("2026-11-15 17:11");
    expect(wrapper.text()).not.toContain("724583");
    expect(wrapper.text()).toContain("重建失败");
    expect(readFileSync(join(process.cwd(), "src/views/Release.vue"), "utf8")).toContain("max-height: 240px");
  });

  it("says the lists are loading instead of claiming they are empty", () => {
    const original = globalThis.fetch;
    globalThis.fetch = () => new Promise(() => {});
    try {
      const wrapper = mount(Release, { global: { plugins: [ArcoVue] } });
      expect(wrapper.text()).toContain("正在读取");
      expect(wrapper.text()).not.toContain("没有可构建的版本");
      expect(wrapper.text()).not.toContain("没有已构建的版本");
    } finally {
      globalThis.fetch = original;
    }
  });

  it("shows the server message when a deploy is rejected", async () => {
    const latest = "sha256:" + "ab".repeat(32);
    const { wrapper } = mountRelease({ releases: [], active: false, rollback: false }, [
      { tag: "latest", image_id: latest, created_at: "2026-11-15T08:00:00Z" },
    ]);
    const original = globalThis.fetch;
    globalThis.fetch = async (input, init) => {
      if (init?.method === "POST") {
        return new Response(JSON.stringify({ error: "已有发布在进行" }), { status: 409 });
      }
      return original(input, init);
    };
    await flushPromises();
    const deploy = wrapper.findAll("button").find((button) => button.text() === "部署");
    await deploy?.trigger("click");
    await flushPromises();
    expect(wrapper.text()).toContain("已有发布在进行");
  });
});
