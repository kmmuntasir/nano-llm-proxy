import type { ReactNode } from "react"
import { Box, Card, HStack, Table, Text, VStack } from "@chakra-ui/react"

export interface Column<T> {
  header: string
  render: (row: T) => ReactNode
  /** cell adds no information on a phone card (e.g. redundant labels) */
  hideOnMobile?: boolean
}

interface Props<T> {
  columns: Column<T>[]
  rows: T[]
  rowKey: (row: T, index: number) => string
  empty?: string
}

// DataTable renders a real table on md+ screens and stacked label/value
// cards on phones — wide tables simply don't fit a phone viewport, and
// horizontal scrolling for every row is miserable.
export function DataTable<T>({ columns, rows, rowKey, empty = "Nothing here yet" }: Props<T>) {
  const isEmpty = rows.length === 0
  const emptyNode = (
    <Text color="fg.muted" fontSize="sm" textAlign="center" py={4}>
      {empty}
    </Text>
  )

  return (
    <>
      {/* mobile: one card per row */}
      <Box hideFrom="md">
        {isEmpty && emptyNode}
        <VStack align="stretch" gap={2}>
          {rows.map((row, i) => (
            <Card.Root key={rowKey(row, i)} size="sm">
              <Card.Body gap={2} px={4} py={3}>
                {columns
                  .filter((c) => !c.hideOnMobile)
                  .map((c) => (
                    <HStack key={c.header} justify="space-between" align="start" gap={3}>
                      <Text fontSize="xs" color="fg.muted" flexShrink={0} pt={0.5}>
                        {c.header}
                      </Text>
                      <Box textAlign="right" minW={0}>
                        {c.render(row)}
                      </Box>
                    </HStack>
                  ))}
              </Card.Body>
            </Card.Root>
          ))}
        </VStack>
      </Box>

      {/* desktop: the real table */}
      <Box hideBelow="md">
        {isEmpty ? (
          emptyNode
        ) : (
          <Table.Root size="sm">
            <Table.Header>
              <Table.Row>
                {columns.map((c) => (
                  <Table.ColumnHeader key={c.header}>{c.header}</Table.ColumnHeader>
                ))}
              </Table.Row>
            </Table.Header>
            <Table.Body>
              {rows.map((row, i) => (
                <Table.Row key={rowKey(row, i)}>
                  {columns.map((c) => (
                    <Table.Cell key={c.header}>{c.render(row)}</Table.Cell>
                  ))}
                </Table.Row>
              ))}
            </Table.Body>
          </Table.Root>
        )}
      </Box>
    </>
  )
}
