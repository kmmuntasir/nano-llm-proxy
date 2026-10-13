import { Card, Heading, VStack } from "@chakra-ui/react"
import { useNavigate } from "react-router-dom"
import { BreakdownTable } from "./BreakdownTable"
import SeriesCard from "./SeriesCard"
import { filterSearch, useUsageFilters } from "./urlState"

// Usage per (provider, model), row → model detail.
export default function ModelsTab() {
  const { filters, isSuperadmin, searchParams } = useUsageFilters()
  const navigate = useNavigate()

  return (
    <VStack align="stretch" gap={4}>
      <SeriesCard filters={filters} groupBy="model" />
      <Card.Root>
        <Card.Header>
          <Heading size="sm">By model</Heading>
        </Card.Header>
        <Card.Body pt={3}>
          <BreakdownTable
            filters={filters}
            groupBy="model"
            showRangeNotice
            isSuperadmin={isSuperadmin}
            onRowClick={(r) =>
              navigate(
                `/usage/model${filterSearch(searchParams, { provider: r.provider, model: r.model })}`,
              )
            }
          />
        </Card.Body>
      </Card.Root>
    </VStack>
  )
}
