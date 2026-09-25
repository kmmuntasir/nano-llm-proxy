import { useMemo, useState } from "react"
import { useQuery } from "@tanstack/react-query"
import {
  Button,
  Card,
  Field,
  Heading,
  HStack,
  Input,
  NativeSelect,
  SimpleGrid,
  Switch,
  Text,
  VStack,
} from "@chakra-ui/react"
import { api } from "../api/client"
import ModelCard from "../components/ModelCard"

// Models page: every model the gateway currently serves, merged across
// providers. Read-only browsing — search plus filters over provider,
// context size, reasoning, responses support, and input modalities.

interface CatalogEntry {
  id: string // "provider/model" (prefix stripped of cosmetic suffixes? no — ids are prefixed+suffixes here)
  owned_by?: string
  context_window?: number
  max_output_tokens?: number
  input_modalities?: string[]
  reasoning?: boolean
  responses_api?: boolean
  free?: boolean
  description?: string
}

const MODALITIES = ["text", "image", "audio", "video", "pdf"]

const CONTEXT_TIERS = [
  { label: "Any context", min: 0 },
  { label: "≥ 32K", min: 32_000 },
  { label: "≥ 128K", min: 128_000 },
  { label: "≥ 200K", min: 200_000 },
  { label: "≥ 1M", min: 1_000_000 },
]

export default function ModelsPage() {
  const { data, isLoading, error } = useQuery({
    queryKey: ["models"],
    queryFn: () => api<{ data: CatalogEntry[] }>("/api/models"),
    staleTime: 60_000,
  })

  const [search, setSearch] = useState("")
  const [provider, setProvider] = useState("all")
  const [minCtx, setMinCtx] = useState(0)
  const [reasoningOnly, setReasoningOnly] = useState(false)
  const [responsesOnly, setResponsesOnly] = useState(false)
  const [modalities, setModalities] = useState<string[]>([])

  const entries = data?.data ?? []
  const providers = useMemo(
    () => [...new Set(entries.map((e) => e.id.split("/")[0]))].sort(),
    [entries],
  )

  const filtered = entries.filter((e) => {
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
      const hay = `${e.id} ${e.description ?? ""}`.toLowerCase()
      if (!hay.includes(q)) return false
    }
    return true
  })

  const toggleModality = (m: string) =>
    setModalities((cur) => (cur.includes(m) ? cur.filter((x) => x !== m) : [...cur, m]))

  return (
    <VStack align="stretch" gap={6}>
      <Heading size="lg">Models</Heading>

      <Card.Root>
        <Card.Body gap={4}>
          <HStack flexWrap="wrap" gap={3} align="end">
            <Field.Root flex={1} minW="220px">
              <Field.Label>Search</Field.Label>
              <Input autoComplete="off"
                placeholder="model name or description…"
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
        <Text fontSize="sm" color="fg.muted">
          {filtered.length} of {entries.length} models
        </Text>
      )}

      <SimpleGrid columns={{ base: 1, md: 2, xl: 3 }} gap={4}>
        {filtered.map((e) => {
          const [prov, ...rest] = e.id.split("/")
          return (
            <ModelCard
              key={e.id}
              name={rest.join("/") || e.id}
              provider={prov}
              description={e.description}
              contextWindow={e.context_window}
              maxOutputTokens={e.max_output_tokens}
              inputModalities={e.input_modalities}
              reasoning={e.reasoning}
              responsesApi={e.responses_api}
              free={e.free}
            />
          )
        })}
      </SimpleGrid>
      {!isLoading && !error && filtered.length === 0 && entries.length > 0 && (
        <Text color="fg.muted" fontSize="sm">
          No models match the current filters.
        </Text>
      )}
    </VStack>
  )
}
