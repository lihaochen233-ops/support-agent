export type Surface = "visitor" | "staff";
export type Status = "ai" | "waiting" | "human" | "closed";
export interface Actor {
  id: string;
  name: string;
  role: "visitor" | "agent" | "admin";
  email?: string;
  enabled?: boolean;
  available?: boolean;
  online?: boolean;
  created_at?: string;
}
export interface Bootstrap {
  ai_enabled: boolean;
  instance_id: string;
  staff_online: number;
}
export interface Conversation {
  id: string;
  visitor_id: string;
  visitor_name?: string;
  agent_id?: string;
  agent_name?: string;
  status: Status;
  title?: string;
  last_seq: number;
  unread_count?: number;
  summary?: string;
  handoff_reason?: string;
  created_at: string;
  updated_at: string;
  feedback?: string;
  ai_phase?: string;
  last_message?: string;
  waiting_at?: string;
}
export interface Citation {
  id: string;
  document_id?: string;
  document_name?: string;
  title?: string;
  name?: string;
  source?: string;
  page?: number;
  section?: string;
  content?: string;
  text?: string;
  score?: number;
}
export interface Message {
  id: string;
  conversation_id: string;
  client_id?: string;
  seq: number;
  sender_id?: string;
  sender_role: string;
  sender_name?: string;
  body: string;
  image_id?: string;
  image_url?: string;
  citations?: Citation[];
  created_at: string;
}
export interface PendingMessage {
  client_id: string;
  body: string;
  image_id?: string;
  image_url?: string;
  state: "sending" | "failed";
  error?: string;
  created_at: string;
  conversation_id: string;
}
export interface Document {
  id: string;
  name: string;
  filename?: string;
  enabled: boolean;
  status: string;
  error?: string;
  active_version_id?: string;
  pending_version_id?: string;
  chunk_count?: number;
  created_at: string;
  updated_at?: string;
  version?: number;
  processing?: boolean;
}
export interface AICall {
  id: string;
  conversation_id?: string;
  kind: string;
  model: string;
  status: string;
  latency_ms: number;
  input_tokens: number;
  output_tokens: number;
  error?: string;
  created_at: string;
}
export interface Stats {
  conversations: number;
  waiting: number;
  human: number;
  closed: number;
  handoffs: number;
  ai_solved: number;
  solved: number;
  unsolved: number;
  avg_wait_seconds: number;
  avg_first_response_seconds: number;
  model_calls: number;
  total_tokens: number;
}
