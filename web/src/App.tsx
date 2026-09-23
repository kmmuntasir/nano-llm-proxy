import { useEffect } from "react"
import { Navigate, Route, Routes, useNavigate } from "react-router-dom"
import { useQuery } from "@tanstack/react-query"
import { Box, Spinner } from "@chakra-ui/react"
import { api, setUnauthorizedHandler } from "./api/client"
import type { UserView } from "./api/types"
import AppLayout from "./components/AppLayout"
import LoginPage from "./pages/LoginPage"
import DashboardPage from "./pages/DashboardPage"
import ProfilePage from "./pages/ProfilePage"
import UsagePage from "./pages/UsagePage"
import UsersPage from "./pages/UsersPage"
import ProvidersPage from "./pages/ProvidersPage"
import SettingsPage from "./pages/SettingsPage"

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

  useEffect(() => {
    setUnauthorizedHandler(() => navigate("/login"))
  }, [navigate])

  if (me.isLoading) {
    return (
      <Box p={10} display="grid" placeItems="center" minH="100vh">
        <Spinner size="lg" />
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

  const isSuperadmin = me.data.user.role === "superadmin"

  return (
    <AppLayout>
      <Routes>
        <Route path="/" element={<DashboardPage />} />
        <Route path="/usage" element={<UsagePage />} />
        <Route path="/profile" element={<ProfilePage />} />
        <Route path="/keys" element={<Navigate to="/profile" replace />} />
        {isSuperadmin && (
          <>
            <Route path="/users" element={<UsersPage />} />
            <Route path="/providers" element={<ProvidersPage />} />
            <Route path="/settings" element={<SettingsPage />} />
          </>
        )}
        <Route path="*" element={<Navigate to="/" replace />} />
      </Routes>
    </AppLayout>
  )
}
