# Z.ai GLM Coding Plan — captured fixtures

Request/response pairs captured from the GLM Coding Plan on 2026-09-25 with
`glm-5.3-flash` (see `scripts/capture-zai-fixtures.sh`). The upstream API key
is never stored here; fixtures contain model output only. Use them as the
reference shapes for the provider adapters and for offline tests.

## Endpoints

- OpenAI-compatible base: `https://api.z.ai/api/coding/paas/v4`
- Anthropic-compatible base: `https://api.z.ai/api/anthropic`
- Mainland China equivalents: `https://open.bigmodel.cn/api/coding/paas/v4`

## Findings

- OpenAI side is plain OpenAI Chat Completions. Auth is
  `Authorization: Bearer <key>`; `GET /models` returns the standard
  `{"object":"list","data":[...]}` shape.
- Reasoning surfaces as `reasoning_content` (message field / delta field) on
  the OpenAI side, and counts toward `usage.completion_tokens_details.reasoning_tokens`.
  With a small `max_tokens`, the budget is consumed by reasoning and
  `finish_reason` is `length` with empty `content` — that is expected, not an error.
- Streaming emits standard `chat.completion.chunk` SSE with a final chunk that
  carries the full `usage` object, then `data: [DONE]`.
- Anthropic side accepts `x-api-key` plus `anthropic-version: 2023-06-01`
  (see `AUTH_STYLE.txt`). Responses are standard Anthropic Messages: thinking
  blocks with `signature`, `tool_use` with `call_*` ids, `stop_reason`
  `max_tokens` / `tool_use`, and usage including `cache_read_input_tokens`.
- Anthropic streaming is standard SSE (`message_start`, `ping`,
  `content_block_start/delta/stop`, `message_delta`, `message_stop`) with
  `thinking_delta` / `signature_delta` deltas.
- The Anthropic base also exposes `GET /v1/models`. It returns an
  Anthropic-style list, but note the camelCase envelope fields `firstId`,
  `hasMore`, `lastId` (stock Anthropic uses snake_case). The `data[].id`
  entries are what the proxy catalog needs.

## Files

| Prefix | Contents |
| --- | --- |
| `openai_models` | `GET /models` catalog |
| `chat_nonstream` | `POST /chat/completions` |
| `chat_stream` | `POST /chat/completions` with `stream:true` |
| `anthropic_messages` | `POST /v1/messages` |
| `anthropic_messages_stream` | `POST /v1/messages` with `stream:true` |
| `anthropic_messages_tools` | `POST /v1/messages` with a tool (tool_use) |
| `anthropic_models` | `GET /v1/models` on the Anthropic base |

Each prefix has `.request.json` (or `.headers` for the GETs) and a
`.response.json` / `.response.sse`.
