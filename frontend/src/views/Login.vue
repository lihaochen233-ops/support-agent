<script setup lang="ts">
import { ref } from "vue";
import { useRoute, useRouter } from "vue-router";
import { errorText, post, session } from "../api";
import type { Actor } from "../types";
import Icon from "../components/Icon.vue";
import Logo from "../components/Logo.vue";
const email = ref(""),
  password = ref(""),
  busy = ref(false),
  error = ref(""),
  router = useRouter(),
  route = useRoute();
async function login() {
  busy.value = true;
  error.value = "";
  try {
    session.staff = await post<Actor>("/login", {
      email: email.value,
      password: password.value,
    });
    await router.push(route.query.next === "/admin" ? "/admin" : "/desk");
  } catch (e) {
    error.value = errorText(e);
  } finally {
    busy.value = false;
  }
}
</script>
<template>
  <div class="login-page">
    <aside class="login-story">
      <Logo light />
      <div>
        <span class="eyebrow">LUMA WORKSPACE</span>
        <h1>把复杂留给系统，<br />把关心留给你。</h1>
        <p>AI 与客服协同工作，让每次接待更从容。</p>
        <div class="login-visual">
          <div class="login-visual-ring"></div>
          <Icon name="headset" :size="72" /><span class="login-spark"
            ><Icon name="spark" :size="25"
          /></span>
        </div>
      </div>
      <small>一个工作台，连接每一次信任。</small>
    </aside>
    <main class="login-form-area">
      <RouterLink to="/" class="back-home"
        ><Icon name="back" :size="16" />返回首页</RouterLink
      >
      <form class="login-form" @submit.prevent="login">
        <div class="login-emblem"><Icon name="shield" :size="27" /></div>
        <span class="eyebrow">欢迎回来</span>
        <h2>登录工作台</h2>
        <p class="muted">使用您的客服或管理员账号继续。</p>
        <label class="field-label" for="email">邮箱地址</label>
        <div class="input-with-icon">
          <Icon name="mail" :size="18" /><input
            id="email"
            v-model="email"
            type="email"
            required
            autocomplete="username"
            placeholder="name@company.com"
          />
        </div>
        <label class="field-label" for="password">登录密码</label>
        <div class="input-with-icon">
          <Icon name="lock" :size="18" /><input
            id="password"
            v-model="password"
            type="password"
            required
            autocomplete="current-password"
            placeholder="请输入密码"
          />
        </div>
        <div v-if="error" class="inline-error">{{ error }}</div>
        <button class="btn btn-primary login-submit" :disabled="busy">
          {{ busy ? "正在登录…" : "进入工作台"
          }}<Icon name="arrow" :size="17" />
        </button>
        <p class="login-help">账号由管理员分配。如需重置密码，请联系管理员。</p>
      </form>
      <small class="login-bottom">Luma · 让每一次咨询得到回应</small>
    </main>
  </div>
</template>
