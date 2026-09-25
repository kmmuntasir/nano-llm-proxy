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
  SimpleGrid,
  NativeSelect,
  Switch,
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
  const [confirm, setConfirm] = useState("")
  const [role, setRole] = useState("user")
  const [error, setError] = useState("")

  const create = useMutation({
    mutationFn: () => post<{ user: UserView }>("/api/users", { name, email, password, role }),
    onSuccess: async () => {
      setName("")
      setEmail("")
      setPassword("")
      setConfirm("")
      setRole("user")
      setError("")
      onOpenChange(false)
      await qc.invalidateQueries({ queryKey: ["users"] })
      toaster.create({ title: "User created", type: "success" })
    },
    onError: (e) => {
      const msg = e instanceof ApiError ? e.message : "create failed"
      setError(msg)
      toaster.create({ title: msg, type: "error" })
    },
  })

  const submit = () => {
    setError("")
    if (password !== confirm) {
      setError("Passwords don't match")
      return
    }
    create.mutate()
  }

  return (
    <Dialog.Root open={open} onOpenChange={(e) => onOpenChange(e.open)}>
      <Dialog.Backdrop />
      <Dialog.Positioner>
        <Dialog.Content
          as="form"
          maxW="440px"
          onSubmit={(e) => {
            e.preventDefault()
            submit()
          }}
        >
          <Dialog.Header>
            <Dialog.Title>Add user</Dialog.Title>
          </Dialog.Header>
          <Dialog.Body>
            <VStack align="stretch" gap={3}>
              <Field.Root required>
                <Field.Label>Name <Field.RequiredIndicator /></Field.Label>
                <Input autoComplete="off" value={name} onChange={(e) => setName(e.target.value)} />
              </Field.Root>
              <Field.Root required>
                <Field.Label>Email <Field.RequiredIndicator /></Field.Label>
                <Input autoComplete="off" type="email" value={email} onChange={(e) => setEmail(e.target.value)} />
              </Field.Root>
              <Field.Root required>
                <Field.Label>New password <Field.RequiredIndicator /></Field.Label>
                <PasswordInput
                  value={password}
                  onChange={(e) => setPassword(e.target.value)}
                  autoComplete="new-password"
                />
                <Field.HelperText>min 10 chars</Field.HelperText>
              </Field.Root>
              <Field.Root required>
                <Field.Label>Confirm password <Field.RequiredIndicator /></Field.Label>
                <PasswordInput
                  value={confirm}
                  onChange={(e) => setConfirm(e.target.value)}
                  autoComplete="new-password"
                />
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
              <Button type="button" variant="outline">
                Cancel
              </Button>
            </Dialog.ActionTrigger>
            <Button type="submit" colorPalette="blue" loading={create.isPending}>
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
  const [confirm, setConfirm] = useState("")
  const [error, setError] = useState("")

  const reset = useMutation({
    mutationFn: () => patch(`/api/users/${user.id}`, { password }),
    onSuccess: async () => {
      setPassword("")
      setConfirm("")
      setError("")
      onOpenChange(false)
      await qc.invalidateQueries({ queryKey: ["users"] })
      toaster.create({
        title: `Password reset — ${user.email}'s sessions were logged out`,
        type: "success",
      })
    },
    onError: (e) => {
      const msg = e instanceof ApiError ? e.message : "Failed to reset the password"
      setError(msg)
      toaster.create({ title: msg, type: "error" })
    },
  })

  const submit = () => {
    setError("")
    if (password !== confirm) {
      setError("passwords don't match")
      return
    }
    reset.mutate()
  }

  return (
    <Dialog.Root open={open} onOpenChange={(e) => onOpenChange(e.open)}>
      <Dialog.Backdrop />
      <Dialog.Positioner>
        <Dialog.Content
          as="form"
          maxW="420px"
          onSubmit={(e) => {
            e.preventDefault()
            submit()
          }}
        >
          <Dialog.Header>
            <Dialog.Title>Reset password — {user.email}</Dialog.Title>
          </Dialog.Header>
          <Dialog.Body>
            <VStack align="stretch" gap={3}>
              <Field.Root required>
                <Field.Label>New password <Field.RequiredIndicator /></Field.Label>
                <PasswordInput
                  value={password}
                  onChange={(e) => setPassword(e.target.value)}
                  autoComplete="new-password"
                />
                <Field.HelperText>
                  min 10 chars; all their sessions are logged out
                </Field.HelperText>
              </Field.Root>
              <Field.Root required>
                <Field.Label>Confirm password <Field.RequiredIndicator /></Field.Label>
                <PasswordInput
                  value={confirm}
                  onChange={(e) => setConfirm(e.target.value)}
                  autoComplete="new-password"
                />
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
              <Button type="button" variant="outline">
                Cancel
              </Button>
            </Dialog.ActionTrigger>
            <Button type="submit" colorPalette="blue" loading={reset.isPending}>
              Reset password
            </Button>
          </Dialog.Footer>
          <Dialog.CloseTrigger />
        </Dialog.Content>
      </Dialog.Positioner>
    </Dialog.Root>
  )
}

// UserKeysModal manages one user's client keys.
function UserKeysModal({
  userID,
  open,
  onOpenChange,
}: {
  userID: number
  open: boolean
  onOpenChange: (o: boolean) => void
}) {
  const qc = useQueryClient()
  const [revealed, setRevealed] = useState<string | null>(null)
  const { data } = useQuery({
    queryKey: ["userKeys", userID],
    queryFn: () => api<{ keys: ClientKeyView[] }>(`/api/users/${userID}/keys`),
    enabled: open,
  })

  const create = useMutation({
    mutationFn: () => post<{ key: string }>(`/api/users/${userID}/keys`, { alias: "gui" }),
    onSuccess: async (res) => {
      setRevealed(res.key)
      await qc.invalidateQueries({ queryKey: ["userKeys", userID] })
      await qc.invalidateQueries({ queryKey: ["users"] })
      toaster.create({ title: "Key created", type: "success" })
    },
    onError: (e) =>
      toaster.create({ title: e instanceof ApiError ? e.message : "create failed", type: "error" }),
  })
  const remove = useMutation({
    mutationFn: (id: number) => del(`/api/users/${userID}/keys/${id}`),
    onSuccess: async () => {
      await qc.invalidateQueries({ queryKey: ["userKeys", userID] })
      await qc.invalidateQueries({ queryKey: ["users"] })
      toaster.create({ title: "Key deleted", type: "success" })
    },
    onError: (e) =>
      toaster.create({ title: e instanceof ApiError ? e.message : "delete failed", type: "error" }),
  })
  const toggle = useMutation({
    mutationFn: ({ id, disabled }: { id: number; disabled: boolean }) =>
      patch(`/api/users/${userID}/keys/${id}`, { disabled }),
    onSuccess: async () => {
      await qc.invalidateQueries({ queryKey: ["userKeys", userID] })
      toaster.create({ title: "Key updated", type: "success" })
    },
    onError: (e) =>
      toaster.create({ title: e instanceof ApiError ? e.message : "update failed", type: "error" }),
  })

  return (
    <Dialog.Root open={open} onOpenChange={(e) => onOpenChange(e.open)} size="lg">
      <Dialog.Backdrop />
      <Dialog.Positioner>
        <Dialog.Content>
          <Dialog.Header pb={2}>
            <Dialog.Title>Client keys</Dialog.Title>
          </Dialog.Header>
          <Dialog.Body>
            <Button size="xs" colorPalette="blue" mb={3} onClick={() => create.mutate()}>
              <Plus /> New key
            </Button>
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
          </Dialog.Body>
          <Dialog.CloseTrigger />
        </Dialog.Content>
      </Dialog.Positioner>
    </Dialog.Root>
  )
}

function UserCard({ u, isSelf }: { u: UserView; isSelf: boolean }) {
  const qc = useQueryClient()
  const [toDelete, setToDelete] = useState(false)
  const [managingKeys, setManagingKeys] = useState(false)
  const [resetting, setResetting] = useState(false)

  const patchU = useMutation({
    mutationFn: (body: Record<string, unknown>) => patch(`/api/users/${u.id}`, body),
    onSuccess: async () => {
      await qc.invalidateQueries({ queryKey: ["users"] })
      toaster.create({ title: "User updated", type: "success" })
    },
    onError: (e) =>
      toaster.create({ title: e instanceof ApiError ? e.message : "update failed", type: "error" }),
  })

  const removeU = useMutation({
    mutationFn: () => del(`/api/users/${u.id}`),
    onSuccess: async () => {
      setToDelete(false)
      await qc.invalidateQueries({ queryKey: ["users"] })
      toaster.create({ title: "User deleted", type: "success" })
    },
    onError: (e) =>
      toaster.create({ title: e instanceof ApiError ? e.message : "delete failed", type: "error" }),
  })

  return (
    <Card.Root size="sm" opacity={u.disabled ? 0.5 : 1}>
      <Card.Body gap={2} px={4} py={3}>
        <HStack justify="space-between" flexWrap="wrap" gap={2}>
          <VStack align="start" gap={0}>
            <HStack gap={2}>
              <Text fontWeight="medium">{u.name}</Text>
              <Badge variant={u.role === "superadmin" ? "solid" : "subtle"}>
                {u.role === "superadmin" ? "Superadmin" : "User"}
              </Badge>
            </HStack>
            <Text fontSize="xs" color="fg.muted">
              {u.email} · {u.keyCount ?? 0} keys · created {fmtTs(u.createdAt)}
            </Text>
          </VStack>
          <HStack gap={2} flexWrap="wrap">
            {isSelf ? (
              <Text fontSize="xs" color="fg.muted">
                you
              </Text>
            ) : (
              <Switch.Root
                size="sm"
                checked={!u.disabled}
                onCheckedChange={(e) => patchU.mutate({ disabled: !e.checked })}
              >
                <Switch.HiddenInput />
                <Switch.Control>
                  <Switch.Thumb />
                </Switch.Control>
              </Switch.Root>
            )}
          </HStack>
        </HStack>
        <HStack gap={1} flexWrap="wrap">
          <Button variant="ghost" size="2xs" onClick={() => setManagingKeys(true)}>
            Keys
          </Button>
          {!isSelf && (
            <Button variant="ghost" size="2xs" onClick={() => setResetting(true)}>
              <KeyRound /> Reset password
            </Button>
          )}
          {!isSelf && (
            <IconButton
              variant="ghost"
              size="2xs"
              aria-label="delete user"
              colorPalette="red"
              onClick={() => setToDelete(true)}
            >
              <Trash2 />
            </IconButton>
          )}
        </HStack>
      </Card.Body>
      <UserKeysModal userID={u.id} open={managingKeys} onOpenChange={setManagingKeys} />
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
    </Card.Root>
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
      <HStack justify="space-between" flexWrap="wrap" gap={2}>
        <Heading size="lg">Users</Heading>
        <Button colorPalette="blue" size="sm" onClick={() => setAdding(true)}>
          <Plus /> Add user
        </Button>
      </HStack>
      {isLoading && <Text>Loading…</Text>}
      {error && <Text color="red.fg">Failed to load users</Text>}
      {!isLoading && !error && (
        <SimpleGrid columns={{ base: 1, md: 2, xl: 3 }} gap={4}>
          {(data?.users ?? []).map((u) => (
            <UserCard key={u.id} u={u} isSelf={u.id === me?.user.id} />
          ))}
        </SimpleGrid>
      )}
      <AddUserModal open={adding} onOpenChange={setAdding} />
    </VStack>
  )
}
