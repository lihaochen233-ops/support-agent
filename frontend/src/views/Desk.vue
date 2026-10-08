<script setup lang="ts">
import {
  computed,
  onBeforeUnmount,
  onMounted,
  ref,
  watch,
  nextTick,
} from "vue";
import { useRoute } from "vue-router";
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
import StaffShell from "../components/StaffShell.vue";
import ChatPane from "../components/ChatPane.vue";
import Icon from "../components/Icon.vue";
const route = useRoute(),
  scope = ref("mine"),
  query = ref(""),
  items = ref<Conversation[]>([]),
  selected = ref<Conversation | null>(null),
  loading = ref(true),
  error = ref(""),
  busy = ref(false),
  mobileChat = ref(false);
let poll: ReturnType<typeof setInterval> | undefined,
  searchTimer: ReturnType<typeof setTimeout> | undefined,
  refreshing = false;
const canRead = computed(
  () =>
    selected.value &&
    (session.staff?.role === "admin" ||
      selected.value.agent_id === session.staff?.id),
);
const mine = computed(() => selected.value?.agent_id === session.staff?.id);
const title = computed(() =>
  scope.value === "waiting"
    ? "等待接待"
    : scope.value === "history"
      ? "历史会话"
      : "我的接待",
);
const unread = computed(() =>
  items.value.reduce((n, c) => n + (c.unread_count || 0), 0),
);
async function refresh() {
  if (refreshing) return;
  refreshing = true;
  const requestedScope = scope.value;
  const requestedQuery = query.value;
  try {
    await refreshBootstrap();
    const result = (
      await api<{ items: Conversation[] }>(
        `/conversations?scope=${requestedScope}&q=${encodeURIComponent(requestedQuery)}`,
      )
    ).items;
    if (requestedScope !== scope.value || requestedQuery !== query.value)
      return;
    items.value = result;
    if (selected.value) {
      const fresh = items.value.find((c) => c.id === selected.value!.id);
      if (fresh) selected.value = fresh;
      else if (canRead.value) {
        const selectedID = selected.value.id;
        const detail = await api<Conversation>(`/conversations/${selectedID}`);
        if (selected.value?.id === selectedID) selected.value = detail;
      } else selected.value = null;
    }
    error.value = "";
  } catch (e) {
    error.value = errorText(e);
  } finally {
    loading.value = false;
    refreshing = false;
    if (requestedScope !== scope.value || requestedQuery !== query.value)
      void refresh();
  }
}
function select(c: Conversation) {
  selected.value = c;
  mobileChat.value = true;
}
async function presence() {
  busy.value = true;
  try {
    session.staff = await post<Actor>("/presence", {
      available: !session.staff?.available,
    });
    toast(
      session.staff.available
        ? "已开启接待状态"
        : "已设为暂离，已有会话仍由您接待",
    );
  } catch (e) {
    toast(errorText(e), "error");
  } finally {
    busy.value = false;
  }
}
async function action(kind: "claim" | "release" | "close") {
  if (!selected.value) return;
  busy.value = true;
  try {
    const updated = await post<Conversation>(
      `/conversations/${selected.value.id}/${kind}`,
    );
    if (kind === "claim") {
      scope.value = "mine";
      await nextTick();
    }
    selected.value = updated;
    toast(
      kind === "claim"
        ? "接单成功，可以开始回复"
        : kind === "release"
          ? "已退回公共等待队列"
          : "会话已结束",
    );
    await refresh();
  } catch (e) {
    toast(errorText(e), "error");
    await refresh();
  } finally {
    busy.value = false;
  }
}
watch(scope, () => {
  selected.value = null;
  loading.value = true;
  void refresh();
});
watch(query, () => {
  clearTimeout(searchTimer);
  searchTimer = setTimeout(() => void refresh(), 300);
});
onMounted(async () => {
  await refresh();
  if (typeof route.query.conversation === "string") {
    try {
      selected.value = await api<Conversation>(
        `/conversations/${encodeURIComponent(route.query.conversation)}`,
      );
      mobileChat.value = true;
    } catch (e) {
      toast(errorText(e), "error");
    }
  }
  poll = setInterval(() => void refresh(), 5000);
});
onBeforeUnmount(() => {
  clearInterval(poll);
  clearTimeout(searchTimer);
});
</script>
<template>
  <StaffShell
    ><header class="workspace-header">
      <div>
        <div class="eyebrow">SERVICE WORKSPACE</div>
        <h1>
          接待工作台<span class="header-divider">/</span
          ><span class="header-subtitle">让每次回应都刚刚好</span>
        </h1>
      </div>
      <div class="workspace-header-actions">
        <span class="current-date">{{
          new Intl.DateTimeFormat("zh-CN", {
            month: "long",
            day: "numeric",
            weekday: "long",
          }).format(new Date())
        }}</span
        ><button
          class="availability-toggle"
          :class="{ available: session.staff?.available }"
          :disabled="busy"
          @click="presence"
        >
          <span class="status-dot" />{{
            session.staff?.available ? "接待中" : "暂离"
          }}<span class="toggle-track"><i /></span>
        </button>
      </div>
    </header>
    <div class="desk-body" :class="{ 'show-mobile-chat': mobileChat }">
      <aside class="inbox-sidebar">
        <div class="inbox-heading">
          <h2>会话收件箱</h2>
          <span class="count-badge">{{ items.length }}</span>
        </div>
        <div class="inbox-tabs">
          <button :class="{ active: scope === 'mine' }" @click="scope = 'mine'">
            我的接待<span
              v-if="unread && scope === 'mine'"
              class="tiny-count"
              >{{ unread }}</span
            ></button
          ><button
            :class="{ active: scope === 'waiting' }"
            @click="scope = 'waiting'"
          >
            待接单</button
          ><button
            :class="{ active: scope === 'history' }"
            @click="scope = 'history'"
          >
            历史
          </button>
        </div>
        <div class="inbox-search">
          <Icon name="search" :size="17" /><input
            v-model="query"
            :placeholder="
              scope === 'history' ? '搜索历史会话' : '搜索访客或会话'
            "
            aria-label="搜索会话"
          />
        </div>
        <div class="list-section-label">
          {{ title
          }}<button
            class="icon-button"
            title="刷新会话"
            aria-label="刷新会话"
            @click="refresh"
          >
            <Icon name="refresh" :size="14" />
          </button>
        </div>
        <div v-if="error" class="inline-error">
          {{ error }}<button @click="refresh">重试</button>
        </div>
        <div class="conversation-list">
          <div v-if="loading" class="empty-state"><span class="spinner" /></div>
          <div v-else-if="!items.length" class="list-empty">
            <Icon
              :name="scope === 'waiting' ? 'check' : 'chat'"
              :size="32"
            /><strong>{{
              scope === "waiting" ? "暂时没有等待的访客" : "暂无会话"
            }}</strong>
            <p>
              {{
                scope === "mine"
                  ? "前往「待接单」开始接待。"
                  : "新的咨询会出现在这里。"
              }}
            </p>
          </div>
          <button
            v-for="c in items"
            :key="c.id"
            class="conversation-card"
            :class="{ selected: selected?.id === c.id }"
            @click="select(c)"
          >
            <span class="list-avatar" :class="c.status">{{
              (c.visitor_name || "访客").slice(0, 1)
            }}</span
            ><span class="conversation-card-main"
              ><span class="conversation-card-top"
                ><strong>{{
                  c.visitor_name || "访客 " + c.id.slice(0, 5)
                }}</strong
                ><time>{{
                  dateTime(c.updated_at).split(" ").at(-1)
                }}</time></span
              ><span class="conversation-preview">{{
                c.title || c.last_message || c.handoff_reason || "等待开始对话"
              }}</span
              ><span class="conversation-card-bottom"
                ><span class="status-pill" :class="c.status">{{
                  statusLabels[c.status]
                }}</span
                ><span v-if="c.unread_count" class="unread-badge">{{
                  c.unread_count > 99 ? "99+" : c.unread_count
                }}</span></span
              ></span
            >
          </button>
        </div>
        <div class="inbox-footnote">
          <span class="status-dot" />每 5 秒同步会话状态
        </div>
      </aside>
      <main class="desk-conversation">
        <template v-if="selected"
          ><div class="desk-chat-heading">
            <button
              class="icon-button mobile-back"
              aria-label="返回会话列表"
              @click="mobileChat = false"
            >
              <Icon name="back" />
            </button>
            <div class="list-avatar large">
              {{ (selected.visitor_name || "访客").slice(0, 1) }}
            </div>
            <div>
              <h2>{{ selected.visitor_name || "访客" }}</h2>
              <p>
                会话 #{{ selected.id.slice(0, 8)
                }}<span class="dot-separator">·</span
                >{{ dateTime(selected.created_at) }}
              </p>
            </div>
            <div class="chat-heading-actions">
              <button
                v-if="selected.status === 'waiting'"
                class="btn btn-primary btn-small"
                :disabled="busy"
                @click="action('claim')"
              >
                接待此访客<Icon name="arrow" :size="15" /></button
              ><template
                v-if="
                  selected.status === 'human' &&
                  (mine || session.staff?.role === 'admin')
                "
                ><button
                  class="btn btn-quiet btn-small"
                  :disabled="busy"
                  @click="action('release')"
                >
                  退回队列</button
                ><button
                  class="btn btn-outline btn-small"
                  :disabled="busy"
                  @click="action('close')"
                >
                  <Icon name="check" :size="15" />结束接待
                </button></template
              >
            </div>
          </div>
          <ChatPane
            v-if="canRead"
            :conversation="selected"
            surface="staff"
            @changed="refresh" />
          <div v-else class="claim-preview">
            <div class="claim-symbol"><Icon name="headset" :size="34" /></div>
            <h2>一位访客，等待您的回应</h2>
            <p>接单后可查看完整聊天记录并回复访客。</p>
            <div class="handoff-preview">
              <span class="eyebrow">转接信息</span
              ><strong>{{
                selected.handoff_reason || "访客请求人工服务"
              }}</strong>
              <p v-if="selected.summary">{{ selected.summary }}</p>
            </div>
            <button
              class="btn btn-primary"
              :disabled="busy"
              @click="action('claim')"
            >
              开始接待<Icon name="arrow" :size="17" />
            </button></div
        ></template>
        <div v-else class="desk-empty">
          <div class="desk-empty-art">
            <Icon name="chat" :size="48" /><span
              ><Icon name="spark" :size="22"
            /></span>
          </div>
          <span class="eyebrow">READY WHEN YOU ARE</span>
          <h2>好的服务，从一次回应开始</h2>
          <p>
            从左侧选择会话，或领取一位等待中的访客。<br />AI
            已整理好上下文，您可以从这里自然接续。
          </p>
          <button
            v-if="scope !== 'waiting'"
            class="btn btn-outline"
            @click="scope = 'waiting'"
          >
            查看等待队列<Icon name="arrow" :size="16" />
          </button>
        </div>
      </main>
      <aside class="context-panel">
        <div class="context-heading">
          <Icon name="user" :size="17" />
          <h3>接待上下文</h3>
        </div>
        <template v-if="selected"
          ><div class="visitor-profile">
            <div class="profile-avatar">
              {{ (selected.visitor_name || "访客").slice(0, 1) }}
            </div>
            <h3>{{ selected.visitor_name || "访客" }}</h3>
            <span class="status-pill" :class="selected.status">{{
              statusLabels[selected.status]
            }}</span>
          </div>
          <section class="context-section">
            <h4>会话信息</h4>
            <dl>
              <dt>创建时间</dt>
              <dd>{{ dateTime(selected.created_at) }}</dd>
              <dt>当前客服</dt>
              <dd>{{ selected.agent_name || "尚未接入" }}</dd>
              <dt>访客反馈</dt>
              <dd>
                {{
                  selected.feedback === "solved"
                    ? "已解决"
                    : selected.feedback === "unsolved"
                      ? "未解决"
                      : "暂无反馈"
                }}
              </dd>
            </dl>
          </section>
          <section class="context-section">
            <h4><Icon name="spark" :size="15" />交接摘要</h4>
            <div class="summary-note">
              <p>{{ selected.summary || "暂无交接摘要。" }}</p>
            </div>
            <span class="context-caption">结合完整聊天记录核实访客需求。</span>
          </section>
          <section v-if="selected.handoff_reason" class="context-section">
            <h4>转接原因</h4>
            <p class="context-text">{{ selected.handoff_reason }}</p>
          </section></template
        >
        <div v-else class="context-empty">
          <Icon name="log" :size="28" />
          <p>选择会话后，<br />在这里了解访客需求。</p>
        </div>
        <div class="care-note">
          <Icon name="shield" :size="19" />
          <p>回应之前，多理解一点。<br />专业之外，多关心一点。</p>
        </div>
      </aside>
    </div></StaffShell
  >
</template>
