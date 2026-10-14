import { useState } from "react"
import { Link as RouterLink, NavLink, useNavigate } from "react-router-dom"
import type { ComponentProps, ComponentType, ReactNode } from "react"
import { useMutation, useQueryClient } from "@tanstack/react-query"
import {
  Badge,
  Box,
  Drawer,
  Flex,
  Heading,
  HStack,
  IconButton,
  Link,
  Menu,
  Portal,
  Text,
  VStack,
} from "@chakra-ui/react"
import {
  Activity,
  BarChart3,
  BookOpen,
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
import { isAdminRole } from "../api/roles"
import { useAbout } from "../api/useAbout"
import { useIsLocal } from "../api/useAuthConfig"
import { toaster } from "./ui/toaster"
import UserAvatar from "./UserAvatar"
import { useSession } from "../App"
import logoUrl from "../assets/logo.png"

interface NavItem {
  to: string
  label: string
  icon: ComponentType
  adminOnly?: boolean
  /** hidden in local mode (single owner, no other accounts) */
  serverOnly?: boolean
}

const navItems: NavItem[] = [
  { to: "/", label: "Dashboard", icon: Activity },
  { to: "/models", label: "Models", icon: List },
  { to: "/docs", label: "Guide", icon: BookOpen },
  { to: "/usage", label: "Usage", icon: BarChart3 },
  { to: "/profile", label: "Profile", icon: User },
  { to: "/users", label: "Users", icon: Users, adminOnly: true, serverOnly: true },
  { to: "/providers", label: "Providers", icon: Server, adminOnly: true },
  { to: "/settings", label: "Settings", icon: Settings, adminOnly: true },
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
// The brand lockup — logo plus wordmark. Defined once and shared by the
// desktop rail and the mobile top bar: they had drifted apart, and only the
// top bar carried the logo, so the wordmark appeared bare in the rail.
//
// Sized so it never widens the 220px rail: the logo never shrinks and the
// wordmark truncates rather than wrapping or pushing the layout.
//
// inlineBadge=false leaves the "Local" badge to the caller: the rail is too
// narrow to fit it beside the wordmark without truncating the name.
function Brand({
  size = "sm",
  inlineBadge = true,
}: {
  size?: ComponentProps<typeof Heading>["size"]
  inlineBadge?: boolean
}) {
  const isLocal = useIsLocal()
  return (
    <HStack gap={2} align="center" minW={0} overflow="hidden">
      <img
        src={logoUrl}
        alt="Nano LLM Proxy logo"
        width={24}
        height={24}
        style={{ flexShrink: 0 }}
      />
      <Heading size={size} minW={0} truncate>
        Nano LLM Proxy
      </Heading>
      {isLocal && inlineBadge && <LocalBadge />}
    </HStack>
  )
}

// LocalBadge marks a gateway running in local mode (`nano-llm-proxy local`).
function LocalBadge() {
  return (
    <Badge
      variant="subtle"
      colorPalette="teal"
      size="sm"
      flexShrink={0}
      title="Running on this computer"
    >
      Local
    </Badge>
  )
}

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
  const isLocal = useIsLocal()
  return (
    <>
      {showBrand && (
        <Box mb={6} px={2} minW={0} overflow="hidden">
          <Brand size="md" inlineBadge={false} />
          {isLocal && (
            <Box mt={1} pl={8}>
              <LocalBadge />
            </Box>
          )}
        </Box>
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
        <SidebarFooter onNavigate={onNavigate} />
      </Box>
    </>
  )
}

// VersionLabel links the running build to the About page. Hidden until
// /api/about answers; truncates so it never widens the rail or the drawer.
function VersionLabel({ onNavigate }: { onNavigate?: () => void }) {
  const { data } = useAbout()
  if (!data?.displayVersion) return null
  return (
    <Link
      asChild
      variant="plain"
      ml="auto"
      minW={0}
      px={1}
      color="fg.muted"
      _hover={{ color: "fg" }}
    >
      <RouterLink
        to="/about"
        title={`Nano LLM Proxy ${data.version} — about & changelog`}
        onClick={onNavigate}
      >
        <Text as="span" fontSize="xs" fontFamily="mono" truncate>
          {data.displayVersion}
        </Text>
      </RouterLink>
    </Link>
  )
}

function SidebarFooter({ onNavigate }: { onNavigate?: () => void }) {
  const { data } = useSession()
  const navigate = useNavigate()
  const qc = useQueryClient()
  // local mode has no sign-in, so nothing to log out of
  const isLocal = useIsLocal()
  const logout = useMutation({
    mutationFn: () => post("/api/auth/logout"),
    onSuccess: () => {
      toaster.create({ title: "Logged out", type: "success" })
      qc.clear()
      navigate("/login")
    },
    onError: () => toaster.create({ title: "Logout failed", type: "error" }),
  })

  // Stacked, so the drawer footer (a flex row) lays out like the desktop rail.
  return (
    <VStack align="stretch" gap={0} w="full" minW={0}>
      {data && (
        <HStack gap={2} px={2} mb={2} minW={0}>
          <UserAvatar name={data.user.name} avatarUrl={data.user.avatarUrl} size="xs" />
          <VStack align="start" gap={0} minW={0}>
            <Text fontSize="sm" fontWeight="medium" truncate maxW="full">
              {data.user.name}
            </Text>
            <Text fontSize="xs" color="fg.muted" truncate maxW="full">
              {data.user.email}
            </Text>
          </VStack>
        </HStack>
      )}
      <HStack px={1} minW={0}>
        <ColorModeMenu />
        {!isLocal && (
          <IconButton
            variant="ghost"
            size="sm"
            aria-label="log out"
            title="log out"
            onClick={() => logout.mutate()}
          >
            <LogOut />
          </IconButton>
        )}
        <VersionLabel onNavigate={onNavigate} />
      </HStack>
    </VStack>
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
  const isLocal = useIsLocal()
  return (
    <Drawer.Root open={open} onOpenChange={(e) => onOpenChange(e.open)}>
      <Drawer.Backdrop />
      <Drawer.Positioner>
        <Drawer.Content maxW="260px" bg="bg" minH="100vh">
          <Drawer.Header pt={5}>
            <HStack gap={2} minW={0}>
              <Heading size="md" minW={0} truncate>
                Nano LLM Proxy
              </Heading>
              {isLocal && <LocalBadge />}
            </HStack>
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
            <SidebarFooter onNavigate={() => onOpenChange(false)} />
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

  const isLocal = useIsLocal()
  const isAdmin = isAdminRole(data?.user.role)
  const items = navItems.filter(
    (i) => (!i.adminOnly || isAdmin) && !(i.serverOnly && isLocal),
  )

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
          <Brand />
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
