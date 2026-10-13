import { useState } from "react"
import { useMutation, useQueryClient } from "@tanstack/react-query"
import {
  Badge,
  Button,
  Card,
  Dialog,
  Field,
  Heading,
  HStack,
  Input,
  Text,
  VStack,
} from "@chakra-ui/react"
import { useNavigate } from "react-router-dom"
import { KeyRound } from "lucide-react"
import { patch, post, ApiError } from "../api/client"
import { useCredentialLogin } from "../api/useAuthConfig"
import type { UserView } from "../api/types"
import { PasswordInput } from "../components/ui/password-input"
import { toaster } from "../components/ui/toaster"
import UserAvatar from "../components/UserAvatar"
import { useSession } from "../App"
import type { Session } from "../App"
import MyKeysPage from "./MyKeysPage"

// NameField edits the signed-in user's display name. Google-linked users get
// it read-only: the server rewrites it from the Google profile on each sign-in.
function NameField({ user }: { user: UserView }) {
  const qc = useQueryClient()
  const [name, setName] = useState(user.name)
  const trimmed = name.trim()

  const save = useMutation({
    mutationFn: () => patch<Session>("/api/me", { name: trimmed }),
    onSuccess: async (res) => {
      qc.setQueryData<Session>(["me"], res)
      setName(res.user.name)
      await qc.invalidateQueries({ queryKey: ["users"] })
      toaster.create({ title: "Name updated", type: "success" })
    },
    onError: (e) =>
      toaster.create({
        title: e instanceof ApiError ? e.message : "Failed to update the name",
        type: "error",
      }),
  })

  if (user.googleLinked) {
    return (
      <VStack align="start" gap={0}>
        <Text fontSize="xs" color="fg.muted">
          Name
        </Text>
        <Text wordBreak="break-word">{user.name}</Text>
        <Text fontSize="xs" color="fg.muted" mt={1}>
          Your name comes from your Google account and updates each time you sign in
          with Google.
        </Text>
      </VStack>
    )
  }

  return (
    <Field.Root
      as="form"
      maxW="md"
      onSubmit={(e) => {
        e.preventDefault()
        if (!trimmed || trimmed === user.name) return
        save.mutate()
      }}
    >
      <Field.Label fontSize="xs" color="fg.muted">
        Name
      </Field.Label>
      <HStack gap={2} w="full">
        <Input
          size="sm"
          flex="1"
          minW={0}
          autoComplete="name"
          value={name}
          onChange={(e) => setName(e.target.value)}
        />
        <Button
          type="submit"
          size="sm"
          colorPalette="blue"
          loading={save.isPending}
          disabled={!trimmed || trimmed === user.name}
        >
          Save
        </Button>
      </HStack>
    </Field.Root>
  )
}

// ChangePasswordModal: self-service reset gated on the current password. The
// server wipes every session on success, so the client bounces to login.
function ChangePasswordModal({ open, onOpenChange }: { open: boolean; onOpenChange: (o: boolean) => void }) {
  const qc = useQueryClient()
  const navigate = useNavigate()
  const [current, setCurrent] = useState("")
  const [next, setNext] = useState("")
  const [confirm, setConfirm] = useState("")
  const [error, setError] = useState("")

  const change = useMutation({
    mutationFn: () => post("/api/me/password", { currentPassword: current, newPassword: next }),
    onSuccess: async () => {
      onOpenChange(false)
      toaster.create({
        title: "Password changed",
        description: "Sign in again with your new password",
        type: "success",
      })
      qc.clear()
      navigate("/login")
    },
    onError: (e) => {
      const msg = e instanceof ApiError ? e.message : "Failed to change the password"
      setError(msg)
      toaster.create({ title: msg, type: "error" })
    },
  })

  const submit = () => {
    setError("")
    if (next !== confirm) {
      setError("New passwords don't match")
      return
    }
    change.mutate()
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
            <Dialog.Title>Change password</Dialog.Title>
          </Dialog.Header>
          <Dialog.Body>
            <VStack align="stretch" gap={3}>
              <Field.Root required>
                <Field.Label>Current password <Field.RequiredIndicator /></Field.Label>
                <PasswordInput
                  value={current}
                  onChange={(e) => setCurrent(e.target.value)}
                  autoComplete="new-password"
                />
              </Field.Root>
              <Field.Root required>
                <Field.Label>New password <Field.RequiredIndicator /></Field.Label>
                <PasswordInput
                  value={next}
                  onChange={(e) => setNext(e.target.value)}
                  autoComplete="new-password"
                />
                <Field.HelperText>min 10 chars</Field.HelperText>
              </Field.Root>
              <Field.Root required>
                <Field.Label>Confirm new password <Field.RequiredIndicator /></Field.Label>
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
              <Text fontSize="xs" color="fg.muted">
                Changing your password logs out every session, including this
                one.
              </Text>
            </VStack>
          </Dialog.Body>
          <Dialog.Footer>
            <Dialog.ActionTrigger asChild>
              <Button type="button" variant="outline">
                Cancel
              </Button>
            </Dialog.ActionTrigger>
            <Button type="submit" colorPalette="blue" loading={change.isPending}>
              Change password
            </Button>
          </Dialog.Footer>
          <Dialog.CloseTrigger />
        </Dialog.Content>
      </Dialog.Positioner>
    </Dialog.Root>
  )
}

export default function ProfilePage() {
  const { data } = useSession()
  const [changing, setChanging] = useState(false)
  // a password is useless while password sign-in is off
  const credentialLogin = useCredentialLogin()

  return (
    <VStack align="stretch" gap={6}>
      <Heading size="lg">Profile</Heading>

      <Card.Root>
        <Card.Header>
          <HStack justify="space-between" flexWrap="wrap" gap={2}>
            <Heading size="sm">Account</Heading>
            {credentialLogin && (
              <Button
                size="xs"
                variant="outline"
                onClick={() => setChanging(true)}
                aria-label="change password"
              >
                <KeyRound /> Change password
              </Button>
            )}
          </HStack>
        </Card.Header>
        <Card.Body pt={3} gap={4}>
          {data && (
            <HStack gap={4} minW={0}>
              <UserAvatar name={data.user.name} avatarUrl={data.user.avatarUrl} size="xl" />
              <VStack align="start" gap={0} minW={0}>
                <Text fontWeight="medium" wordBreak="break-word">
                  {data.user.name}
                </Text>
                <Text fontSize="sm" color="fg.muted" wordBreak="break-all">
                  {data.user.email}
                </Text>
              </VStack>
            </HStack>
          )}
          {data && <NameField key={data.user.id} user={data.user} />}
          <HStack gap={6} flexWrap="wrap">
            <VStack align="start" gap={0}>
              <Text fontSize="xs" color="fg.muted">
                Role
              </Text>
              <Badge variant={data?.user.role === "superadmin" ? "solid" : "subtle"}>
                {data?.user.role === "superadmin" ? "Superadmin" : "User"}
              </Badge>
            </VStack>
          </HStack>
        </Card.Body>
      </Card.Root>

      <ChangePasswordModal open={changing} onOpenChange={setChanging} />

      <MyKeysPage />
    </VStack>
  )
}
