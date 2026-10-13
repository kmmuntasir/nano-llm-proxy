import { useMemo, useState } from "react"
import { useQuery } from "@tanstack/react-query"
import {
  Box,
  Button,
  Card,
  Code,
  Field,
  Heading,
  HStack,
  Input,
  NativeSelect,
  SimpleGrid,
  Switch,
  Tag,
  Text,
  VStack,
} from "@chakra-ui/react"
import { Bot, GitCompare, Info } from "lucide-react"
import { api } from "../api/client"
import ModelCard, { splitModelId } from "../components/ModelCard"
import CopyButton from "../components/CopyButton"
import ClaudeCodeSetupDialog from "../components/ClaudeCodeSetupDialog"
import BenchmarkScores from "../components/BenchmarkScores"
import ModelDetailsDialog from "../components/ModelDetailsDialog"
import ModelCompareDialog, { MAX_COMPARE } from "../components/ModelCompareDialog"
import type { CatalogModelView, CatalogModelsView } from "../api/types"

// Models page: every model the gateway currently serves, merged across
// providers. Read-only browsing — search plus filters over provider,
// context size, reasoning, responses support, benchmarks, and input
// modalities; sortable by benchmark index or context window. Up to
// MAX_COMPARE models can be collected into a side-by-side comparison.

const MODALITIES = ["text", "image", "audio", "video", "pdf"]

const CONTEXT_TIERS = [
  { label: "Any context", min: 0 },
  { label: "≥ 32K", min: 32_000 },
  { label: "≥ 128K", min: 128_000 },
  { label: "≥ 200K", min: 200_000 },
  { label: "≥ 1M", min: 1_000_000 },
]

type SortKey = "default" | "intelligence_index" | "coding_index" | "agentic_index" | "context"

const SORT_OPTIONS: { value: SortKey; label: string }[] = [
  { value: "default", label: "Default" },
  { value: "intelligence_index", label: "Intelligence" },
  { value: "coding_index", label: "Coding" },
  { value: "agentic_index", label: "Agentic" },
  { value: "context", label: "Context window" },
]

function sortValue(e: CatalogModelView, key: SortKey): number | undefined {
  if (key === "default") return undefined
  if (key === "context") return e.context_window || undefined
  return e.benchmarks?.[key]
}

// descending; entries without the value keep their relative order at the end
// (Array.prototype.sort is stable)
function sortEntries(list: CatalogModelView[], key: SortKey): CatalogModelView[] {
  if (key === "default") return list
  return [...list].sort((a, b) => {
    const va = sortValue(a, key)
    const vb = sortValue(b, key)
    if (va === undefined && vb === undefined) return 0
    if (va === undefined) return 1
    if (vb === undefined) return -1
    return vb - va
  })
}

export default function ModelsPage() {
  const { data, isLoading, error } = useQuery({
    queryKey: ["models"],
    queryFn: () => api<CatalogModelsView>("/api/models"),
    staleTime: 60_000,
  })

  const [search, setSearch] = useState("")
  const [provider, setProvider] = useState("all")
  const [minCtx, setMinCtx] = useState(0)
  const [reasoningOnly, setReasoningOnly] = useState(false)
  const [responsesOnly, setResponsesOnly] = useState(false)
  const [modalities, setModalities] = useState<string[]>([])
  const [benchOnly, setBenchOnly] = useState(false)
  const [sortKey, setSortKey] = useState<SortKey>("default")
  const [claudeOpen, setClaudeOpen] = useState(false)
  // the details dialog keeps its model while closing so the exit animation
  // doesn't render an empty dialog
  const [detailsId, setDetailsId] = useState<string | null>(null)
  const [detailsOpen, setDetailsOpen] = useState(false)
  const [compare, setCompare] = useState<string[]>([])
  const [compareOpen, setCompareOpen] = useState(false)

  const entries = data?.data ?? []
  const providers = useMemo(
    () => [...new Set(entries.map((e) => e.id.split("/")[0]))].sort(),
    [entries],
  )

  const filtered = entries.filter((e) => {
    if (benchOnly && !e.benchmarks) return false
    const prov = e.id.split("/")[0]
    if (provider !== "all" && prov !== provider) return false
    if (minCtx && (e.context_window ?? 0) < minCtx) return false
    if (reasoningOnly && !e.reasoning) return false
    if (responsesOnly && !e.responses_api) return false
    if (modalities.length > 0) {
      const mods = e.input_modalities ?? []
      if (!modalities.every((m) => mods.includes(m))) return false
    }
    if (search) {
      const q = search.toLowerCase()
      const hay = `${e.id} ${e.description ?? ""} ${e.benchmarks?.source_name ?? ""}`.toLowerCase()
      if (!hay.includes(q)) return false
    }
    return true
  })

  const toggleModality = (m: string) =>
    setModalities((cur) => (cur.includes(m) ? cur.filter((x) => x !== m) : [...cur, m]))

  const sorted = sortEntries(filtered, sortKey)
  const benchStatus = data?.benchmarks_status

  const addCompare = (id: string) =>
    setCompare((cur) => (cur.includes(id) || cur.length >= MAX_COMPARE ? cur : [...cur, id]))
  const removeCompare = (id: string) => setCompare((cur) => cur.filter((x) => x !== id))
  const toggleCompare = (id: string) =>
    compare.includes(id) ? removeCompare(id) : addCompare(id)
  const compareFull = compare.length >= MAX_COMPARE

  const openDetails = (id: string) => {
    setDetailsId(id)
    setDetailsOpen(true)
  }
  const detailsModel = entries.find((e) => e.id === detailsId)

  const origin = window.location.origin

  return (
    <VStack align="stretch" gap={6}>
      <Heading size="lg">Models</Heading>

      <Card.Root>
        <Card.Body px={4} py={3}>
          <HStack justify="space-between" flexWrap="wrap" gap={3}>
            <VStack align="start" gap={0}>
              <Text fontSize="xs" fontWeight="medium" color="fg.muted" letterSpacing="wide">
                GATEWAY ENDPOINT
              </Text>
              <HStack gap={1}>
                <Code fontFamily="mono" fontSize="sm">
                  {origin}
                </Code>
                <CopyButton text={origin} label="copy base URL" />
              </HStack>
              <Text fontSize="xs" color="fg.muted">
                One URL for OpenAI chat, OpenAI Responses, and Anthropic Messages —
                authenticate with a client key (fg-…).
              </Text>
            </VStack>
            <Button
              size="sm"
              variant="outline"
              colorPalette="blue"
              onClick={() => setClaudeOpen(true)}
            >
              <Bot /> Claude Code setup
            </Button>
          </HStack>
        </Card.Body>
      </Card.Root>

      <Card.Root>
        <Card.Body gap={4}>
          <HStack flexWrap="wrap" gap={3} align="end">
            <Field.Root flex={1} minW="220px">
              <Field.Label>Search</Field.Label>
              <Input autoComplete="off"
                placeholder="model name, description, or benchmark name…"
                value={search}
                onChange={(e) => setSearch(e.target.value)}
              />
            </Field.Root>
            <Field.Root w="170px">
              <Field.Label>Provider</Field.Label>
              <NativeSelect.Root size="sm">
                <NativeSelect.Field value={provider} onChange={(e) => setProvider(e.target.value)}>
                  <option value="all">All providers</option>
                  {providers.map((p) => (
                    <option key={p} value={p}>
                      {p}
                    </option>
                  ))}
                </NativeSelect.Field>
                <NativeSelect.Indicator />
              </NativeSelect.Root>
            </Field.Root>
            <Field.Root w="150px">
              <Field.Label>Context window</Field.Label>
              <NativeSelect.Root size="sm">
                <NativeSelect.Field
                  value={String(minCtx)}
                  onChange={(e) => setMinCtx(Number(e.target.value))}
                >
                  {CONTEXT_TIERS.map((t) => (
                    <option key={t.min} value={t.min}>
                      {t.label}
                    </option>
                  ))}
                </NativeSelect.Field>
                <NativeSelect.Indicator />
              </NativeSelect.Root>
            </Field.Root>
            <Field.Root w="150px">
              <Field.Label>Sort by</Field.Label>
              <NativeSelect.Root size="sm">
                <NativeSelect.Field
                  value={sortKey}
                  onChange={(e) => setSortKey(e.target.value as SortKey)}
                >
                  {SORT_OPTIONS.map((o) => (
                    <option key={o.value} value={o.value}>
                      {o.label}
                    </option>
                  ))}
                </NativeSelect.Field>
                <NativeSelect.Indicator />
              </NativeSelect.Root>
            </Field.Root>
          </HStack>
          <HStack gap={4} flexWrap="wrap" align="center">
            <Switch.Root
              size="sm"
              checked={reasoningOnly}
              onCheckedChange={(e) => setReasoningOnly(e.checked)}
            >
              <Switch.HiddenInput />
              <Switch.Control>
                <Switch.Thumb />
              </Switch.Control>
              <Switch.Label>Reasoning only</Switch.Label>
            </Switch.Root>
            <Switch.Root
              size="sm"
              checked={responsesOnly}
              onCheckedChange={(e) => setResponsesOnly(e.checked)}
            >
              <Switch.HiddenInput />
              <Switch.Control>
                <Switch.Thumb />
              </Switch.Control>
              <Switch.Label>Responses API only</Switch.Label>
            </Switch.Root>
            <Switch.Root
              size="sm"
              checked={benchOnly}
              onCheckedChange={(e) => setBenchOnly(e.checked)}
            >
              <Switch.HiddenInput />
              <Switch.Control>
                <Switch.Thumb />
              </Switch.Control>
              <Switch.Label>Has benchmarks</Switch.Label>
            </Switch.Root>
            <HStack gap={1} flexWrap="wrap">
              {MODALITIES.map((m) => (
                <Button
                  key={m}
                  size="2xs"
                  variant={modalities.includes(m) ? "solid" : "outline"}
                  colorPalette={modalities.includes(m) ? "blue" : "gray"}
                  onClick={() => toggleModality(m)}
                >
                  {m}
                </Button>
              ))}
            </HStack>
          </HStack>
        </Card.Body>
      </Card.Root>

      {isLoading && <Text>Loading…</Text>}
      {error && <Text color="red.fg">Failed to load models</Text>}
      {!isLoading && !error && (
        <HStack gap={2} flexWrap="wrap" fontSize="sm" color="fg.muted">
          <Text>
            {filtered.length} of {entries.length} models
          </Text>
          {benchStatus && benchStatus.fetched_at > 0 && (
            <Text>· Benchmarks updated {new Date(benchStatus.fetched_at * 1000).toLocaleDateString()}</Text>
          )}
          {benchStatus && benchStatus.fetched_at === 0 && benchStatus.error && (
            <Text wordBreak="break-word">· Benchmarks unavailable: {benchStatus.error}</Text>
          )}
        </HStack>
      )}

      <SimpleGrid columns={{ base: 1, md: 2, xl: 3 }} gap={4}>
        {sorted.map((e) => {
          const { provider: prov, name } = splitModelId(e.id)
          const selected = compare.includes(e.id)
          return (
            <ModelCard
              key={e.id}
              name={name}
              modelId={e.id}
              provider={prov}
              description={e.description}
              contextWindow={e.context_window}
              maxOutputTokens={e.max_output_tokens}
              inputModalities={e.input_modalities}
              reasoning={e.reasoning}
              reasoningOptions={e.reasoning_options}
              responsesApi={e.responses_api}
              free={e.free}
              footer={
                <VStack align="stretch" gap={2} mt="auto" pt={2} borderTopWidth="1px">
                  <BenchmarkScores benchmarks={e.benchmarks} />
                  <HStack justify="end" gap={2}>
                    <Button size="2xs" variant="ghost" onClick={() => openDetails(e.id)}>
                      <Info /> Details
                    </Button>
                    <Button
                      size="2xs"
                      variant={selected ? "solid" : "outline"}
                      colorPalette={selected ? "blue" : "gray"}
                      aria-pressed={selected}
                      disabled={!selected && compareFull}
                      title={!selected && compareFull ? `Compare holds up to ${MAX_COMPARE} models` : undefined}
                      onClick={() => toggleCompare(e.id)}
                    >
                      <GitCompare /> {selected ? "Comparing" : "Compare"}
                    </Button>
                  </HStack>
                </VStack>
              }
            />
          )
        })}
      </SimpleGrid>
      {!isLoading && !error && filtered.length === 0 && entries.length > 0 && (
        <Text color="fg.muted" fontSize="sm">
          No models match the current filters.
        </Text>
      )}

      {compare.length > 0 && (
        <Box
          position="sticky"
          bottom={0}
          zIndex={10}
          bg="bg.panel"
          borderWidth="1px"
          rounded="md"
          boxShadow="lg"
          px={3}
          py={2}
        >
          <HStack justify="space-between" flexWrap="wrap" gap={2}>
            <HStack gap={1} flexWrap="wrap" minW={0}>
              {compare.map((id) => (
                <Tag.Root key={id} size="md" maxW="full">
                  <Tag.Label fontFamily="mono" title={id} truncate>
                    {splitModelId(id).name}
                  </Tag.Label>
                  <Tag.EndElement>
                    <Tag.CloseTrigger
                      aria-label={`remove ${id} from compare`}
                      onClick={() => removeCompare(id)}
                    />
                  </Tag.EndElement>
                </Tag.Root>
              ))}
            </HStack>
            <HStack gap={2}>
              <Button size="xs" variant="ghost" onClick={() => setCompare([])}>
                Clear
              </Button>
              <Button size="xs" colorPalette="blue" onClick={() => setCompareOpen(true)}>
                <GitCompare /> Compare ({compare.length})
              </Button>
            </HStack>
          </HStack>
        </Box>
      )}

      <ModelDetailsDialog
        open={detailsOpen}
        onOpenChange={setDetailsOpen}
        model={detailsModel}
        inCompare={detailsId !== null && compare.includes(detailsId)}
        compareFull={compareFull}
        onToggleCompare={() => detailsId !== null && toggleCompare(detailsId)}
      />

      <ModelCompareDialog
        open={compareOpen}
        onOpenChange={setCompareOpen}
        entries={entries}
        selected={compare}
        onAdd={addCompare}
        onRemove={removeCompare}
      />

      <ClaudeCodeSetupDialog
        open={claudeOpen}
        onOpenChange={setClaudeOpen}
        entries={entries}
      />
    </VStack>
  )
}
