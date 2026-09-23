import { useState } from "react"
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query"
import {
  Badge,
  Button,
  Card,
  Code,
  Collapsible,
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
import { ChevronDown, ChevronUp, Plus, Trash2 } from "lucide-react"
import { api, del, patch, post, ApiError } from "../api/client"
import type { ProviderKeyView, ProviderView } from "../api/types"
import ConfirmDialog from "../components/ConfirmDialog"
import StatusBadge from "../components/StatusBadge"
import { toaster } from "../components/ui/toaster"

// AddProviderCard creates a new generic openai-type provider. The two builtin
// providers (zen/opencode, kilo/openai) are seeded and only editable.
function AddProviderCard() {
  const qc = useQueryClient()
  const [name, setName] = useState("")
  const [baseURL, setBaseURL] = useState("")
  const [key, setKey] = useState("")
  const [error, setError] = useState("")

  const create = useMutation({
    mutationFn: () =>
      post<{ id: number }>("/api/providers", { name, baseUrl: baseURL, keys: [key] }),
    onSuccess: async () => {
      setName("")
      setBaseURL("")
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
        <Heading size="sm">Add OpenAI-compatible provider</Heading>
        <Text fontSize="xs" color="fg.muted">
          The name becomes the model prefix (<Code fontFamily="mono">name/model-id</Code>).
          opencode-style providers can't be added — zen is one of a kind.
        </Text>
      </Card.Header>
      <Card.Body>
        <form
          onSubmit={(e) => {
            e.preventDefault()
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
            <Field.Root required minW="280px" flex={1}>
              <Field.Label>Base URL</Field.Label>
              <Input
                placeholder="https://api.example.com/v1"
                value={baseURL}
                onChange={(e) => setBaseURL(e.target.value)}
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

function ProviderCard({ p }: { p: ProviderView }) {
  const qc = useQueryClient()
  const [newKey, setNewKey] = useState("")
  const [toDelete, setToDelete] = useState(false)
  const [editURL, setEditURL] = useState<string | null>(null)
  const [keysOpen, setKeysOpen] = useState(false)
  const invalidate = () => void qc.invalidateQueries({ queryKey: ["providers"] })

  const patchP = useMutation({
    mutationFn: (body: Record<string, unknown>) => patch(`/api/providers/${p.id}`, body),
    onSuccess: async () => {
      setEditURL(null)
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
        <HStack justify="space-between">
          <HStack gap={2}>
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

        <HStack fontSize="xs" color="fg.muted">
          {editURL === null ? (
            <>
              <Text fontFamily="mono" truncate>
                {p.baseUrl}
              </Text>
              <Button variant="ghost" size="2xs" onClick={() => setEditURL(p.baseUrl)}>
                Edit
              </Button>
            </>
          ) : (
            <>
              <Input
                size="xs"
                fontFamily="mono"
                value={editURL}
                onChange={(e) => setEditURL(e.target.value)}
                maxW="360px"
              />
              <Button size="2xs" colorPalette="blue" onClick={() => patchP.mutate({ baseUrl: editURL })}>
                Save
              </Button>
              <Button size="2xs" variant="ghost" onClick={() => setEditURL(null)}>
                Cancel
              </Button>
            </>
          )}
        </HStack>

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
            </Collapsible.Content>
          </Card.Root>
        </Collapsible.Root>
      </Card.Body>

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
