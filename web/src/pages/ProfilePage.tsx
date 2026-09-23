import { useState } from "react"
import { useMutation, useQueryClient } from "@tanstack/react-query"
import {
  Badge,
  Button,
  Card,
  Field,
  Heading,
  HStack,
  Text,
  VStack,
} from "@chakra-ui/react"
import { useNavigate } from "react-router-dom"
import { post, ApiError } from "../api/client"
import { PasswordInput } from "../components/ui/password-input"
import { useSession } from "../App"
import MyKeysPage from "./MyKeysPage"

// ChangePasswordCard: self-service reset gated on the current password. The
// server wipes every session on success, so the client bounces to login.
function ChangePasswordCard() {
  const qc = useQueryClient()
  const navigate = useNavigate()
  const [current, setCurrent] = useState("")
  const [next, setNext] = useState("")
  const [confirm, setConfirm] = useState("")
  const [error, setError] = useState("")

  const change = useMutation({
    mutationFn: () => post("/api/me/password", { currentPassword: current, newPassword: next }),
    onSuccess: async () => {
      qc.clear()
      navigate("/login")
    },
    onError: (e) => setError(e instanceof ApiError ? e.message : "change failed"),
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
    <Card.Root>
      <Card.Header>
        <Heading size="sm">Change password</Heading>
        <Text fontSize="xs" color="fg.muted">
          Changing your password logs out every session, including this one.
        </Text>
      </Card.Header>
      <Card.Body>
        <form
          onSubmit={(e) => {
            e.preventDefault()
            submit()
          }}
        >
          <VStack align="stretch" gap={3} maxW="360px">
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
            <Button type="submit" colorPalette="blue" loading={change.isPending} w="fit-content">
              Change password
            </Button>
          </VStack>
        </form>
      </Card.Body>
    </Card.Root>
  )
}

export default function ProfilePage() {
  const { data } = useSession()

  return (
    <VStack align="stretch" gap={6}>
      <Heading size="lg">Profile</Heading>

      <Card.Root>
        <Card.Header>
          <Heading size="sm">Account</Heading>
        </Card.Header>
        <Card.Body>
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

      <ChangePasswordCard />

      <MyKeysPage />
    </VStack>
  )
}
