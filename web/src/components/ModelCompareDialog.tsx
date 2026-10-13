import type { ReactNode } from "react"
import { Box, Button, HStack, IconButton, Table, Text, VStack } from "@chakra-ui/react"
import { X } from "lucide-react"
import {
  DialogBody,
  DialogCloseTrigger,
  DialogContent,
  DialogFooter,
  DialogHeader,
  DialogRoot,
  DialogTitle,
} from "./ui/dialog"
import ComboSelect from "./ComboSelect"
import { BENCHMARK_INDEXES, arenaCategoryLabel, fmtScore } from "./BenchmarkScores"
import { cleanModelId, fmtTokens, splitModelId } from "./ModelCard"
import { fmtReasoningLevels } from "./ModelDetailsDialog"
import type { CatalogModelView } from "../api/types"

// ModelCompareDialog lays up to MAX_COMPARE catalog models side by side:
// capabilities, limits, benchmark indexes, and Design Arena Elo per category
// (union across the compared models). The best numeric value per row is
// highlighted; missing values show "—".

export const MAX_COMPARE = 3

const MISSING = "—"

interface Cell {
  content: ReactNode
  /** numeric value used to pick the row's best (higher is better) */
  num?: number
}

interface CompareRow {
  label: string
  cells: Cell[]
  /** highlight the highest numeric cell */
  rank?: boolean
}

const yesNo = (v?: boolean): Cell => ({ content: v ? "yes" : "no" })

function numCell(n: number | undefined, fmt: (n: number) => string): Cell {
  return n === undefined || n === 0 ? { content: MISSING } : { content: fmt(n), num: n }
}

function bestOf(row: CompareRow): number | undefined {
  if (!row.rank) return undefined
  const nums = row.cells.map((c) => c.num).filter((n): n is number => n !== undefined)
  if (nums.length < 2) return undefined
  const max = Math.max(...nums)
  return max === Math.min(...nums) ? undefined : max
}

function buildRows(models: CatalogModelView[]): { base: CompareRow[]; arena: CompareRow[] } {
  const base: CompareRow[] = [
    { label: "Provider", cells: models.map((m) => ({ content: splitModelId(m.id).provider })) },
    {
      label: "Context window",
      rank: true,
      cells: models.map((m) => numCell(m.context_window, fmtTokens)),
    },
    {
      label: "Max output",
      rank: true,
      cells: models.map((m) => numCell(m.max_output_tokens, fmtTokens)),
    },
    {
      label: "Input modalities",
      cells: models.map((m) => ({
        content: (m.input_modalities ?? []).length > 0 ? (m.input_modalities ?? []).join(", ") : MISSING,
      })),
    },
    {
      label: "Reasoning",
      cells: models.map((m) => {
        const levels = fmtReasoningLevels(m.reasoning_options)
        if (!m.reasoning) return { content: "no" }
        return {
          content: (
            <>
              yes
              {levels && (
                <Text as="span" display="block" fontSize="xs" color="fg.muted">
                  {levels}
                </Text>
              )}
            </>
          ),
        }
      }),
    },
    { label: "Responses API", cells: models.map((m) => yesNo(m.responses_api)) },
    { label: "Free", cells: models.map((m) => yesNo(m.free)) },
    ...BENCHMARK_INDEXES.map((ix) => ({
      label: ix.label,
      rank: true,
      cells: models.map((m) => {
        const v = m.benchmarks?.[ix.key]
        return v === undefined ? { content: MISSING } : { content: fmtScore(v), num: v }
      }),
    })),
  ]

  // union of arena/category pairs; the arena name is only shown when the
  // compared models span more than one arena
  const pairs = new Map<string, { arena: string; category: string }>()
  for (const m of models) {
    for (const a of m.benchmarks?.design_arena ?? []) {
      pairs.set(`${a.arena}:${a.category}`, { arena: a.arena, category: a.category })
    }
  }
  const multiArena = new Set([...pairs.values()].map((p) => p.arena)).size > 1
  const arena: CompareRow[] = [...pairs.entries()]
    .map(([key, p]) => ({
      key,
      label: arenaCategoryLabel(p.category) + (multiArena ? ` (${p.arena})` : ""),
    }))
    .sort((a, b) => a.label.localeCompare(b.label))
    .map(({ key, label }) => ({
      label,
      rank: true,
      cells: models.map((m) => {
        const hit = (m.benchmarks?.design_arena ?? []).find((a) => `${a.arena}:${a.category}` === key)
        if (!hit) return { content: MISSING }
        return {
          content: (
            <>
              {Math.round(hit.elo)}{" "}
              <Text as="span" fontSize="xs" color="fg.muted">
                #{hit.rank}
              </Text>
            </>
          ),
          num: hit.elo,
        }
      }),
    }))

  return { base, arena }
}

function RowView({ row }: { row: CompareRow }) {
  const best = bestOf(row)
  return (
    <Table.Row>
      <Table.Cell
        position="sticky"
        left={0}
        bg="bg.panel"
        zIndex={1}
        color="fg.muted"
        fontSize="xs"
        whiteSpace="nowrap"
      >
        {row.label}
      </Table.Cell>
      {row.cells.map((c, i) => {
        const isBest = best !== undefined && c.num === best
        return (
          <Table.Cell
            key={i}
            fontWeight={isBest ? "semibold" : undefined}
            color={isBest ? "green.fg" : undefined}
            bg={isBest ? "green.subtle" : undefined}
          >
            {c.content}
          </Table.Cell>
        )
      })}
    </Table.Row>
  )
}

export default function ModelCompareDialog({
  open,
  onOpenChange,
  entries,
  selected,
  onAdd,
  onRemove,
}: {
  open: boolean
  onOpenChange: (o: boolean) => void
  /** the whole catalog — the add picker offers what isn't selected yet */
  entries: CatalogModelView[]
  selected: string[]
  onAdd: (id: string) => void
  onRemove: (id: string) => void
}) {
  const models = selected
    .map((id) => entries.find((e) => e.id === id))
    .filter((m): m is CatalogModelView => m !== undefined)
  const { base, arena } = buildRows(models)
  const options = entries
    .filter((e) => !selected.includes(e.id))
    .map((e) => ({ value: e.id, label: cleanModelId(e.id), search: e.id }))
  const canAdd = selected.length < MAX_COMPARE

  return (
    <DialogRoot open={open} onOpenChange={(e) => onOpenChange(e.open)} size="xl">
      <DialogContent>
        <DialogHeader>
          <DialogTitle>Compare models</DialogTitle>
        </DialogHeader>
        <DialogBody>
          <VStack align="stretch" gap={4}>
            {canAdd && (
              <ComboSelect
                options={options}
                value=""
                onChange={(id) => id && onAdd(id)}
                placeholder={`Add a model (up to ${MAX_COMPARE})…`}
                emptyText="No models match"
                mono
                ariaLabel="add model to compare"
              />
            )}
            {models.length === 0 ? (
              <Text fontSize="sm" color="fg.muted">
                Pick models to compare.
              </Text>
            ) : (
              <Box overflowX="auto" borderWidth="1px" rounded="md">
                <Table.Root size="sm">
                  <Table.Header>
                    <Table.Row>
                      <Table.ColumnHeader position="sticky" left={0} bg="bg.panel" zIndex={1} />
                      {models.map((m) => (
                        <Table.ColumnHeader key={m.id} minW="10rem" verticalAlign="top">
                          <HStack justify="space-between" align="start" gap={1}>
                            <Text
                              fontFamily="mono"
                              fontSize="xs"
                              fontWeight="medium"
                              wordBreak="break-all"
                              title={m.id}
                            >
                              {splitModelId(m.id).name}
                            </Text>
                            <IconButton
                              variant="ghost"
                              size="2xs"
                              aria-label={`remove ${m.id} from compare`}
                              onClick={() => onRemove(m.id)}
                            >
                              <X />
                            </IconButton>
                          </HStack>
                        </Table.ColumnHeader>
                      ))}
                    </Table.Row>
                  </Table.Header>
                  <Table.Body>
                    {base.map((r) => (
                      <RowView key={r.label} row={r} />
                    ))}
                    {arena.length > 0 && (
                      <>
                        <Table.Row>
                          <Table.Cell
                            colSpan={models.length + 1}
                            fontSize="xs"
                            fontWeight="semibold"
                            bg="bg.muted"
                          >
                            Design Arena Elo
                          </Table.Cell>
                        </Table.Row>
                        {arena.map((r) => (
                          <RowView key={`arena-${r.label}`} row={r} />
                        ))}
                      </>
                    )}
                  </Table.Body>
                </Table.Root>
              </Box>
            )}
          </VStack>
        </DialogBody>
        <DialogFooter>
          <Button colorPalette="blue" onClick={() => onOpenChange(false)}>
            Done
          </Button>
        </DialogFooter>
        <DialogCloseTrigger />
      </DialogContent>
    </DialogRoot>
  )
}
