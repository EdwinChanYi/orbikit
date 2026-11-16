import { readFileSync } from "node:fs";
import { join } from "node:path";
import { flushPromises, mount } from "@vue/test-utils";
import { describe, expect, it } from "vitest";
import Cluster from "./views/Cluster.vue";

describe("Cluster", () => {
  it("shows 故障 and does not show 正常 when status is fault", async () => {
    const calls: { url: string; credentials?: RequestCredentials }[] = [];
    const original = globalThis.fetch;
    globalThis.fetch = async (input, init) => {
      calls.push({ url: String(input), credentials: init?.credentials });
      return new Response(
        JSON.stringify({
          status: "fault",
          error: "dial tcp: connection refused",
          instances: null,
        }),
        { status: 200, headers: { "Content-Type": "application/json" } },
      );
    };
    try {
      const wrapper = mount(Cluster);
      await flushPromises();
      expect(calls).toEqual([{ url: "/api/cluster", credentials: "include" }]);
      expect(wrapper.text()).toContain("故障");
      expect(wrapper.text()).not.toContain("正常");
      expect(wrapper.find(".status-fault").text()).toContain("故障");
      expect(wrapper.text()).toContain("dial tcp: connection refused");
    } finally {
      globalThis.fetch = original;
    }
  });

  it("shows the leader and instance address when status is ok", async () => {
    const original = globalThis.fetch;
    globalThis.fetch = async (_input, init) => {
      expect(init?.credentials).toBe("include");
      return new Response(
        JSON.stringify({
          status: "ok",
          leader: "etcd",
          db_size: 4096,
          alarms: [],
          instances: [{ service: "core", id: "1", addr: "core:9001" }],
        }),
        { status: 200, headers: { "Content-Type": "application/json" } },
      );
    };
    try {
      const wrapper = mount(Cluster);
      await flushPromises();
      expect(wrapper.text()).toContain("etcd");
      expect(wrapper.text()).toContain("core:9001");
      expect(wrapper.text()).toContain("正常");
      expect(wrapper.find(".status-ok").text()).toContain("正常");
      expect(wrapper.text()).toContain("主节点");
      expect(wrapper.text()).toContain("库大小");
      expect(wrapper.text()).toContain("4096");
      const source = readFileSync(join(process.cwd(), "src/views/Cluster.vue"), "utf8");
      expect(source).toContain(".status-ok { color: #00b42a; }");
      expect(source).toContain(".status-degraded { color: #ff7d00; }");
      expect(source).toContain(".status-fault { color: #f53f3f; }");
      expect(source).toContain(".status-unknown { color: #86909c; }");
    } finally {
      globalThis.fetch = original;
    }
  });

  it("shows 降级 with the leader and address when status is degraded", async () => {
    const original = globalThis.fetch;
    globalThis.fetch = async () =>
      new Response(
        JSON.stringify({
          status: "degraded",
          leader: "etcd",
          db_size: 12,
          alarms: ["NOSPACE"],
          instances: [{ service: "core", id: "1", addr: "core:9001" }],
        }),
        { status: 200, headers: { "Content-Type": "application/json" } },
      );
    try {
      const wrapper = mount(Cluster);
      await flushPromises();
      expect(wrapper.find(".status-degraded").text()).toBe("降级");
      expect(wrapper.text()).toContain("etcd");
      expect(wrapper.text()).toContain("core:9001");
      expect(wrapper.text()).toContain("NOSPACE");
      expect(wrapper.text()).not.toContain("正常");
    } finally {
      globalThis.fetch = original;
    }
  });

  it("shows an unknown status value instead of leaving the cell blank", async () => {
    const original = globalThis.fetch;
    globalThis.fetch = async () =>
      new Response(
        JSON.stringify({
          status: "mystery",
          leader: "etcd",
          db_size: 1,
          alarms: [],
          instances: [],
        }),
        { status: 200, headers: { "Content-Type": "application/json" } },
      );
    try {
      const wrapper = mount(Cluster);
      await flushPromises();
      expect(wrapper.text()).toContain("mystery");
    } finally {
      globalThis.fetch = original;
    }
  });

  it("shows 故障 when the cluster request fails", async () => {
    const original = globalThis.fetch;
    globalThis.fetch = async () => {
      throw new Error("network down");
    };
    try {
      const wrapper = mount(Cluster);
      await flushPromises();
      expect(wrapper.text()).toContain("故障");
      expect(wrapper.text()).not.toContain("正常");
      expect(wrapper.find(".status-fault").exists()).toBe(true);
    } finally {
      globalThis.fetch = original;
    }
  });
});
