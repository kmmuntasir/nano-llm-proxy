import { useEffect, useState } from "react"
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query"
import {
  Button,
  Card,
  Field,
  Heading,
  HStack,
  IconButton,
  Input,
  NativeSelect,
  Switch,
  Text,
  Textarea,
  VStack,
} from "@chakra-ui/react"
import { FiPlus, FiRefreshCw, FiTrash2 } from "react-icons/fi"
import { api, post, put, ApiError } from "../api/client"
import type { ModelMetaSyncStatusView, RuntimeSettingsView } from "../api/types"
import { toaster } from "../components/ui/toaster"

// Editing form for the runtime settings document. responsesModels and
// modelMeta are kept as raw text while editing and parsed on save; everything
// else round-trips as-is. modelMetaSyncStatus is server-owned and never sent.

interface AliasRow {
  from: string
  to: string
}

interface SettingsForm {
  rotation: "priority" | "lru"
  retry: RuntimeSettingsView["retry"]
  aliases: AliasRow[]
  userAgent: string
  injectTools: boolean
  zenFreeOnly: boolean
  responsesModelsText: string
  modelMetaAutoSync: boolean
  modelMetaJSON: string
  kiloFreeOnly: boolean
}

function hydrate(s: RuntimeSettingsView): SettingsForm {
  return {
    rotation: s.rotation,
    retry: { ...s.retry },
    aliases: Object.entries(s.anthropic.aliases ?? {}).map(([from, to]) => ({ from, to })),
    userAgent: s.zen.userAgent,
    injectTools: s.zen.injectTools,
    zenFreeOnly: s.zen.freeOnly,
    responsesModelsText: (s.zen.responsesModels ?? []).join(", "),
    modelMetaAutoSync: s.zen.modelMetaAutoSync,
    modelMetaJSON: JSON.stringify(s.zen.modelMeta ?? {}, null, 2),
    kiloFreeOnly: s.kilo.freeOnly,
  }
}

function buildPayload(
  f: SettingsForm,
): { ok: true; value: RuntimeSettingsView } | { ok: false; error: string } {
  let modelMeta: RuntimeSettingsView["zen"]["modelMeta"]
  try {
    modelMeta = JSON.parse(f.modelMetaJSON || "{}")
  } catch (e) {
    return { ok: false, error: `modelMeta is not valid JSON: ${e instanceof Error ? e.message : "parse error"}` }
  }
  const aliases: Record<string, string> = {}
  for (const { from, to } of f.aliases) {
    if (from.trim() === "") continue
    aliases[from.trim()] = to.trim()
  }
  return {
    ok: true,
    value: {
      rotation: f.rotation,
      retry: { ...f.retry },
      anthropic: { aliases },
      zen: {
        userAgent: f.userAgent,
        injectTools: f.injectTools,
        responsesModels: f.responsesModelsText
          .split(",")
          .map((s) => s.trim())
          .filter(Boolean),
        freeOnly: f.zenFreeOnly,
        modelMeta,
        modelMetaAutoSync: f.modelMetaAutoSync,
      },
      kilo: { freeOnly: f.kiloFreeOnly },
    },
  }
}

function SyncCard({ status }: { status?: ModelMetaSyncStatusView }) {
  const qc = useQueryClient()
  const sync = useMutation({
    mutationFn: () =>
      post<{ status: ModelMetaSyncStatusView }>("/api/settings/model-meta/sync"),
    onSuccess: async ({ status }) => {
      await qc.invalidateQueries({ queryKey: ["settings"] })
      if (status.ok) {
        toaster.create({
          title: `Catalog synced: +${status.added} ~${status.updated} −${status.pruned}`,
          type: "success",
        })
      } else {
        toaster.create({ title: `Sync failed: ${status.error ?? "unknown error"}`, type: "error" })
      }
    },
    onError: (e) =>
      toaster.create({ title: e instanceof ApiError ? e.message : "sync failed", type: "error" }),
  })

  return (
    <Card.Root>
      <Card.Header>
        <Heading size="sm">Zen model catalog (models.dev)</Heading>
        <Text fontSize="xs" color="fg.muted">
          Zen's own /models only lists ids — context windows, output limits and
          reasoning flags come from models.dev. Auto-sync refreshes them daily;
          entries for models models.dev doesn't know are left as you wrote them,
          and dead ids are pruned. Manual edits to catalog-known models are
          overwritten on the next sync.
        </Text>
      </Card.Header>
      <Card.Body>
        <HStack gap={3} align="center">
          <Button colorPalette="blue" size="sm" loading={sync.isPending} onClick={() => sync.mutate()}>
            <FiRefreshCw /> Sync now
          </Button>
          {status ? (
            status.ok ? (
              <Text fontSize="sm" color="fg.muted">
                Last sync {new Date(status.at * 1000).toLocaleString()} · +{status.added} added ·{" "}
                {status.updated} updated · {status.pruned} pruned
              </Text>
            ) : (
              <Text fontSize="sm" color="red.fg" truncate>
                Last sync failed: {status.error}
              </Text>
            )
          ) : (
            <Text fontSize="sm" color="fg.muted">
              Never synced
            </Text>
          )}
        </HStack>
      </Card.Body>
    </Card.Root>
  )
}

export default function SettingsPage() {
  const qc = useQueryClient()
  const { data, isLoading, error } = useQuery({
    queryKey: ["settings"],
    queryFn: () => api<{ settings: RuntimeSettingsView }>("/api/settings"),
  })
  const [form, setForm] = useState<SettingsForm | null>(null)
  const [saveError, setSaveError] = useState("")

  useEffect(() => {
    if (data?.settings) setForm(hydrate(data.settings))
  }, [data])

  const save = useMutation({
    mutationFn: (f: SettingsForm) => {
      const payload = buildPayload(f)
      if (!payload.ok) throw new ApiError(400, payload.error)
      return put<{ ok: boolean }>("/api/settings", payload.value)
    },
    onSuccess: async () => {
      setSaveError("")
      await qc.invalidateQueries({ queryKey: ["settings"] })
      toaster.create({ title: "Settings saved — live immediately", type: "success" })
    },
    onError: (e) =>
      setSaveError(e instanceof ApiError ? e.message : "save failed"),
  })

  if (isLoading || (!form && !error)) return <Text>Loading…</Text>
  if (error || !form) return <Text color="red.fg">Failed to load settings</Text>
  const set = (patch: Partial<SettingsForm>) => setForm({ ...form, ...patch })
  const setRetry = (patch: Partial<SettingsForm["retry"]>) =>
    setForm({ ...form, retry: { ...form.retry, ...patch } })

  return (
    <VStack align="stretch" gap={6}>
      <Heading size="lg">Settings</Heading>
      <Text fontSize="sm" color="fg.muted">
        Runtime settings live in the database and apply without a restart.
        Bootstrap values (port, bind, db path) come from the environment — see{" "}
        <code>.env.example</code>.
      </Text>
      {isLoading && <Text>Loading…</Text>}

      <SyncCard status={data?.settings.zen.modelMetaSyncStatus} />

      {/* routing */}
      <Card.Root>
        <Card.Header>
          <Heading size="sm">Routing</Heading>
        </Card.Header>
        <Card.Body>
          <Field.Root w="280px">
            <Field.Label>Rotation strategy</Field.Label>
            <NativeSelect.Root size="sm">
              <NativeSelect.Field
                value={form.rotation}
                onChange={(e) => set({ rotation: e.target.value as SettingsForm["rotation"] })}
              >
                <option value="priority">priority — stick to the first healthy key</option>
                <option value="lru">lru — spread load evenly</option>
              </NativeSelect.Field>
            </NativeSelect.Root>
            <Field.HelperText>
              Applies to every provider pool the moment you save.
            </Field.HelperText>
          </Field.Root>
        </Card.Body>
      </Card.Root>

      {/* retries */}
      <Card.Root>
        <Card.Header>
          <Heading size="sm">Retries &amp; cooldowns</Heading>
        </Card.Header>
        <Card.Body>
          <HStack gap={4} align="end" flexWrap="wrap">
            <Field.Root w="200px">
              <Field.Label>Max keys per request</Field.Label>
              <Input
                type="number"
                min={1}
                max={100}
                value={form.retry.maxKeysPerRequest}
                onChange={(e) => setRetry({ maxKeysPerRequest: Number(e.target.value) })}
              />
              <Field.HelperText>Failover depth before the client sees 502.</Field.HelperText>
            </Field.Root>
            <Field.Root w="200px">
              <Field.Label>Cooldown seconds</Field.Label>
              <Input
                type="number"
                min={0}
                max={86400}
                value={form.retry.cooldownSeconds}
                onChange={(e) => setRetry({ cooldownSeconds: Number(e.target.value) })}
              />
              <Field.HelperText>0 = only honor upstream Retry-After.</Field.HelperText>
            </Field.Root>
            <Field.Root w="200px">
              <Field.Label>Daily cap per key</Field.Label>
              <Input
                type="number"
                min={0}
                value={form.retry.maxRequestsPerKeyPerDay}
                onChange={(e) => setRetry({ maxRequestsPerKeyPerDay: Number(e.target.value) })}
              />
              <Field.HelperText>0 = off; an exhausted key cools until midnight.</Field.HelperText>
            </Field.Root>
          </HStack>
          <HStack mt={4} gap={6}>
            <Switch.Root
              checked={form.retry.respectRetryAfter}
              onCheckedChange={(e) => setRetry({ respectRetryAfter: e.checked })}
            >
              <Switch.HiddenInput />
              <Switch.Control>
                <Switch.Thumb />
              </Switch.Control>
              <Switch.Label>Honor upstream Retry-After</Switch.Label>
            </Switch.Root>
          </HStack>
        </Card.Body>
      </Card.Root>

      {/* anthropic aliases */}
      <Card.Root>
        <Card.Header>
          <Heading size="sm">Anthropic aliases</Heading>
          <Text fontSize="xs" color="fg.muted">
            Rewrites model names on /v1/messages — e.g. Claude Code asking for{" "}
            <code>claude-sonnet-5</code> can be routed to any provider/model.
          </Text>
        </Card.Header>
        <Card.Body>
          <VStack align="stretch" gap={2}>
            {form.aliases.map((row, i) => (
              <HStack key={i} gap={2}>
                <Input
                  placeholder="claude-sonnet-5"
                  value={row.from}
                  fontFamily="mono"
                  size="sm"
                  onChange={(e) => {
                    const aliases = [...form.aliases]
                    aliases[i] = { ...aliases[i], from: e.target.value }
                    set({ aliases })
                  }}
                />
                <Text fontSize="sm" color="fg.muted">
                  →
                </Text>
                <Input
                  placeholder="zen/glm-5"
                  value={row.to}
                  fontFamily="mono"
                  size="sm"
                  onChange={(e) => {
                    const aliases = [...form.aliases]
                    aliases[i] = { ...aliases[i], to: e.target.value }
                    set({ aliases })
                  }}
                />
                <IconButton
                  variant="ghost"
                  size="xs"
                  aria-label="remove alias"
                  colorPalette="red"
                  onClick={() => set({ aliases: form.aliases.filter((_, j) => j !== i) })}
                >
                  <FiTrash2 />
                </IconButton>
              </HStack>
            ))}
          </VStack>
          <Button
            size="2xs"
            variant="outline"
            mt={3}
            onClick={() => set({ aliases: [...form.aliases, { from: "", to: "" }] })}
          >
            <FiPlus /> Add alias
          </Button>
        </Card.Body>
      </Card.Root>

      {/* zen adapter */}
      <Card.Root>
        <Card.Header>
          <Heading size="sm">Zen adapter</Heading>
        </Card.Header>
        <Card.Body>
          <VStack align="stretch" gap={4}>
            <Field.Root w="320px">
              <Field.Label>User agent</Field.Label>
              <Input
                value={form.userAgent}
                fontFamily="mono"
                onChange={(e) => set({ userAgent: e.target.value })}
              />
              <Field.HelperText>
                Must be <code>opencode/1.18.0</code> or newer — the whole pool
                would get 426s otherwise.
              </Field.HelperText>
            </Field.Root>
            <Field.Root w="320px">
              <Field.Label>Responses-API models</Field.Label>
              <Input
                value={form.responsesModelsText}
                placeholder="comma-separated model ids"
                onChange={(e) => set({ responsesModelsText: e.target.value })}
              />
              <Field.HelperText>Which zen models speak /v1/responses.</Field.HelperText>
            </Field.Root>
            <HStack gap={6} flexWrap="wrap">
              <Switch.Root
                checked={form.injectTools}
                onCheckedChange={(e) => set({ injectTools: e.checked })}
              >
                <Switch.HiddenInput />
                <Switch.Control>
                  <Switch.Thumb />
                </Switch.Control>
                <Switch.Label>Inject chat fingerprint tools</Switch.Label>
              </Switch.Root>
              <Switch.Root
                checked={form.zenFreeOnly}
                onCheckedChange={(e) => set({ zenFreeOnly: e.checked })}
              >
                <Switch.HiddenInput />
                <Switch.Control>
                  <Switch.Thumb />
                </Switch.Control>
                <Switch.Label>Free models only</Switch.Label>
              </Switch.Root>
              <Switch.Root
                checked={form.modelMetaAutoSync}
                onCheckedChange={(e) => set({ modelMetaAutoSync: e.checked })}
              >
                <Switch.HiddenInput />
                <Switch.Control>
                  <Switch.Thumb />
                </Switch.Control>
                <Switch.Label>Auto-sync modelMeta daily</Switch.Label>
              </Switch.Root>
            </HStack>
            <Field.Root>
              <Field.Label>modelMeta (JSON)</Field.Label>
              <Textarea
                value={form.modelMetaJSON}
                fontFamily="mono"
                fontSize="sm"
                rows={12}
                onChange={(e) => set({ modelMetaJSON: e.target.value })}
              />
              <Field.HelperText>
                Per-model corrections over the catalog defaults; validated on
                save.
              </Field.HelperText>
            </Field.Root>
          </VStack>
        </Card.Body>
      </Card.Root>

      {/* kilo adapter */}
      <Card.Root>
        <Card.Header>
          <Heading size="sm">Kilo adapter</Heading>
        </Card.Header>
        <Card.Body>
          <Switch.Root
            checked={form.kiloFreeOnly}
            onCheckedChange={(e) => set({ kiloFreeOnly: e.checked })}
          >
            <Switch.HiddenInput />
            <Switch.Control>
              <Switch.Thumb />
            </Switch.Control>
            <Switch.Label>Free models only</Switch.Label>
          </Switch.Root>
        </Card.Body>
      </Card.Root>

      {saveError && (
        <Text color="red.fg" fontSize="sm">
          {saveError}
        </Text>
      )}
      <Button
        colorPalette="blue"
        loading={save.isPending}
        onClick={() => save.mutate(form)}
        w="fit-content"
      >
        Save settings
      </Button>
    </VStack>
  )
}
