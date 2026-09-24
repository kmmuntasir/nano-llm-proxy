import { useEffect, useState } from "react"
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query"
import {
  Box,
  Button,
  Card,
  Field,
  Heading,
  HStack,
  Input,
  NativeSelect,
  Switch,
  Text,
  VStack,
} from "@chakra-ui/react"
import { RefreshCw } from "lucide-react"
import { api, post, put, ApiError } from "../api/client"
import type {
  ModelMetaSyncStatusView,
  ModelMetaView,
  RuntimeSettingsView,
} from "../api/types"
import { toaster } from "../components/ui/toaster"

// Editing form for the runtime settings document. The Zen model catalog is
// sync-managed (models.dev) and browsable on the Models page; the Responses
// API flag is toggled per model on the Providers page. modelMetaSyncStatus
// is server-owned and never sent.


// ModelPicker is a searchable dropdown over the merged model catalog.
// Options are catalog ids with cosmetic suffixes stripped (the gateway
// strips them anyway), so a picked value reads like a clean provider/model.
function ModelPicker({
  models,
  value,
  onChange,
  placeholder,
}: {
  models: string[]
  value: string
  onChange: (v: string) => void
  placeholder?: string
}) {
  const [query, setQuery] = useState("")
  const [open, setOpen] = useState(false)
  const filtered = models.filter((m) => m.toLowerCase().includes(query.toLowerCase()))

  return (
    <Box position="relative">
      <Input
        placeholder={placeholder}
        value={open ? query : value}
        fontFamily="mono"
        onChange={(e) => {
          setQuery(e.target.value)
          setOpen(true)
        }}
        onFocus={() => {
          setQuery("")
          setOpen(true)
        }}
        onBlur={() => setTimeout(() => setOpen(false), 150)}
      />
      {open && (
        <Box
          position="absolute"
          zIndex={20}
          top="100%"
          left={0}
          right={0}
          mt={1}
          bg="bg.panel"
          borderWidth="1px"
          rounded="md"
          boxShadow="md"
          maxH="240px"
          overflowY="auto"
        >
          <Box
            px={3}
            py={2}
            fontSize="sm"
            cursor="pointer"
            _hover={{ bg: "bg.subtle" }}
            onClick={() => {
              onChange("")
              setOpen(false)
            }}
          >
            None — pass claude-* through untouched
          </Box>
          {filtered.length === 0 && (
            <Text px={3} py={2} fontSize="sm" color="fg.muted">
              No models match “{query}”
            </Text>
          )}
          {filtered.map((m) => (
            <Box
              key={m}
              px={3}
              py={2}
              fontSize="sm"
              fontFamily="mono"
              cursor="pointer"
              _hover={{ bg: "bg.subtle" }}
              onClick={() => {
                onChange(m)
                setOpen(false)
              }}
            >
              {m}
            </Box>
          ))}
        </Box>
      )}
    </Box>
  )
}

// cosmetic context/modality suffix the gateway strips anyway
const SUFFIX_RE = /-\d+(?:\.\d+)?[KMG](?:-txt|-img|-vid|-aud|-pdf)*$/

interface CatalogEntry {
  id: string
}

function cleanModelId(id: string): string {
  const [prov, ...rest] = id.split("/")
  return rest.length > 0 ? `${prov}/${rest.join("/").replace(SUFFIX_RE, "")}` : id
}

interface SettingsForm {
  rotation: "priority" | "lru"
  retry: RuntimeSettingsView["retry"]
  fallbackModel: string
  userAgent: string
  injectTools: boolean
  zenFreeOnly: boolean
  modelMetaAutoSync: boolean
  /** sync-managed; the Models page reads it — saved back untouched */
  modelMeta: Record<string, ModelMetaView>
  kiloFreeOnly: boolean
}

function hydrate(s: RuntimeSettingsView): SettingsForm {
  return {
    rotation: s.rotation,
    retry: { ...s.retry },
    fallbackModel: s.anthropic.fallbackModel ?? "",
    userAgent: s.zen.userAgent,
    injectTools: s.zen.injectTools,
    zenFreeOnly: s.zen.freeOnly,
    modelMetaAutoSync: s.zen.modelMetaAutoSync,
    modelMeta: { ...(s.zen.modelMeta ?? {}) },
    kiloFreeOnly: s.kilo.freeOnly,
  }
}

function buildPayload(
  f: SettingsForm,
): { ok: true; value: RuntimeSettingsView } | { ok: false; error: string } {
  return {
    ok: true,
    value: {
      rotation: f.rotation,
      retry: { ...f.retry },
      anthropic: { fallbackModel: f.fallbackModel.trim() },
      zen: {
        userAgent: f.userAgent,
        injectTools: f.injectTools,
        freeOnly: f.zenFreeOnly,
        modelMeta: f.modelMeta,
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
            <RefreshCw /> Sync now
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
  const catalog = useQuery({
    queryKey: ["models"],
    queryFn: () => api<{ data: CatalogEntry[] }>("/api/models"),
    staleTime: 5 * 60_000,
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
    onError: (e) => {
      const msg = e instanceof ApiError ? e.message : "Failed to save settings"
      setSaveError(msg)
      toaster.create({ title: msg, type: "error" })
    },
  })

  if (isLoading || (!form && !error)) return <Text>Loading…</Text>
  if (error || !form) return <Text color="red.fg">Failed to load settings</Text>
  const catalogOptions = [
    ...new Set((catalog.data?.data ?? []).map((e) => cleanModelId(e.id))),
  ].sort()

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
          <Field.Root w={{ base: "full", md: "380px" }}>
            <Field.Label>Rotation strategy</Field.Label>
            <NativeSelect.Root size="sm">
              <NativeSelect.Field
                value={form.rotation}
                onChange={(e) => set({ rotation: e.target.value as SettingsForm["rotation"] })}
              >
                <option value="priority">Priority — stick to the first healthy key</option>
                <option value="lru">LRU — spread load evenly</option>
              </NativeSelect.Field>
              <NativeSelect.Indicator />
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
            <Field.Root w={{ base: "full", sm: "200px" }}>
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
            <Field.Root w={{ base: "full", sm: "200px" }}>
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
            <Field.Root w={{ base: "full", sm: "200px" }}>
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

      {/* anthropic fallback */}
      <Card.Root>
        <Card.Header>
          <Heading size="sm">Anthropic fallback</Heading>
          <Text fontSize="xs" color="fg.muted">
            Clients configure their main models verbatim (Claude Code env slots
            in <code>~/.claude/settings.json</code>). This target catches the
            literal <code>claude-*</code> names clients still send for
            background tasks — titling, summarization. Point it at a cheap
            model; leave empty to pass such requests through untouched.
          </Text>
        </Card.Header>
        <Card.Body>
          <Field.Root w={{ base: "full", md: "380px" }}>
            <Field.Label>Fallback target</Field.Label>
            <ModelPicker
              models={catalogOptions}
              value={form.fallbackModel}
              onChange={(v) => set({ fallbackModel: v })}
              placeholder="zen/mimo-v2.6-flash-free"
            />
            <Field.HelperText>
              Any model starting with <code>claude-</code> (without an explicit{" "}
              <code>provider/</code> prefix) routes here.
            </Field.HelperText>
          </Field.Root>
        </Card.Body>
      </Card.Root>

      {/* zen adapter */}
      <Card.Root>
        <Card.Header>
          <Heading size="sm">Zen adapter</Heading>
        </Card.Header>
        <Card.Body>
          <VStack align="stretch" gap={4}>
            <Field.Root w={{ base: "full", md: "380px" }}>
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
