# 数据结构与接口详细约定

Project: `support-agent`, module `support-agent`, Go 1.25+, all backend in `internal/platform`. Frontend: Vue 3 + TS, root / visitor `/chat`, staff `/desk`, admin `/admin`, login `/login`.

## Backend shared surface
config.go and model.go define `Config` and JSON model types in model.go. server.go defines `Server` with exported `DB *pgxpool.Pool`, `Redis *redis.Client`, `Config Config` fields. `NewServer(db *pgxpool.Pool, redis *redis.Client, cfg Config) *Server`, `Handler() http.Handler`, `Start(ctx context.Context)`.
Handler calls `s.registerKnowledge(mux *http.ServeMux)` (implemented in knowledge.go / ai.go). Start calls `s.startAIWorkers(ctx)` (implemented in knowledge.go / ai.go), starts notification subscriber. The service exposes `s.actor(r *http.Request) (Actor,error)`, `s.require(r, roles ...string) (Actor,error)`, `s.conversation(ctx,id string) (Conversation,error)`, `s.canRead(ctx,actor,conv) bool`, `s.notify(ctx,conversationID string)`, `writeJSON(w,status,value)`, `writeError(w,status,message)`, `decodeJSON(w,r,value) error`. The helper `s.cancelAI(conversationID string)` cancels active runs on current instance; chat.go calls after handoff/close then broadcasts invalidation. Other instances cancel from events and workers also check DB state/version before committing. No package import cycles.

## Auth and API shape
JSON snake_case. IDs UUID strings. Timestamps RFC3339. Same-origin HttpOnly cookies, separate `assist_visitor` and `assist_staff`. `X-Surface: visitor` selects visitor cookie; staff is default. WS uses `?surface=visitor` or `staff`. Do not put tokens in query strings. Mutations enforce Origin (allow absent for CLI but require custom X-Requested-With: support-agent header on HTTP mutations); WS always validates exact Origin. Errors `{"error":"message"}`. Collections `{"items": [...]}`. Routes below all under `/api`.

- GET `/bootstrap`: `{ai_enabled, instance_id, staff_online}` public.
- POST `/visitor` `{name?}` => Actor, sets guest cookie, reuses valid guest. GET `/me` => Actor or 401.
- POST `/login` `{email,password}` => Actor + staff cookie. POST `/logout` => `{ok:true}`.
- POST `/presence` `{available:boolean}` => Actor; authenticated staff; heartbeat from websocket renews presence.
- GET `/conversations?scope=mine|waiting|history|all&status=&q=&before=` => `{items:Conversation[]}`. Visitors only own; agents only assigned plus waiting list; admin all. Queue and summaries must not leak full unassigned transcript to agents until claim.
- POST `/conversations` `{}` => Conversation (existing active guest conversation or new AI one).
- GET `/conversations/{id}` => Conversation.
- GET `/conversations/{id}/messages?after_seq=0&before_seq=&limit=50` => `{items:Message[],has_more:boolean}` ascending; after_seq catches up, before_seq pages older. No user-supplied sender identity accepted.
- POST `/conversations/{id}/messages` `{client_id,body,image_id?}` => Message after commit. body or image required, body <=4000 runes. WS sends same command, REST is fallback.
- POST `/conversations/{id}/handoff` `{reason?}` => Conversation (guest owner, idempotent).
- POST `/conversations/{id}/claim` `{}` => Conversation, agent/admin atomic claim.
- POST `/conversations/{id}/release` `{}` => Conversation, assigned staff/admin.
- POST `/conversations/{id}/assign` `{agent_id}` => Conversation admin only.
- POST `/conversations/{id}/close` `{}` => Conversation, guest owner/assigned/admin; all states can close.
- POST `/conversations/{id}/feedback` `{value:"solved"|"unsolved"}` => `{ok:true}`, visitor owner.
- POST `/conversations/{id}/read` `{seq}` => `{ok:true}` monotonic validated <=last_seq.
- POST `/attachments` multipart `file`, `conversation_id` => `{id,url,mime,name}`. GET `/attachments/{id}` enforces same conversation access and surface query for `<img>`.
- GET `/staff` => `{items:Actor[]}` admin. POST `/staff` `{name,email,password,role:"agent"}` => Actor. PATCH `/staff/{id}` `{name?,enabled?,password?}` => Actor.
- GET `/stats` admin => `{conversations,waiting,human,closed,handoffs,ai_solved,solved,unsolved,avg_wait_seconds,avg_first_response_seconds,model_calls,total_tokens}`.
- GET `/ai-calls?limit=50` admin => `{items:[{id,conversation_id,kind,model,status,latency_ms,input_tokens,output_tokens,error,created_at}]}`.
- GET `/documents` admin => `{items:Document[]}`. POST `/documents` multipart `file`, optional `replace_id` => Document. GET `/documents/{id}` => `{document:Document,chunks:Citation[]}`. POST `/documents/{id}/retry` `{}` => Document. POST `/documents/{id}/toggle` `{enabled:boolean}` => Document. DELETE `/documents/{id}` => `{ok:true}`. POST `/knowledge/search` `{query}` => `{items:Citation[]}` admin.
- GET `/health` => `{ok,db,redis,instance_id}` (503 only if DB down; Redis degraded reported).
- GET `/ws?surface=visitor|staff`: auth upgrade; server events `{type:"changed",conversation_id}` / `{type:"ack",client_id,message:Message}` / `{type:"error",client_id,error}` / `{type:"ready"}`. Client message `{type:"send",conversation_id,client_id,body,image_id?}`, or `{type:"ping"}`. Refetch authoritative state on changed, reconnect, and every 5s for active conversation/10s lists. Bounded send queues.

## Persistence and jobs
Database migrations are authoritative. Core appends message and increments conversations.last_seq under a row lock in one transaction; unique (conversation_id,sender_id,client_id) deduplicates BEFORE rejecting retries due to state changes. Every auth/state/image ownership check is inside transaction where relevant. Agent/admin sender must be currently assigned and status human. Waiting accepts visitor only; closed rejects new messages. guest explicit human-request phrases or button move ai to waiting with version++; terminate active ai jobs. AI job insert for new visitor messages in AI state: one pending job per conversation (`UNIQUE conversation_id WHERE status IN ('pending','running')`). Worker consumes messages after conversations.ai_handled_seq; new messages arriving while running trigger another pending job after finish. Store read cursors independently, unread counts ignore own messages. Lock visitor row when creating active conversation; partial unique index ensures one active.

AI uses tasks table fields provided in migration. Claim via SKIP LOCKED and lease_owner/lease_until; fencing token lease_generation increases each claim. Complete only matching owner/generation and conversation version/state, with reply+job completion atomic. Never hold DB tx during external model call. Retry <=1 on transient provider failure; max 3 tool calls; final answer in validated JSON {kind:"answer"|"clarify"|"handoff",body,citation_ids:[],reason?}; factual answers require citations from actual retrieved ready/enabled document revisions. Clarification count <=1. Cloud provider supports tool calls search_knowledge and request_handoff; no arbitrary tools. Vision receives authenticated image bytes as data URI, never public directory. Human handoff gets extractive summary immediately; optional model refinement must not block.

Documents: active_version_id; versions status processing/ready/failed; replacement old active stays until new successful; parent enabled=false/deleted immediately filters. Chunks vector(1024), page/section/source. Jobs persistent via document_versions lease fields. Embedding fingerprint stored with chunks/version; query mismatch fails clearly. PDF max50 pages, text extraction with ledongthuc/pdf; reject empty/scanned/encrypted. 10MB doc,5MB image/max20 megapixels/max3 per model request. Chunk ~700 runes overlap100, retrieval top5 cosine, threshold configurable .25 initial (evaluate, not a guarantee). Log real model usage, no fabricated latency/tokens.

## UI direction
Brand “Luma · 智能客服”, polished teal/slate/ivory, editorial landing + professional light inbox workspace. All visible UI Chinese. Real data only; deliberate empty/error/loading states; no fake chat/metrics. Visitor mobile, desktop workbench three columns. Inline sources expand; safe text rendering; no raw v-html. Staff navigation /desk, /admin with role gates. Client message IDs retained on retry. Clear AI unavailable/waiting/human/closed banners, typing phase, scroll paging, attachments, feedback, queue actions, admin documents/staff/stats/calls. Shared visitor/staff browser cookies separated above. Vite proxy /api to 127.0.0.1:8090; production Go serves frontend/dist SPA.
