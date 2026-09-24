import { useState } from "react"
import { useNavigate } from "react-router-dom"
import { useQueryClient } from "@tanstack/react-query"
import {
  Box,
  Button,
  Card,
  Field,
  HStack,
  Heading,
  Input,
  Text,
  VStack,
} from "@chakra-ui/react"
import { post, ApiError } from "../api/client"
import { PasswordInput } from "../components/ui/password-input"
import { toaster } from "../components/ui/toaster"
import logoUrl from "../assets/logo.png"
import type { UserView } from "../api/types"

export default function LoginPage() {
  const [email, setEmail] = useState("")
  const [password, setPassword] = useState("")
  const [error, setError] = useState("")
  const [busy, setBusy] = useState(false)
  const qc = useQueryClient()
  const navigate = useNavigate()

  const submit = async (e: React.FormEvent) => {
    e.preventDefault()
    setError("")
    setBusy(true)
    try {
      await post<{ user: UserView }>("/api/auth/login", { email, password })
      await qc.invalidateQueries()
      navigate("/")
    } catch (err) {
      const msg = err instanceof ApiError ? err.message : "Login failed — check your email and password"
      setError(msg)
      toaster.create({ title: msg, type: "error" })
    } finally {
      setBusy(false)
    }
  }

  return (
    <Box minH="100vh" display="grid" placeItems="center" p={4}>
      <Card.Root w="full" maxW="sm">
        <Card.Header gap={1}>
          <HStack gap={2} align="center">
            <img src={logoUrl} alt="Nano LLM Proxy logo" width={32} height={32} />
            <Heading size="lg">Nano LLM Proxy</Heading>
          </HStack>
          <Text color="fg.muted" fontSize="sm">
            Sign in to manage keys and providers
          </Text>
        </Card.Header>
        <Card.Body>
          <form onSubmit={submit}>
            <VStack gap={4}>
              <Field.Root required>
                <Field.Label>Email</Field.Label>
                <Input
                  type="email"
                  autoComplete="username"
                  value={email}
                  onChange={(e) => setEmail(e.target.value)}
                  autoFocus
                />
              </Field.Root>
              <Field.Root required>
                <Field.Label>Password</Field.Label>
                <PasswordInput
                  autoComplete="current-password"
                  value={password}
                  onChange={(e) => setPassword(e.target.value)}
                />
              </Field.Root>
              {error && (
                <Text color="red.fg" fontSize="sm" role="alert">
                  {error}
                </Text>
              )}
              <Button type="submit" colorPalette="blue" w="full" loading={busy}>
                Sign in
              </Button>
            </VStack>
          </form>
        </Card.Body>
      </Card.Root>
    </Box>
  )
}
