import { Box, HStack, Progress, Text } from "@chakra-ui/react"
import type { ModelBenchmarksView } from "../api/types"

// BenchmarkScores renders the three Artificial Analysis headline indexes
// (0–100, higher is better) as label + value + thin bar. Used on the model
// cards ("sm") and in the details dialog ("lg").

export type BenchmarkIndexKey = "intelligence_index" | "coding_index" | "agentic_index"

export const BENCHMARK_INDEXES: { key: BenchmarkIndexKey; label: string; palette: string }[] = [
  { key: "intelligence_index", label: "Intelligence", palette: "blue" },
  { key: "coding_index", label: "Coding", palette: "teal" },
  { key: "agentic_index", label: "Agentic", palette: "purple" },
]

const MISSING = "—"

// fmtScore keeps at most one decimal and drops a trailing ".0"
export function fmtScore(n: number | undefined): string {
  if (n === undefined) return MISSING
  return String(Math.round(n * 10) / 10)
}

// Design Arena category slugs → display names; unknown slugs show as-is
const ARENA_CATEGORY_LABELS: Record<string, string> = {
  "3d": "3D",
  agenticgamedev: "Agentic game dev",
  androidnative: "Android native",
  asciiart: "ASCII art",
  codecategories: "Code categories",
  datavisualization: "Data visualization",
  dataviz: "Data visualization",
  fullstack: "Full stack",
  gamedev: "Game dev",
  godotgamedev: "Godot game dev",
  graphicdesign: "Graphic design",
  image: "Image",
  imageediting: "Image editing",
  logo: "Logo",
  mobileapps: "Mobile apps",
  svg: "SVG",
  uicomponent: "UI component",
  webapps: "Web apps",
  website: "Website",
}

export function arenaCategoryLabel(slug: string): string {
  return ARENA_CATEGORY_LABELS[slug] ?? slug
}

const clampPct = (n: number) => Math.min(100, Math.max(0, n))

export default function BenchmarkScores({
  benchmarks,
  size = "sm",
}: {
  benchmarks?: ModelBenchmarksView
  size?: "sm" | "lg"
}) {
  if (!benchmarks) {
    return (
      <Text fontSize="xs" color="fg.muted">
        No benchmark data
      </Text>
    )
  }

  const lg = size === "lg"
  return (
    <HStack gap={lg ? 5 : 3} flexWrap="wrap" align="start">
      {BENCHMARK_INDEXES.map((ix) => {
        const value = benchmarks[ix.key]
        return (
          <Box key={ix.key} flex="1 1 0" minW={lg ? "7rem" : "5rem"}>
            <HStack justify="space-between" gap={1} align="baseline">
              <Text fontSize={lg ? "sm" : "2xs"} color="fg.muted" truncate>
                {ix.label}
              </Text>
              <Text
                fontSize={lg ? "xl" : "xs"}
                fontWeight="semibold"
                color={value === undefined ? "fg.subtle" : undefined}
              >
                {fmtScore(value)}
              </Text>
            </HStack>
            <Progress.Root
              value={value === undefined ? 0 : clampPct(value)}
              size={lg ? "sm" : "xs"}
              colorPalette={ix.palette}
              mt={1}
              aria-label={`${ix.label} index`}
            >
              <Progress.Track>
                <Progress.Range />
              </Progress.Track>
            </Progress.Root>
          </Box>
        )
      })}
    </HStack>
  )
}
