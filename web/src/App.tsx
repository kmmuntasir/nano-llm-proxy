import { Suspense, lazy, useEffect } from "react"
import { Navigate, Route, Routes, useNavigate } from "react-router-dom"
import { useQuery } from "@tanstack/react-query"
import { Box, Button, Spinner, Text, VStack } from "@chakra-ui/react"
import { api, setUnauthorizedHandler } from "./api/client"
import { isAdminRole } from "./api/roles"
import { useAuthConfig, useIsLocal } from "./api/useAuthConfig"
import type { UserView } from "./api/types"
import AppLayout from "./components/AppLayout"
import LoginPage from "./pages/LoginPage"
import DashboardPage from "./pages/DashboardPage"
import AboutPage from "./pages/AboutPage"
import DocsPage from "./pages/DocsPage"
import ModelsPage from "./pages/ModelsPage"
import ProfilePage from "./pages/ProfilePage"
import UsersPage from "./pages/UsersPage"
import ProvidersPage from "./pages/ProvidersPage"
import SettingsPage from "./pages/SettingsPage"

// The usage section pulls in Recharts and several views — load it on demand.
const UsageSection = lazy(() => import("./pages/usage/UsageSection"))

export interface Session {
  user: UserView
}

export function useSession() {
  return useQuery({
    queryKey: ["me"],
    queryFn: () => api<Session>("/api/auth/me"),
    staleTime: 60_000,
    retry: false,
  })
}

export default function App() {
  const navigate = useNavigate()
  const me = useSession()
  const authConfig = useAuthConfig()
  // Local mode has no sign-in: /api/auth/me always answers with the owner,
  // so nothing may ever route to /login.
  const isLocal = useIsLocal()

  useEffect(() => {
    setUnauthorizedHandler(() => navigate(isLocal ? "/" : "/login"))
  }, [navigate, isLocal])

  if (me.isLoading || (!me.data && authConfig.isLoading)) {
    return (
      <Box p={10} display="grid" placeItems="center" minH="100vh">
        <Spinner size="lg" />
      </Box>
    )
  }

  if (!me.data && isLocal) {
    return (
      <Box p={10} display="grid" placeItems="center" minH="100vh">
        <VStack gap={3} textAlign="center" maxW="md">
          <Text fontWeight="medium">Can't reach nano-llm-proxy</Text>
          {me.error && (
            <Text fontSize="sm" color="fg.muted" wordBreak="break-word">
              {me.error.message}
            </Text>
          )}
          <Button size="sm" loading={me.isFetching} onClick={() => void me.refetch()}>
            Retry
          </Button>
        </VStack>
      </Box>
    )
  }

  if (!me.data) {
    return (
      <Routes>
        <Route path="/login" element={<LoginPage />} />
        <Route path="*" element={<Navigate to="/login" replace />} />
      </Routes>
    )
  }

  const isAdmin = isAdminRole(me.data.user.role)
  // local mode has a single owner — no other accounts to manage
  const showUsers = isAdmin && !isLocal

  return (
    <AppLayout>
      <Routes>
        <Route path="/" element={<DashboardPage />} />
        <Route path="/models" element={<ModelsPage />} />
        <Route path="/docs" element={<DocsPage />} />
        <Route path="/about" element={<AboutPage />} />
        <Route
          path="/usage/*"
          element={
            <Suspense
              fallback={
                <Box p={10} display="grid" placeItems="center">
                  <Spinner />
                </Box>
              }
            >
              <UsageSection />
            </Suspense>
          }
        />
        <Route path="/profile" element={<ProfilePage />} />
        <Route path="/keys" element={<Navigate to="/profile" replace />} />
        {isAdmin && (
          <>
            {showUsers && <Route path="/users" element={<UsersPage />} />}
            <Route path="/providers" element={<ProvidersPage />} />
            <Route path="/settings" element={<SettingsPage />} />
          </>
        )}
        <Route path="*" element={<Navigate to="/" replace />} />
      </Routes>
    </AppLayout>
  )
}
