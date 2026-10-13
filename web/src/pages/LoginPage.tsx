import { useState } from "react"
import { useNavigate } from "react-router-dom"
import { useMutation, useQueryClient } from "@tanstack/react-query"
import {
  Box,
  Button,
  Card,
  Field,
  HStack,
  Heading,
  Input,
  Separator,
  Spinner,
  Text,
  VStack,
} from "@chakra-ui/react"
import { post, ApiError } from "../api/client"
import { useAuthConfig } from "../api/useAuthConfig"
import { PasswordInput } from "../components/ui/password-input"
import { toaster } from "../components/ui/toaster"
import GoogleSignInButton from "../components/GoogleSignInButton"
import logoUrl from "../assets/logo.png"
import type { UserView } from "../api/types"

export default function LoginPage() {
  const [email, setEmail] = useState("")
  const [password, setPassword] = useState("")
  const [error, setError] = useState("")
  const [busy, setBusy] = useState(false)
  const qc = useQueryClient()
  const navigate = useNavigate()
  const config = useAuthConfig()

  const signedIn = async () => {
    await qc.invalidateQueries()
    navigate("/")
  }

  const google = useMutation({
    mutationFn: (credential: string) => post<{ user: UserView }>("/api/auth/google", { credential }),
    onMutate: () => setError(""),
    onSuccess: signedIn,
    onError: (err) => {
      const msg = err instanceof ApiError ? err.message : "Google sign-in failed"
      setError(msg)
      toaster.create({ title: msg, type: "error" })
    },
  })

  // Config failure falls back to the password form; the server still enforces.
  const googleConfig = config.data?.google
  const showPassword = config.data?.credentialLogin ?? true

  const submit = async (e: React.FormEvent) => {
    e.preventDefault()
    setError("")
    setBusy(true)
    try {
      await post<{ user: UserView }>("/api/auth/login", { email, password })
      await signedIn()
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
          {config.isLoading ? (
            <Box display="grid" placeItems="center" py={6}>
              <Spinner />
            </Box>
          ) : (
            <VStack gap={4} align="stretch">
              {googleConfig && (
                <VStack gap={2} align="stretch">
                  <GoogleSignInButton
                    clientId={googleConfig.clientId}
                    onCredential={(credential) => google.mutate(credential)}
                  />
                  {googleConfig.domain && (
                    <Text color="fg.muted" fontSize="xs" textAlign="center" wordBreak="break-word">
                      Use your {googleConfig.domain} Google account
                    </Text>
                  )}
                  {google.isPending && (
                    <HStack gap={2} justify="center" color="fg.muted" fontSize="sm">
                      <Spinner size="xs" />
                      <Text>Signing in…</Text>
                    </HStack>
                  )}
                </VStack>
              )}
              {googleConfig && showPassword && (
                <HStack gap={3}>
                  <Separator flex="1" />
                  <Text color="fg.muted" fontSize="xs" flexShrink={0}>
                    or
                  </Text>
                  <Separator flex="1" />
                </HStack>
              )}
              {showPassword && (
                <form onSubmit={submit}>
                  <VStack gap={4}>
                    <Field.Root required>
                      <Field.Label>Email <Field.RequiredIndicator /></Field.Label>
                      <Input
                        type="email"
                        autoComplete="username"
                        value={email}
                        onChange={(e) => setEmail(e.target.value)}
                        autoFocus
                      />
                    </Field.Root>
                    <Field.Root required>
                      <Field.Label>Password <Field.RequiredIndicator /></Field.Label>
                      {/* native password field here: password managers may save/fill the login */}
                      <PasswordInput
                        native
                        autoComplete="current-password"
                        value={password}
                        onChange={(e) => setPassword(e.target.value)}
                      />
                    </Field.Root>
                    <Button type="submit" colorPalette="blue" w="full" loading={busy}>
                      Sign in
                    </Button>
                  </VStack>
                </form>
              )}
              {error && (
                <Text color="red.fg" fontSize="sm" role="alert">
                  {error}
                </Text>
              )}
            </VStack>
          )}
        </Card.Body>
      </Card.Root>
    </Box>
  )
}
