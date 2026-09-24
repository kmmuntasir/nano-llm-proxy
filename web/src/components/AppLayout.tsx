import { useState } from "react"
import { NavLink, useNavigate } from "react-router-dom"
import type { ComponentType, ReactNode } from "react"
import { useMutation, useQueryClient } from "@tanstack/react-query"
import {
  Box,
  Drawer,
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
  Activity,
  BarChart3,
  List,
  LogOut,
  Menu as MenuIcon,
  Moon,
  Server,
  Settings,
  Sun,
  User,
  Users,
} from "lucide-react"
import { useColorMode } from "./ui/color-mode"
import { useTheme } from "next-themes"
import { post } from "../api/client"
import { toaster } from "./ui/toaster"
import { useSession } from "../App"
import logoUrl from "../assets/logo.png"

interface NavItem {
  to: string
  label: string
  icon: ComponentType
  superadminOnly?: boolean
}

const navItems: NavItem[] = [
  { to: "/", label: "Dashboard", icon: Activity },
  { to: "/models", label: "Models", icon: List },
  { to: "/usage", label: "Usage", icon: BarChart3 },
  { to: "/profile", label: "Profile", icon: User },
  { to: "/users", label: "Users", icon: Users, superadminOnly: true },
  { to: "/providers", label: "Providers", icon: Server, superadminOnly: true },
  { to: "/settings", label: "Settings", icon: Settings, superadminOnly: true },
]

function ColorModeMenu() {
  const { colorMode } = useColorMode()
  const { setTheme } = useTheme()
  return (
    <Menu.Root>
      <Menu.Trigger asChild>
        <IconButton variant="ghost" size="sm" aria-label="color mode">
          {colorMode === "dark" ? <Moon /> : <Sun />}
        </IconButton>
      </Menu.Trigger>
      <Portal>
        <Menu.Positioner>
          <Menu.Content>
            <Menu.Item value="light" onClick={() => setTheme("light")}>
              <Moon /> Light
            </Menu.Item>
            <Menu.Item value="dark" onClick={() => setTheme("dark")}>
              <Sun /> Dark
            </Menu.Item>
            <Menu.Item value="system" onClick={() => setTheme("system")}>
              <MenuIcon /> System
            </Menu.Item>
          </Menu.Content>
        </Menu.Positioner>
      </Portal>
    </Menu.Root>
  )
}

// SidebarContent is the shared nav: the desktop aside renders it inline, the
// mobile drawer renders the same thing behind a hamburger. onNavigate fires
// after a link is tapped so the drawer can close itself.
function SidebarContent({
  items,
  showBrand = false,
  onNavigate,
}: {
  items: NavItem[]
  showBrand?: boolean
  onNavigate?: () => void
}) {
  return (
    <>
      {showBrand && (
        <Heading size="md" mb={6} px={2}>
          Nano LLM Proxy
        </Heading>
      )}
      <VStack align="stretch" gap={1}>
        {items.map((item) => (
          <NavLink
            key={item.to}
            to={item.to}
            end={item.to === "/"}
            onClick={onNavigate}
          >
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
      </VStack>
      <Box mt="auto" pt={6}>
        <SidebarFooter />
      </Box>
    </>
  )
}

function SidebarFooter() {
  const { data } = useSession()
  const navigate = useNavigate()
  const qc = useQueryClient()
  const logout = useMutation({
    mutationFn: () => post("/api/auth/logout"),
    onSuccess: () => {
      toaster.create({ title: "Logged out", type: "success" })
      qc.clear()
      navigate("/login")
    },
    onError: () => toaster.create({ title: "Logout failed", type: "error" }),
  })

  return (
    <>
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
          <LogOut />
        </IconButton>
      </HStack>
    </>
  )
}

// MobileSidebar is the full left sidebar, opened by the hamburger on small
// screens — same nav, same theme switcher, same logout as the desktop rail.
function MobileSidebar({
  items,
  open,
  onOpenChange,
}: {
  items: NavItem[]
  open: boolean
  onOpenChange: (o: boolean) => void
}) {
  return (
    <Drawer.Root open={open} onOpenChange={(e) => onOpenChange(e.open)}>
      <Drawer.Backdrop />
      <Drawer.Positioner>
        <Drawer.Content maxW="260px" bg="bg" minH="100vh">
          <Drawer.Header pt={5}>
            <Heading size="md">Nano LLM Proxy</Heading>
          </Drawer.Header>
          <Drawer.Body flex={1}>
            <VStack align="stretch" gap={1}>
              {items.map((item) => (
                <NavLink
                  key={item.to}
                  to={item.to}
                  end={item.to === "/"}
                  onClick={() => onOpenChange(false)}
                >
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
            </VStack>
          </Drawer.Body>
          <Drawer.Footer pb={5}>
            <SidebarFooter />
          </Drawer.Footer>
          <Drawer.CloseTrigger />
        </Drawer.Content>
      </Drawer.Positioner>
    </Drawer.Root>
  )
}

export default function AppLayout({ children }: { children: ReactNode }) {
  const { data } = useSession()
  const [mobileOpen, setMobileOpen] = useState(false)

  const isSuperadmin = data?.user.role === "superadmin"
  const items = navItems.filter((i) => !i.superadminOnly || isSuperadmin)

  return (
    <Flex minH="100vh">
      {/* sidebar (desktop) — fixed to the viewport, scrolls independently */}
      <VStack
        as="aside"
        align="stretch"
        gap={1}
        w="220px"
        p={4}
        borderRightWidth="1px"
        display={{ base: "none", md: "flex" }}
        position="sticky"
        top={0}
        h="100vh"
        overflowY="auto"
        flexShrink={0}
      >
        <SidebarContent items={items} showBrand />
      </VStack>

      <Box flex={1} minW={0} display="flex" flexDirection="column">
        {/* mobile top bar: brand + hamburger opening the full sidebar */}
        <HStack
          display={{ base: "flex", md: "none" }}
          position="sticky"
          top={0}
          zIndex={20}
          bg="bg"
          px={4}
          py={3}
          justifyContent="space-between"
          borderBottomWidth="1px"
        >
          <HStack gap={2} align="center">
            <img src={logoUrl} alt="Nano LLM Proxy logo" width={24} height={24} />
            <Heading size="sm">Nano LLM Proxy</Heading>
          </HStack>
          <IconButton
            variant="ghost"
            size="sm"
            aria-label="open menu"
            onClick={() => setMobileOpen(true)}
          >
            <MenuIcon />
          </IconButton>
        </HStack>

        <Box as="main" flex={1} p={{ base: 4, md: 6 }} minW={0} w="full">
          {children}
        </Box>
      </Box>

      <MobileSidebar items={items} open={mobileOpen} onOpenChange={setMobileOpen} />
    </Flex>
  )
}
