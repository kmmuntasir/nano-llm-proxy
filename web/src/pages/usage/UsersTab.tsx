import { Card, Heading, VStack } from "@chakra-ui/react"
import { useNavigate } from "react-router-dom"
import { BreakdownTable } from "./BreakdownTable"
import SeriesCard from "./SeriesCard"
import { filterSearch, useUsageFilters } from "./urlState"

// Superadmin only (route-gated): usage per user, row → user detail.
export default function UsersTab() {
  const { filters, isSuperadmin, searchParams } = useUsageFilters()
  const navigate = useNavigate()

  return (
    <VStack align="stretch" gap={4}>
      <SeriesCard filters={filters} groupBy="user" />
      <Card.Root>
        <Card.Header>
          <Heading size="sm">By user</Heading>
        </Card.Header>
        <Card.Body pt={3}>
          <BreakdownTable
            filters={filters}
            groupBy="user"
            showRangeNotice
            isSuperadmin={isSuperadmin}
            onRowClick={(r) =>
              navigate(`/usage/users/${r.userId}${filterSearch(searchParams, { userId: "" })}`)
            }
          />
        </Card.Body>
      </Card.Root>
    </VStack>
  )
}
