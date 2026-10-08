import { onBeforeUnmount, ref } from "vue";
import type { Message, Surface } from "./types";
export interface ServerEvent {
  type: string;
  conversation_id?: string;
  client_id?: string;
  message?: Message;
  error?: string;
}
export function useRealtime(
  surface: Surface,
  onEvent: (event: ServerEvent) => void,
) {
  const connected = ref(false);
  let socket: WebSocket | null = null,
    stopped = false,
    attempt = 0;
  let reconnectTimer: ReturnType<typeof setTimeout> | undefined,
    heartbeat: ReturnType<typeof setInterval> | undefined;
  function connect() {
    if (
      stopped ||
      socket?.readyState === WebSocket.OPEN ||
      socket?.readyState === WebSocket.CONNECTING
    )
      return;
    socket = new WebSocket(
      `${location.protocol === "https:" ? "wss:" : "ws:"}//${location.host}/api/ws?surface=${surface}`,
    );
    socket.onopen = () => {
      connected.value = true;
      attempt = 0;
      heartbeat = setInterval(() => {
        if (socket?.readyState === WebSocket.OPEN)
          socket.send(JSON.stringify({ type: "ping" }));
      }, 20000);
      onEvent({ type: "reconnected" });
    };
    socket.onmessage = (e) => {
      try {
        onEvent(JSON.parse(e.data));
      } catch {
        /* Ignore malformed notifications; reconciliation restores state. */
      }
    };
    socket.onclose = () => {
      connected.value = false;
      clearInterval(heartbeat);
      if (!stopped)
        reconnectTimer = setTimeout(
          connect,
          Math.min(15000, 1000 * 2 ** attempt++) + Math.random() * 500,
        );
    };
    socket.onerror = () => socket?.close();
  }
  function send(data: unknown) {
    if (socket?.readyState !== WebSocket.OPEN) return false;
    socket.send(JSON.stringify(data));
    return true;
  }
  function stop() {
    stopped = true;
    clearTimeout(reconnectTimer);
    clearInterval(heartbeat);
    socket?.close();
    socket = null;
  }
  onBeforeUnmount(stop);
  return { connected, connect, send, stop };
}
