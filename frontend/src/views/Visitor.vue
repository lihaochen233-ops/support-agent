<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref } from "vue";
import {
  api,
  dateTime,
  errorText,
  post,
  refreshBootstrap,
  session,
  statusLabels,
  toast,
} from "../api";
import type { Actor, Conversation } from "../types";
import ChatPane from "../components/ChatPane.vue";
import Icon from "../components/Icon.vue";
import Logo from "../components/Logo.vue";
const conversation = ref<Conversation | null>(null),
  history = ref<Conversation[]>([]),
  loading = ref(true),
  error = ref(""),
  busy = ref(false),
  showHistory = ref(false);
let poll: ReturnType<typeof setInterval> | undefined,
  refreshing = false;
const closed = computed(() => conversation.value?.status === "closed");
async function refresh() {
  if (refreshing) return;
  refreshing = true;
  try {
    await refreshBootstrap();
    history.value = (
      await api<{ items: Conversation[] }>(
        "/conversations?scope=all",
        {},
        "visitor",
      )
    ).items;
    if (conversation.value) {
      const selectedID = conversation.value.id;
      const detail = await api<Conversation>(
        `/conversations/${selectedID}`,
        {},
        "visitor",
      );
      if (conversation.value?.id === selectedID) conversation.value = detail;
    }
    error.value = "";
  } catch (e) {
    error.value = errorText(e);
  } finally {
    refreshing = false;
  }
}
async function start() {
  busy.value = true;
  try {
    conversation.value = await post<Conversation>(
      "/conversations",
      {},
      "visitor",
    );
    showHistory.value = false;
    await refresh();
  } catch (e) {
    error.value = errorText(e);
  } finally {
    busy.value = false;
  }
}
async function select(item: Conversation) {
  conversation.value = item;
  showHistory.value = false;
  await refresh();
}
async function action(kind: "handoff" | "close") {
  if (!conversation.value) return;
  busy.value = true;
  try {
    conversation.value = await post<Conversation>(
      `/conversations/${conversation.value.id}/${kind}`,
      {},
      "visitor",
    );
    toast(
      kind === "handoff"
        ? "已进入人工接待队列，您可以继续留言"
        : "本次咨询已结束",
    );
    await refresh();
  } catch (e) {
    toast(errorText(e), "error");
  } finally {
    busy.value = false;
  }
}
async function feedback(value: "solved" | "unsolved") {
  if (!conversation.value) return;
  try {
    await post(
      `/conversations/${conversation.value.id}/feedback`,
      { value },
      "visitor",
    );
    conversation.value.feedback = value;
    toast("感谢您的反馈");
  } catch (e) {
    toast(errorText(e), "error");
  }
}
async function initialize() {
  loading.value = true;
  error.value = "";
  try {
    session.visitor = await post<Actor>("/visitor", {}, "visitor");
    await refresh();
    const active = history.value.find((c) => c.status !== "closed");
    if (active) conversation.value = active;
    else await start();
  } catch (e) {
    error.value = errorText(e);
  } finally {
    loading.value = false;
  }
}
onMounted(() => {
  void initialize();
  poll = setInterval(() => void refresh(), 5000);
});
onBeforeUnmount(() => clearInterval(poll));
</script>
<template>
  <div class="visitor-page">
    <header class="visitor-header">
      <Logo />
      <div class="visitor-header-links">
        <span class="visitor-online"
          ><span class="status-dot" />{{
            session.bootstrap?.staff_online
              ? `${session.bootstrap.staff_online} 位客服在线`
              : "随时为您留下回应"
          }}</span
        ><button class="btn btn-quiet" @click="showHistory = !showHistory">
          <Icon name="clock" :size="17" /><span>咨询记录</span></button
        ><RouterLink
          to="/"
          class="icon-button"
          title="返回首页"
          aria-label="返回首页"
          ><Icon name="close"
        /></RouterLink>
      </div>
    </header>
    <div class="visitor-layout">
      <aside class="visitor-intro">
        <div class="eyebrow">LUMA CARE</div>
        <h1>我们在这里，<br />听您说。</h1>
        <p>一个问题，一次认真回应。<br />文字、截图，都可以成为开始。</p>
        <div class="intro-illustration">
          <div class="intro-circle"></div>
          <Icon name="chat" :size="63" /><span
            ><Icon name="spark" :size="24"
          /></span>
        </div>
        <div class="service-promise">
          <Icon name="book" />
          <div>
            <strong>答案，来自知识库</strong>
            <p>点击回答下方的来源，查看依据。</p>
          </div>
        </div>
        <div class="service-promise">
          <Icon name="headset" />
          <div>
            <strong>需要时，人工接力</strong>
            <p>带着上下文，继续为您解决问题。</p>
          </div>
        </div>
        <div class="privacy-note">
          <Icon
            name="shield"
            :size="15"
          />咨询记录保存在当前浏览器身份下。清除浏览器数据后将无法找回。
        </div>
      </aside>
      <main class="visitor-chat-card">
        <div class="visitor-chat-heading">
          <div class="assistant-avatar"><Icon name="spark" :size="23" /></div>
          <div>
            <h2>
              {{
                conversation?.status === "human"
                  ? conversation.agent_name || "专属客服"
                  : "Luma 服务助手"
              }}
            </h2>
            <p>
              {{
                conversation
                  ? statusLabels[conversation.status]
                  : "正在准备您的咨询"
              }}
            </p>
          </div>
          <div class="chat-heading-actions">
            <button
              v-if="conversation?.status === 'ai'"
              class="btn btn-outline btn-small"
              :disabled="busy"
              @click="action('handoff')"
            >
              <Icon name="headset" :size="15" />转人工</button
            ><button
              v-if="conversation && !closed"
              class="icon-button end-consultation"
              title="结束本次咨询"
              aria-label="结束本次咨询"
              :disabled="busy"
              @click="action('close')"
            >
              <Icon name="check" :size="19" />
            </button>
          </div>
        </div>
        <div v-if="loading" class="empty-state fill">
          <span class="spinner" />
          <h3>正在建立连接</h3>
          <p>马上开始您的咨询。</p>
        </div>
        <div v-else-if="!conversation" class="empty-state fill">
          <Icon name="chat" :size="36" />
          <h3>暂时无法建立会话</h3>
          <p>{{ error }}</p>
          <button class="btn btn-primary" @click="initialize">重新连接</button>
        </div>
        <template v-else
          ><div v-if="error" class="connection-warning">
            {{ error }} · 正在自动重试
          </div>
          <ChatPane
            :conversation="conversation"
            surface="visitor"
            @changed="refresh" />
          <div v-if="closed" class="feedback-bar">
            <div>
              <strong>{{
                conversation.feedback
                  ? "感谢您的反馈"
                  : "本次咨询解决了您的问题吗？"
              }}</strong>
              <div v-if="!conversation.feedback" class="feedback-actions">
                <button @click="feedback('solved')">
                  <Icon name="thumb" :size="15" />已解决</button
                ><button @click="feedback('unsolved')">仍需帮助</button>
              </div>
              <p v-else>
                {{
                  conversation.feedback === "solved"
                    ? "很高兴能帮到您。"
                    : "我们已记录，期待下次为您提供更好的服务。"
                }}
              </p>
            </div>
            <button
              class="btn btn-primary btn-small"
              :disabled="busy"
              @click="start"
            >
              再次咨询<Icon name="plus" :size="15" />
            </button></div
        ></template>
      </main>
    </div>
    <Teleport to="body"
      ><div
        v-if="showHistory"
        class="modal-backdrop"
        @click.self="showHistory = false"
      >
        <section
          class="modal history-modal"
          role="dialog"
          aria-modal="true"
          aria-label="咨询记录"
        >
          <div class="modal-header">
            <h3><Icon name="clock" />我的咨询记录</h3>
            <button
              class="icon-button"
              aria-label="关闭"
              @click="showHistory = false"
            >
              <Icon name="close" />
            </button>
          </div>
          <p class="muted">当前浏览器中的咨询记录</p>
          <div v-if="!history.length" class="empty-state">
            <p>还没有咨询记录</p>
          </div>
          <button
            v-for="item in history"
            :key="item.id"
            class="history-row"
            @click="select(item)"
          >
            <span class="history-icon"><Icon name="chat" /></span
            ><span
              ><strong>{{ item.title || "咨询 " + item.id.slice(0, 8) }}</strong
              ><small
                >{{ dateTime(item.created_at) }} ·
                {{ item.last_message || "暂无消息" }}</small
              ></span
            ><span class="status-pill" :class="item.status">{{
              statusLabels[item.status]
            }}</span
            ><Icon name="chevron" :size="17" />
          </button>
        </section></div
    ></Teleport>
  </div>
</template>
