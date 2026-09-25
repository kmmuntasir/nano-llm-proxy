#!/usr/bin/env bash
# Captures Z.ai GLM Coding Plan API fixtures (request/response pairs) so the
# proxy's Z.ai support can be developed and tested offline. Makes 7 small
# requests in one sequence (max_tokens <= 128 each) and stores everything under
# internal/gateway/testdata/zai/. The API key is redacted from all stored files.
#
# Usage:
#   ZAI_KEY=<key> [ZAI_MODEL=glm-5.3-flash] ./scripts/capture-zai-fixtures.sh
set -euo pipefail

KEY="${ZAI_KEY:?set ZAI_KEY}"
MODEL="${ZAI_MODEL:-glm-5.3-flash}"
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
OUT="$ROOT/internal/gateway/testdata/zai"
mkdir -p "$OUT"

OPENAI="https://api.z.ai/api/coding/paas/v4"
ANTHROPIC="https://api.z.ai/api/anthropic"

redact() { # scrub the key out of everything we just stored (no-op if absent)
  grep -rl "$KEY" "$OUT" 2>/dev/null | xargs -r sed -i "s|$KEY|<REDACTED>|g" || true
}

capture() { # <name> <curl args...> — stores headers + body, prints status
  local name="$1"; shift
  curl -sS --max-time 90 -D "$OUT/$name.headers" -o "$OUT/$name.body" "$@"
  echo "== $name: $(head -1 "$OUT/$name.headers" | tr -d '\r')"
  redact
}

# --- OpenAI-compatible endpoint -------------------------------------------

# 1. Model catalog (powers the proxy's merged /v1/models).
capture openai_models -X GET "$OPENAI/models" -H "Authorization: Bearer $KEY"

# 2. Chat completions, non-streaming.
printf '{"model":"%s","messages":[{"role":"user","content":"Say hi in exactly three words."}],"max_tokens":64}\n' "$MODEL" \
  > "$OUT/chat_nonstream.request.json"
capture chat_nonstream -X POST "$OPENAI/chat/completions" \
  -H "Authorization: Bearer $KEY" -H "Content-Type: application/json" \
  --data-binary @"$OUT/chat_nonstream.request.json"

# 3. Chat completions, streaming (SSE).
printf '{"model":"%s","messages":[{"role":"user","content":"Say hi in exactly three words."}],"max_tokens":64,"stream":true}\n' "$MODEL" \
  > "$OUT/chat_stream.request.json"
capture chat_stream -X POST "$OPENAI/chat/completions" \
  -H "Authorization: Bearer $KEY" -H "Content-Type: application/json" \
  --data-binary @"$OUT/chat_stream.request.json"

# --- Anthropic-compatible endpoint ----------------------------------------

# 4. Messages, non-streaming, Anthropic-style headers first (x-api-key +
#    anthropic-version). On 401, retry once with Authorization: Bearer and
#    remember which style works for the remaining cases.
printf '{"model":"%s","max_tokens":64,"messages":[{"role":"user","content":"Say hi in exactly three words."}]}\n' "$MODEL" \
  > "$OUT/anthropic_messages.request.json"

AUTH="x-api-key: $KEY"
capture anthropic_messages -X POST "$ANTHROPIC/v1/messages" \
  -H "x-api-key: $KEY" -H "anthropic-version: 2023-06-01" -H "Content-Type: application/json" \
  --data-binary @"$OUT/anthropic_messages.request.json"
if head -1 "$OUT/anthropic_messages.headers" | grep -qE 'HTTP/[^ ]+ 401'; then
  echo "   x-api-key rejected — retrying with Authorization: Bearer"
  AUTH="Authorization: Bearer $KEY"
  capture anthropic_messages -X POST "$ANTHROPIC/v1/messages" \
    -H "$AUTH" -H "anthropic-version: 2023-06-01" -H "Content-Type: application/json" \
    --data-binary @"$OUT/anthropic_messages.request.json"
  printf 'anthropic auth style: Authorization Bearer\n' > "$OUT/AUTH_STYLE.txt"
else
  printf 'anthropic auth style: x-api-key\n' > "$OUT/AUTH_STYLE.txt"
fi

# 5. Messages, streaming (SSE).
sed 's/"max_tokens":64/"max_tokens":64,"stream":true/' \
  "$OUT/anthropic_messages.request.json" > "$OUT/anthropic_messages_stream.request.json"
capture anthropic_messages_stream -X POST "$ANTHROPIC/v1/messages" \
  -H "$AUTH" -H "anthropic-version: 2023-06-01" -H "Content-Type: application/json" \
  --data-binary @"$OUT/anthropic_messages_stream.request.json"

# 6. Tool use (tool_use block shape in the response).
cat > "$OUT/anthropic_messages_tools.request.json" <<EOF
{"model":"$MODEL","max_tokens":128,"tools":[{"name":"get_weather","description":"Get the current weather for a city","input_schema":{"type":"object","properties":{"city":{"type":"string"}},"required":["city"]}}],"messages":[{"role":"user","content":"What is the weather in Paris? Use the get_weather tool."}]}
EOF
capture anthropic_messages_tools -X POST "$ANTHROPIC/v1/messages" \
  -H "$AUTH" -H "anthropic-version: 2023-06-01" -H "Content-Type: application/json" \
  --data-binary @"$OUT/anthropic_messages_tools.request.json"

# 7. Catalog fallback probe — does the Anthropic side expose GET /v1/models?
capture anthropic_models -X GET "$ANTHROPIC/v1/models" -H "$AUTH" -H "anthropic-version: 2023-06-01"

echo "done — fixtures in $OUT"
