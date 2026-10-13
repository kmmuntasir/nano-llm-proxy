import { Box, HStack, Heading, VStack } from "@chakra-ui/react"
import { NavLink, Navigate, Route, Routes, useMatch } from "react-router-dom"
import ModelDetailPage from "./ModelDetailPage"
import ModelsTab from "./ModelsTab"
import OverviewTab from "./OverviewTab"
import RequestsTab from "./RequestsTab"
import UsageFilterBar from "./UsageFilterBar"
import UserDetailPage from "./UserDetailPage"
import UserModelsTab from "./UserModelsTab"
import UsersTab from "./UsersTab"
import { filterSearch, useUsageFilters } from "./urlState"
import type { FilterKey } from "./urlState"

interface Tab {
  to: string
  label: string
  superadminOnly?: boolean
}

const TABS: Tab[] = [
  { to: "/usage", label: "Overview" },
  { to: "/usage/users", label: "Users", superadminOnly: true },
  { to: "/usage/models", label: "Models" },
  { to: "/usage/user-models", label: "Users × Models", superadminOnly: true },
  { to: "/usage/requests", label: "Requests" },
]

// Tabs are links (each tab is a route), styled as a line tab bar. Links
// carry only the filter params, so table paging/sort start fresh per tab.
function TabBar({ tabs, search }: { tabs: Tab[]; search: string }) {
  return (
    // Horizontal scroll only when the tabs overflow (phones), never vertical,
    // and no visible scrollbar. The bottom rule lives on the inner row so the
    // active tab's -1px overlap stays inside the scroll box.
    <Box
      as="nav"
      aria-label="Usage views"
      overflowX="auto"
      overflowY="hidden"
      css={{ scrollbarWidth: "none", "&::-webkit-scrollbar": { display: "none" } }}
    >
      <HStack gap={0} minW="max-content" w="full" borderBottomWidth="1px">
        {tabs.map((t) => (
          <NavLink key={t.to} to={`${t.to}${search}`} end={t.to === "/usage"}>
            {({ isActive }) => (
              <Box
                px={3}
                py={2}
                fontSize="sm"
                whiteSpace="nowrap"
                mb="-1px"
                borderBottomWidth="2px"
                borderColor={isActive ? "colorPalette.solid" : "transparent"}
                colorPalette="blue"
                color={isActive ? "fg" : "fg.muted"}
                fontWeight={isActive ? "medium" : undefined}
                _hover={{ color: "fg" }}
              >
                {t.label}
              </Box>
            )}
          </NavLink>
        ))}
      </HStack>
    </Box>
  )
}

// UsageSection: the /usage/* subtree (lazy-loaded from App). Shared filter
// bar + tab bar on top, then the tab or drill-down page.
export default function UsageSection() {
  const { isSuperadmin, searchParams } = useUsageFilters()
  const onUserDetail = useMatch("/usage/users/:userId") !== null
  const onModelDetail = useMatch("/usage/model") !== null
  const locked: FilterKey[] = onUserDetail ? ["userId"] : onModelDetail ? ["provider", "model"] : []
  const tabs = TABS.filter((t) => !t.superadminOnly || isSuperadmin)
  const search = filterSearch(searchParams)

  return (
    <VStack align="stretch" gap={4}>
      <Heading size="lg">Usage</Heading>
      <UsageFilterBar locked={locked} />
      <TabBar tabs={tabs} search={search} />
      <Routes>
        <Route index element={<OverviewTab />} />
        <Route path="models" element={<ModelsTab />} />
        <Route path="model" element={<ModelDetailPage />} />
        <Route path="requests" element={<RequestsTab />} />
        {isSuperadmin && (
          <>
            <Route path="users" element={<UsersTab />} />
            <Route path="users/:userId" element={<UserDetailPage />} />
            <Route path="user-models" element={<UserModelsTab />} />
          </>
        )}
        <Route path="*" element={<Navigate to={`/usage${search}`} replace />} />
      </Routes>
    </VStack>
  )
}
