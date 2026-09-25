import { useId, useMemo, useRef, useState } from "react"
import { Box, Input, Text } from "@chakra-ui/react"
import type { ReactNode } from "react"

// ComboSelect is the searchable dropdown used across the GUI (model pickers
// on Settings and the Claude Code setup dialog). Type to filter, then pick
// with the mouse or the keyboard: ArrowUp/ArrowDown move the highlight
// (wrapping), Enter picks it, Escape closes, Tab closes and moves on.

export interface ComboOption {
  value: string
  label: string
  /** extra text matched by the filter but not displayed */
  search?: string
}

interface ComboSelectProps {
  options: ComboOption[]
  value: string
  onChange: (value: string) => void
  placeholder?: string
  /** a leading, always-visible option with the empty value ("None — …") */
  noneLabel?: string
  emptyText?: string
  /** mono font for the input and the options (model ids) */
  mono?: boolean
  /** inline label rendered inside the input's left edge */
  startElement?: ReactNode
  ariaLabel?: string
}

// the inline label ("Opus") lives on an overlay above the input's left edge
// (Chakra's InputGroup only reserves its own height as padding — wider labels
// end up under the typed text), and the input reserves this much space for it
const LABEL_PS = "4.25rem"

export default function ComboSelect({
  options,
  value,
  onChange,
  placeholder,
  noneLabel,
  emptyText = "No matches",
  mono,
  startElement,
  ariaLabel,
}: ComboSelectProps) {
  const listId = useId()
  const [open, setOpen] = useState(false)
  const [query, setQuery] = useState("")
  const [highlight, setHighlight] = useState(0)
  const inputRef = useRef<HTMLInputElement>(null)
  const optionRefs = useRef<(HTMLDivElement | null)[]>([])

  // displayed list: the "None" row first (it is a choice, not a filter hit),
  // then the options matching the query on label + search text
  const items = useMemo<{ value: string; label: string }[]>(() => {
    const out: { value: string; label: string }[] = []
    if (noneLabel !== undefined) out.push({ value: "", label: noneLabel })
    const q = query.toLowerCase()
    if (q === "") {
      for (const o of options) out.push(o)
    } else {
      for (const o of options) {
        if (o.label.toLowerCase().includes(q) || (o.search ?? "").toLowerCase().includes(q)) {
          out.push(o)
        }
      }
    }
    return out
  }, [options, query, noneLabel])

  const labelOf = (v: string) => options.find((o) => o.value === v)?.label ?? v

  // highlight the first real match after the "None" row, so Enter on a
  // fresh query picks a model rather than clearing the slot
  const firstMatchIndex = (q: string) => {
    if (noneLabel === undefined) return 0
    const ql = q.toLowerCase()
    const any = options.some(
      (o) => o.label.toLowerCase().includes(ql) || (o.search ?? "").toLowerCase().includes(ql),
    )
    return any ? 1 : 0
  }

  const openList = () => {
    setQuery("")
    // items still reflects the previous query here; the list is about to
    // become [none?, ...options], so locate the current value in THAT list
    const full = noneLabel !== undefined ? [{ value: "", label: noneLabel }, ...options] : options
    const current = full.findIndex((o) => o.value === value)
    setHighlight(current >= 0 ? current : 0)
    setOpen(true)
  }

  const pick = (i: number) => {
    const item = items[i]
    if (!item) return
    onChange(item.value)
    setOpen(false)
    inputRef.current?.focus()
  }

  // keep the highlighted row visible while arrowing through a long list
  const scrollTo = (i: number) => {
    requestAnimationFrame(() => optionRefs.current[i]?.scrollIntoView({ block: "nearest" }))
  }

  const move = (delta: 1 | -1) => {
    if (items.length === 0) return
    const next = (highlight + delta + items.length) % items.length
    setHighlight(next)
    scrollTo(next)
  }

  const onKeyDown = (e: React.KeyboardEvent) => {
    if (!open && (e.key === "ArrowDown" || e.key === "ArrowUp")) {
      e.preventDefault()
      openList()
      return
    }
    switch (e.key) {
      case "ArrowDown":
        e.preventDefault()
        move(1)
        break
      case "ArrowUp":
        e.preventDefault()
        move(-1)
        break
      case "Enter":
        if (open) {
          e.preventDefault() // pick, don't submit any surrounding form
          pick(highlight)
        }
        break
      case "Escape":
        if (open) {
          e.preventDefault()
          setOpen(false)
        }
        break
      case "Home":
        if (open && items.length > 0) {
          e.preventDefault()
          setHighlight(0)
          scrollTo(0)
        }
        break
      case "End":
        if (open && items.length > 0) {
          e.preventDefault()
          setHighlight(items.length - 1)
          scrollTo(items.length - 1)
        }
        break
    }
  }

  return (
    <Box position="relative" w="full">
      {startElement && (
        <Box
          position="absolute"
          insetInlineStart="0"
          top="0"
          bottom="0"
          zIndex={1}
          display="flex"
          alignItems="center"
          pl={3}
          pointerEvents="none"
        >
          {startElement}
        </Box>
      )}
      <Input
        ref={inputRef}
        role="combobox"
        aria-expanded={open}
        aria-controls={open ? listId : undefined}
        aria-activedescendant={open && items[highlight] ? `${listId}-opt-${highlight}` : undefined}
        aria-label={ariaLabel}
        autoComplete="off"
        placeholder={placeholder}
        fontFamily={mono ? "mono" : undefined}
        ps={startElement ? LABEL_PS : undefined}
        value={open ? query : value ? labelOf(value) : ""}
        onChange={(e) => {
          const q = e.target.value
          setQuery(q)
          setHighlight(firstMatchIndex(q))
          setOpen(true)
        }}
        onFocus={openList}
        onBlur={() => setOpen(false)}
        onKeyDown={onKeyDown}
      />
      {open && (
        <Box
          id={listId}
          role="listbox"
          position="absolute"
          zIndex={20}
          top="100%"
          left={0}
          right={0}
          mt={1}
          bg="bg.panel"
          borderWidth="1px"
          rounded="md"
          boxShadow="md"
          maxH="240px"
          overflowY="auto"
          // clicking an option must not blur the input away before the click lands
          onMouseDown={(e) => e.preventDefault()}
        >
          {items.length === 0 && (
            <Text px={3} py={2} fontSize="sm" color="fg.muted">
              {emptyText} {query !== "" && <>“{query}”</>}
            </Text>
          )}
          {items.map((o, i) => (
            <Box
              key={`${o.value}-${i}`}
              ref={(el: HTMLDivElement | null) => {
                optionRefs.current[i] = el
              }}
              id={`${listId}-opt-${i}`}
              role="option"
              aria-selected={o.value === value}
              px={3}
              py={2}
              fontSize="sm"
              fontFamily={mono ? "mono" : undefined}
              cursor="pointer"
              // an unmistakable accent — a subtle gray highlight reads as
              // "keyboard does nothing"
              bg={i === highlight ? "blue.subtle" : undefined}
              color={i === highlight ? "blue.fg" : undefined}
              onMouseEnter={() => setHighlight(i)}
              onClick={() => pick(i)}
              whiteSpace="nowrap"
              overflow="hidden"
              textOverflow="ellipsis"
              title={o.label}
            >
              {o.label}
            </Box>
          ))}
        </Box>
      )}
    </Box>
  )
}
