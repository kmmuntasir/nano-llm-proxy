import type { ReactNode } from "react"
import {
  Button,
  Card,
  Field,
  HStack,
  Input,
  NativeSelect,
  SimpleGrid,
  Text,
  VStack,
} from "@chakra-ui/react"
import { X } from "lucide-react"
import { useUsageFacets } from "../../api/usage"
import { fmtDateTime, fromLocalInput, providerLabel, toLocalInput } from "./format"
import { RANGE_PRESETS, STATUSES, hasSelectFilters, useUsageFilters } from "./urlState"
import type { FilterKey, FilterPatch } from "./urlState"

interface Option {
  value: string
  label: string
}

const ANY = ""
// model options carry provider + model so picking one sets both filters
const modelValue = (provider: string, model: string) => JSON.stringify([provider, model])

function parseModelValue(v: string): [string, string] | null {
  try {
    const parsed: unknown = JSON.parse(v)
    if (Array.isArray(parsed) && parsed.length === 2 && parsed.every((x) => typeof x === "string")) {
      return [parsed[0], parsed[1]]
    }
  } catch {
    // not a model option
  }
  return null
}

/** Ensures the current URL value is selectable even if the facets don't list it. */
function withCurrent(options: Option[], current: string | undefined, label: string): Option[] {
  if (!current || options.some((o) => o.value === current)) return options
  return [{ value: current, label }, ...options]
}

function FilterSelect({
  label,
  value,
  options,
  onChange,
  anyLabel = "All",
}: {
  label: string
  value: string
  options: Option[]
  onChange: (v: string) => void
  anyLabel?: string
}) {
  return (
    <Field.Root minW={0}>
      <Field.Label fontSize="xs">{label}</Field.Label>
      <NativeSelect.Root size="xs">
        <NativeSelect.Field value={value} onChange={(e) => onChange(e.target.value)}>
          <option value={ANY}>{anyLabel}</option>
          {options.map((o) => (
            <option key={o.value} value={o.value}>
              {o.label}
            </option>
          ))}
        </NativeSelect.Field>
        <NativeSelect.Indicator />
      </NativeSelect.Root>
    </Field.Root>
  )
}

// UsageFilterBar: range presets + custom from/to + facet selects, all read
// from and written to the URL. `locked` filters are fixed by the page (e.g.
// the model on the model detail page) — they are hidden and survive "clear".
export default function UsageFilterBar({ locked = [] }: { locked?: FilterKey[] }) {
  const { filters, preset, isAdmin, searchParams, setFilters } = useUsageFilters()
  const facets = useUsageFacets(filters.from, filters.to, filters.tz)
  const f = facets.data
  const show = (k: FilterKey) => !locked.includes(k)

  const set = (patch: FilterPatch) => setFilters(patch)

  const userOptions = withCurrent(
    (f?.users ?? []).map((u) => ({ value: String(u.id), label: u.email || `user #${u.id}` })),
    filters.userId ? String(filters.userId) : undefined,
    `user #${filters.userId}`,
  )

  const providerOptions = withCurrent(
    // "" (rejected before routing) can't be sent as a filter — an empty
    // provider param means "any" — so it is only listed via its models
    (f?.providers ?? []).filter(Boolean).map((p) => ({ value: p, label: p })),
    filters.provider,
    filters.provider ?? "",
  )

  const models = (f?.models ?? []).filter((m) => !filters.provider || m.provider === filters.provider)
  const currentModel = filters.model
    ? (models.find((m) => m.model === filters.model) ?? {
        provider: filters.provider ?? "",
        model: filters.model,
      })
    : undefined
  const modelOptions = withCurrent(
    models.map((m) => ({
      value: modelValue(m.provider, m.model),
      label: filters.provider ? m.model : `${providerLabel(m.provider)}/${m.model}`,
    })),
    currentModel ? modelValue(currentModel.provider, currentModel.model) : undefined,
    currentModel ? `${providerLabel(currentModel.provider)}/${currentModel.model}` : "",
  )

  const emailById = new Map((f?.users ?? []).map((u) => [u.id, u.email]))
  const keyOptions = withCurrent(
    (f?.keys ?? [])
      .filter((k) => !filters.userId || k.userId === filters.userId)
      .map((k) => {
        const name = k.alias || `#${k.id}`
        const owner = isAdmin ? emailById.get(k.userId) : undefined
        return { value: String(k.id), label: owner ? `${name} (${owner})` : name }
      }),
    filters.keyId ? String(filters.keyId) : undefined,
    `#${filters.keyId}`,
  )

  const surfaceOptions = withCurrent(
    (f?.surfaces ?? []).filter(Boolean).map((s) => ({ value: s, label: s })),
    filters.surface,
    filters.surface ?? "",
  )

  const selects: ReactNode[] = []
  if (isAdmin && show("userId")) {
    selects.push(
      <FilterSelect
        key="user"
        label="User"
        value={filters.userId ? String(filters.userId) : ANY}
        options={userOptions}
        onChange={(v) => {
          // drop a key filter that belongs to someone else
          const keyOwner = f?.keys.find((k) => k.id === filters.keyId)?.userId
          const keyMismatch = !!v && keyOwner !== undefined && String(keyOwner) !== v
          set(keyMismatch ? { userId: v, keyId: undefined } : { userId: v })
        }}
      />,
    )
  }
  if (show("provider")) {
    selects.push(
      <FilterSelect
        key="provider"
        label="Provider"
        value={filters.provider ?? ANY}
        options={providerOptions}
        onChange={(v) => {
          const keepModel =
            !!filters.model &&
            (!v || (f?.models ?? []).some((m) => m.provider === v && m.model === filters.model))
          set({ provider: v, model: keepModel ? filters.model : undefined })
        }}
      />,
    )
  }
  if (show("model")) {
    selects.push(
      <FilterSelect
        key="model"
        label="Model"
        value={currentModel ? modelValue(currentModel.provider, currentModel.model) : ANY}
        options={modelOptions}
        onChange={(v) => {
          const pm = parseModelValue(v)
          if (!pm) {
            set({ model: undefined })
            return
          }
          set({ provider: pm[0] || filters.provider, model: pm[1] })
        }}
      />,
    )
  }
  if (show("keyId")) {
    selects.push(
      <FilterSelect
        key="key"
        label="Client key"
        value={filters.keyId ? String(filters.keyId) : ANY}
        options={keyOptions}
        onChange={(v) => set({ keyId: v })}
      />,
    )
  }
  if (show("surface")) {
    selects.push(
      <FilterSelect
        key="surface"
        label="Surface"
        value={filters.surface ?? ANY}
        options={surfaceOptions}
        onChange={(v) => set({ surface: v })}
      />,
    )
  }
  if (show("status")) {
    selects.push(
      <FilterSelect
        key="status"
        label="Status"
        value={filters.status ?? ANY}
        options={STATUSES.map((s) => ({ value: s, label: s }))}
        onChange={(v) => set({ status: v })}
      />,
    )
  }

  const clearable = hasSelectFilters(searchParams, isAdmin ? locked : [...locked, "userId"])
  const clear = () => {
    const patch: FilterPatch = {}
    for (const k of ["userId", "keyId", "provider", "model", "surface", "status"] as const) {
      if (show(k)) patch[k] = undefined
    }
    set(patch)
  }

  return (
    <Card.Root size="sm">
      <Card.Body gap={3}>
        <HStack gap={2} flexWrap="wrap" align="end">
          <HStack gap={1} flexWrap="wrap" role="group" aria-label="Time range">
            {RANGE_PRESETS.map((p) => (
              <Button
                key={p.id}
                size="xs"
                variant={preset === p.id ? "solid" : "outline"}
                colorPalette={preset === p.id ? "blue" : "gray"}
                aria-pressed={preset === p.id}
                onClick={() => set({ range: p.id, from: undefined, to: undefined })}
              >
                {p.label}
              </Button>
            ))}
          </HStack>
          <HStack gap={2} flexWrap="wrap" align="end">
            <Field.Root w={{ base: "full", sm: "200px" }}>
              <Field.Label fontSize="xs">From</Field.Label>
              <Input
                autoComplete="off"
                type="datetime-local"
                size="xs"
                value={toLocalInput(filters.from)}
                onChange={(e) => {
                  const from = fromLocalInput(e.target.value)
                  if (from === undefined || from >= filters.to) return
                  set({ range: undefined, from: String(from), to: String(filters.to) })
                }}
              />
            </Field.Root>
            <Field.Root w={{ base: "full", sm: "200px" }}>
              <Field.Label fontSize="xs">To</Field.Label>
              <Input
                autoComplete="off"
                type="datetime-local"
                size="xs"
                value={toLocalInput(filters.to)}
                onChange={(e) => {
                  const to = fromLocalInput(e.target.value)
                  if (to === undefined || to <= filters.from) return
                  set({ range: undefined, from: String(filters.from), to: String(to) })
                }}
              />
            </Field.Root>
          </HStack>
        </HStack>

        {selects.length > 0 && (
          <SimpleGrid columns={{ base: 2, lg: 3, xl: 6 }} gap={2} alignItems="end">
            {selects}
          </SimpleGrid>
        )}

        <HStack justify="space-between" flexWrap="wrap" gap={2}>
          <VStack align="start" gap={0}>
            <Text fontSize="xs" color="fg.muted">
              {fmtDateTime(filters.from)} – {fmtDateTime(filters.to)}
            </Text>
            {facets.error && (
              <Text fontSize="xs" color="red.fg">
                Filter options unavailable: {facets.error.message}
              </Text>
            )}
          </VStack>
          {clearable && (
            <Button size="xs" variant="ghost" onClick={clear}>
              <X /> Clear filters
            </Button>
          )}
        </HStack>
      </Card.Body>
    </Card.Root>
  )
}
