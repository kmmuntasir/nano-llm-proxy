import type { ReactNode } from "react"
import {
  Badge,
  Box,
  Button,
  Code,
  Heading,
  HStack,
  Link,
  SimpleGrid,
  Table,
  Text,
  VStack,
} from "@chakra-ui/react"
import { ExternalLink, GitCompare } from "lucide-react"
import {
  DialogBody,
  DialogCloseTrigger,
  DialogContent,
  DialogFooter,
  DialogHeader,
  DialogRoot,
  DialogTitle,
} from "./ui/dialog"
import CopyButton from "./CopyButton"
import BenchmarkScores, { arenaCategoryLabel, fmtScore } from "./BenchmarkScores"
import { fmtTokens, splitModelId } from "./ModelCard"
import type { CatalogModelView, ReasoningOptionView } from "../api/types"

// ModelDetailsDialog is the full view of one catalog model: identity,
// capabilities, limits, untruncated description, and benchmark scores
// (Artificial Analysis indexes + Design Arena) with their attribution.

const OPENROUTER_MODEL_URL = "https://openrouter.ai/"

// fmtReasoningLevels describes the reasoning controls a model accepts:
// the effort ladder, a token-budget range, or a plain on/off toggle.
export function fmtReasoningLevels(options?: ReasoningOptionView[]): string {
  const parts: string[] = []
  for (const o of options ?? []) {
    if ((o.values ?? []).length > 0) {
      parts.push((o.values ?? []).join(" · "))
    } else if (o.type === "budget_tokens" && o.min !== undefined && o.max !== undefined) {
      parts.push(`budget ${fmtTokens(o.min)}–${fmtTokens(o.max)} tokens`)
    }
  }
  return parts.join("; ")
}

function Fact({ label, children }: { label: string; children: ReactNode }) {
  return (
    <VStack align="start" gap={0} minW={0}>
      <Text fontSize="xs" color="fg.muted">
        {label}
      </Text>
      <Box fontSize="sm">{children}</Box>
    </VStack>
  )
}

export default function ModelDetailsDialog({
  open,
  onOpenChange,
  model,
  inCompare,
  compareFull,
  onToggleCompare,
}: {
  open: boolean
  onOpenChange: (o: boolean) => void
  model?: CatalogModelView
  inCompare: boolean
  /** the compare set is at its cap and this model isn't in it */
  compareFull: boolean
  onToggleCompare: () => void
}) {
  return (
    <DialogRoot open={open} onOpenChange={(e) => onOpenChange(e.open)} size="lg">
      <DialogContent>
        {model && (
          <DetailsContent
            model={model}
            inCompare={inCompare}
            compareFull={compareFull}
            onToggleCompare={onToggleCompare}
            onClose={() => onOpenChange(false)}
          />
        )}
      </DialogContent>
    </DialogRoot>
  )
}

function DetailsContent({
  model,
  inCompare,
  compareFull,
  onToggleCompare,
  onClose,
}: {
  model: CatalogModelView
  inCompare: boolean
  compareFull: boolean
  onToggleCompare: () => void
  onClose: () => void
}) {
  const { provider, name } = splitModelId(model.id)
  const levels = fmtReasoningLevels(model.reasoning_options)
  const bench = model.benchmarks
  const arena = bench?.design_arena ?? []
  // Design Arena publishes several arenas ("models", "agents"); name the arena
  // only when this model has results in more than one
  const multiArena = new Set(arena.map((a) => a.arena)).size > 1

  return (
    <>
      <DialogHeader pr={10}>
        <DialogTitle fontFamily="mono" wordBreak="break-all">
          {name}
        </DialogTitle>
      </DialogHeader>
      <DialogBody>
        <VStack align="stretch" gap={4}>
          <HStack gap={1} align="center" minW={0}>
            <Code fontFamily="mono" fontSize="xs" px={2} py={1} wordBreak="break-all">
              {model.id}
            </Code>
            <CopyButton text={model.id} label={`copy ${model.id}`} />
          </HStack>

          <HStack gap={1} flexWrap="wrap">
            <Badge variant="outline" colorPalette="gray">
              {provider}
            </Badge>
            {model.free && (
              <Badge colorPalette="green" variant="subtle">
                free
              </Badge>
            )}
            {model.responses_api && (
              <Badge colorPalette="blue" variant="subtle">
                responses API
              </Badge>
            )}
            {model.reasoning && (
              <Badge colorPalette="purple" variant="subtle">
                reasoning
              </Badge>
            )}
          </HStack>

          {model.description && (
            <Text fontSize="sm" color="fg.muted" whiteSpace="pre-wrap">
              {model.description}
            </Text>
          )}

          <SimpleGrid columns={{ base: 2, sm: 4 }} gap={3}>
            <Fact label="Context window">
              {model.context_window ? fmtTokens(model.context_window) : "—"}
            </Fact>
            <Fact label="Max output">
              {model.max_output_tokens ? fmtTokens(model.max_output_tokens) : "—"}
            </Fact>
            <Fact label="Input modalities">
              {(model.input_modalities ?? []).length > 0 ? (
                <HStack gap={1} flexWrap="wrap">
                  {(model.input_modalities ?? []).map((m) => (
                    <Badge key={m} variant="subtle">
                      {m}
                    </Badge>
                  ))}
                </HStack>
              ) : (
                "—"
              )}
            </Fact>
            <Fact label="Reasoning levels">{levels || (model.reasoning ? "on/off" : "—")}</Fact>
          </SimpleGrid>

          <VStack align="stretch" gap={3} borderTopWidth="1px" pt={4}>
            <Heading size="sm">Benchmarks</Heading>
            {bench ? (
              <>
                <BenchmarkScores benchmarks={bench} size="lg" />
                {arena.length > 0 && (
                  <VStack align="stretch" gap={1}>
                    <Text fontSize="sm" fontWeight="medium">
                      Design Arena
                    </Text>
                    <Box overflowX="auto">
                      <Table.Root size="sm">
                        <Table.Header>
                          <Table.Row>
                            {multiArena && <Table.ColumnHeader>Arena</Table.ColumnHeader>}
                            <Table.ColumnHeader>Category</Table.ColumnHeader>
                            <Table.ColumnHeader textAlign="end">Elo</Table.ColumnHeader>
                            <Table.ColumnHeader textAlign="end">Win rate</Table.ColumnHeader>
                            <Table.ColumnHeader textAlign="end">Rank</Table.ColumnHeader>
                          </Table.Row>
                        </Table.Header>
                        <Table.Body>
                          {arena.map((a, i) => (
                            <Table.Row key={`${a.arena}-${a.category}-${i}`}>
                              {multiArena && <Table.Cell>{a.arena}</Table.Cell>}
                              <Table.Cell>{arenaCategoryLabel(a.category)}</Table.Cell>
                              <Table.Cell textAlign="end">{Math.round(a.elo)}</Table.Cell>
                              <Table.Cell textAlign="end">{fmtScore(a.win_rate)}%</Table.Cell>
                              <Table.Cell textAlign="end">#{a.rank}</Table.Cell>
                            </Table.Row>
                          ))}
                        </Table.Body>
                      </Table.Root>
                    </Box>
                  </VStack>
                )}
                <Text fontSize="xs" color="fg.muted">
                  Scores: Artificial Analysis indexes and Design Arena, via OpenRouter
                  (matched to{" "}
                  <Link
                    href={`${OPENROUTER_MODEL_URL}${bench.source_id}`}
                    target="_blank"
                    rel="noopener noreferrer"
                    colorPalette="blue"
                    color="colorPalette.fg"
                    wordBreak="break-all"
                  >
                    {bench.source_id} <ExternalLink size={12} />
                  </Link>
                  )
                </Text>
              </>
            ) : (
              <Text fontSize="sm" color="fg.muted">
                No benchmark data is published for this model (common for free and
                stealth models).
              </Text>
            )}
          </VStack>
        </VStack>
      </DialogBody>
      <DialogFooter flexWrap="wrap">
        <Button
          variant={inCompare ? "solid" : "outline"}
          colorPalette="blue"
          disabled={!inCompare && compareFull}
          onClick={onToggleCompare}
        >
          <GitCompare /> {inCompare ? "Remove from compare" : "Add to compare"}
        </Button>
        <Button variant="outline" onClick={onClose}>
          Close
        </Button>
      </DialogFooter>
      <DialogCloseTrigger />
    </>
  )
}
