import { reactive } from "vue";
import type { Actor, Bootstrap, Surface } from "./types";
export class APIError extends Error {
  constructor(
    message: string,
    public status: number,
  ) {
    super(message);
  }
}
export async function api<T>(
  path: string,
  options: RequestInit = {},
  surface: Surface = "staff",
): Promise<T> {
  const headers = new Headers(options.headers);
  headers.set("X-Surface", surface);
  if (options.method && options.method !== "GET")
    headers.set("X-Requested-With", "support-agent");
  if (options.body && !(options.body instanceof FormData))
    headers.set("Content-Type", "application/json");
  const response = await fetch("/api" + path, {
    ...options,
    headers,
    credentials: "same-origin",
  });
  const payload = await response
    .json()
    .catch(() => ({ error: "服务返回了无效响应" }));
  if (!response.ok)
    throw new APIError(
      payload.error || `请求失败（${response.status}）`,
      response.status,
    );
  return payload as T;
}
export const post = <T>(
  path: string,
  data: unknown = {},
  surface: Surface = "staff",
) => api<T>(path, { method: "POST", body: JSON.stringify(data) }, surface);
export const session = reactive<{
  staff: Actor | null;
  visitor: Actor | null;
  bootstrap: Bootstrap | null;
}>({ staff: null, visitor: null, bootstrap: null });
export const notice = reactive({
  text: "",
  kind: "success" as "success" | "error",
});
let noticeTimer: ReturnType<typeof setTimeout>;
export function toast(text: string, kind: "success" | "error" = "success") {
  notice.text = text;
  notice.kind = kind;
  clearTimeout(noticeTimer);
  noticeTimer = setTimeout(() => (notice.text = ""), 5000);
}
export function errorText(error: unknown) {
  return error instanceof Error ? error.message : "请求失败，请稍后重试";
}
export async function refreshBootstrap() {
  session.bootstrap = await api<Bootstrap>("/bootstrap");
  return session.bootstrap;
}
export async function loadActor(surface: Surface) {
  const actor = await api<Actor>("/me", {}, surface);
  session[surface] = actor;
  return actor;
}
export function attachmentURL(id: string, surface: Surface) {
  return `/api/attachments/${encodeURIComponent(id)}?surface=${surface}`;
}
export function dateTime(value: string) {
  if (!value) return "—";
  return new Intl.DateTimeFormat("zh-CN", {
    month: "2-digit",
    day: "2-digit",
    hour: "2-digit",
    minute: "2-digit",
    hour12: false,
  }).format(new Date(value));
}
export function clockTime(value: string) {
  return new Intl.DateTimeFormat("zh-CN", {
    hour: "2-digit",
    minute: "2-digit",
    hour12: false,
  }).format(new Date(value));
}
export const statusLabels: Record<string, string> = {
  ai: "AI 接待中",
  waiting: "等待人工",
  human: "人工接待中",
  closed: "已结束",
};
