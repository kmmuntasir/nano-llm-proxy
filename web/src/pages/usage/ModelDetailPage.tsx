import { Badge, Card, Heading, HStack, SimpleGrid, Text, VStack } from "@chakra-ui/react"
import { useNavigate } from "react-router-dom"
import { useUsageOverview } from "../../api/usage"
import BackLink from "./BackLink"
import { BreakdownTable } from "./BreakdownTable"
import { LatencyCard } from "./OverviewTab"
import { RequestsTable } from "./RequestsTable"
import SeriesCard from "./SeriesCard"
import { OverviewStats, RangeNotice } from "./UsageParts"
import { filterSearch, useUsageFilters } from "./urlState"

// One model's usage. The model rides in ?provider=&model= (ids may contain
// "/"), i.e. it is just the regular filter pair pinned by this page. The
// by-user parts are superadmin only.
export default function ModelDetailPage() {
  const { filters, isSuperadmin, searchParams } = useUsageFilters()
  const navigate = useNavigate()
  const hasModel = !!filters.model
  const overview = useUsageOverview(filters, undefined, { enabled: hasModel })
  const back = (
    <BackLink to={`/usage/models${filterSearch(searchParams, { provider: "", model: "" })}`} label="All models" />
  )

  if (!hasModel) {
    return (
      <VStack align="stretch" gap={3}>
        {back}
        <Text color="fg.muted">No model selected.</Text>
      </VStack>
    )
  }

  return (
    <VStack align="stretch" gap={4}>
      {back}
      <HStack gap={2} flexWrap="wrap">
        {filters.provider && <Badge variant="outline">{filters.provider}</Badge>}
        <Heading size="md" fontFamily="mono" wordBreak="break-all">
          {filters.model}
        </Heading>
      </HStack>
      <RangeNotice filters={filters} range={overview.data?.range} />
      <OverviewStats totals={overview.data?.totals} loading={overview.isPending} showUsers={isSuperadmin} />
      <SimpleGrid columns={{ base: 1, xl: isSuperadmin ? 2 : 1 }} gap={4}>
        {isSuperadmin && <SeriesCard filters={filters} groupBy="user" />}
        <LatencyCard filters={filters} />
      </SimpleGrid>
      {isSuperadmin && (
        <Card.Root>
          <Card.Header>
            <Heading size="sm">By user</Heading>
          </Card.Header>
          <Card.Body pt={3}>
            <BreakdownTable
              filters={filters}
              groupBy="user"
              prefix="u."
              defaultPageSize={10}
              isSuperadmin={isSuperadmin}
              onRowClick={(r) =>
                navigate(`/usage/users/${r.userId}${filterSearch(searchParams, { userId: "" })}`)
              }
            />
          </Card.Body>
        </Card.Root>
      )}
      <Card.Root>
        <Card.Header>
          <Heading size="sm">Recent requests</Heading>
        </Card.Header>
        <Card.Body pt={3}>
          <RequestsTable filters={filters} prefix="r." defaultPageSize={25} isSuperadmin={isSuperadmin} />
        </Card.Body>
      </Card.Root>
    </VStack>
  )
}
