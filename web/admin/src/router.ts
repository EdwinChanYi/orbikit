import { createRouter, createWebHistory } from "vue-router";
import Login from "./views/Login.vue";
import Cluster from "./views/Cluster.vue";
import Overview from "./views/Overview.vue";
import Config from "./views/Config.vue";
import Section from "./views/Section.vue";

const router = createRouter({
  history: createWebHistory(),
  routes: [
    { path: "/login", name: "login", component: Login },
    { path: "/", name: "总览", component: Overview },
    { path: "/config", name: "配置", component: Config },
    { path: "/cluster", name: "集群", component: Cluster },
    { path: "/logs", name: "日志", component: Section },
    { path: "/release", name: "发布", component: Section },
  ],
});

router.beforeEach(async (to) => {
  if (to.path === "/login") {
    return true;
  }
  const res = await fetch("/api/me", { credentials: "include" });
  if (res.status === 401) {
    return "/login";
  }
  return true;
});

export default router;
