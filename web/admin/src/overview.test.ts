import { readFileSync } from "node:fs";
import { join } from "node:path";
import { flushPromises, mount } from "@vue/test-utils";
import { describe, expect, it } from "vitest";
import router from "./router";
import Cluster from "./views/Cluster.vue";
import Overview from "./views/Overview.vue";
import Section from "./views/Section.vue";

// GET /api/overview has no top-level etcd field. A registered service with
// instances: null is the etcd fault; it must not be presented as zero instances.
const etcdFaultOverview = {
  services: [
    {
      name: "core",
      container: "running",
      grpc: "serving",
      instances: null,
      image_id: "img-core",
      last_release: null,
    },
    {
      name: "chatroom",
      container: "exited",
      grpc: "not_serving",
      instances: null,
      image_id: "img-chat",
      last_release: null,
    },
    {
      name: "webhook",
      container: "missing",
      grpc: "unknown",
      instances: null,
      image_id: "img-webhook",
      last_release: null,
    },
    {
      name: "gateway",
      container: "unknown",
      grpc: "skipped",
      instances: [],
      image_id: "img-gateway",
      last_release: null,
    },
  ],
};

describe("Overview", () => {
  it("shows etcd fault, running, skipped, and no release without a zero instance count", async () => {
    const calls: { url: string; credentials?: RequestCredentials }[] = [];
    const original = globalThis.fetch;
    globalThis.fetch = async (input, init) => {
      calls.push({ url: String(input), credentials: init?.credentials });
      return new Response(JSON.stringify(etcdFaultOverview), {
        status: 200,
        headers: { "Content-Type": "application/json" },
      });
    };
    try {
      const wrapper = mount(Overview);
      await flushPromises();

      expect(calls).toEqual([{ url: "/api/overview", credentials: "include" }]);
      expect(wrapper.text()).toContain("故障");
      expect(wrapper.text()).toContain("运行中");
      expect(wrapper.text()).toContain("不适用");
      expect(wrapper.text()).toContain("尚无发布");
      expect(wrapper.text()).not.toContain("0 个实例");

      expect(
        wrapper.findAll(".status-unknown").some((node) => node.text() === "尚无发布"),
      ).toBe(true);
      expect(
        wrapper.findAll(".status-ok").some((node) => node.text() === "运行中"),
      ).toBe(true);

      const etcd = wrapper.findAll("p").find((node) => node.text().includes("etcd"));
      expect(etcd?.text()).toContain("故障");
      expect(etcd?.text()).not.toContain("正常");
      expect(etcd?.find(".status-fault").exists()).toBe(true);

      for (const header of ["服务", "容器", "gRPC", "实例", "镜像", "最近发布"]) {
        expect(wrapper.text()).toContain(header);
      }

      const core = rowNamed(wrapper, "core");
      expect(cellText(core, 1)).toBe("运行中");
      expect(core.findAll("td")[1].classes()).toContain("status-ok");
      expect(cellText(core, 2)).toBe("正常");
      expect(core.findAll("td")[2].classes()).toContain("status-ok");
      expect(cellText(core, 3)).toBe("故障");
      expect(core.findAll("td")[3].classes()).toContain("status-fault");
      expect(cellText(core, 4)).toBe("img-core");
      expect(cellText(core, 5)).toBe("尚无发布");
      expect(core.findAll("td")[5].classes()).toContain("status-unknown");

      const chatroom = rowNamed(wrapper, "chatroom");
      expect(cellText(chatroom, 1)).toBe("已停止");
      expect(chatroom.findAll("td")[1].classes()).toContain("status-fault");
      expect(cellText(chatroom, 2)).toBe("故障");
      expect(chatroom.findAll("td")[2].classes()).toContain("status-fault");
      expect(cellText(chatroom, 3)).toBe("故障");

      const webhook = rowNamed(wrapper, "webhook");
      expect(cellText(webhook, 1)).toBe("不存在");
      expect(webhook.findAll("td")[1].classes()).toContain("status-fault");
      expect(cellText(webhook, 2)).toBe("未知");
      expect(webhook.findAll("td")[2].classes()).toContain("status-unknown");
      expect(cellText(webhook, 3)).toBe("故障");

      const gateway = rowNamed(wrapper, "gateway");
      expect(cellText(gateway, 1)).toBe("未知");
      expect(gateway.findAll("td")[1].classes()).toContain("status-unknown");
      expect(cellText(gateway, 2)).toBe("不适用");
      expect(gateway.findAll("td")[2].classes()).toContain("status-unknown");
      expect(cellText(gateway, 3)).toBe("不适用");
      expect(gateway.findAll("td")[3].classes()).toContain("status-unknown");
      expect(cellText(gateway, 5)).toBe("尚无发布");

      const source = readFileSync(join(process.cwd(), "src/views/Overview.vue"), "utf8");
      expect(source).toContain(".status-ok { color: #00b42a; }");
      expect(source).toContain(".status-degraded { color: #ff7d00; }");
      expect(source).toContain(".status-fault { color: #f53f3f; }");
      expect(source).toContain(".status-unknown { color: #86909c; }");
    } finally {
      globalThis.fetch = original;
    }
  });

  it("shows a real instance count when etcd lists instances", async () => {
    const original = globalThis.fetch;
    globalThis.fetch = async () =>
      new Response(
        JSON.stringify({
          services: [
            {
              name: "core",
              container: "running",
              grpc: "serving",
              instances: [{ service: "core", id: "1", addr: "core:9001" }],
              image_id: "img-core",
              last_release: null,
            },
            {
              name: "chatroom",
              container: "running",
              grpc: "serving",
              instances: [],
              image_id: "img-chat",
              last_release: null,
            },
            {
              name: "webhook",
              container: "running",
              grpc: "serving",
              instances: [],
              image_id: "img-webhook",
              last_release: null,
            },
            {
              name: "gateway",
              container: "running",
              grpc: "skipped",
              instances: [],
              image_id: "img-gateway",
              last_release: null,
            },
          ],
        }),
        { status: 200, headers: { "Content-Type": "application/json" } },
      );
    try {
      const wrapper = mount(Overview);
      await flushPromises();
      const etcd = wrapper.findAll("p").find((node) => node.text().includes("etcd"));
      expect(etcd?.text()).toContain("正常");
      expect(etcd?.find(".status-ok").exists()).toBe(true);
      expect(cellText(rowNamed(wrapper, "core"), 3)).toBe("1 个实例");
      expect(cellText(rowNamed(wrapper, "chatroom"), 3)).toBe("0 个实例");
      expect(cellText(rowNamed(wrapper, "gateway"), 3)).toBe("不适用");
      expect(wrapper.text()).not.toContain("故障");
    } finally {
      globalThis.fetch = original;
    }
  });

  it("shows 未知 and not 正常 before the overview request settles", () => {
    const original = globalThis.fetch;
    globalThis.fetch = () => new Promise(() => {});
    try {
      const wrapper = mount(Overview);
      expect(wrapper.text()).toContain("未知");
      expect(wrapper.text()).not.toContain("正常");
      const etcd = wrapper.findAll("p").find((node) => node.text().includes("etcd"));
      expect(etcd?.find(".status-unknown").text()).toBe("未知");
    } finally {
      globalThis.fetch = original;
    }
  });

  it("shows 故障 when the overview request fails", async () => {
    const original = globalThis.fetch;
    globalThis.fetch = async () => new Response("no", { status: 500 });
    try {
      const wrapper = mount(Overview);
      await flushPromises();
      expect(wrapper.findAll(".status-fault").some((node) => node.text() === "故障")).toBe(true);
      expect(wrapper.text()).not.toContain("0 个实例");
    } finally {
      globalThis.fetch = original;
    }
  });
});

describe("status routes", () => {
  it("renders Overview and Cluster while leaving the other sections and the guard in place", async () => {
    const routes = router.getRoutes();
    const componentFor = (path: string) => routes.find((route) => route.path === path)?.components?.default;
    expect(componentFor("/")).toBe(Overview);
    expect(componentFor("/cluster")).toBe(Cluster);
    expect(componentFor("/config")).toBe(Section);
    expect(componentFor("/logs")).toBe(Section);
    expect(componentFor("/release")).toBe(Section);

    const original = globalThis.fetch;
    const calls: { url: string; credentials?: RequestCredentials }[] = [];
    globalThis.fetch = async (input, init) => {
      calls.push({ url: String(input), credentials: init?.credentials });
      return new Response(null, { status: 401 });
    };
    try {
      await router.push("/config");
      expect(router.currentRoute.value.path).toBe("/login");
      expect(calls).toEqual([{ url: "/api/me", credentials: "include" }]);
    } finally {
      globalThis.fetch = original;
    }
  });
});

function rowNamed(wrapper: ReturnType<typeof mount>, name: string) {
  const row = wrapper.findAll("tbody tr").find((item) => item.findAll("td")[0]?.text() === name);
  expect(row).toBeTruthy();
  return row!;
}

function cellText(row: ReturnType<typeof rowNamed>, index: number) {
  return row.findAll("td")[index].text();
}
