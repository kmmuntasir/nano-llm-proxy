import { useEffect, useState } from "react"
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query"
import {
  Button,
  Card,
  Code,
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
  WebToolsSettingsView,
  WebToolsTestResult,
} from "../api/types"
import { toaster } from "../components/ui/toaster"
import ComboSelect from "../components/ComboSelect"

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
  return (
    <ComboSelect
      options={models.map((m) => ({ value: m, label: m }))}
      value={value}
      onChange={onChange}
      placeholder={placeholder}
      noneLabel="None — pass claude-* through untouched"
      emptyText="No models match"
      mono
      ariaLabel="Fallback target model"
    />
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
  /** sync-managed (models.dev, same sync as zen's) — saved back untouched */
  zaiModelMeta: Record<string, ModelMetaView>
  kiloFreeOnly: boolean
  webTools: WebToolsSettingsView
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
    zaiModelMeta: { ...(s.zai.modelMeta ?? {}) },
    kiloFreeOnly: s.kilo.freeOnly,
    // The PUT replaces the whole document — webTools must always ride
    // along or a save from this page would revert it to defaults.
    webTools: { ...s.webTools },
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
      zai: { modelMeta: f.zaiModelMeta },
      webTools: f.webTools,
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
        <Heading size="sm">Model catalog (models.dev)</Heading>
        <Text fontSize="xs" color="fg.muted">
          Zen and Z.ai list bare model ids — context windows, output limits and
          reasoning flags come from models.dev. Auto-sync refreshes both catalogs
          daily; entries for models models.dev doesn't know are left as you wrote
          them, and dead ids are pruned. Manual edits to catalog-known models are
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

// WebToolsCard edits the /mcp web-tools settings and live-tests both
// backends (SearXNG search, obscura render) without saving first.
function WebToolsCard({
  value,
  onChange,
}: {
  value: WebToolsSettingsView
  onChange: (patch: Partial<WebToolsSettingsView>) => void
}) {
  const test = useMutation({
    mutationFn: (target: "searxng" | "obscura") =>
      post<WebToolsTestResult>("/api/webtools/test", { target, deep: target === "obscura" }),
    onSuccess: (res, target) => {
      const label = target === "searxng" ? "SearXNG" : "obscura"
      if (res.ok) {
        toaster.create({
          title: `${label} OK (${res.latencyMs ?? 0} ms)`,
          description: res.version ? `${res.version} — ${res.detail ?? ""}` : res.detail,
          type: "success",
        })
      } else {
        toaster.create({ title: `${label} failed: ${res.detail ?? "unknown error"}`, type: "error" })
      }
    },
    onError: (e) =>
      toaster.create({ title: e instanceof ApiError ? e.message : "test failed", type: "error" }),
  })

  const num = (v: string) => (v === "" ? 0 : Number(v))

  return (
    <Card.Root>
      <Card.Header>
        <Heading size="sm">Web tools (MCP)</Heading>
        <Text fontSize="xs" color="fg.muted">
          Exposes <Code fontFamily="mono">web_search</Code> and{" "}
          <Code fontFamily="mono">web_read</Code> at <Code fontFamily="mono">/mcp</Code> to every
          client key. Search uses a self-hosted SearXNG; page reads use a fast native
          fetch and fall back to the obscura headless browser for JavaScript-heavy or
          bot-protected pages. Install them on the host with{" "}
          <Code fontFamily="mono">scripts/install-web-tools.sh</Code>.
        </Text>
      </Card.Header>
      <Card.Body>
        <VStack align="stretch" gap={4}>
          <Switch.Root
            checked={value.enabled}
            onCheckedChange={(e) => onChange({ enabled: e.checked })}
          >
            <Switch.HiddenInput />
            <Switch.Control>
              <Switch.Thumb />
            </Switch.Control>
            <Switch.Label color={value.enabled ? undefined : "fg.muted"}>
              {value.enabled
                ? "Enabled — serve /mcp to client keys"
                : "Disabled — /mcp answers 404 until you save with this on"}
            </Switch.Label>
          </Switch.Root>

          <HStack gap={4} align="end" flexWrap="wrap">
            <Field.Root w={{ base: "full", sm: "340px" }}>
              <Field.Label>SearXNG URL</Field.Label>
              <Input autoComplete="off"
                value={value.searxngUrl}
                fontFamily="mono"
                onChange={(e) => onChange({ searxngUrl: e.target.value })}
              />
              <Field.HelperText>JSON API endpoint (default port 8888).</Field.HelperText>
            </Field.Root>
            <Field.Root w={{ base: "full", sm: "180px" }}>
              <Field.Label>Reader order</Field.Label>
              <NativeSelect.Root size="sm">
                <NativeSelect.Field
                  value={value.readerMode}
                  onChange={(e) =>
                    onChange({ readerMode: e.target.value as WebToolsSettingsView["readerMode"] })
                  }
                >
                  <option value="fast">fast — native fetch first</option>
                  <option value="render">render — browser first</option>
                </NativeSelect.Field>
                <NativeSelect.Indicator />
              </NativeSelect.Root>
              <Field.HelperText>Both legs back each other up.</Field.HelperText>
            </Field.Root>
            <Field.Root w={{ base: "full", sm: "200px" }}>
              <Field.Label>obscura path</Field.Label>
              <Input autoComplete="off"
                value={value.obscuraPath}
                fontFamily="mono"
                onChange={(e) => onChange({ obscuraPath: e.target.value })}
              />
              <Field.HelperText>Binary on PATH or absolute.</Field.HelperText>
            </Field.Root>
          </HStack>

          <HStack gap={4} align="end" flexWrap="wrap">
            <Field.Root w={{ base: "full", sm: "150px" }}>
              <Field.Label>Browser concurrency</Field.Label>
              <Input autoComplete="off"
                type="number" min={1} max={8}
                value={value.obscuraConcurrency}
                onChange={(e) => onChange({ obscuraConcurrency: num(e.target.value) })}
              />
            </Field.Root>
            <Field.Root w={{ base: "full", sm: "150px" }}>
              <Field.Label>Fetch timeout (s)</Field.Label>
              <Input autoComplete="off"
                type="number" min={1} max={120}
                value={value.fetchTimeoutSeconds}
                onChange={(e) => onChange({ fetchTimeoutSeconds: num(e.target.value) })}
              />
            </Field.Root>
            <Field.Root w={{ base: "full", sm: "150px" }}>
              <Field.Label>Browser timeout (s)</Field.Label>
              <Input autoComplete="off"
                type="number" min={5} max={300}
                value={value.obscuraTimeoutSeconds}
                onChange={(e) => onChange({ obscuraTimeoutSeconds: num(e.target.value) })}
              />
            </Field.Root>
            <Field.Root w={{ base: "full", sm: "150px" }}>
              <Field.Label>Max chars</Field.Label>
              <Input autoComplete="off"
                type="number" min={1000} max={200000}
                value={value.maxChars}
                onChange={(e) => onChange({ maxChars: num(e.target.value) })}
              />
            </Field.Root>
            <Switch.Root
              checked={value.obscuraStealth}
              onCheckedChange={(e) => onChange({ obscuraStealth: e.checked })}
            >
              <Switch.HiddenInput />
              <Switch.Control>
                <Switch.Thumb />
              </Switch.Control>
              <Switch.Label>Stealth mode</Switch.Label>
            </Switch.Root>
          </HStack>

          <HStack gap={3}>
            <Button size="sm" loading={test.isPending && test.variables === "searxng"} onClick={() => test.mutate("searxng")}>
              Test SearXNG
            </Button>
            <Button size="sm" loading={test.isPending && test.variables === "obscura"} onClick={() => test.mutate("obscura")}>
              Test obscura
            </Button>
          </HStack>
        </VStack>
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
              <Input autoComplete="off"
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
              <Input autoComplete="off"
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
              <Input autoComplete="off"
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
              <Input autoComplete="off"
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

      {/* web tools (MCP) */}
      <WebToolsCard
        value={form.webTools}
        onChange={(patch) => set({ webTools: { ...form.webTools, ...patch } })}
      />

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
