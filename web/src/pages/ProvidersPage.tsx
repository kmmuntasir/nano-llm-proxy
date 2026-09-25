import { useEffect, useState } from "react"
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query"
import {
  Badge,
  Box,
  Button,
  Card,
  Code,
  Collapsible,
  Dialog,
  Field,
  Heading,
  HStack,
  IconButton,
  Input,
  NativeSelect,
  Progress,
  SimpleGrid,
  Stack,
  Switch,
  Text,
  VStack,
} from "@chakra-ui/react"
import { Activity, ChevronDown, ChevronUp, List, Pencil, Plus, Trash2 } from "lucide-react"
import { api, del, patch, post, put, ApiError } from "../api/client"
import ModelCard from "../components/ModelCard"
import type { PresetSpecView, ProviderKeyView, ProviderView, ZaiUsage, ZaiUsageLimit } from "../api/types"
import ConfirmDialog from "../components/ConfirmDialog"
import StatusBadge from "../components/StatusBadge"
import { toaster } from "../components/ui/toaster"

// AddProviderCard creates a provider two ways: pick a curated preset (the
// name and release-managed endpoints come from the server — shown read-only
// below the form — and only an API key is needed) or choose "Custom
// Provider…" and enter the endpoint roots yourself. At least one endpoint is
// required for custom: OpenAI-compatible and/or Anthropic-compatible (e.g.
// the Z.ai GLM Coding Plan exposes both; /v1/messages is served natively
// when the Anthropic root is set). The preset id is stored on the provider
// row, so presets already added show as disabled in the dropdown.
function AddProviderCard({ added }: { added: Set<string> }) {
  const qc = useQueryClient()
  const [presetID, setPresetID] = useState("") // "custom" = the custom form
  const [name, setName] = useState("")
  const [baseURL, setBaseURL] = useState("")
  const [anthropicURL, setAnthropicURL] = useState("")
  const [key, setKey] = useState("")
  const [keyLabel, setKeyLabel] = useState("")
  const [error, setError] = useState("")

  const { data } = useQuery({
    queryKey: ["presets"],
    queryFn: () => api<{ presets: PresetSpecView[] }>("/api/providers/presets"),
  })
  const presets = data?.presets ?? []
  const isCustom = presetID === "custom"
  const spec = presets.find((p) => p.id === presetID)

  const select = (id: string) => {
    setPresetID(id)
    // presets default the name to the preset id (still editable); custom
    // starts blank
    setName(id === "custom" ? "" : (presets.find((p) => p.id === id)?.id ?? ""))
    if (id !== "custom") {
      setBaseURL("")
      setAnthropicURL("")
    }
    setError("")
  }

  const create = useMutation({
    mutationFn: () =>
      post<{ id: number }>(
        "/api/providers",
        isCustom
          ? { name, baseUrl: baseURL, anthropicBaseUrl: anthropicURL, keys: [{ key, label: keyLabel }] }
          : { name, preset: presetID, keys: [{ key, label: keyLabel }] },
      ),
    onSuccess: async () => {
      setPresetID("")
      setName("")
      setBaseURL("")
      setAnthropicURL("")
      setKey("")
      setKeyLabel("")
      setError("")
      await qc.invalidateQueries({ queryKey: ["providers"] })
      toaster.create({ title: "Provider added", type: "success" })
    },
    onError: (e) => setError(e instanceof ApiError ? e.message : "Failed to create the provider"),
  })

  return (
    <Card.Root>
      <Card.Header>
        <Heading size="sm">Add Provider</Heading>
        <Text fontSize="xs" color="fg.muted">
          The name becomes the model prefix (<Code fontFamily="mono">name/model-id</Code>).
          Presets ship release-managed endpoints — just paste an API key. Custom
          providers need at least one endpoint root; <Code fontFamily="mono">/v1/messages</Code>{" "}
          is served natively when the Anthropic root is set. opencode-style providers can't
          be added — zen is one of a kind.
        </Text>
      </Card.Header>
      <Card.Body>
        <form
          onSubmit={(e) => {
            e.preventDefault()
            if (!presetID) {
              setError("Choose a provider from the dropdown")
              return
            }
            if (name.trim() === "") {
              setError("A provider name is required (it becomes the model prefix)")
              return
            }
            if (isCustom && baseURL.trim() === "" && anthropicURL.trim() === "") {
              setError("At least one endpoint is required: base URL and/or Anthropic endpoint")
              return
            }
            if (keyLabel.trim() === "") {
              setError("Every API key needs a label (shown next to the key in the pool)")
              return
            }
            if (key.trim() === "") {
              setError("An upstream API key is required")
              return
            }
            create.mutate()
          }}
        >
          <HStack gap={3} align="end" flexWrap="wrap">
            <Field.Root required w="220px">
              <Field.Label>Provider <Field.RequiredIndicator /></Field.Label>
              <NativeSelect.Root size="sm">
                <NativeSelect.Field value={presetID} onChange={(e) => select(e.target.value)}>
                  <option value="">Choose a provider…</option>
                  {presets.map((p) => (
                    <option key={p.id} value={p.id} disabled={added.has(p.id)}>
                      {p.label}
                      {added.has(p.id) ? " — added" : ""}
                    </option>
                  ))}
                  <option value="custom">Custom Provider…</option>
                </NativeSelect.Field>
                <NativeSelect.Indicator />
              </NativeSelect.Root>
            </Field.Root>
            <Field.Root required flex={1} minW="160px">
              <Field.Label>Name <Field.RequiredIndicator /></Field.Label>
              <Input autoComplete="off"
                placeholder={isCustom ? "e.g. together" : spec?.id}
                value={name}
                onChange={(e) => setName(e.target.value.toLowerCase())}
                fontFamily="mono"
              />
            </Field.Root>
            {isCustom && (
              <>
                <Field.Root minW="280px" flex={1}>
                  <Field.Label>OpenAI-compatible endpoint</Field.Label>
                  <Input autoComplete="off"
                    placeholder="https://api.example.com/v1"
                    value={baseURL}
                    onChange={(e) => setBaseURL(e.target.value)}
                    fontFamily="mono"
                  />
                </Field.Root>
                <Field.Root minW="280px" flex={1}>
                  <Field.Label>Anthropic-compatible endpoint</Field.Label>
                  <Input autoComplete="off"
                    placeholder="https://api.z.ai/api/anthropic"
                    value={anthropicURL}
                    onChange={(e) => setAnthropicURL(e.target.value)}
                    fontFamily="mono"
                  />
                </Field.Root>
              </>
            )}
            <Field.Root required minW="220px" flex={1}>
              <Field.Label>API key <Field.RequiredIndicator /></Field.Label>
              <Input autoComplete="new-password"
                type="password"
                value={key}
                onChange={(e) => setKey(e.target.value)}
                fontFamily="mono"
              />
            </Field.Root>
            <Field.Root required w="170px">
              <Field.Label>Key label <Field.RequiredIndicator /></Field.Label>
              <Input autoComplete="off"
                placeholder={isCustom ? "e.g. primary" : "e.g. glm"}
                value={keyLabel}
                onChange={(e) => setKeyLabel(e.target.value)}
              />
            </Field.Root>
            <Button type="submit" colorPalette="blue" loading={create.isPending}>
              <Plus /> Add
            </Button>
          </HStack>
          {spec && (
            <Stack fontSize="xs" color="fg.muted" gap={1} mt={3}>
              {spec.openaiBaseUrl && (
                <HStack fontSize="xs" color="fg.muted">
                  <Badge variant="subtle" colorPalette="blue" flexShrink={0}>
                    openai
                  </Badge>
                  <Text fontFamily="mono">{spec.openaiBaseUrl}</Text>
                </HStack>
              )}
              {spec.anthropicBaseUrl && (
                <HStack fontSize="xs" color="fg.muted">
                  <Badge variant="subtle" colorPalette="orange" flexShrink={0}>
                    anthropic
                  </Badge>
                  <Text fontFamily="mono">{spec.anthropicBaseUrl}</Text>
                </HStack>
              )}
            </Stack>
          )}
          {error && (
            <Text color="red.fg" fontSize="sm" mt={2}>
              {error}
            </Text>
          )}
        </form>
      </Card.Body>
    </Card.Root>
  )
}

function ProviderKeyRow({ provider, k }: { provider: ProviderView; k: ProviderKeyView }) {
  const qc = useQueryClient()
  const [toDelete, setToDelete] = useState(false)
  const [renaming, setRenaming] = useState(false)
  const [labelDraft, setLabelDraft] = useState("")
  const invalidate = () => void qc.invalidateQueries({ queryKey: ["providers"] })

  const toggle = useMutation({
    mutationFn: (disabled: boolean) =>
      patch(`/api/providers/${provider.id}/keys/${k.id}`, { disabled }),
    onSuccess: async () => {
      invalidate()
      toaster.create({ title: "Key updated", type: "success" })
    },
    onError: (e) =>
      toaster.create({ title: e instanceof ApiError ? e.message : "Failed to update the key", type: "error" }),
  })
  const rename = useMutation({
    mutationFn: () => patch(`/api/providers/${provider.id}/keys/${k.id}`, { label: labelDraft }),
    onSuccess: async () => {
      setRenaming(false)
      invalidate()
      toaster.create({ title: "Key renamed", type: "success" })
    },
    onError: (e) =>
      toaster.create({ title: e instanceof ApiError ? e.message : "Failed to rename the key", type: "error" }),
  })
  const remove = useMutation({
    mutationFn: () => del(`/api/providers/${provider.id}/keys/${k.id}`),
    onSuccess: async () => {
      setToDelete(false)
      invalidate()
      toaster.create({ title: "Key removed", type: "success" })
    },
    onError: (e) =>
      toaster.create({ title: e instanceof ApiError ? e.message : "Failed to remove the key", type: "error" }),
  })

  return (
    <HStack gap={3} fontSize="sm" py={1}>
      {renaming ? (
        <>
          <Input autoComplete="off"
            size="xs"
            fontFamily="mono"
            value={labelDraft}
            onChange={(e) => setLabelDraft(e.target.value)}
            maxW="160px"
            autoFocus
          />
          <Button
            size="2xs"
            colorPalette="blue"
            loading={rename.isPending}
            onClick={() => rename.mutate()}
          >
            Save
          </Button>
          <Button size="2xs" variant="ghost" onClick={() => setRenaming(false)}>
            Cancel
          </Button>
        </>
      ) : (
        <>
          <Code fontFamily="mono" fontSize="xs">
            {k.label || k.hash || `#${k.id}`}
          </Code>
          <IconButton
            variant="ghost"
            size="2xs"
            aria-label={`rename key ${k.label || k.hash || `#${k.id}`}`}
            onClick={() => {
              setLabelDraft(k.label)
              setRenaming(true)
            }}
          >
            <Pencil />
          </IconButton>
        </>
      )}
      {k.status && <StatusBadge status={k.status} />}
      {k.cooldownRemaining && (
        <Text fontSize="xs" color="fg.muted">
          {k.cooldownRemaining}
        </Text>
      )}
      <Text color="fg.muted" fontSize="xs">
        {k.requests ?? 0} reqs · {k.rateLimited ?? 0} 429s · {k.errors ?? 0} err
      </Text>
      <HStack ml="auto">
        <Switch.Root
          size="sm"
          checked={!k.disabled}
          onCheckedChange={(e) => toggle.mutate(!e.checked)}
        >
          <Switch.HiddenInput />
          <Switch.Control>
            <Switch.Thumb />
          </Switch.Control>
        </Switch.Root>
        <IconButton
          variant="ghost"
          size="xs"
          aria-label="delete key"
          colorPalette="red"
          onClick={() => setToDelete(true)}
        >
          <Trash2 />
        </IconButton>
      </HStack>
      <ConfirmDialog
        open={toDelete}
        onOpenChange={() => setToDelete(false)}
        title="Remove upstream key?"
        body={<Text>The pool rebuilds immediately without this key.</Text>}
        confirmText="Remove"
        destructive
        busy={remove.isPending}
        onConfirm={() => remove.mutate()}
      />
    </HStack>
  )
}

interface ProviderCatalogEntry {
  id: string
  owned_by?: string
  context_window?: number
  max_output_tokens?: number
  input_modalities?: string[]
  reasoning?: boolean
  responses_api?: boolean
  free?: boolean
  description?: string
}

// ModelsListModal shows the provider's live catalog as model cards. For the
// zen builtin (opencode-typed), models whose per-model Responses-API flag is
// on carry a "Served on /v1/responses" footer with a toggle (written straight
// to the flag in settings); everything else is view-only.
function ModelsListModal({
  provider,
  open,
  onOpenChange,
}: {
  provider: ProviderView
  open: boolean
  onOpenChange: (o: boolean) => void
}) {
  const qc = useQueryClient()
  const canToggleResponses = provider.type === "opencode"
  const [filter, setFilter] = useState("")
  const { data, isLoading, error } = useQuery({
    queryKey: ["providerModels", provider.id],
    queryFn: () => api<{ models: ProviderCatalogEntry[] }>(`/api/providers/${provider.id}/models`),
    enabled: open,
  })

  const toggle = useMutation({
    mutationFn: ({ model, responsesApi }: { model: string; responsesApi: boolean }) =>
      put("/api/settings/responses-api", { model, responsesApi }),
    onSuccess: async () => {
      await qc.invalidateQueries({ queryKey: ["providerModels", provider.id] })
      await qc.invalidateQueries({ queryKey: ["settings"] })
      await qc.invalidateQueries({ queryKey: ["models"] })
      toaster.create({ title: "Responses API updated", type: "success" })
    },
    onError: (e) =>
      toaster.create({ title: e instanceof ApiError ? e.message : "update failed", type: "error" }),
  })

  const models = data?.models ?? []
  const visible = models.filter(
    (m) => filter === "" || m.id.toLowerCase().includes(filter.toLowerCase()),
  )
  const bare = (id: string) => id.replace(/^[^/]+\//, "")

  return (
    <Dialog.Root open={open} onOpenChange={(e: { open: boolean }) => onOpenChange(e.open)} size="xl">
      <Dialog.Backdrop />
      <Dialog.Positioner>
        <Dialog.Content>
          <Dialog.Header pb={2}>
            <Dialog.Title>Models — {provider.name}</Dialog.Title>
          </Dialog.Header>
          <Dialog.Body px={6}>
            <HStack mb={4} flexWrap="wrap" gap={2} align="center">
              <Input autoComplete="off"
                placeholder="Search models…"
                flex={1}
                minW="200px"
                value={filter}
                onChange={(e) => setFilter(e.target.value)}
              />
              <Text fontSize="xs" color="fg.muted" whiteSpace="nowrap">
                {models.length} models
              </Text>
            </HStack>
            {isLoading && <Text fontSize="sm">Fetching catalog…</Text>}
            {error && (
              <Text color="red.fg" fontSize="sm">
                Failed to fetch the catalog from upstream.
              </Text>
            )}
            <VStack align="stretch" gap={3} maxH="58vh" overflowY="auto" pr={1}>
              {visible.map((m) => {
                const name = bare(m.id)
                return (
                  <ModelCard
                    key={m.id}
                    name={name}
                    modelId={m.id}
                    provider={provider.name}
                    description={m.description}
                    contextWindow={m.context_window}
                    maxOutputTokens={m.max_output_tokens}
                    inputModalities={m.input_modalities}
                    reasoning={m.reasoning}
                    responsesApi={m.responses_api}
                    free={m.free}
                    footer={
                      canToggleResponses && m.responses_api ? (
                        <HStack justify="space-between" pt={1}>
                          <Text fontSize="xs" color="fg.muted">
                            Served on /v1/responses
                          </Text>
                          <Switch.Root
                            size="sm"
                            checked={!!m.responses_api}
                            onCheckedChange={(e) =>
                              toggle.mutate({ model: name, responsesApi: e.checked })
                            }
                          >
                            <Switch.HiddenInput />
                            <Switch.Control>
                              <Switch.Thumb />
                            </Switch.Control>
                          </Switch.Root>
                        </HStack>
                      ) : undefined
                    }
                  />
                )
              })}
            </VStack>
          </Dialog.Body>
          <Dialog.Footer>
            <Dialog.ActionTrigger asChild>
              <Button type="button">Close</Button>
            </Dialog.ActionTrigger>
          </Dialog.Footer>
          <Dialog.CloseTrigger />
        </Dialog.Content>
      </Dialog.Positioner>
    </Dialog.Root>
  )
}

// --- Z.ai per-key usage (preset zai) ---

// classifyLimit maps a Z.ai quota-limit entry to its display role: the
// 5-hour window arrives as TOKENS_LIMIT on legacy v1 plans and as
// CREDIT_LIMIT with number=5 on credit plans; other CREDIT_LIMIT entries
// are the weekly cap; TIME_LIMIT tracks monthly MCP tool usage.
function classifyLimit(l: ZaiUsageLimit): "5h" | "week" | "mcp" | null {
  if (l.type === "TOKENS_LIMIT") return "5h"
  if (l.type === "TIME_LIMIT") return "mcp"
  if (l.type === "CREDIT_LIMIT") return l.number === 5 ? "5h" : "week"
  return null
}

const fmtResetAbs = (ms: number) =>
  new Date(ms).toLocaleString(undefined, {
    weekday: "short",
    month: "short",
    day: "numeric",
    hour: "numeric",
    minute: "2-digit",
  })

const fmtResetRel = (ms: number) => {
  const m = Math.round((ms - Date.now()) / 60000)
  if (m <= 0) return "soon"
  if (m < 60) return `in ${m}m`
  if (m < 48 * 60) return `in ${Math.floor(m / 60)}h ${m % 60}m`
  return `in ${Math.floor(m / 1440)}d ${Math.floor((m % 1440) / 60)}h`
}

// usageColor mirrors the standalone monitor's thresholds.
const usageColor = (pct: number) => (pct >= 85 ? "red" : pct >= 60 ? "orange" : "green")

const tierPalette = (level: string) =>
  level === "lite" ? "blue" : level === "pro" ? "purple" : level === "max" ? "orange" : "gray"

// KeyUsageCard is deliberately tiny: label + tier badge, then one line per
// usage window with a mini bar and the reset instant.
function KeyUsageCard({ label, usage, error }: { label: string; usage?: ZaiUsage; error?: string }) {
  const rows = usage?.limits ?? []
  const windows = rows.filter((l) => classifyLimit(l) !== null)
  return (
    <Card.Root variant="subtle" bg="bg.subtle" height="100%">
      <Card.Body px={3} py={3} gap={2}>
        <HStack gap={2}>
          <Code fontFamily="mono" fontSize="xs">
            {label}
          </Code>
          {usage && (
            <Badge colorPalette={tierPalette(usage.level)} variant="subtle">
              {usage.level || "?"}
            </Badge>
          )}
        </HStack>
        {error && (
          <Text fontSize="xs" color="red.fg" wordBreak="break-word">
            {error}
          </Text>
        )}
        {usage && windows.length === 0 && (
          <Text fontSize="xs" color="fg.muted">
            No usage windows reported.
          </Text>
        )}
        {windows.map((l, i) => {
          const kind = classifyLimit(l)!
          const pct = Math.max(0, Math.min(100, l.percentage ?? 0))
          const title = kind === "5h" ? "5-hour" : kind === "week" ? "Weekly" : "MCP this month"
          const detail =
            l.type === "TOKENS_LIMIT"
              ? `${pct}% used`
              : `${(l.currentValue ?? 0).toLocaleString()} / ${(l.usage ?? 0).toLocaleString()} · ${pct}%`
          return (
            <Box key={`${l.type}-${i}`}>
              <HStack justify="space-between" gap={2}>
                <Text fontSize="2xs" color="fg.muted">
                  {title}
                </Text>
                <Text fontSize="2xs" fontFamily="mono" whiteSpace="nowrap">
                  {detail}
                </Text>
              </HStack>
              <Progress.Root value={pct} size="xs" colorPalette={usageColor(pct)} mt={1}>
                <Progress.Track>
                  <Progress.Range />
                </Progress.Track>
              </Progress.Root>
              {kind !== "mcp" && l.nextResetTime ? (
                <Text fontSize="2xs" color="fg.subtle" mt={0.5}>
                  resets {fmtResetAbs(l.nextResetTime)} · {fmtResetRel(l.nextResetTime)}
                </Text>
              ) : null}
              {kind === "mcp" && (l.usageDetails ?? []).some((u) => u.usage > 0) ? (
                <Text fontSize="2xs" color="fg.subtle" mt={0.5}>
                  {(l.usageDetails ?? [])
                    .filter((u) => u.usage > 0)
                    .map((u) => `${u.modelCode} ${u.usage}`)
                    .join(" · ")}
                </Text>
              ) : null}
            </Box>
          )
        })}
        {usage && rows.some((l) => l.type === "TOKENS_LIMIT") && (
          <Text fontSize="2xs" color="fg.subtle">
            Legacy plan — no weekly cap.
          </Text>
        )}
      </Card.Body>
    </Card.Root>
  )
}

type KeyUsageResult = { id: number; label: string; usage?: ZaiUsage; error: string }

// KeyUsageModal fetches every key's Z.ai quota in parallel when opened (and
// on Refresh) — deliberately no auto-refresh: upstream calls stay on demand.
function KeyUsageModal({
  provider,
  open,
  onOpenChange,
}: {
  provider: ProviderView
  open: boolean
  onOpenChange: (o: boolean) => void
}) {
  const [results, setResults] = useState<KeyUsageResult[] | null>(null)
  const [busy, setBusy] = useState(false)
  const [nonce, setNonce] = useState(0)

  useEffect(() => {
    if (!open) return
    let cancelled = false
    setBusy(true)
    void Promise.all(
      provider.keys.map((k) =>
        api<ZaiUsage>(`/api/providers/${provider.id}/keys/${k.id}/usage`)
          .then((usage) => ({ id: k.id, label: k.label || k.hash || `#${k.id}`, usage, error: "" }))
          .catch((e) => ({
            id: k.id,
            label: k.label || k.hash || `#${k.id}`,
            usage: undefined,
            error: e instanceof ApiError ? e.message : "fetch failed",
          })),
      ),
    ).then((rs) => {
      if (!cancelled) {
        setResults(rs)
        setBusy(false)
      }
    })
    return () => {
      cancelled = true
    }
  }, [open, nonce, provider])

  return (
    <Dialog.Root open={open} onOpenChange={(e: { open: boolean }) => onOpenChange(e.open)} size="xl">
      <Dialog.Backdrop />
      <Dialog.Positioner>
        <Dialog.Content>
          <Dialog.Header pb={2}>
            <Dialog.Title>Plan usage — {provider.name}</Dialog.Title>
          </Dialog.Header>
          <Dialog.Body px={6}>
            {!results && busy && <Text fontSize="sm">Checking every key…</Text>}
            {results && (
              <SimpleGrid columns={{ base: 1, md: 2 }} gap={3}>
                {results.map((r) => (
                  <KeyUsageCard key={r.id} label={r.label} usage={r.usage} error={r.error} />
                ))}
              </SimpleGrid>
            )}
            {results && results.length === 0 && (
              <Text fontSize="sm" color="fg.muted">
                No upstream keys on this provider.
              </Text>
            )}
          </Dialog.Body>
          <Dialog.Footer>
            <Button type="button" variant="ghost" loading={busy} onClick={() => setNonce((n) => n + 1)}>
              Refresh
            </Button>
            <Dialog.ActionTrigger asChild>
              <Button type="button">Close</Button>
            </Dialog.ActionTrigger>
          </Dialog.Footer>
          <Dialog.CloseTrigger />
        </Dialog.Content>
      </Dialog.Positioner>
    </Dialog.Root>
  )
}

function ProviderCard({ p }: { p: ProviderView }) {
  const qc = useQueryClient()
  const [newKey, setNewKey] = useState("")
  const [newKeyLabel, setNewKeyLabel] = useState("")
  const [toDelete, setToDelete] = useState(false)
  const [editField, setEditField] = useState<null | "baseUrl" | "anthropicBaseUrl">(null)
  const [editURL, setEditURL] = useState("")
  const [keysOpen, setKeysOpen] = useState(false)
  const [modelsOpen, setModelsOpen] = useState(false)
  const [usageOpen, setUsageOpen] = useState(false)
  const invalidate = () => void qc.invalidateQueries({ queryKey: ["providers"] })

  const patchP = useMutation({
    mutationFn: (body: Record<string, unknown>) => patch(`/api/providers/${p.id}`, body),
    onSuccess: async () => {
      setEditField(null)
      invalidate()
      toaster.create({ title: "Provider updated", type: "success" })
    },
    onError: (e) =>
      toaster.create({ title: e instanceof ApiError ? e.message : "Failed to update the provider", type: "error" }),
  })
  const removeP = useMutation({
    mutationFn: () => del(`/api/providers/${p.id}`),
    onSuccess: async () => {
      setToDelete(false)
      invalidate()
      toaster.create({ title: "Provider deleted", type: "success" })
    },
    onError: (e) =>
      toaster.create({ title: e instanceof ApiError ? e.message : "Failed to delete the provider", type: "error" }),
  })
  const addKey = useMutation({
    mutationFn: () => post(`/api/providers/${p.id}/keys`, { key: newKey, label: newKeyLabel }),
    onSuccess: async () => {
      setNewKey("")
      setNewKeyLabel("")
      invalidate()
      toaster.create({ title: "Key added", type: "success" })
    },
    onError: (e) =>
      toaster.create({ title: e instanceof ApiError ? e.message : "Failed to add the key", type: "error" }),
  })

  return (
    <Card.Root height="100%">
      <Card.Body pt={4} gap={3}>
        <HStack justify="space-between" flexWrap="wrap" gap={2}>
          <HStack gap={2} flexWrap="wrap">
            <Heading size="sm" fontFamily="mono">
              {p.name}
            </Heading>
            <Badge variant="outline">{p.type}</Badge>
            {p.builtin && <Badge variant="solid" colorPalette="purple">builtin</Badge>}
            {p.preset && <Badge variant="subtle" colorPalette="teal">{p.preset}</Badge>}
            <Badge colorPalette={p.healthy > 0 ? "green" : "red"}>
              {p.healthy}/{p.total} healthy
            </Badge>
          </HStack>
          <HStack gap={3}>
            {p.preset === "zai" && (
              <Button
                variant="ghost"
                size="xs"
                onClick={() => setUsageOpen(true)}
                aria-label={`view usage for ${p.name}`}
              >
                <Activity /> Usage
              </Button>
            )}
            <Button
              variant="ghost"
              size="xs"
              onClick={() => setModelsOpen(true)}
              aria-label={`view models for ${p.name}`}
            >
              <List /> Models
            </Button>
            {!p.builtin && (
              <IconButton
                variant="ghost"
                size="xs"
                aria-label="delete provider"
                colorPalette="red"
                onClick={() => setToDelete(true)}
              >
                <Trash2 />
              </IconButton>
            )}
            <Switch.Root
              checked={p.enabled}
              onCheckedChange={(e) => patchP.mutate({ enabled: e.checked })}
            >
              <Switch.HiddenInput />
              <Switch.Control>
                <Switch.Thumb />
              </Switch.Control>
            </Switch.Root>
          </HStack>
        </HStack>

        {/* endpoint roots: whichever are set route the matching surface */}
        <Stack fontSize="xs" color="fg.muted" gap={1}>
          {(["baseUrl", "anthropicBaseUrl"] as const).map((field) => {
            const url = field === "baseUrl" ? p.baseUrl : p.anthropicBaseUrl
            // builtins never use the anthropic root — don't offer it
            if (field === "anthropicBaseUrl" && url === "" && p.builtin) return null
            const editing = editField === field
            return (
              <HStack key={field} fontSize="xs" color="fg.muted">
                <Badge
                  variant="subtle"
                  colorPalette={field === "baseUrl" ? "blue" : "orange"}
                  flexShrink={0}
                >
                  {field === "baseUrl" ? "openai" : "anthropic"}
                </Badge>
                {editing ? (
                  <>
                    <Input autoComplete="off"
                      size="xs"
                      fontFamily="mono"
                      value={editURL}
                      onChange={(e) => setEditURL(e.target.value)}
                      maxW="360px"
                    />
                    <Button
                      size="2xs"
                      colorPalette="blue"
                      onClick={() => patchP.mutate({ [field]: editURL })}
                    >
                      Save
                    </Button>
                    <Button size="2xs" variant="ghost" onClick={() => setEditField(null)}>
                      Cancel
                    </Button>
                  </>
                ) : (
                  <>
                    <Text fontFamily="mono" truncate>
                      {url || "not set"}
                    </Text>
                    <Button
                      variant="ghost"
                      size="2xs"
                      onClick={() => {
                        setEditField(field)
                        setEditURL(url)
                      }}
                    >
                      Edit
                    </Button>
                  </>
                )}
              </HStack>
            )
          })}
        </Stack>

        {/* keys live in their own collapsed sub-card: builtin providers carry
            long key lists that would otherwise dwarf the card header */}
        <Collapsible.Root
          open={keysOpen}
          onOpenChange={(e) => setKeysOpen(e.open)}
        >
          <Card.Root variant="subtle" bg="bg.subtle">
            <Collapsible.Trigger asChild>
              <HStack
                as="button"
                px={4}
                py={3}
                justify="space-between"
                width="full"
                cursor="pointer"
                _hover={{ bg: "bg.emphasized" }}
              >
                <HStack gap={2}>
                  {keysOpen ? <ChevronUp size="14" /> : <ChevronDown size="14" />}
                  <Text fontSize="sm" fontWeight="medium">
                    Upstream keys ({p.keys.length})
                  </Text>
                </HStack>
                <Badge colorPalette={p.healthy > 0 ? "green" : "red"} variant="subtle">
                  {p.healthy}/{p.total} healthy
                </Badge>
              </HStack>
            </Collapsible.Trigger>
            <Collapsible.Content>
              <Box overflowX="auto">
              <Stack gap={0} px={4} pb={4}>
                {p.keys.map((k) => (
                  <ProviderKeyRow key={k.id} provider={p} k={k} />
                ))}
                {p.keys.length === 0 && (
                  <Text fontSize="sm" color="fg.muted">
                    No keys yet — add one below.
                  </Text>
                )}
                <form
                  onSubmit={(e) => {
                    e.preventDefault()
                    addKey.mutate()
                  }}
                >
                  <HStack gap={2} mt={2} flexWrap="wrap" align="end">
                    <Field.Root required w="160px">
                      <Field.Label>Key label <Field.RequiredIndicator /></Field.Label>
                      <Input autoComplete="off"
                        size="xs"
                        placeholder="e.g. backup"
                        value={newKeyLabel}
                        onChange={(e) => setNewKeyLabel(e.target.value)}
                      />
                    </Field.Root>
                    <Field.Root required flex={1} minW="220px">
                      <Field.Label>API key <Field.RequiredIndicator /></Field.Label>
                      <Input autoComplete="new-password"
                        size="xs"
                        type="password"
                        placeholder="upstream API key"
                        value={newKey}
                        onChange={(e) => setNewKey(e.target.value)}
                        fontFamily="mono"
                      />
                    </Field.Root>
                    <Button size="2xs" type="submit" loading={addKey.isPending}>
                      <Plus /> Add key
                    </Button>
                  </HStack>
                </form>
              </Stack>
              </Box>
            </Collapsible.Content>
          </Card.Root>
        </Collapsible.Root>
      </Card.Body>

      <ModelsListModal provider={p} open={modelsOpen} onOpenChange={setModelsOpen} />
      {p.preset === "zai" && <KeyUsageModal provider={p} open={usageOpen} onOpenChange={setUsageOpen} />}
      <ConfirmDialog
        open={toDelete}
        onOpenChange={() => setToDelete(false)}
        title={`Delete provider "${p.name}"?`}
        body={<Text>Its keys are removed and <Code fontFamily="mono">{p.name}/*</Code> models stop routing immediately.</Text>}
        confirmText="Delete provider"
        destructive
        busy={removeP.isPending}
        onConfirm={() => removeP.mutate()}
      />
    </Card.Root>
  )
}

// ProvidersPage: one add card on top (preset dropdown with a "Custom
// Provider…" escape hatch), then a Models-page-style filter bar and the
// provider cards in a responsive grid.
export default function ProvidersPage() {
  const { data, isLoading, error } = useQuery({
    queryKey: ["providers"],
    queryFn: () => api<{ providers: ProviderView[] }>("/api/providers"),
  })
  const [search, setSearch] = useState("")
  const [show, setShow] = useState("all") // all | enabled | disabled | unhealthy
  const [kind, setKind] = useState("all") // all | builtin | preset | custom

  const providers = data?.providers ?? []
  // presets already added (any provider row carrying that preset id)
  const added = new Set(providers.filter((p) => p.preset).map((p) => p.preset))

  const filtered = providers.filter((p) => {
    if (show === "enabled" && !p.enabled) return false
    if (show === "disabled" && p.enabled) return false
    if (show === "unhealthy" && p.healthy > 0) return false
    if (kind === "builtin" && !p.builtin) return false
    if (kind === "preset" && !p.preset) return false
    if (kind === "custom" && (p.builtin || p.preset)) return false
    if (search !== "") {
      const q = search.toLowerCase()
      const hay = `${p.name} ${p.baseUrl} ${p.anthropicBaseUrl} ${p.preset}`.toLowerCase()
      if (!hay.includes(q)) return false
    }
    return true
  })

  return (
    <VStack align="stretch" gap={6}>
      <Heading size="lg">Providers</Heading>
      <AddProviderCard added={added} />
      <Card.Root>
        <Card.Body>
          <HStack flexWrap="wrap" gap={3} align="end">
            <Field.Root flex={1} minW="220px">
              <Field.Label>Search</Field.Label>
              <Input autoComplete="off"
                placeholder="Name, endpoint, preset…"
                value={search}
                onChange={(e) => setSearch(e.target.value)}
              />
            </Field.Root>
            <Field.Root w="150px">
              <Field.Label>Show</Field.Label>
              <NativeSelect.Root size="sm">
                <NativeSelect.Field value={show} onChange={(e) => setShow(e.target.value)}>
                  <option value="all">All</option>
                  <option value="enabled">Enabled</option>
                  <option value="disabled">Disabled</option>
                  <option value="unhealthy">Unhealthy</option>
                </NativeSelect.Field>
                <NativeSelect.Indicator />
              </NativeSelect.Root>
            </Field.Root>
            <Field.Root w="150px">
              <Field.Label>Kind</Field.Label>
              <NativeSelect.Root size="sm">
                <NativeSelect.Field value={kind} onChange={(e) => setKind(e.target.value)}>
                  <option value="all">All</option>
                  <option value="builtin">Built-in</option>
                  <option value="preset">Preset</option>
                  <option value="custom">Custom</option>
                </NativeSelect.Field>
                <NativeSelect.Indicator />
              </NativeSelect.Root>
            </Field.Root>
          </HStack>
        </Card.Body>
      </Card.Root>
      {isLoading && <Text>Loading…</Text>}
      {error && <Text color="red.fg">Failed to load providers</Text>}
      {!isLoading && !error && (
        <Text fontSize="sm" color="fg.muted">
          {filtered.length} of {providers.length} providers
        </Text>
      )}
      <SimpleGrid columns={{ base: 1, lg: 2 }} gap={4}>
        {filtered.map((p) => (
          <ProviderCard key={p.id} p={p} />
        ))}
      </SimpleGrid>
      {!isLoading && !error && providers.length > 0 && filtered.length === 0 && (
        <Text fontSize="sm" color="fg.muted">
          No providers match the current filters.
        </Text>
      )}
    </VStack>
  )
}
