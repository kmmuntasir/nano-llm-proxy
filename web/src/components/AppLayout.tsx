import { NavLink, useNavigate } from "react-router-dom"
import type { ComponentType, ReactNode } from "react"
import { useMutation, useQueryClient } from "@tanstack/react-query"
import {
  Box,
  Flex,
  Heading,
  HStack,
  IconButton,
  Menu,
  Portal,
  Text,
  VStack,
} from "@chakra-ui/react"
import {
  FiActivity,
  FiLogOut,
  FiMenu,
  FiMoon,
  FiServer,
  FiSliders,
  FiSun,
  FiUsers,
  FiKey,
} from "react-icons/fi"
import { useColorMode } from "./ui/color-mode"
import { useTheme } from "next-themes"
import { post } from "../api/client"
import { useSession } from "../App"

interface NavItem {
  to: string
  label: string
  icon: ComponentType
  superadminOnly?: boolean
}

const navItems: NavItem[] = [
  { to: "/", label: "Dashboard", icon: FiActivity },
  { to: "/keys", label: "My API Keys", icon: FiKey },
  { to: "/users", label: "Users", icon: FiUsers, superadminOnly: true },
  { to: "/providers", label: "Providers", icon: FiServer, superadminOnly: true },
  { to: "/settings", label: "Settings", icon: FiSliders, superadminOnly: true },
]

function ColorModeMenu() {
  const { colorMode } = useColorMode()
  const { setTheme } = useTheme()
  return (
    <Menu.Root>
      <Menu.Trigger asChild>
        <IconButton variant="ghost" size="sm" aria-label="color mode">
          {colorMode === "dark" ? <FiMoon /> : <FiSun />}
        </IconButton>
      </Menu.Trigger>
      <Portal>
        <Menu.Positioner>
          <Menu.Content>
            <Menu.Item value="light" onClick={() => setTheme("light")}>
              <FiSun /> Light
            </Menu.Item>
            <Menu.Item value="dark" onClick={() => setTheme("dark")}>
              <FiMoon /> Dark
            </Menu.Item>
            <Menu.Item value="system" onClick={() => setTheme("system")}>
              <FiMenu /> System
            </Menu.Item>
          </Menu.Content>
        </Menu.Positioner>
      </Portal>
    </Menu.Root>
  )
}

export default function AppLayout({ children }: { children: ReactNode }) {
  const { data } = useSession()
  const qc = useQueryClient()
  const navigate = useNavigate()
  const logout = useMutation({
    mutationFn: () => post("/api/auth/logout"),
    onSuccess: () => {
      qc.clear()
      navigate("/login")
    },
  })

  const isSuperadmin = data?.user.role === "superadmin"
  const items = navItems.filter((i) => !i.superadminOnly || isSuperadmin)

  return (
    <Flex minH="100vh">
      {/* sidebar (desktop) */}
      <VStack
        as="aside"
        align="stretch"
        gap={1}
        w="220px"
        p={4}
        borderRightWidth="1px"
        display={{ base: "none", md: "flex" }}
      >
        <Heading size="md" mb={6} px={2}>
          nano-llm-proxy
        </Heading>
        {items.map((item) => (
          <NavLink key={item.to} to={item.to} end={item.to === "/"}>
            {({ isActive }) => (
              <Box
                p={2}
                px={3}
                rounded="md"
                display="flex"
                alignItems="center"
                gap={2}
                fontSize="sm"
                bg={isActive ? "bg.emphasized" : undefined}
                fontWeight={isActive ? "medium" : undefined}
                _hover={{ bg: "bg.subtle" }}
              >
                <item.icon />
                {item.label}
              </Box>
            )}
          </NavLink>
        ))}
        <Box mt="auto" pt={6}>
          <Text fontSize="xs" color="fg.muted" px={2} mb={2} truncate>
            {data?.user.email}
          </Text>
          <HStack px={1}>
            <ColorModeMenu />
            <IconButton
              variant="ghost"
              size="sm"
              aria-label="log out"
              title="log out"
              onClick={() => logout.mutate()}
            >
              <FiLogOut />
            </IconButton>
          </HStack>
        </Box>
      </VStack>

      {/* mobile menu */}
      <Box display={{ base: "block", md: "none" }} position="fixed" top={2} right={2} zIndex={10}>
        <Menu.Root>
          <Menu.Trigger asChild>
            <IconButton variant="outline" aria-label="menu">
              <FiMenu />
            </IconButton>
          </Menu.Trigger>
          <Portal>
            <Menu.Positioner>
              <Menu.Content>
                {items.map((item) => (
                  <Menu.Item key={item.to} value={item.to} onClick={() => navigate(item.to)}>
                    {item.label}
                  </Menu.Item>
                ))}
                <Menu.Separator />
                <Menu.Item
                  value="logout"
                  onClick={() => {
                    logout.mutate()
                  }}
                >
                  <FiLogOut /> Log out
                </Menu.Item>
              </Menu.Content>
            </Menu.Positioner>
          </Portal>
        </Menu.Root>
      </Box>

      <Box as="main" flex={1} p={6} maxW="1100px">
        {children}
      </Box>
    </Flex>
  )
}
