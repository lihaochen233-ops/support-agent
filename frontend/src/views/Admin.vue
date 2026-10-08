<script setup lang="ts">
import {
  computed,
  onBeforeUnmount,
  onMounted,
  reactive,
  ref,
  watch,
} from "vue";
import {
  api,
  dateTime,
  errorText,
  post,
  session,
  statusLabels,
  toast,
} from "../api";
import type {
  Actor,
  AICall,
  Citation,
  Conversation,
  Document as KnowledgeDocument,
  Stats,
} from "../types";
import StaffShell from "../components/StaffShell.vue";
import Icon from "../components/Icon.vue";
type Tab = "overview" | "knowledge" | "staff" | "conversations" | "calls";
const tab = ref<Tab>("overview"),
  loading = ref(true),
  error = ref(""),
  busy = ref(false),
  stats = ref<Stats | null>(null),
  documents = ref<KnowledgeDocument[]>([]),
  staff = ref<Actor[]>([]),
  conversations = ref<Conversation[]>([]),
  calls = ref<AICall[]>([]);
const query = ref(""),
  status = ref(""),
  searchQuery = ref(""),
  results = ref<Citation[]>([]),
  searching = ref(false),
  searched = ref(false),
  searchError = ref(""),
  fileInput = ref<HTMLInputElement>(),
  replaceID = ref(""),
  uploading = ref(false),
  preview = ref<{ document: KnowledgeDocument; chunks: Citation[] } | null>(
    null,
  ),
  previewLoading = ref(false),
  deleteDoc = ref<KnowledgeDocument | null>(null),
  assignConversation = ref<Conversation | null>(null),
  assignID = ref(""),
  staffModal = ref(false),
  editingID = ref(""),
  staffError = ref("");
const form = reactive({ name: "", email: "", password: "" });
const navigation: { id: Tab; label: string; icon: string }[] = [
  { id: "overview", label: "服务概览", icon: "grid" },
  { id: "knowledge", label: "知识库", icon: "book" },
  { id: "staff", label: "客服团队", icon: "users" },
  { id: "conversations", label: "全部会话", icon: "chat" },
  { id: "calls", label: "AI 调用记录", icon: "activity" },
];
const heading = computed(() => navigation.find((n) => n.id === tab.value)!);
const availableStaff = computed(() => staff.value.filter((a) => a.enabled));
const docStatus = (d: KnowledgeDocument) =>
  d.status === "processing"
    ? "处理中"
    : d.status === "failed"
      ? "处理失败"
      : !d.enabled
        ? "已停用"
        : d.status === "ready"
          ? "可用"
          : d.status;
const callKindLabel = (kind: string) =>
  (
    ({
      chat: "对话生成",
      embedding: "向量生成",
      vision: "图片理解",
      summary: "交接摘要",
    }) as Record<string, string>
  )[kind] || kind;
const duration = (s: number) =>
  s < 60
    ? `${Math.round(s)} 秒`
    : `${Math.floor(s / 60)} 分 ${Math.round(s % 60)} 秒`;
const outcomeMax = computed(() => Math.max(1, stats.value?.conversations || 1));
let poll: ReturnType<typeof setInterval> | undefined,
  debounce: ReturnType<typeof setTimeout> | undefined,
  generation = 0;
async function refresh(silent = false) {
  const currentTab = tab.value,
    currentGeneration = ++generation;
  if (!silent) loading.value = true;
  try {
    if (currentTab === "overview") stats.value = await api<Stats>("/stats");
    if (currentTab === "knowledge")
      documents.value = (
        await api<{ items: KnowledgeDocument[] }>("/documents")
      ).items;
    if (currentTab === "staff")
      staff.value = (await api<{ items: Actor[] }>("/staff")).items;
    if (currentTab === "conversations")
      conversations.value = (
        await api<{ items: Conversation[] }>(
          `/conversations?scope=all&q=${encodeURIComponent(query.value)}&status=${encodeURIComponent(status.value)}`,
        )
      ).items;
    if (currentTab === "calls")
      calls.value = (
        await api<{ items: AICall[] }>("/ai-calls?limit=100")
      ).items;
    if (currentGeneration === generation) error.value = "";
  } catch (e) {
    if (currentGeneration === generation) error.value = errorText(e);
  } finally {
    if (currentGeneration === generation) loading.value = false;
  }
}
function chooseFile(doc?: KnowledgeDocument) {
  replaceID.value = doc?.id || "";
  fileInput.value?.click();
}
async function upload(event: Event) {
  const input = event.target as HTMLInputElement,
    file = input.files?.[0];
  input.value = "";
  if (!file) return;
  if (file.size > 10 * 1024 * 1024) {
    toast("文档不能超过 10 MB", "error");
    return;
  }
  if (!/\.(txt|md|pdf)$/i.test(file.name)) {
    toast("请选择 TXT、Markdown 或 PDF 文档", "error");
    return;
  }
  uploading.value = true;
  try {
    const data = new FormData();
    data.append("file", file);
    if (replaceID.value) data.append("replace_id", replaceID.value);
    await api("/documents", { method: "POST", body: data });
    toast(
      replaceID.value
        ? "新版本已上传，处理完成后自动切换"
        : "文档已上传，正在处理",
    );
    await refresh(true);
  } catch (e) {
    toast(errorText(e), "error");
  } finally {
    uploading.value = false;
    replaceID.value = "";
  }
}
async function documentAction(
  doc: KnowledgeDocument,
  action: "toggle" | "retry" | "delete",
) {
  busy.value = true;
  try {
    if (action === "delete")
      await api(`/documents/${doc.id}`, { method: "DELETE" });
    else
      await post(
        `/documents/${doc.id}/${action}`,
        action === "toggle" ? { enabled: !doc.enabled } : {},
      );
    deleteDoc.value = null;
    toast(
      action === "delete"
        ? "文档已删除"
        : action === "retry"
          ? "已重新开始处理"
          : doc.enabled
            ? "已停用，不再参与检索"
            : "文档已启用",
    );
    await refresh(true);
  } catch (e) {
    toast(errorText(e), "error");
  } finally {
    busy.value = false;
  }
}
async function showDocument(doc: KnowledgeDocument) {
  previewLoading.value = true;
  try {
    preview.value = await api(`/documents/${doc.id}`);
  } catch (e) {
    toast(errorText(e), "error");
  } finally {
    previewLoading.value = false;
  }
}
async function searchKnowledge() {
  if (!searchQuery.value.trim()) return;
  searching.value = true;
  searchError.value = "";
  searched.value = false;
  try {
    results.value = (
      await post<{ items: Citation[] }>("/knowledge/search", {
        query: searchQuery.value.trim(),
      })
    ).items;
    searched.value = true;
  } catch (e) {
    searchError.value = errorText(e);
  } finally {
    searching.value = false;
  }
}
function openStaff(actor?: Actor) {
  editingID.value = actor?.id || "";
  form.name = actor?.name || "";
  form.email = actor?.email || "";
  form.password = "";
  staffError.value = "";
  staffModal.value = true;
}
async function saveStaff() {
  busy.value = true;
  staffError.value = "";
  try {
    if (editingID.value)
      await api(`/staff/${editingID.value}`, {
        method: "PATCH",
        body: JSON.stringify({
          name: form.name,
          ...(form.password ? { password: form.password } : {}),
        }),
      });
    else await post("/staff", { ...form, role: "agent" });
    staffModal.value = false;
    toast(editingID.value ? "账号已更新" : "客服账号已创建");
    await refresh(true);
  } catch (e) {
    staffError.value = errorText(e);
  } finally {
    busy.value = false;
  }
}
async function toggleStaff(actor: Actor) {
  busy.value = true;
  try {
    await api(`/staff/${actor.id}`, {
      method: "PATCH",
      body: JSON.stringify({ enabled: !actor.enabled }),
    });
    toast(actor.enabled ? "账号已停用" : "账号已启用");
    await refresh(true);
  } catch (e) {
    toast(errorText(e), "error");
  } finally {
    busy.value = false;
  }
}
async function openAssign(c: Conversation) {
  busy.value = true;
  try {
    staff.value = (await api<{ items: Actor[] }>("/staff")).items;
    assignID.value = c.agent_id || "";
    assignConversation.value = c;
  } catch (e) {
    toast(errorText(e), "error");
  } finally {
    busy.value = false;
  }
}
async function assign() {
  if (!assignConversation.value || !assignID.value) return;
  busy.value = true;
  try {
    await post(`/conversations/${assignConversation.value.id}/assign`, {
      agent_id: assignID.value,
    });
    assignConversation.value = null;
    toast("会话已分配");
    await refresh(true);
  } catch (e) {
    toast(errorText(e), "error");
  } finally {
    busy.value = false;
  }
}
watch(tab, () => void refresh());
watch([query, status], () => {
  clearTimeout(debounce);
  debounce = setTimeout(() => void refresh(), 300);
});
onMounted(() => {
  void refresh();
  poll = setInterval(() => void refresh(true), 10000);
});
onBeforeUnmount(() => {
  clearInterval(poll);
  clearTimeout(debounce);
});
</script>
<template>
  <StaffShell
    ><header class="workspace-header">
      <div>
        <div class="eyebrow">LUMA ADMINISTRATION</div>
        <h1>
          管理中心<span class="header-divider">/</span
          ><span class="header-subtitle">让服务持续变好</span>
        </h1>
      </div>
      <div class="admin-identity">
        <span class="mini-avatar">{{ session.staff?.name.slice(0, 1) }}</span>
        <div>
          <strong>{{ session.staff?.name }}</strong
          ><small>管理员</small>
        </div>
      </div>
    </header>
    <div class="admin-layout">
      <aside class="admin-sidebar">
        <span class="sidebar-caption">工作空间</span>
        <nav>
          <button
            v-for="n in navigation"
            :key="n.id"
            :class="{ active: tab === n.id }"
            @click="tab = n.id"
          >
            <Icon :name="n.icon" :size="19" />{{ n.label
            }}<Icon v-if="tab === n.id" name="chevron" :size="14" />
          </button>
        </nav>
        <div class="admin-sidebar-note">
          <div class="small-spark"><Icon name="spark" :size="20" /></div>
          <strong>更好的知识，更好的回答</strong>
          <p>持续更新资料，让每一次咨询都有据可循。</p>
        </div>
      </aside>
      <main class="admin-content">
        <div class="page-heading">
          <div>
            <span class="eyebrow">{{
              tab === "overview"
                ? "一目了然，心中有数"
                : tab === "knowledge"
                  ? "让每一个答案有据可循"
                  : tab === "staff"
                    ? "专业的人，一起做好服务"
                    : tab === "conversations"
                      ? "服务全程，清晰可追溯"
                      : "真实调用，可观测可追踪"
            }}</span>
            <h2>{{ heading.label }}</h2>
          </div>
          <div class="page-heading-actions">
            <button
              class="icon-button"
              aria-label="刷新"
              title="刷新"
              @click="refresh()"
            >
              <Icon name="refresh" :size="18" /></button
            ><button
              v-if="tab === 'knowledge'"
              class="btn btn-primary"
              :disabled="uploading"
              @click="chooseFile()"
            >
              <Icon name="plus" :size="17" />{{
                uploading ? "正在上传…" : "上传文档"
              }}</button
            ><button
              v-if="tab === 'staff'"
              class="btn btn-primary"
              @click="openStaff()"
            >
              <Icon name="plus" :size="17" />添加客服
            </button>
          </div>
        </div>
        <div v-if="error" class="inline-error page-error">
          {{ error }}<button @click="refresh()">重新加载</button>
        </div>
        <div v-if="loading" class="loading-state admin-loading">
          <span class="spinner" />正在加载
        </div>
        <template v-else-if="tab === 'overview' && stats"
          ><div class="stats-grid">
            <article class="metric-card">
              <span>累计咨询</span><Icon name="chat" /><strong>{{
                stats.conversations.toLocaleString()
              }}</strong
              ><small>所有已创建会话</small>
            </article>
            <article class="metric-card accent">
              <span>等待人工</span><Icon name="clock" /><strong>{{
                stats.waiting.toLocaleString()
              }}</strong
              ><small>当前等待接单的会话</small>
            </article>
            <article class="metric-card">
              <span>人工接待中</span><Icon name="headset" /><strong>{{
                stats.human.toLocaleString()
              }}</strong
              ><small>正在由客服跟进</small>
            </article>
            <article class="metric-card">
              <span>AI 已解决</span><Icon name="spark" /><strong>{{
                stats.ai_solved.toLocaleString()
              }}</strong
              ><small>访客明确反馈解决的 AI 会话</small>
            </article>
          </div>
          <div class="overview-grid">
            <section class="panel service-outcomes">
              <div class="panel-heading">
                <h3>服务结果</h3>
                <span class="muted">累计</span>
              </div>
              <div class="outcome-row">
                <span>已结束会话</span><strong>{{ stats.closed }}</strong>
                <div>
                  <i
                    :style="{ width: `${(stats.closed / outcomeMax) * 100}%` }"
                  />
                </div>
              </div>
              <div class="outcome-row">
                <span>发生转人工</span><strong>{{ stats.handoffs }}</strong>
                <div>
                  <i
                    class="teal-light"
                    :style="{
                      width: `${(stats.handoffs / outcomeMax) * 100}%`,
                    }"
                  />
                </div>
              </div>
              <div class="outcome-row">
                <span>访客反馈已解决</span><strong>{{ stats.solved }}</strong>
                <div>
                  <i
                    class="sage"
                    :style="{ width: `${(stats.solved / outcomeMax) * 100}%` }"
                  />
                </div>
              </div>
              <div class="outcome-row">
                <span>访客反馈未解决</span><strong>{{ stats.unsolved }}</strong>
                <div>
                  <i
                    class="sand"
                    :style="{
                      width: `${(stats.unsolved / outcomeMax) * 100}%`,
                    }"
                  />
                </div>
              </div>
              <p class="panel-footnote">
                解决情况以访客反馈为准。未转人工不等于已解决。
              </p>
            </section>
            <section class="panel response-panel">
              <div class="panel-heading">
                <h3>响应与用量</h3>
                <Icon name="activity" :size="19" />
              </div>
              <div class="response-metric">
                <span><Icon name="clock" :size="18" />平均等待时长</span
                ><strong>{{ duration(stats.avg_wait_seconds) }}</strong>
              </div>
              <div class="response-metric">
                <span><Icon name="headset" :size="18" />人工首次响应</span
                ><strong>{{
                  duration(stats.avg_first_response_seconds)
                }}</strong>
              </div>
              <div class="response-metric">
                <span><Icon name="spark" :size="18" />模型调用次数</span
                ><strong>{{ stats.model_calls.toLocaleString() }}</strong>
              </div>
              <div class="response-metric">
                <span><Icon name="log" :size="18" />模型返回的 Token 用量</span
                ><strong>{{ stats.total_tokens.toLocaleString() }}</strong>
              </div>
            </section>
          </div>
          <div class="insight-banner">
            <div class="insight-icon"><Icon name="book" :size="27" /></div>
            <div>
              <h3>让知识持续更新，让服务更加从容</h3>
              <p>
                上传业务说明、服务政策和常见问题，为 AI 提供准确的回答依据。
              </p>
            </div>
            <button class="btn btn-outline" @click="tab = 'knowledge'">
              管理知识库<Icon name="arrow" :size="16" />
            </button></div
        ></template>
        <template v-else-if="tab === 'knowledge'"
          ><div class="knowledge-info">
            <Icon name="book" :size="21" />
            <div>
              <strong>您的专属知识库</strong>
              <p>
                支持 TXT、Markdown 和文字型 PDF；每份最大 10 MB，PDF 最多 50
                页。文档处理完成后自动参与检索。
              </p>
            </div>
          </div>
          <section class="panel documents-panel">
            <div class="panel-heading">
              <h3>
                知识文档 <span class="count-badge">{{ documents.length }}</span>
              </h3>
              <span class="muted">上传 → 解析 → 分段 → 索引</span>
            </div>
            <div v-if="!documents.length" class="empty-state">
              <div class="empty-icon"><Icon name="file" :size="30" /></div>
              <h3>从第一份资料开始</h3>
              <p>上传服务政策或常见问题，让智能助手了解您的业务。</p>
              <button
                class="btn btn-outline"
                :disabled="uploading"
                @click="chooseFile()"
              >
                上传文档<Icon name="upload" :size="16" />
              </button>
            </div>
            <div v-else class="table-scroll">
              <table>
                <thead>
                  <tr>
                    <th>文档名称</th>
                    <th>状态</th>
                    <th>片段</th>
                    <th>更新时间</th>
                    <th>操作</th>
                  </tr>
                </thead>
                <tbody>
                  <tr v-for="d in documents" :key="d.id">
                    <td>
                      <button
                        class="document-name"
                        :disabled="previewLoading"
                        @click="showDocument(d)"
                      >
                        <span class="document-icon"
                          ><Icon name="file" :size="19" /></span
                        ><span
                          ><strong>{{ d.name }}</strong
                          ><small
                            v-if="
                              d.status === 'processing' && d.active_version_id
                            "
                            >新版本处理中，旧版继续服务</small
                          ><small
                            v-else-if="
                              d.status === 'failed' && d.active_version_id
                            "
                            >新版本失败，旧版仍可检索</small
                          ></span
                        >
                      </button>
                      <p v-if="d.error" class="document-error">{{ d.error }}</p>
                    </td>
                    <td>
                      <span
                        class="status-pill"
                        :class="
                          d.status === 'failed'
                            ? 'danger'
                            : !d.enabled
                              ? 'closed'
                              : d.status === 'processing'
                                ? 'waiting'
                                : 'ai'
                        "
                        >{{ docStatus(d) }}</span
                      >
                    </td>
                    <td>{{ d.chunk_count || 0 }}</td>
                    <td class="nowrap">
                      {{ dateTime(d.updated_at || d.created_at) }}
                    </td>
                    <td>
                      <div class="table-actions">
                        <button
                          :disabled="
                            busy || uploading || d.status === 'processing'
                          "
                          @click="chooseFile(d)"
                        >
                          替换</button
                        ><button
                          v-if="d.status === 'failed'"
                          :disabled="busy"
                          @click="documentAction(d, 'retry')"
                        >
                          重试</button
                        ><button
                          :disabled="busy"
                          @click="documentAction(d, 'toggle')"
                        >
                          {{ d.enabled ? "停用" : "启用" }}</button
                        ><button
                          class="danger-link"
                          :disabled="busy"
                          @click="deleteDoc = d"
                        >
                          删除
                        </button>
                      </div>
                    </td>
                  </tr>
                </tbody>
              </table>
            </div>
          </section>
          <section class="panel retrieval-panel">
            <div class="panel-heading">
              <div>
                <h3><Icon name="search" :size="18" />检索效果预览</h3>
                <p class="muted">
                  输入访客可能提出的问题，查看实际召回的知识片段。
                </p>
              </div>
            </div>
            <form class="retrieval-form" @submit.prevent="searchKnowledge">
              <div class="input-with-icon">
                <Icon name="search" :size="18" /><input
                  v-model="searchQuery"
                  placeholder="例如：收到的商品有破损，应该如何处理？"
                  maxlength="2000"
                  aria-label="检索问题"
                />
              </div>
              <button
                class="btn btn-primary"
                :disabled="searching || !searchQuery.trim()"
              >
                {{ searching ? "检索中…" : "检索知识" }}
              </button>
            </form>
            <div v-if="searchError" class="inline-error">{{ searchError }}</div>
            <p v-else-if="searched && !results.length" class="search-empty">
              没有找到达到匹配要求的片段。请检查资料内容或换一个问题。
            </p>
            <div v-if="results.length" class="search-results">
              <article
                v-for="(c, i) in results"
                :key="c.id"
                class="citation-card"
              >
                <header>
                  <span class="result-number">{{ i + 1 }}</span
                  ><strong>{{
                    c.title || c.document_name || "知识文档"
                  }}</strong
                  ><span v-if="c.page" class="muted">第 {{ c.page }} 页</span
                  ><span v-if="c.score !== undefined" class="score"
                    >相似度 {{ (c.score * 100).toFixed(1) }}%</span
                  >
                </header>
                <p>{{ c.text || c.content }}</p>
              </article>
            </div>
          </section>
          <input
            ref="fileInput"
            type="file"
            accept=".txt,.md,.pdf"
            hidden
            @change="upload"
        /></template>
        <template v-else-if="tab === 'staff'"
          ><section class="panel">
            <div class="panel-heading">
              <h3>
                客服团队 <span class="count-badge">{{ staff.length }}</span>
              </h3>
              <span class="muted">停用账号不会删除历史记录</span>
            </div>
            <div class="table-scroll">
              <table>
                <thead>
                  <tr>
                    <th>团队成员</th>
                    <th>角色</th>
                    <th>接待状态</th>
                    <th>账号状态</th>
                    <th>操作</th>
                  </tr>
                </thead>
                <tbody>
                  <tr v-for="a in staff" :key="a.id">
                    <td>
                      <div class="team-member">
                        <span class="member-avatar">{{
                          a.name.slice(0, 1)
                        }}</span>
                        <div>
                          <strong
                            >{{ a.name
                            }}<span
                              v-if="a.id === session.staff?.id"
                              class="self-label"
                              >你</span
                            ></strong
                          ><small>{{ a.email }}</small>
                        </div>
                      </div>
                    </td>
                    <td>{{ a.role === "admin" ? "管理员" : "客服" }}</td>
                    <td>
                      <span class="presence-label"
                        ><span
                          class="status-dot"
                          :class="{ offline: !a.online || !a.available }"
                        />{{
                          !a.online ? "离线" : a.available ? "接待中" : "暂离"
                        }}</span
                      >
                    </td>
                    <td>
                      <span
                        class="status-pill"
                        :class="a.enabled ? 'ai' : 'closed'"
                        >{{ a.enabled ? "正常" : "已停用" }}</span
                      >
                    </td>
                    <td>
                      <div class="table-actions">
                        <button v-if="a.role === 'agent'" @click="openStaff(a)">
                          编辑 / 重置密码</button
                        ><button
                          v-if="a.role === 'agent'"
                          :class="{ 'danger-link': a.enabled }"
                          :disabled="busy"
                          @click="toggleStaff(a)"
                        >
                          {{ a.enabled ? "停用" : "启用" }}
                        </button>
                      </div>
                    </td>
                  </tr>
                </tbody>
              </table>
            </div>
          </section></template
        >
        <template v-else-if="tab === 'conversations'"
          ><div class="table-filters">
            <div class="input-with-icon">
              <Icon name="search" :size="18" /><input
                v-model="query"
                placeholder="搜索访客或会话"
                aria-label="搜索全部会话"
              />
            </div>
            <select v-model="status" aria-label="会话状态">
              <option value="">全部状态</option>
              <option value="ai">AI 接待中</option>
              <option value="waiting">等待人工</option>
              <option value="human">人工接待中</option>
              <option value="closed">已结束</option>
            </select>
          </div>
          <section class="panel">
            <div v-if="!conversations.length" class="empty-state">
              <Icon name="chat" :size="32" />
              <h3>暂无符合条件的会话</h3>
              <p>新的咨询会实时出现在这里。</p>
            </div>
            <div v-else class="table-scroll">
              <table>
                <thead>
                  <tr>
                    <th>会话 / 访客</th>
                    <th>状态</th>
                    <th>当前客服</th>
                    <th>创建时间</th>
                    <th>操作</th>
                  </tr>
                </thead>
                <tbody>
                  <tr v-for="c in conversations" :key="c.id">
                    <td>
                      <strong>{{ c.visitor_name || "访客" }}</strong
                      ><small class="table-subline">{{
                        c.title || "#" + c.id.slice(0, 8)
                      }}</small>
                    </td>
                    <td>
                      <span class="status-pill" :class="c.status">{{
                        statusLabels[c.status]
                      }}</span>
                    </td>
                    <td>{{ c.agent_name || "—" }}</td>
                    <td class="nowrap">{{ dateTime(c.created_at) }}</td>
                    <td>
                      <div class="table-actions">
                        <RouterLink
                          :to="{ path: '/desk', query: { conversation: c.id } }"
                          >查看记录</RouterLink
                        ><button
                          v-if="c.status !== 'closed'"
                          :disabled="busy"
                          @click="openAssign(c)"
                        >
                          {{ c.agent_id ? "重新分配" : "分配客服" }}
                        </button>
                      </div>
                    </td>
                  </tr>
                </tbody>
              </table>
            </div>
          </section></template
        >
        <template v-else-if="tab === 'calls'"
          ><div class="knowledge-info">
            <Icon name="activity" :size="21" />
            <div>
              <strong>来自真实请求的调用记录</strong>
              <p>
                记录模型、结果、响应时间及接口返回的 Token 用量。展示最近 100
                条调用。
              </p>
            </div>
          </div>
          <section class="panel">
            <div v-if="!calls.length" class="empty-state">
              <Icon name="activity" :size="32" />
              <h3>暂时没有模型调用</h3>
              <p>上传知识文档或发起 AI 咨询后，会在这里生成记录。</p>
            </div>
            <div v-else class="table-scroll">
              <table>
                <thead>
                  <tr>
                    <th>模型 / 用途</th>
                    <th>状态</th>
                    <th>耗时</th>
                    <th>输入 / 输出 Token</th>
                    <th>调用时间</th>
                    <th>关联会话</th>
                  </tr>
                </thead>
                <tbody>
                  <tr v-for="c in calls" :key="c.id">
                    <td>
                      <strong>{{ c.model }}</strong
                      ><small class="table-subline">{{
                        callKindLabel(c.kind)
                      }}</small>
                      <p v-if="c.error" class="document-error">{{ c.error }}</p>
                    </td>
                    <td>
                      <span
                        class="status-pill"
                        :class="
                          ['success', 'ok'].includes(c.status) ? 'ai' : 'danger'
                        "
                        >{{
                          ["success", "ok"].includes(c.status)
                            ? "成功"
                            : c.status === "error" || c.status === "failed"
                              ? "失败"
                              : c.status
                        }}</span
                      >
                    </td>
                    <td>{{ c.latency_ms.toLocaleString() }} ms</td>
                    <td>
                      {{ c.input_tokens.toLocaleString() }} /
                      {{ c.output_tokens.toLocaleString() }}
                    </td>
                    <td class="nowrap">{{ dateTime(c.created_at) }}</td>
                    <td>
                      <RouterLink
                        v-if="c.conversation_id"
                        class="text-link"
                        :to="{
                          path: '/desk',
                          query: { conversation: c.conversation_id },
                        }"
                        >{{ c.conversation_id.slice(0, 8)
                        }}<Icon name="arrow" :size="13" /></RouterLink
                      ><span v-else class="muted">知识处理</span>
                    </td>
                  </tr>
                </tbody>
              </table>
            </div>
          </section></template
        >
        <footer class="admin-footer">
          <span>Luma · 服务管理</span><span>数据每 10 秒自动更新</span>
        </footer>
      </main>
    </div>
    <Teleport to="body"
      ><div v-if="preview" class="modal-backdrop" @click.self="preview = null">
        <section
          class="modal document-modal"
          role="dialog"
          aria-modal="true"
          aria-label="文档内容"
        >
          <div class="modal-header">
            <h3><Icon name="file" />{{ preview.document.name }}</h3>
            <button
              class="icon-button"
              aria-label="关闭"
              @click="preview = null"
            >
              <Icon name="close" />
            </button>
          </div>
          <p class="muted">
            {{ docStatus(preview.document) }} ·
            {{ preview.chunks.length }} 个知识片段
          </p>
          <div v-if="preview.document.error" class="inline-error">
            {{ preview.document.error }}
          </div>
          <p v-if="!preview.chunks.length" class="search-empty">
            暂无已解析的片段。
          </p>
          <article
            v-for="(c, i) in preview.chunks"
            :key="c.id"
            class="document-chunk"
          >
            <span
              >片段 {{ i + 1
              }}<template v-if="c.page"> · 第 {{ c.page }} 页</template
              ><template v-else-if="c.section">
                · {{ c.section }}</template
              ></span
            >
            <p>{{ c.text || c.content }}</p>
          </article>
        </section>
      </div>
      <div
        v-if="deleteDoc"
        class="modal-backdrop"
        @click.self="deleteDoc = null"
      >
        <section
          class="modal small-modal"
          role="dialog"
          aria-modal="true"
          aria-label="删除文档"
        >
          <div class="modal-header">
            <h3>删除知识文档</h3>
            <button
              class="icon-button"
              aria-label="关闭"
              @click="deleteDoc = null"
            >
              <Icon name="close" />
            </button>
          </div>
          <p>
            确认删除「{{ deleteDoc.name }}」？删除后将立即停止参与知识检索。
          </p>
          <div class="modal-actions">
            <button class="btn btn-outline" @click="deleteDoc = null">
              取消</button
            ><button
              class="btn btn-danger"
              :disabled="busy"
              @click="documentAction(deleteDoc, 'delete')"
            >
              {{ busy ? "正在删除…" : "确认删除" }}
            </button>
          </div>
        </section>
      </div>
      <div
        v-if="staffModal"
        class="modal-backdrop"
        @click.self="staffModal = false"
      >
        <form
          class="modal small-modal"
          role="dialog"
          aria-modal="true"
          aria-label="客服账号"
          @submit.prevent="saveStaff"
        >
          <div class="modal-header">
            <h3>{{ editingID ? "编辑客服账号" : "添加客服" }}</h3>
            <button
              type="button"
              class="icon-button"
              aria-label="关闭"
              @click="staffModal = false"
            >
              <Icon name="close" />
            </button>
          </div>
          <label class="field-label" for="staff-name">姓名</label
          ><input
            id="staff-name"
            v-model="form.name"
            required
            maxlength="40"
            placeholder="客服姓名"
          /><label class="field-label" for="staff-email">登录邮箱</label
          ><input
            id="staff-email"
            v-model="form.email"
            type="email"
            required
            :disabled="!!editingID"
            placeholder="name@company.com"
          /><label class="field-label" for="staff-password">{{
            editingID ? "重置密码（留空则保持不变）" : "登录密码"
          }}</label
          ><input
            id="staff-password"
            v-model="form.password"
            type="password"
            :required="!editingID"
            minlength="10"
            maxlength="72"
            autocomplete="new-password"
            placeholder="至少 10 位字符"
          />
          <div v-if="staffError" class="inline-error">{{ staffError }}</div>
          <div class="modal-actions">
            <button
              type="button"
              class="btn btn-outline"
              @click="staffModal = false"
            >
              取消</button
            ><button class="btn btn-primary" :disabled="busy">
              {{ busy ? "正在保存…" : "保存账号" }}
            </button>
          </div>
        </form>
      </div>
      <div
        v-if="assignConversation"
        class="modal-backdrop"
        @click.self="assignConversation = null"
      >
        <form
          class="modal small-modal"
          role="dialog"
          aria-modal="true"
          aria-label="分配客服"
          @submit.prevent="assign"
        >
          <div class="modal-header">
            <h3>分配客服</h3>
            <button
              type="button"
              class="icon-button"
              aria-label="关闭"
              @click="assignConversation = null"
            >
              <Icon name="close" />
            </button>
          </div>
          <p class="muted">
            会话 #{{ assignConversation.id.slice(0, 8) }} ·
            {{ assignConversation.visitor_name || "访客" }}
          </p>
          <label class="field-label" for="assign-staff">接待客服</label
          ><select id="assign-staff" v-model="assignID" required>
            <option value="" disabled>请选择客服</option>
            <option v-for="a in availableStaff" :key="a.id" :value="a.id">
              {{ a.name }} ·
              {{
                a.online && a.available ? "接待中" : a.online ? "暂离" : "离线"
              }}
            </option>
          </select>
          <p class="muted form-help">
            分配后该客服可查看完整记录并回复。已有消息会保留。
          </p>
          <div class="modal-actions">
            <button
              type="button"
              class="btn btn-outline"
              @click="assignConversation = null"
            >
              取消</button
            ><button class="btn btn-primary" :disabled="busy || !assignID">
              {{ busy ? "正在分配…" : "确认分配" }}
            </button>
          </div>
        </form>
      </div>
    </Teleport></StaffShell
  >
</template>
