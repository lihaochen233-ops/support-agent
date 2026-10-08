<script setup lang="ts">
import { useRouter } from "vue-router";
import { onMounted } from "vue";
import { useRealtime } from "../realtime";
import { errorText, post, session, toast } from "../api";
import Logo from "./Logo.vue";
import Icon from "./Icon.vue";
const router = useRouter();
const presenceSocket = useRealtime("staff", () => {});
onMounted(() => presenceSocket.connect());
async function logout() {
  try {
    await post("/logout");
    session.staff = null;
    await router.push("/login");
  } catch (e) {
    toast(errorText(e), "error");
  }
}
</script>
<template>
  <div class="staff-shell">
    <aside class="app-rail">
      <Logo compact />
      <nav>
        <RouterLink
          to="/desk"
          class="rail-link"
          title="接待工作台"
          aria-label="接待工作台"
          ><Icon name="chat" /><span>接待</span></RouterLink
        ><RouterLink
          v-if="session.staff?.role === 'admin'"
          to="/admin"
          class="rail-link"
          title="管理后台"
          aria-label="管理后台"
          ><Icon name="grid" /><span>管理</span></RouterLink
        ><RouterLink
          to="/chat"
          class="rail-link"
          title="访客体验"
          aria-label="访客体验"
          ><Icon name="headset" /><span>访客</span></RouterLink
        >
      </nav>
      <div class="rail-bottom">
        <span class="rail-avatar" :title="session.staff?.name">{{
          session.staff?.name?.slice(0, 1) || "L"
        }}</span
        ><button
          class="rail-link"
          @click="logout"
          title="退出登录"
          aria-label="退出登录"
        >
          <Icon name="logout" />
        </button>
      </div>
    </aside>
    <main class="staff-main"><slot /></main>
  </div>
</template>
