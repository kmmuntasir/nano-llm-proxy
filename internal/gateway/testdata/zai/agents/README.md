# Real coding-agent captures (Claude Code + pi) against the GLM Coding Plan

Recorded 2026-09-25 through a local recording reverse proxy
(`zai-recorder`, run one-shot, then discarded): Claude Code 2.1.281 headless
(`-p`) against the Anthropic-compatible base, and pi 0.87.1 headless (`-p`)
against the OpenAI-compatible base. Each did one full tool-using turn:
read a file, run `ls`, answer. Keys are redacted. The proxy must mimic these
shapes — Z.ai validates that coding-plan traffic looks like coding agents.

## Claude Code → `https://api.z.ai/api/anthropic`

With `CLAUDE_CODE_ENABLE_GATEWAY_MODEL_DISCOVERY=1` Claude Code probes the
base before the first turn (see `claude-code.discovery.txt`):

- `GET /health` → 200
- `HEAD /api/hello` → 200
- `GET /v1/models` → 200, `{"data":[{"id":...}]}` — the discovered ids are
  what Claude Code then uses verbatim.

Request (`claude-code.request.meta.json`, `claude-code.request.body.json`):

- Auth `Authorization: Bearer` (from `ANTHROPIC_AUTH_TOKEN`); the endpoint
  equally accepts `x-api-key` (proved by the plain fixture captures).
- The `[1m]` model suffix never reaches the wire — Claude Code strips it
  client-side once the model is known via gateway discovery.
- Headers worth preserving on a passthrough: `anthropic-version: 2023-06-01`,
  `anthropic-beta: claude-code-20250219,...`, `user-agent: claude-cli/...`,
  `x-app: cli`, `x-claude-code-session-id`, `x-stainless-*`.
- Body top-level keys: `model`, `max_tokens` (int, 32000), `stream`,
  `system` (array of text blocks, **no** `cache_control` — the gateway
  catalog advertised no caching), `tools` (58 entries), `thinking:
  {"type":"adaptive","display":"omitted"}`, `context_management`,
  `output_config`, `metadata.user_id`. No `temperature`, no `tool_choice`.
- Quirk: Claude Code interleaves `role:"system"` messages inside `messages`
  (not just the top-level `system` field). Z.ai accepts this.
- Turn 2 (`claude-code.turn2.body.json`) shows assistant history carrying
  `thinking` blocks **with `signature`** plus `tool_use`, and the matching
  `tool_result` blocks in the next user message — accepted unchanged.

Response (`claude-code.response.sse`): standard Anthropic SSE —
`message_start`, `ping`, thinking block (`thinking_delta` +
`signature_delta`), tool_use blocks streamed as `input_json_delta`,
`message_delta` (`stop_reason: tool_use` / `end_turn`) with usage including
`cache_read_input_tokens`, `message_stop`.

## pi → `https://open.bigmodel.cn/api/coding/paas/v4`

Request (`pi.request.meta.json`, `pi.request.body.json`):

- Auth `Authorization: Bearer`; `user-agent: pi (linux ...; x64)`.
- Body keys: `model`, `max_tokens` (int, 131072), `stream: true`,
  `stream_options: {"include_usage":true}`, `messages`, `tools` (OpenAI
  function format), `tool_choice` absent — **plus three Z.ai extensions a
  stock OpenAI client never sends**:
  - `thinking: {"type":"enabled","clear_thinking":false}` — GLM thinking toggle.
  - `reasoning_effort: "high"` — mapped from the agent's thinking level.
  - `tool_stream: true` — stream tool_calls as delta fragments.
- No `store`, no `developer` role; plain `system` string message.
- Turn 2 (`pi.turn2.body.json`): assistant message with `content: null` +
  `tool_calls`, then two `role:"tool"` messages with `tool_call_id` —
  parallel tool calls, standard chat protocol.

Response (`pi.response.sse`): standard chat-completion chunks —
`reasoning_content` deltas while thinking, `tool_calls` delta fragments
(`{id,index,type:"function",function:{name,arguments:"<fragment>"}}`), and a
final chunk with `finish_reason` (`tool_calls` / `stop`) plus full `usage`
(prompt/completion/total + `*_tokens_details`), then `data: [DONE]`.

## Implications for the proxy

1. Native Anthropic passthrough must forward the client's original
   headers (`anthropic-version`, `anthropic-beta`, `user-agent`, `x-app`,
   `x-stainless-*`), replacing only the auth credential with the pooled key.
2. A provider with both endpoints should use the Anthropic endpoint for
   `/v1/messages` (zero translation loss: thinking signatures, system-role
   messages, and every Z.ai/Anthropic extension survive verbatim) and the
   OpenAI endpoint for `/v1/chat/completions`.
3. Reverse translation (OpenAI client → Anthropic-only provider) must map
   `role:"tool"` + `tool_call_id` history into `tool_result` blocks and
   `tool_calls` into `tool_use` blocks; system messages become the
   top-level `system` field.
4. `/health` and `GET /v1/models` on the proxy already satisfy Claude Code's
   gateway discovery; nothing extra is needed there.
