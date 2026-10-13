import { Card, Heading, Text, VStack } from "@chakra-ui/react"
import { RequestsTable } from "./RequestsTable"
import { useUsageFilters } from "./urlState"

export default function RequestsTab() {
  const { filters, isSuperadmin } = useUsageFilters()

  return (
    <VStack align="stretch" gap={4}>
      <Card.Root>
        <Card.Header>
          <Heading size="sm">Request log</Heading>
          <Text fontSize="xs" color="fg.muted">
            Expand a row for upstream status, attempts and the failure reason
          </Text>
        </Card.Header>
        <Card.Body pt={3}>
          <RequestsTable filters={filters} isSuperadmin={isSuperadmin} showRangeNotice />
        </Card.Body>
      </Card.Root>
    </VStack>
  )
}
