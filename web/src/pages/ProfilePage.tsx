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
  Text,
  VStack,
} from "@chakra-ui/react"
import { useNavigate } from "react-router-dom"
import { KeyRound } from "lucide-react"
import { post, ApiError } from "../api/client"
import { PasswordInput } from "../components/ui/password-input"
import { toaster } from "../components/ui/toaster"
import { useSession } from "../App"
import MyKeysPage from "./MyKeysPage"

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
      const msg = e instanceof ApiError ? e.message : "change failed"
      setError(msg)
      toaster.create({ title: msg, type: "error" })
    },
  })

  const submit = () => {
    setError("")
    if (next !== confirm) {
      setError("new passwords don't match")
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
                <Field.Label>Current password</Field.Label>
                <PasswordInput
                  value={current}
                  onChange={(e) => setCurrent(e.target.value)}
                  autoComplete="current-password"
                />
              </Field.Root>
              <Field.Root required>
                <Field.Label>New password</Field.Label>
                <PasswordInput
                  value={next}
                  onChange={(e) => setNext(e.target.value)}
                  autoComplete="new-password"
                />
                <Field.HelperText>min 10 chars</Field.HelperText>
              </Field.Root>
              <Field.Root required>
                <Field.Label>Confirm new password</Field.Label>
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

  return (
    <VStack align="stretch" gap={6}>
      <Heading size="lg">Profile</Heading>

      <Card.Root>
        <Card.Header>
          <HStack justify="space-between" flexWrap="wrap" gap={2}>
            <Heading size="sm">Account</Heading>
            <Button
              size="xs"
              variant="outline"
              onClick={() => setChanging(true)}
              aria-label="change password"
            >
              <KeyRound /> Change password
            </Button>
          </HStack>
        </Card.Header>
        <Card.Body pt={3}>
          <HStack gap={6} flexWrap="wrap">
            <VStack align="start" gap={0}>
              <Text fontSize="xs" color="fg.muted">
                Name
              </Text>
              <Text>{data?.user.name ?? "—"}</Text>
            </VStack>
            <VStack align="start" gap={0}>
              <Text fontSize="xs" color="fg.muted">
                Email
              </Text>
              <Text>{data?.user.email ?? "—"}</Text>
            </VStack>
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
