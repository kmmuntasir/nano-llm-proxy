import { useState } from "react"
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query"
import {
  Button,
  Card,
  Code,
  Field,
  Heading,
  HStack,
  IconButton,
  Input,
  Switch,
  Text,
  VStack,
} from "@chakra-ui/react"
import { Plus, Trash2 } from "lucide-react"
import { api, del, patch, post, ApiError } from "../api/client"
import type { ClientKeyView } from "../api/types"
import ConfirmDialog from "../components/ConfirmDialog"
import KeyRevealDialog from "../components/KeyRevealDialog"
import { toaster } from "../components/ui/toaster"
import { DataTable } from "../components/DataTable"

function fmtTs(ts: number) {
  return ts ? new Date(ts * 1000).toLocaleString() : "never"
}

export default function MyKeysPage() {
  const qc = useQueryClient()
  const { data, isLoading } = useQuery({
    queryKey: ["myKeys"],
    queryFn: () => api<{ keys: ClientKeyView[] }>("/api/me/keys"),
  })

  const [alias, setAlias] = useState("")
  const [revealed, setRevealed] = useState<string | null>(null)
  const [toDelete, setToDelete] = useState<ClientKeyView | null>(null)

  const create = useMutation({
    mutationFn: () => post<{ key: string }>("/api/me/keys", { alias }),
    onSuccess: async (res) => {
      setAlias("")
      setRevealed(res.key)
      await qc.invalidateQueries({ queryKey: ["myKeys"] })
      toaster.create({ title: "Key created", type: "success" })
    },
    onError: (e) =>
      toaster.create({ title: e instanceof ApiError ? e.message : "Failed to create the API key", type: "error" }),
  })

  const toggle = useMutation({
    mutationFn: ({ id, disabled }: { id: number; disabled: boolean }) =>
      patch(`/api/me/keys/${id}`, { disabled }),
    onSuccess: async () => {
      await qc.invalidateQueries({ queryKey: ["myKeys"] })
      toaster.create({ title: "Key updated", type: "success" })
    },
    onError: (e) =>
      toaster.create({ title: e instanceof ApiError ? e.message : "Failed to update the key", type: "error" }),
  })

  const remove = useMutation({
    mutationFn: (id: number) => del(`/api/me/keys/${id}`),
    onSuccess: async () => {
      setToDelete(null)
      await qc.invalidateQueries({ queryKey: ["myKeys"] })
      toaster.create({ title: "Key deleted", type: "success" })
    },
    onError: (e) =>
      toaster.create({ title: e instanceof ApiError ? e.message : "Failed to delete the key", type: "error" }),
  })

  const keys = data?.keys ?? []

  return (
    <VStack align="stretch" gap={6}>
      <Heading size="lg">My API Keys</Heading>

      <Card.Root>
        <Card.Header>
          <Heading size="sm">New key</Heading>
        </Card.Header>
        <Card.Body>
          <form
            onSubmit={(e) => {
              e.preventDefault()
              create.mutate()
            }}
          >
            <HStack gap={3} align="end">
              <Field.Root flex={1} maxW="sm">
                <Field.Label>Alias (optional)</Field.Label>
                <Input autoComplete="off"
                  placeholder="e.g. my laptop"
                  value={alias}
                  onChange={(e) => setAlias(e.target.value)}
                  maxLength={100}
                />
              </Field.Root>
              <Button type="submit" colorPalette="blue" loading={create.isPending}>
                <Plus /> Generate key
              </Button>
            </HStack>
          </form>
        </Card.Body>
      </Card.Root>

      <Card.Root>
        <Card.Header>
          <Heading size="sm">Keys</Heading>
        </Card.Header>
        <Card.Body pt={3}>
          {isLoading ? (
            <Text>Loading…</Text>
          ) : (
            <DataTable
              rows={keys}
              rowKey={(k) => String(k.id)}
              empty="No keys yet — generate one above."
              columns={[
                {
                  header: "Key",
                  render: (k) => (
                    <Text fontFamily="mono" fontSize="xs">
                      {k.keyHint}…
                    </Text>
                  ),
                },
                { header: "Alias", render: (k) => k.alias || "—" },
                { header: "Requests", render: (k) => k.requestCount },
                { header: "Last used", render: (k) => fmtTs(k.lastUsedAt) },
                {
                  header: "Enabled",
                  render: (k) => (
                    <Switch.Root
                      size="sm"
                      checked={!k.disabled}
                      onCheckedChange={(e) =>
                        toggle.mutate({ id: k.id, disabled: !e.checked })
                      }
                    >
                      <Switch.HiddenInput />
                      <Switch.Control>
                        <Switch.Thumb />
                      </Switch.Control>
                    </Switch.Root>
                  ),
                },
                {
                  header: "",
                  render: (k) => (
                    <IconButton
                      variant="ghost"
                      size="xs"
                      aria-label="delete key"
                      colorPalette="red"
                      onClick={() => setToDelete(k)}
                    >
                      <Trash2 />
                    </IconButton>
                  ),
                },
              ]}
            />
          )}
        </Card.Body>
      </Card.Root>

      <KeyRevealDialog plaintext={revealed} onClose={() => setRevealed(null)} />
      <ConfirmDialog
        open={toDelete !== null}
        onOpenChange={() => setToDelete(null)}
        title="Delete key?"
        body={
          <Text>
            Clients using <Code fontFamily="mono">{toDelete?.keyHint}…</Code> will get 401s
            immediately. This cannot be undone.
          </Text>
        }
        confirmText="Delete"
        destructive
        busy={remove.isPending}
        onConfirm={() => toDelete && remove.mutate(toDelete.id)}
      />
    </VStack>
  )
}
