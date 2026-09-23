import { useState } from "react"
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query"
import {
  Badge,
  Button,
  Card,
  Code,
  Field,
  Heading,
  HStack,
  IconButton,
  Input,
  NativeSelect,
  Switch,
  Table,
  Text,
  VStack,
} from "@chakra-ui/react"
import { KeyRound, Plus, Trash2 } from "lucide-react"
import { api, del, patch, post, ApiError } from "../api/client"
import type { ClientKeyView, UserView } from "../api/types"
import ConfirmDialog from "../components/ConfirmDialog"
import KeyRevealDialog from "../components/KeyRevealDialog"
import { PasswordInput } from "../components/ui/password-input"
import { Dialog } from "@chakra-ui/react"
import { toaster } from "../components/ui/toaster"
import { useSession } from "../App"

function fmtTs(ts: number) {
  return ts ? new Date(ts * 1000).toLocaleDateString() : "—"
}

// RoleField keeps the wire values lowercase (the API's vocabulary) while
// showing titlecase options.
function RoleField({ value, onChange }: { value: string; onChange: (v: string) => void }) {
  return (
    <Field.Root>
      <Field.Label>Role</Field.Label>
      <NativeSelect.Root size="sm">
        <NativeSelect.Field value={value} onChange={(e) => onChange(e.target.value)}>
          <option value="user">User</option>
          <option value="superadmin">Superadmin</option>
        </NativeSelect.Field>
        <NativeSelect.Indicator />
      </NativeSelect.Root>
    </Field.Root>
  )
}

// AddUserModal creates a user from a modal, invoked by the page-header button.
function AddUserModal({ open, onOpenChange }: { open: boolean; onOpenChange: (o: boolean) => void }) {
  const qc = useQueryClient()
  const [name, setName] = useState("")
  const [email, setEmail] = useState("")
  const [password, setPassword] = useState("")
  const [role, setRole] = useState("user")
  const [error, setError] = useState("")

  const create = useMutation({
    mutationFn: () => post<{ user: UserView }>("/api/users", { name, email, password, role }),
    onSuccess: async () => {
      setName("")
      setEmail("")
      setPassword("")
      setRole("user")
      setError("")
      onOpenChange(false)
      await qc.invalidateQueries({ queryKey: ["users"] })
      toaster.create({ title: "User created", type: "success" })
    },
    onError: (e) => setError(e instanceof ApiError ? e.message : "create failed"),
  })

  return (
    <Dialog.Root open={open} onOpenChange={(e) => onOpenChange(e.open)}>
      <Dialog.Backdrop />
      <Dialog.Positioner>
        <Dialog.Content as="form" maxW="440px">
          <Dialog.Header>
            <Dialog.Title>Add user</Dialog.Title>
          </Dialog.Header>
          <Dialog.Body
            as="form"
            onSubmit={(e) => {
              e.preventDefault()
              create.mutate()
            }}
          >
            <VStack align="stretch" gap={3}>
              <Field.Root required>
                <Field.Label>Name</Field.Label>
                <Input value={name} onChange={(e) => setName(e.target.value)} />
              </Field.Root>
              <Field.Root required>
                <Field.Label>Email</Field.Label>
                <Input type="email" value={email} onChange={(e) => setEmail(e.target.value)} />
              </Field.Root>
              <Field.Root required>
                <Field.Label>Password</Field.Label>
                <PasswordInput
                  value={password}
                  onChange={(e) => setPassword(e.target.value)}
                  autoComplete="new-password"
                />
                <Field.HelperText>min 10 chars</Field.HelperText>
              </Field.Root>
              <RoleField value={role} onChange={setRole} />
              {error && (
                <Text color="red.fg" fontSize="sm">
                  {error}
                </Text>
              )}
            </VStack>
          </Dialog.Body>
          <Dialog.Footer>
            <Dialog.ActionTrigger asChild>
              <Button variant="outline">Cancel</Button>
            </Dialog.ActionTrigger>
            <Button colorPalette="blue" loading={create.isPending} onClick={() => create.mutate()}>
              Create
            </Button>
          </Dialog.Footer>
          <Dialog.CloseTrigger />
        </Dialog.Content>
      </Dialog.Positioner>
    </Dialog.Root>
  )
}

// ResetPasswordModal lets a superadmin set any user a new password.
function ResetPasswordModal({
  user,
  open,
  onOpenChange,
}: {
  user: UserView
  open: boolean
  onOpenChange: (o: boolean) => void
}) {
  const qc = useQueryClient()
  const [password, setPassword] = useState("")
  const [error, setError] = useState("")

  const reset = useMutation({
    mutationFn: () => patch(`/api/users/${user.id}`, { password }),
    onSuccess: async () => {
      setPassword("")
      setError("")
      onOpenChange(false)
      await qc.invalidateQueries({ queryKey: ["users"] })
      toaster.create({
        title: `Password reset — ${user.email}'s sessions were logged out`,
        type: "success",
      })
    },
    onError: (e) => setError(e instanceof ApiError ? e.message : "reset failed"),
  })

  return (
    <Dialog.Root open={open} onOpenChange={(e) => onOpenChange(e.open)}>
      <Dialog.Backdrop />
      <Dialog.Positioner>
        <Dialog.Content as="form" maxW="420px">
          <Dialog.Header>
            <Dialog.Title>Reset password — {user.email}</Dialog.Title>
          </Dialog.Header>
          <Dialog.Body>
            <VStack align="stretch" gap={3}>
              <Field.Root required>
                <Field.Label>New password</Field.Label>
                <PasswordInput
                  value={password}
                  onChange={(e) => setPassword(e.target.value)}
                  autoComplete="new-password"
                />
                <Field.HelperText>
                  min 10 chars; all their sessions are logged out
                </Field.HelperText>
              </Field.Root>
              {error && (
                <Text color="red.fg" fontSize="sm">
                  {error}
                </Text>
              )}
            </VStack>
          </Dialog.Body>
          <Dialog.Footer>
            <Dialog.ActionTrigger asChild>
              <Button variant="outline">Cancel</Button>
            </Dialog.ActionTrigger>
            <Button
              colorPalette="blue"
              loading={reset.isPending}
              onClick={() => reset.mutate()}
            >
              Reset password
            </Button>
          </Dialog.Footer>
          <Dialog.CloseTrigger />
        </Dialog.Content>
      </Dialog.Positioner>
    </Dialog.Root>
  )
}

// UserKeysRow renders an expandable sub-row with the user's client keys.
function UserKeysRow({ userID, onClose }: { userID: number; onClose: () => void }) {
  const qc = useQueryClient()
  const [revealed, setRevealed] = useState<string | null>(null)
  const { data } = useQuery({
    queryKey: ["userKeys", userID],
    queryFn: () => api<{ keys: ClientKeyView[] }>(`/api/users/${userID}/keys`),
  })

  const create = useMutation({
    mutationFn: () => post<{ key: string }>(`/api/users/${userID}/keys`, { alias: "gui" }),
    onSuccess: async (res) => {
      setRevealed(res.key)
      await qc.invalidateQueries({ queryKey: ["userKeys", userID] })
    },
  })
  const remove = useMutation({
    mutationFn: (id: number) => del(`/api/users/${userID}/keys/${id}`),
    onSuccess: () => void qc.invalidateQueries({ queryKey: ["userKeys", userID] }),
  })
  const toggle = useMutation({
    mutationFn: ({ id, disabled }: { id: number; disabled: boolean }) =>
      patch(`/api/users/${userID}/keys/${id}`, { disabled }),
    onSuccess: () => void qc.invalidateQueries({ queryKey: ["userKeys", userID] }),
  })

  return (
    <Table.Row bg="bg.subtle">
      <Table.Cell colSpan={7}>
        <HStack mb={2}>
          <Button size="xs" colorPalette="blue" onClick={() => create.mutate()}>
            <Plus /> New key
          </Button>
          <Button size="xs" variant="ghost" onClick={onClose}>
            Close
          </Button>
        </HStack>
        <VStack align="stretch" gap={1}>
          {(data?.keys ?? []).map((k) => (
            <HStack key={k.id} gap={3} fontSize="sm">
              <Code fontFamily="mono">{k.keyHint}…</Code>
              <Text>{k.alias || "—"}</Text>
              <Text color="fg.muted">{k.requestCount} reqs</Text>
              <Switch.Root
                size="sm"
                checked={!k.disabled}
                onCheckedChange={(e) => toggle.mutate({ id: k.id, disabled: !e.checked })}
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
                onClick={() => remove.mutate(k.id)}
              >
                <Trash2 />
              </IconButton>
            </HStack>
          ))}
          {(data?.keys ?? []).length === 0 && (
            <Text fontSize="sm" color="fg.muted">
              No keys for this user yet.
            </Text>
          )}
        </VStack>
        <KeyRevealDialog plaintext={revealed} onClose={() => setRevealed(null)} />
      </Table.Cell>
    </Table.Row>
  )
}

function UserRow({ u, isSelf }: { u: UserView; isSelf: boolean }) {
  const qc = useQueryClient()
  const [toDelete, setToDelete] = useState(false)
  const [managingKeys, setManagingKeys] = useState(false)
  const [resetting, setResetting] = useState(false)

  const patchU = useMutation({
    mutationFn: (body: Record<string, unknown>) => patch(`/api/users/${u.id}`, body),
    onSuccess: () => void qc.invalidateQueries({ queryKey: ["users"] }),
    onError: (e) =>
      toaster.create({ title: e instanceof ApiError ? e.message : "update failed", type: "error" }),
  })

  const removeU = useMutation({
    mutationFn: () => del(`/api/users/${u.id}`),
    onSuccess: () => {
      setToDelete(false)
      void qc.invalidateQueries({ queryKey: ["users"] })
    },
    onError: (e) =>
      toaster.create({ title: e instanceof ApiError ? e.message : "delete failed", type: "error" }),
  })

  return (
    <Table.Row opacity={u.disabled ? 0.5 : 1}>
      <Table.Cell>{u.name}</Table.Cell>
      <Table.Cell>{u.email}</Table.Cell>
      <Table.Cell>
        <Badge variant={u.role === "superadmin" ? "solid" : "subtle"}>
          {u.role === "superadmin" ? "Superadmin" : "User"}
        </Badge>
      </Table.Cell>
      <Table.Cell>{u.keyCount ?? 0}</Table.Cell>
      <Table.Cell>{fmtTs(u.createdAt)}</Table.Cell>
      <Table.Cell>
        {isSelf ? (
          <Text fontSize="xs" color="fg.muted">
            you
          </Text>
        ) : (
          <Switch.Root
            checked={!u.disabled}
            onCheckedChange={(e) => patchU.mutate({ disabled: !e.checked })}
          >
            <Switch.HiddenInput />
            <Switch.Control>
              <Switch.Thumb />
            </Switch.Control>
          </Switch.Root>
        )}
      </Table.Cell>
      <Table.Cell textAlign="end">
        <HStack justify="end">
          <Button variant="ghost" size="xs" onClick={() => setManagingKeys((v) => !v)}>
            Keys
          </Button>
          {!isSelf && (
            <Button
              variant="ghost"
              size="xs"
              onClick={() => setResetting(true)}
              aria-label={`reset password for ${u.email}`}
            >
              <KeyRound /> Reset password
            </Button>
          )}
          {!isSelf && (
            <IconButton
              variant="ghost"
              size="xs"
              aria-label="delete user"
              colorPalette="red"
              onClick={() => setToDelete(true)}
            >
              <Trash2 />
            </IconButton>
          )}
        </HStack>
      </Table.Cell>
      {managingKeys && <UserKeysRow userID={u.id} onClose={() => setManagingKeys(false)} />}
      <ResetPasswordModal user={u} open={resetting} onOpenChange={setResetting} />
      <ConfirmDialog
        open={toDelete}
        onOpenChange={() => setToDelete(false)}
        title={`Delete ${u.name}?`}
        body={<Text>Their client keys and sessions are removed immediately.</Text>}
        confirmText="Delete user"
        destructive
        busy={removeU.isPending}
        onConfirm={() => removeU.mutate()}
      />
    </Table.Row>
  )
}

export default function UsersPage() {
  const { data: me } = useSession()
  const [adding, setAdding] = useState(false)
  const { data, isLoading, error } = useQuery({
    queryKey: ["users"],
    queryFn: () => api<{ users: UserView[] }>("/api/users"),
  })

  return (
    <VStack align="stretch" gap={6}>
      <HStack justify="space-between">
        <Heading size="lg">Users</Heading>
        <Button colorPalette="blue" size="sm" onClick={() => setAdding(true)}>
          <Plus /> Add user
        </Button>
      </HStack>
      <Card.Root>
        <Card.Body pt={0}>
          {isLoading ? (
            <Text>Loading…</Text>
          ) : error ? (
            <Text color="red.fg">Failed to load users</Text>
          ) : (
            <Table.Root size="sm">
              <Table.Header>
                <Table.Row>
                  <Table.ColumnHeader>Name</Table.ColumnHeader>
                  <Table.ColumnHeader>Email</Table.ColumnHeader>
                  <Table.ColumnHeader>Role</Table.ColumnHeader>
                  <Table.ColumnHeader>Keys</Table.ColumnHeader>
                  <Table.ColumnHeader>Created</Table.ColumnHeader>
                  <Table.ColumnHeader>Enabled</Table.ColumnHeader>
                  <Table.ColumnHeader />
                </Table.Row>
              </Table.Header>
              <Table.Body>
                {(data?.users ?? []).map((u) => (
                  <UserRow key={u.id} u={u} isSelf={u.id === me?.user.id} />
                ))}
              </Table.Body>
            </Table.Root>
          )}
        </Card.Body>
      </Card.Root>
      <AddUserModal open={adding} onOpenChange={setAdding} />
    </VStack>
  )
}
