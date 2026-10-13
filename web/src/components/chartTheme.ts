import { useColorMode } from "./ui/color-mode"

// Shared Recharts palette. Eight categorical slots, each stepped per color
// mode so adjacent series stay distinguishable on both surfaces; "other" is
// a neutral reserved for folded series, "danger" for error measures. Chrome
// (grid, ticks, tooltip surface) follows the same light/dark split.

const SERIES = {
  light: ["#2a78d6", "#eb6834", "#2e9a5e", "#7c5cd6", "#d0458f", "#1597a8", "#c99a06", "#9a6a3f"],
  dark: ["#3987e5", "#d95926", "#3fb373", "#9479ec", "#e05ea3", "#27b3c4", "#e0b225", "#b58358"],
}

const CHROME = {
  light: {
    other: "#a3a29b",
    danger: "#d43c3c",
    grid: "#e1e0d9",
    tick: "#898781",
    axis: "#c3c2b7",
    surface: "#ffffff",
    ink: "#0b0b0b",
    ink2: "#52514e",
  },
  dark: {
    other: "#6b6a65",
    danger: "#e5534b",
    grid: "#2c2c2a",
    tick: "#898781",
    axis: "#383835",
    surface: "#1a1a19",
    ink: "#ffffff",
    ink2: "#c3c2b7",
  },
}

export type ChartTheme = ReturnType<typeof useChartTheme>

export function useChartTheme() {
  const { colorMode } = useColorMode()
  const mode = colorMode === "dark" ? "dark" : "light"
  const chrome = CHROME[mode]
  const series = SERIES[mode]
  return {
    ...chrome,
    series,
    /** color for the i-th series; "other" always gets the neutral slot */
    color: (i: number, key?: string) => (key === "other" ? chrome.other : series[i % series.length]),
    tooltip: {
      cursor: { stroke: chrome.grid, strokeWidth: 1 },
      contentStyle: {
        background: chrome.surface,
        border: `1px solid ${chrome.grid}`,
        borderRadius: 8,
        fontSize: 12,
      },
      labelStyle: { color: chrome.ink2, fontSize: 12 },
      itemStyle: { color: chrome.ink, fontSize: 12 },
    },
    axis: {
      // the style copy matters: inherited CSS font-size beats the SVG attribute
      tick: { fill: chrome.tick, fontSize: 11, style: { fontSize: 11 } },
      tickLine: false,
      axisLine: { stroke: chrome.axis },
    },
    legend: { wrapperStyle: { fontSize: 12, color: chrome.ink2 } },
  }
}
