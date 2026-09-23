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
  Table,
  Text,
  VStack,
} from "@chakra-ui/react"
import { FiPlus, FiTrash2 } from "react-icons/fi"
import { api, del, patch, post, ApiError } from "../api/client"
import type { ClientKeyView } from "../api/types"
import ConfirmDialog from "../components/ConfirmDialog"
import KeyRevealDialog from "../components/KeyRevealDialog"
import { toaster } from "../components/ui/toaster"

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
    },
    onError: (e) =>
      toaster.create({ title: e instanceof ApiError ? e.message : "create failed", type: "error" }),
  })

  const toggle = useMutation({
    mutationFn: ({ id, disabled }: { id: number; disabled: boolean }) =>
      patch(`/api/me/keys/${id}`, { disabled }),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["myKeys"] }),
    onError: (e) =>
      toaster.create({ title: e instanceof ApiError ? e.message : "update failed", type: "error" }),
  })

  const remove = useMutation({
    mutationFn: (id: number) => del(`/api/me/keys/${id}`),
    onSuccess: () => {
      setToDelete(null)
      void qc.invalidateQueries({ queryKey: ["myKeys"] })
    },
    onError: (e) =>
      toaster.create({ title: e instanceof ApiError ? e.message : "delete failed", type: "error" }),
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
                <Input
                  placeholder="e.g. my laptop"
                  value={alias}
                  onChange={(e) => setAlias(e.target.value)}
                  maxLength={100}
                />
              </Field.Root>
              <Button type="submit" colorPalette="blue" loading={create.isPending}>
                <FiPlus /> Generate key
              </Button>
            </HStack>
          </form>
        </Card.Body>
      </Card.Root>

      <Card.Root>
        <Card.Header>
          <Heading size="sm">Keys</Heading>
        </Card.Header>
        <Card.Body pt={0}>
          {isLoading ? (
            <Text>Loading…</Text>
          ) : (
            <Table.Root size="sm">
              <Table.Header>
                <Table.Row>
                  <Table.ColumnHeader>Key</Table.ColumnHeader>
                  <Table.ColumnHeader>Alias</Table.ColumnHeader>
                  <Table.ColumnHeader>Requests</Table.ColumnHeader>
                  <Table.ColumnHeader>Last used</Table.ColumnHeader>
                  <Table.ColumnHeader>Enabled</Table.ColumnHeader>
                  <Table.ColumnHeader />
                </Table.Row>
              </Table.Header>
              <Table.Body>
                {keys.length === 0 && (
                  <Table.Row>
                    <Table.Cell colSpan={6} color="fg.muted" textAlign="center">
                      No keys yet — generate one above.
                    </Table.Cell>
                  </Table.Row>
                )}
                {keys.map((k) => (
                  <Table.Row key={k.id}>
                    <Table.Cell fontFamily="mono" fontSize="xs">
                      {k.keyHint}…
                    </Table.Cell>
                    <Table.Cell>{k.alias || "—"}</Table.Cell>
                    <Table.Cell>{k.requestCount}</Table.Cell>
                    <Table.Cell whiteSpace="nowrap">{fmtTs(k.lastUsedAt)}</Table.Cell>
                    <Table.Cell>
                      <Switch.Root
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
                    </Table.Cell>
                    <Table.Cell textAlign="end">
                      <IconButton
                        variant="ghost"
                        size="xs"
                        aria-label="delete key"
                        colorPalette="red"
                        onClick={() => setToDelete(k)}
                      >
                        <FiTrash2 />
                      </IconButton>
                    </Table.Cell>
                  </Table.Row>
                ))}
              </Table.Body>
            </Table.Root>
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
