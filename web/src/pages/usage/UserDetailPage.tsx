import { useMemo } from "react"
import { Card, Heading, Text, VStack } from "@chakra-ui/react"
import { useNavigate, useParams } from "react-router-dom"
import { useUsageFacets, useUsageOverview } from "../../api/usage"
import type { UsageFilters } from "../../api/usage"
import BackLink from "./BackLink"
import { BreakdownTable } from "./BreakdownTable"
import { RequestsTable } from "./RequestsTable"
import SeriesCard from "./SeriesCard"
import { OverviewStats, RangeNotice } from "./UsageParts"
import { filterSearch, useUsageFilters } from "./urlState"

// Superadmin only (route-gated): one user's usage. The user comes from the
// path; the other filters still apply. Each table keeps its own URL state
// (m. / k. / r. prefixes).
export default function UserDetailPage() {
  const { userId } = useParams()
  const id = userId && /^\d+$/.test(userId) ? Number(userId) : 0
  const { filters: base, isAdmin, searchParams } = useUsageFilters()
  const navigate = useNavigate()
  const filters: UsageFilters = useMemo(() => ({ ...base, userId: id }), [base, id])
  const facets = useUsageFacets(base.from, base.to, base.tz)
  const overview = useUsageOverview(filters, undefined, { enabled: id > 0 })
  const back = <BackLink to={`/usage/users${filterSearch(searchParams)}`} label="All users" />

  if (!id) {
    return (
      <VStack align="stretch" gap={3}>
        {back}
        <Text color="fg.muted">Unknown user.</Text>
      </VStack>
    )
  }

  const email = facets.data?.users?.find((u) => u.id === id)?.email

  return (
    <VStack align="stretch" gap={4}>
      {back}
      <Heading size="md" wordBreak="break-all">
        {email || `User #${id}`}
      </Heading>
      <RangeNotice filters={filters} range={overview.data?.range} />
      <OverviewStats totals={overview.data?.totals} loading={overview.isPending} showUsers={false} />
      <SeriesCard filters={filters} groupBy="model" />
      <Card.Root>
        <Card.Header>
          <Heading size="sm">By model</Heading>
        </Card.Header>
        <Card.Body pt={3}>
          <BreakdownTable
            filters={filters}
            groupBy="model"
            prefix="m."
            defaultPageSize={10}
            isAdmin={isAdmin}
            onRowClick={(r) =>
              navigate(
                `/usage/model${filterSearch(searchParams, {
                  userId: String(id),
                  provider: r.provider,
                  model: r.model,
                })}`,
              )
            }
          />
        </Card.Body>
      </Card.Root>
      <Card.Root>
        <Card.Header>
          <Heading size="sm">By client key</Heading>
        </Card.Header>
        <Card.Body pt={3}>
          <BreakdownTable
            filters={filters}
            groupBy="key"
            prefix="k."
            defaultPageSize={10}
            isAdmin={false}
          />
        </Card.Body>
      </Card.Root>
      <Card.Root>
        <Card.Header>
          <Heading size="sm">Recent requests</Heading>
        </Card.Header>
        <Card.Body pt={3}>
          <RequestsTable filters={filters} prefix="r." defaultPageSize={25} isAdmin={isAdmin} />
        </Card.Body>
      </Card.Root>
    </VStack>
  )
}
