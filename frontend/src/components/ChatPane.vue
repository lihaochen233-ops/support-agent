<script setup lang="ts">
import {
  computed,
  nextTick,
  onBeforeUnmount,
  onMounted,
  ref,
  watch,
} from "vue";
import {
  api,
  attachmentURL,
  clockTime,
  errorText,
  post,
  session,
  statusLabels,
  toast,
} from "../api";
import { useRealtime } from "../realtime";
import type {
  Citation,
  Conversation,
  Message,
  PendingMessage,
  Surface,
} from "../types";
import Icon from "./Icon.vue";
const props = defineProps<{ conversation: Conversation; surface: Surface }>();
const emit = defineEmits<{ changed: []; action: [action: string] }>();
const messages = ref<Message[]>([]),
  pending = ref<PendingMessage[]>([]),
  draft = ref(""),
  loading = ref(false),
  older = ref(false),
  hasMore = ref(false),
  loadError = ref(""),
  uploading = ref(false),
  imageID = ref(""),
  fileInput = ref<HTMLInputElement>(),
  scrollbox = ref<HTMLElement>(),
  openSource = ref<Citation | null>(null);
const quickOpen = ref(false),
  preview = ref("");
let poll: ReturnType<typeof setInterval> | undefined,
  fetching = false,
  epoch = 0,
  syncCursor = 0;
const timers = new Map<string, ReturnType<typeof setTimeout>>();
const actor = computed(() => session[props.surface]),
  canSend = computed(() =>
    props.surface === "visitor"
      ? props.conversation.status !== "closed"
      : props.conversation.status === "human" &&
        props.conversation.agent_id === actor.value?.id,
  );
const isMine = (m: Message) =>
  m.sender_id === actor.value?.id ||
  (props.surface === "visitor" && m.sender_role === "visitor");
const quickReplies = [
  "您好，我是您的专属客服，请问有什么可以帮您？",
  "我正在为您核实，请稍等片刻。",
  "感谢您的耐心等待，问题已经处理完成。",
  "还有其他需要帮助的地方吗？",
];
const socket = useRealtime(props.surface, (event) => {
  if (event.type === "ack" && event.message) {
    ack(event.message);
    emit("changed");
  }
  if (event.type === "error" && event.client_id)
    fail(event.client_id, event.error || "发送失败");
  if (event.type === "changed" || event.type === "reconnected") {
    if (
      !event.conversation_id ||
      event.conversation_id === props.conversation.id
    )
      void sync();
    emit("changed");
  }
});
function persist() {
  try {
    sessionStorage.setItem(
      `luma-pending-${props.surface}-${props.conversation.id}`,
      JSON.stringify(pending.value),
    );
  } catch {
    /* Browser storage may be disabled. */
  }
}
function merge(incoming: Message[]) {
  const map = new Map(messages.value.map((m) => [m.seq, m]));
  incoming.forEach((m) => map.set(m.seq, m));
  messages.value = [...map.values()].sort((a, b) => a.seq - b.seq);
  const ids = new Set(incoming.map((m) => m.client_id).filter(Boolean));
  pending.value = pending.value.filter((p) => !ids.has(p.client_id));
  persist();
}
function nearBottom() {
  const el = scrollbox.value;
  return !el || el.scrollHeight - el.scrollTop - el.clientHeight < 140;
}
async function bottom() {
  await nextTick();
  if (scrollbox.value) scrollbox.value.scrollTop = scrollbox.value.scrollHeight;
}
async function markRead() {
  if (
    document.visibilityState !== "visible" ||
    !messages.value.length ||
    !nearBottom()
  )
    return;
  await post(
    `/conversations/${props.conversation.id}/read`,
    { seq: syncCursor },
    props.surface,
  ).catch(() => {});
}
async function sync(initial = false) {
  if (fetching && !initial) return;
  const version = epoch,
    id = props.conversation.id;
  fetching = true;
  if (initial) loading.value = true;
  const snap = nearBottom();
  try {
    // ACK 可以先于其他消息抵达；补拉游标只由连续的查询结果推进。
    let after = initial ? undefined : syncCursor;
    for (let page = 0; page < 20; page++) {
      const result = await api<{ items: Message[]; has_more: boolean }>(
        `/conversations/${id}/messages?limit=100${after !== undefined ? `&after_seq=${after}` : ""}`,
        {},
        props.surface,
      );
      if (version !== epoch) return;
      merge(result.items);
      if (result.items.length) syncCursor = result.items.at(-1)!.seq;
      if (initial) {
        hasMore.value = result.has_more;
        break;
      }
      if (!result.has_more || !result.items.length) break;
      after = result.items.at(-1)!.seq;
    }
    loadError.value = "";
    if (initial || snap) await bottom();
    await markRead();
  } catch (e) {
    if (version === epoch) loadError.value = errorText(e);
  } finally {
    if (version === epoch) {
      fetching = false;
      loading.value = false;
    }
  }
}
async function loadOlder() {
  if (!messages.value.length || older.value) return;
  older.value = true;
  const version = epoch,
    el = scrollbox.value,
    oldHeight = el?.scrollHeight || 0;
  try {
    const result = await api<{ items: Message[]; has_more: boolean }>(
      `/conversations/${props.conversation.id}/messages?before_seq=${messages.value[0]!.seq}&limit=50`,
      {},
      props.surface,
    );
    if (version !== epoch) return;
    merge(result.items);
    hasMore.value = result.has_more;
    await nextTick();
    if (el) el.scrollTop = el.scrollHeight - oldHeight;
  } catch (e) {
    toast(errorText(e), "error");
  } finally {
    older.value = false;
  }
}
function ack(message: Message) {
  clearTimeout(timers.get(message.client_id || ""));
  timers.delete(message.client_id || "");
  if (message.conversation_id !== props.conversation.id) return;
  merge([message]);
  void bottom();
}
function fail(id: string, error: string) {
  clearTimeout(timers.get(id));
  timers.delete(id);
  const p = pending.value.find((p) => p.client_id === id);
  if (p) {
    p.state = "failed";
    p.error = error;
    persist();
  }
}
async function sendPending(p: PendingMessage, forceREST = false) {
  p.state = "sending";
  p.error = "";
  persist();
  if (
    !forceREST &&
    socket.send({
      type: "send",
      conversation_id: p.conversation_id,
      client_id: p.client_id,
      body: p.body,
      image_id: p.image_id || undefined,
    })
  ) {
    timers.set(
      p.client_id,
      setTimeout(() => {
        if (pending.value.some((item) => item.client_id === p.client_id))
          void sendPending(p, true);
      }, 5000),
    );
    return;
  }
  try {
    const m = await post<Message>(
      `/conversations/${p.conversation_id}/messages`,
      {
        client_id: p.client_id,
        body: p.body,
        image_id: p.image_id || undefined,
      },
      props.surface,
    );
    ack(m);
    emit("changed");
  } catch (e) {
    fail(p.client_id, errorText(e));
  }
}
function send() {
  if (
    !canSend.value ||
    uploading.value ||
    (!draft.value.trim() && !imageID.value)
  )
    return;
  const p: PendingMessage = {
    client_id: crypto.randomUUID(),
    body: draft.value.trim(),
    image_id: imageID.value || undefined,
    state: "sending",
    created_at: new Date().toISOString(),
    conversation_id: props.conversation.id,
  };
  pending.value.push(p);
  draft.value = "";
  imageID.value = "";
  void sendPending(p);
  void bottom();
}
async function upload(event: Event) {
  const input = event.target as HTMLInputElement,
    file = input.files?.[0];
  if (!file) return;
  input.value = "";
  if (file.size > 5 * 1024 * 1024) {
    toast("图片大小不能超过 5 MB", "error");
    return;
  }
  if (!["image/jpeg", "image/png", "image/webp"].includes(file.type)) {
    toast("请选择 JPEG、PNG 或 WebP 图片", "error");
    return;
  }
  const id = props.conversation.id;
  uploading.value = true;
  try {
    const form = new FormData();
    form.append("file", file);
    form.append("conversation_id", id);
    const data = await api<{ id: string }>(
      "/attachments",
      { method: "POST", body: form },
      props.surface,
    );
    if (id === props.conversation.id) imageID.value = data.id;
  } catch (e) {
    toast(errorText(e), "error");
  } finally {
    uploading.value = false;
  }
}
function keydown(event: KeyboardEvent) {
  if (event.key === "Enter" && !event.shiftKey && !event.isComposing) {
    event.preventDefault();
    send();
  }
}
function sourceTitle(c: Citation) {
  return c.title || c.document_name || c.name || c.source || "知识库来源";
}
function visibility() {
  if (document.visibilityState === "visible") {
    void sync();
    emit("changed");
  }
}
watch(
  () => props.conversation.id,
  () => {
    epoch++;
    syncCursor = 0;
    fetching = false;
    messages.value = [];
    draft.value = "";
    imageID.value = "";
    hasMore.value = false;
    loadError.value = "";
    try {
      pending.value = JSON.parse(
        sessionStorage.getItem(
          `luma-pending-${props.surface}-${props.conversation.id}`,
        ) || "[]",
      ).map((p: PendingMessage) => ({
        ...p,
        state: "failed",
        error: "页面已恢复，请重试确认发送",
      }));
    } catch {
      pending.value = [];
    }
    void sync(true);
  },
  { immediate: true },
);
onMounted(() => {
  socket.connect();
  poll = setInterval(() => void sync(), 5000);
  document.addEventListener("visibilitychange", visibility);
});
onBeforeUnmount(() => {
  clearInterval(poll);
  timers.forEach(clearTimeout);
  document.removeEventListener("visibilitychange", visibility);
});
</script>
<template>
  <section class="chat-pane">
    <div class="chat-state-strip" :class="conversation.status">
      <span class="status-dot" /><span>{{
        statusLabels[conversation.status]
      }}</span
      ><span class="state-detail">{{
        conversation.status === "waiting"
          ? session.bootstrap?.staff_online
            ? "客服将尽快接待，您可以继续留言"
            : "暂无人工在线，可继续留言"
          : conversation.status === "human"
            ? (conversation.agent_name || "专属客服") + " 正在为您服务"
            : conversation.status === "closed"
              ? "本次咨询已归档，感谢您的信任"
              : session.bootstrap?.ai_enabled
                ? "根据知识库为您提供有依据的解答"
                : "AI 暂不可用，可随时转人工"
      }}</span
      ><span v-if="!socket.connected.value" class="offline-tag"
        >正在恢复实时连接</span
      >
    </div>
    <div ref="scrollbox" class="messages-area" @scroll.passive="markRead">
      <button
        v-if="hasMore"
        class="load-older"
        @click="loadOlder"
        :disabled="older"
      >
        {{ older ? "正在加载…" : "查看更早的消息" }}
      </button>
      <div v-if="loading" class="loading-state">
        <span class="spinner" />正在加载对话
      </div>
      <div v-else-if="loadError" class="inline-error">
        {{ loadError }} <button @click="sync(true)">重试</button>
      </div>
      <div v-else-if="!messages.length && !pending.length" class="chat-welcome">
        <div class="welcome-symbol"><Icon name="spark" :size="28" /></div>
        <h2>您好，有什么可以帮您？</h2>
        <p>
          描述您遇到的问题，或发送一张截图。<br />{{
            session.bootstrap?.ai_enabled
              ? "我会检索知识库，帮您找到答案。"
              : "您可以先留言，或转接人工客服。"
          }}
        </p>
        <span class="subtle-chip"
          ><Icon name="shield" :size="13" />每一个问题，都值得被认真回应</span
        >
      </div>
      <template v-for="(message, index) in messages" :key="message.id">
        <div
          v-if="
            index === 0 ||
            new Date(message.created_at).getTime() -
              new Date(messages[index - 1]!.created_at).getTime() >
              300000
          "
          class="time-divider"
        >
          {{ clockTime(message.created_at) }}
        </div>
        <div v-if="message.sender_role === 'system'" class="system-message">
          {{ message.body }}
        </div>
        <div v-else class="message-row" :class="{ mine: isMine(message) }">
          <div class="message-avatar" :class="message.sender_role">
            <Icon
              :name="
                message.sender_role === 'ai'
                  ? 'spark'
                  : message.sender_role === 'visitor'
                    ? 'user'
                    : 'headset'
              "
              :size="17"
            />
          </div>
          <div class="message-content">
            <div class="message-byline">
              {{
                isMine(message)
                  ? "我"
                  : message.sender_name ||
                    (message.sender_role === "ai"
                      ? "Luma 智能助手"
                      : message.sender_role === "visitor"
                        ? "访客"
                        : "客服")
              }}<span v-if="message.sender_role === 'ai'" class="ai-tag"
                >AI</span
              >
            </div>
            <div class="message-bubble">
              <p v-if="message.body">{{ message.body }}</p>
              <button
                v-if="message.image_id"
                class="message-image"
                @click="preview = attachmentURL(message.image_id, surface)"
              >
                <img
                  :src="attachmentURL(message.image_id, surface)"
                  alt="聊天图片"
                  loading="lazy"
                  @load="nearBottom() && bottom()"
                />
              </button>
            </div>
            <div v-if="message.citations?.length" class="citations">
              <button
                v-for="(citation, i) in message.citations"
                :key="citation.id"
                @click="openSource = citation"
              >
                <Icon name="book" :size="12" />{{ i + 1 }}.
                {{ sourceTitle(citation)
                }}<span v-if="citation.page"> · 第 {{ citation.page }} 页</span>
              </button>
            </div>
            <span v-if="isMine(message)" class="delivery-state">已保存</span>
          </div>
        </div>
      </template>
      <div v-for="p in pending" :key="p.client_id" class="message-row mine">
        <div class="message-avatar"><Icon name="user" :size="17" /></div>
        <div class="message-content">
          <div class="message-byline">我</div>
          <div class="message-bubble pending">
            <p v-if="p.body">{{ p.body }}</p>
            <img
              v-if="p.image_id"
              class="pending-image"
              :src="attachmentURL(p.image_id, surface)"
              alt="待发送图片"
            />
          </div>
          <div class="delivery-state" :class="{ failed: p.state === 'failed' }">
            <span v-if="p.state === 'sending'">发送中…</span
            ><button v-else @click="sendPending(p)" :title="p.error">
              {{ p.error || "发送失败" }} · 点击重试
            </button>
          </div>
        </div>
      </div>
      <div
        v-if="
          conversation.status === 'ai' &&
          conversation.ai_phase &&
          conversation.ai_phase !== 'idle'
        "
        class="thinking"
      >
        <Icon name="spark" :size="16" /><span>{{
          conversation.ai_phase === "retrieving"
            ? "正在检索知识库…"
            : conversation.ai_phase === "generating"
              ? "正在组织回答…"
              : conversation.ai_phase === "queued"
                ? "正在准备回答…"
                : conversation.ai_phase
        }}</span
        ><i></i><i></i><i></i>
      </div>
    </div>
    <div v-if="canSend" class="composer-wrap">
      <div v-if="quickOpen" class="quick-replies">
        <button
          v-for="text in quickReplies"
          :key="text"
          @click="
            draft = text;
            quickOpen = false;
          "
        >
          {{ text }}
        </button>
      </div>
      <div v-if="imageID" class="attachment-draft">
        <img :src="attachmentURL(imageID, surface)" alt="已添加的图片" /><span
          >图片已添加</span
        ><button aria-label="移除图片" @click="imageID = ''">
          <Icon name="close" :size="16" />
        </button>
      </div>
      <div class="composer">
        <textarea
          v-model="draft"
          placeholder="输入消息，让我们一起解决问题…"
          aria-label="消息内容"
          rows="2"
          maxlength="4000"
          @keydown="keydown"
        />
        <div class="composer-bottom">
          <div class="composer-tools">
            <button
              class="icon-button"
              aria-label="上传图片"
              title="发送图片（最大 5 MB）"
              :disabled="uploading"
              @click="fileInput?.click()"
            >
              <Icon :name="uploading ? 'clock' : 'image'" /></button
            ><button
              v-if="surface === 'staff'"
              class="icon-button"
              aria-label="快捷回复"
              title="快捷回复"
              @click="quickOpen = !quickOpen"
            >
              <Icon name="spark" /></button
            ><span class="composer-hint">{{
              uploading ? "正在上传图片…" : "Enter 发送 · Shift + Enter 换行"
            }}</span>
          </div>
          <button
            class="btn btn-primary send-button"
            :disabled="(!draft.trim() && !imageID) || uploading"
            @click="send"
          >
            发送<Icon name="send" :size="16" />
          </button>
        </div>
      </div>
      <input
        ref="fileInput"
        type="file"
        accept="image/png,image/jpeg,image/webp"
        hidden
        @change="upload"
      />
      <p class="composer-note">
        {{
          surface === "visitor"
            ? "AI 回答仅供参考，您可以随时转接人工客服。"
            : "回复会实时发送给访客，请核对后发送。"
        }}
      </p>
    </div>
    <div v-else class="readonly-banner">
      <Icon
        :name="conversation.status === 'closed' ? 'check' : 'lock'"
        :size="17"
      />{{
        conversation.status === "closed"
          ? "本次咨询已结束，历史记录已保留"
          : "接单后即可回复访客"
      }}
    </div>
    <Teleport to="body"
      ><div
        v-if="openSource"
        class="modal-backdrop"
        @click.self="openSource = null"
      >
        <section
          class="modal source-modal"
          role="dialog"
          aria-modal="true"
          aria-label="知识来源"
        >
          <div class="modal-header">
            <h3><Icon name="book" />{{ sourceTitle(openSource) }}</h3>
            <button
              class="icon-button"
              aria-label="关闭"
              @click="openSource = null"
            >
              <Icon name="close" />
            </button>
          </div>
          <p class="muted">
            {{
              openSource.page
                ? "第 " + openSource.page + " 页"
                : openSource.section || "文档片段"
            }}
          </p>
          <p class="source-content">
            {{
              openSource.content || openSource.text || "该引用未提供片段正文。"
            }}
          </p>
        </section>
      </div>
      <div v-if="preview" class="image-lightbox" @click="preview = ''">
        <button aria-label="关闭图片"><Icon name="close" :size="28" /></button
        ><img :src="preview" alt="聊天图片大图" /></div
    ></Teleport>
  </section>
</template>
