import { useState } from "react"
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
  Stack,
  Switch,
  Text,
  VStack,
} from "@chakra-ui/react"
import { ChevronDown, ChevronUp, List, Plus, Trash2 } from "lucide-react"
import { api, del, patch, post, put, ApiError } from "../api/client"
import ModelCard from "../components/ModelCard"
import type { ProviderKeyView, ProviderView } from "../api/types"
import ConfirmDialog from "../components/ConfirmDialog"
import StatusBadge from "../components/StatusBadge"
import { toaster } from "../components/ui/toaster"

// AddProviderCard creates a new generic openai-type provider. A provider needs
// at least one endpoint root: OpenAI-compatible and/or Anthropic-compatible
// (e.g. the Z.ai GLM Coding Plan exposes both). The two builtin providers
// (zen/opencode, kilo/openai) are seeded and only editable.
function AddProviderCard() {
  const qc = useQueryClient()
  const [name, setName] = useState("")
  const [baseURL, setBaseURL] = useState("")
  const [anthropicURL, setAnthropicURL] = useState("")
  const [key, setKey] = useState("")
  const [error, setError] = useState("")

  const create = useMutation({
    mutationFn: () =>
      post<{ id: number }>("/api/providers", {
        name,
        baseUrl: baseURL,
        anthropicBaseUrl: anthropicURL,
        keys: [key],
      }),
    onSuccess: async () => {
      setName("")
      setBaseURL("")
      setAnthropicURL("")
      setKey("")
      setError("")
      await qc.invalidateQueries({ queryKey: ["providers"] })
      toaster.create({ title: "Provider added", type: "success" })
    },
    onError: (e) => setError(e instanceof ApiError ? e.message : "Failed to create the provider"),
  })

  return (
    <Card.Root>
      <Card.Header>
        <Heading size="sm">Add provider</Heading>
        <Text fontSize="xs" color="fg.muted">
          The name becomes the model prefix (<Code fontFamily="mono">name/model-id</Code>).
          At least one endpoint is required — OpenAI-compatible, Anthropic-compatible, or
          both (Z.ai GLM Coding Plan has each; /v1/messages is served natively when the
          Anthropic root is set). opencode-style providers can't be added — zen is one of a kind.
        </Text>
      </Card.Header>
      <Card.Body>
        <form
          onSubmit={(e) => {
            e.preventDefault()
            if (baseURL.trim() === "" && anthropicURL.trim() === "") {
              setError("At least one endpoint is required: base URL and/or Anthropic endpoint")
              return
            }
            create.mutate()
          }}
        >
          <HStack gap={3} align="end" flexWrap="wrap">
            <Field.Root required minW="140px">
              <Field.Label>Name</Field.Label>
              <Input
                placeholder="e.g. together"
                value={name}
                onChange={(e) => setName(e.target.value.toLowerCase())}
                fontFamily="mono"
              />
            </Field.Root>
            <Field.Root minW="280px" flex={1}>
              <Field.Label>OpenAI-compatible endpoint</Field.Label>
              <Input
                placeholder="https://api.example.com/v1"
                value={baseURL}
                onChange={(e) => setBaseURL(e.target.value)}
                fontFamily="mono"
              />
            </Field.Root>
            <Field.Root minW="280px" flex={1}>
              <Field.Label>Anthropic-compatible endpoint</Field.Label>
              <Input
                placeholder="https://api.z.ai/api/anthropic"
                value={anthropicURL}
                onChange={(e) => setAnthropicURL(e.target.value)}
                fontFamily="mono"
              />
            </Field.Root>
            <Field.Root required minW="220px" flex={1}>
              <Field.Label>API key</Field.Label>
              <Input
                type="password"
                value={key}
                onChange={(e) => setKey(e.target.value)}
                fontFamily="mono"
              />
            </Field.Root>
            <Button type="submit" colorPalette="blue" loading={create.isPending}>
              <Plus /> Add
            </Button>
          </HStack>
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
      <Code fontFamily="mono" fontSize="xs">
        {k.label || k.hash || `#${k.id}`}
      </Code>
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
// zen builtin, each card carries a Responses-API toggle (written straight to
// the per-model flag in settings); generic providers are view-only.
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
  const isZen = provider.name === "zen"
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
              <Input
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
                    provider={provider.name}
                    description={m.description}
                    contextWindow={m.context_window}
                    maxOutputTokens={m.max_output_tokens}
                    inputModalities={m.input_modalities}
                    reasoning={m.reasoning}
                    responsesApi={m.responses_api}
                    free={m.free}
                    footer={
                      isZen ? (
                        <HStack justify="space-between" pt={1}>
                          <Text fontSize="xs" color="fg.muted">
                            Serve on /v1/responses
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

function ProviderCard({ p }: { p: ProviderView }) {
  const qc = useQueryClient()
  const [newKey, setNewKey] = useState("")
  const [toDelete, setToDelete] = useState(false)
  const [editField, setEditField] = useState<null | "baseUrl" | "anthropicBaseUrl">(null)
  const [editURL, setEditURL] = useState("")
  const [keysOpen, setKeysOpen] = useState(false)
  const [modelsOpen, setModelsOpen] = useState(false)
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
    mutationFn: () => post(`/api/providers/${p.id}/keys`, { key: newKey, label: "gui" }),
    onSuccess: async () => {
      setNewKey("")
      invalidate()
      toaster.create({ title: "Key added", type: "success" })
    },
    onError: (e) =>
      toaster.create({ title: e instanceof ApiError ? e.message : "Failed to add the key", type: "error" }),
  })

  return (
    <Card.Root>
      <Card.Body pt={4} gap={3}>
        <HStack justify="space-between" flexWrap="wrap" gap={2}>
          <HStack gap={2} flexWrap="wrap">
            <Heading size="sm" fontFamily="mono">
              {p.name}
            </Heading>
            <Badge variant="outline">{p.type}</Badge>
            {p.builtin && <Badge variant="solid" colorPalette="purple">builtin</Badge>}
            <Badge colorPalette={p.healthy > 0 ? "green" : "red"}>
              {p.healthy}/{p.total} healthy
            </Badge>
          </HStack>
          <HStack gap={3}>
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
                    <Input
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
                  <HStack gap={2} mt={2}>
                    <Input
                      size="xs"
                      type="password"
                      placeholder="add upstream API key"
                      value={newKey}
                      onChange={(e) => setNewKey(e.target.value)}
                      fontFamily="mono"
                      maxW="280px"
                    />
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

export default function ProvidersPage() {
  const { data, isLoading, error } = useQuery({
    queryKey: ["providers"],
    queryFn: () => api<{ providers: ProviderView[] }>("/api/providers"),
  })

  return (
    <VStack align="stretch" gap={6}>
      <Heading size="lg">Providers</Heading>
      <AddProviderCard />
      {isLoading && <Text>Loading…</Text>}
      {error && <Text color="red.fg">Failed to load providers</Text>}
      <VStack align="stretch" gap={4}>
        {(data?.providers ?? []).map((p) => (
          <ProviderCard key={p.id} p={p} />
        ))}
      </VStack>
    </VStack>
  )
}
