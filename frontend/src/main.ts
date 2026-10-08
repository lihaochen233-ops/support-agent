import { createApp } from "vue";
import { createRouter, createWebHistory } from "vue-router";
import App from "./App.vue";
import Home from "./views/Home.vue";
import Login from "./views/Login.vue";
import Visitor from "./views/Visitor.vue";
import Desk from "./views/Desk.vue";
import Admin from "./views/Admin.vue";
import { loadActor, session } from "./api";
import "./style.css";
const router = createRouter({
  history: createWebHistory(),
  routes: [
    { path: "/", component: Home },
    { path: "/login", component: Login },
    { path: "/chat", component: Visitor },
    { path: "/desk", component: Desk, meta: { staff: true } },
    { path: "/admin", component: Admin, meta: { staff: true, admin: true } },
    { path: "/:pathMatch(.*)*", redirect: "/" },
  ],
});
router.beforeEach(async (to) => {
  if (!to.meta.staff) return;
  try {
    await loadActor("staff");
    if (to.meta.admin && session.staff?.role !== "admin") return "/desk";
  } catch {
    return { path: "/login", query: { next: to.path } };
  }
});
createApp(App).use(router).mount("#app");
