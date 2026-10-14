import { useEffect, useState } from "react"
import { Card, Heading, Input, InputGroup, VStack } from "@chakra-ui/react"
import { Search } from "lucide-react"
import { useNavigate, useSearchParams } from "react-router-dom"
import { useUsageBreakdown } from "../../api/usage"
import { fmtInt } from "./format"
import { BreakdownTable, userLabel } from "./BreakdownTable"
import { ChartCard, RankedBars } from "./UsageChartKit"
import { filterSearch, useUsageFilters } from "./urlState"

const TOP_PAIRS = 10
const SEARCH_DEBOUNCE_MS = 300

// Admins only (route-gated): who uses which model. The search box
// filters the table server-side (?q=, substring on the group label).
export default function UserModelsTab() {
  const { filters, isAdmin, searchParams } = useUsageFilters()
  const [, setSp] = useSearchParams()
  const navigate = useNavigate()
  const q = searchParams.get("q") ?? ""
  const [text, setText] = useState(q)
  // follow outside changes to ?q= (back/forward, clear) without an effect loop
  const [seenQ, setSeenQ] = useState(q)
  if (q !== seenQ) {
    setSeenQ(q)
    setText(q)
  }

  useEffect(() => {
    if (text === q) return
    const t = setTimeout(() => {
      setSp(
        (prev) => {
          const next = new URLSearchParams(prev)
          if (text) next.set("q", text)
          else next.delete("q")
          next.delete("page")
          return next
        },
        { replace: true },
      )
    }, SEARCH_DEBOUNCE_MS)
    return () => clearTimeout(t)
  }, [text, q, setSp])

  const top = useUsageBreakdown(filters, {
    groupBy: "user_model",
    sort: "requests",
    order: "desc",
    page: 1,
    pageSize: TOP_PAIRS,
  })
  const pairs = (top.data?.rows ?? []).map((r) => ({
    label: `${userLabel(r)} · ${r.model}`,
    value: r.requests,
  }))

  return (
    <VStack align="stretch" gap={4}>
      <ChartCard
        title="Top user × model pairs"
        subtitle={`By requests, top ${TOP_PAIRS}`}
        loading={top.isPending}
        error={top.error?.message}
        isEmpty={pairs.length === 0}
      >
        <RankedBars items={pairs} name="requests" format={fmtInt} />
      </ChartCard>
      <Card.Root>
        <Card.Header>
          <Heading size="sm">Users × models</Heading>
        </Card.Header>
        <Card.Body pt={3} gap={3}>
          <InputGroup startElement={<Search size={14} />} maxW={{ base: "full", md: "320px" }}>
            <Input
              size="sm"
              type="search"
              autoComplete="off"
              aria-label="Search users and models"
              placeholder="Search email or model"
              value={text}
              onChange={(e) => setText(e.target.value)}
            />
          </InputGroup>
          <BreakdownTable
            filters={filters}
            groupBy="user_model"
            isAdmin={isAdmin}
            q={q}
            showRangeNotice
            onRowClick={(r) =>
              navigate(
                `/usage/users/${r.userId}${filterSearch(searchParams, {
                  userId: "",
                  provider: r.provider,
                  model: r.model,
                })}`,
              )
            }
          />
        </Card.Body>
      </Card.Root>
    </VStack>
  )
}
