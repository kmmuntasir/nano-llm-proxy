#!/usr/bin/env node
/**
 * validate_md.js — deterministic Markdown linter for the markdown-writer skill.
 *
 * Enforces the rules that LLMs chronically get wrong:
 *   1. Nested code fences: outer fence must use strictly more fence chars than inner.
 *   2. Table cell-count consistency across rows (catches unescaped `|` in cells).
 *   3. Blockquote continuity: blank lines inside a blockquote must begin with `>`.
 *   4. ASCII / Unicode box-drawing alignment: right borders within one fenced
 *      block must share the same column index.
 *   5. Hard line break intent: lines that look like deliberate breaks but lack
 *      trailing two spaces (warning only).
 *
 * Exit codes:
 *   0 = clean
 *   1 = one or more violations
 *   2 = usage / IO error
 *
 * Usage: node validate_md.js <path-to-markdown-file> [--strict]
 *   --strict  promote warnings to errors
 */

'use strict'

const fs = require('fs')
const path = require('path')

const argv = process.argv.slice(2)
const strictFlag = argv.includes('--strict')
const fileArg = argv.find((a) => !a.startsWith('-'))

if (!fileArg) {
  process.stderr.write('usage: validate_md.js <file.md> [--strict]\n')
  process.exit(2)
}

let src
try {
  src = fs.readFileSync(path.resolve(fileArg), 'utf8')
} catch (err) {
  process.stderr.write(`error: cannot read ${fileArg}: ${err.message}\n`)
  process.exit(2)
}

const lines = src.split(/\r?\n/)
const violations = []
const warnings = []

function v(line, msg) {
  violations.push({ line, msg })
}
function w(line, msg) {
  warnings.push({ line, msg })
}

// ---------------------------------------------------------------------------
// 1. Nested code fences
// ---------------------------------------------------------------------------
// A fence opens with a run of backticks or tildes (>=3) optionally followed by
// Rule: outer fence char count must be strictly greater than any inner
// fence's char count of the same kind. If an inner line looks like a fence
// with the same char and count >= outer count, it either prematurely closes
// the outer block (no info string) or renders ambiguously (with info).
// Either way, violation.

const FENCE_RE = /^(\s*)(`{3,}|~{3,})(.*)$/

let openFence = null // { char, count, line }
for (let i = 0; i < lines.length; i++) {
  const m = lines[i].match(FENCE_RE)
  if (!m) continue
  const [, , fence, info] = m
  const char = fence[0]
  const count = fence.length

  if (!openFence) {
    openFence = { char, count, line: i + 1 }
    continue
  }

  const isClose =
    char === openFence.char &&
    count >= openFence.count &&
    info.trim() === ''
  if (isClose) {
    openFence = null
    continue
  }

  if (char === openFence.char && count >= openFence.count) {
    v(
      i + 1,
      `inner fence uses ${count} '${char}'; outer fence (line ${openFence.line}) uses ${openFence.count}. Outer must use strictly more fence chars of the same kind, or switch fence char (backticks vs tildes)`
    )
  }
}
if (openFence) {
  v(openFence.line, `unclosed code fence opened here (no matching close)`)
}

// ---------------------------------------------------------------------------
// Re-scan with fence awareness so subsequent checks skip fenced content.
// Build a boolean array isCode[i] = true when line i is inside a fence.
// ---------------------------------------------------------------------------
const isCode = new Array(lines.length).fill(false)
{
  let open = null
  for (let i = 0; i < lines.length; i++) {
    const m = lines[i].match(FENCE_RE)
    if (m) {
      const char = m[2][0]
      const count = m[2].length
      if (!open) {
        open = { char, count }
        continue // fence line itself not "inside"
      }
      if (char === open.char && count >= open.count && m[3].trim() === '') {
        open = null
        continue
      }
    }
    if (open) isCode[i] = true
  }
}

// ---------------------------------------------------------------------------
// 2. Tables: cell-count consistency
// ---------------------------------------------------------------------------
// A table block is a run of consecutive non-code lines where:
//   - first row has >=2 unescaped pipes (i.e. >=3 cells with edges, or >=1 cell
//     with internal pipes and pipe edges)
//   - second row is a delimiter row: ^\s*\|?\s*:?-{1,}[-:\s|]+\|?\s*$
// For each table, every row must have the same cell count. A mismatch usually
// means an unescaped `|` in cell content.

const CELL_SPLIT_RE = /(?<!\\)\|/ // unescaped pipe
const DELIM_RE = /^\s*\|?\s*:?-{1,}[-:\s|]*\|?\s*$/

let i = 0
while (i < lines.length) {
  if (isCode[i]) {
    i++
    continue
  }
  const line = lines[i]
  const cells = line.split(CELL_SPLIT_RE)
  const hasEdges = line.trim().startsWith('|') || line.trim().endsWith('|')
  const looksRow = hasEdges && cells.length >= 2
  const nextLine = lines[i + 1]
  const isDelimNext =
    nextLine !== undefined && !isCode[i + 1] && DELIM_RE.test(nextLine)
  if (looksRow && isDelimNext) {
    // Table starts here.
    const tableStart = i
    const headerCells = cells.length
    const delimCells = nextLine.split(CELL_SPLIT_RE).length
    const expected = headerCells
    if (delimCells !== expected) {
      v(i + 2, `table delimiter row has ${delimCells} cells, header has ${headerCells}`)
    }
    let j = i + 2
    const rowLines = [i + 1, i + 2]
    while (j < lines.length && !isCode[j]) {
      const l = lines[j]
      if (l.trim() === '') break
      const lc = l.split(CELL_SPLIT_RE).length
      if (lc !== expected) {
        v(j + 1, `table row has ${lc} cells, expected ${expected} (probably an unescaped \`|\` in a cell)`)
      }
      rowLines.push(j + 1)
      j++
    }
    i = j
    continue
  }
  i++
}

// ---------------------------------------------------------------------------
// 3. Blockquote continuity
// ---------------------------------------------------------------------------
// Within a contiguous blockquote run, every line including blank separators
// must begin with `>`. A blank line without `>` ends the blockquote; if the
// next non-blank line is again `>`, GitHub splits it into two <blockquote>
// elements — usually not the author's intent. We flag blank-line-breaks where
// a blockquote resumes immediately after.

for (let k = 0; k < lines.length; k++) {
  if (isCode[k]) continue
  const line = lines[k]
  if (line.trim() === '' && !line.startsWith('>')) {
    // Look forward: is the next non-blank line a blockquote?
    let m = k + 1
    while (m < lines.length && lines[m].trim() === '') m++
    if (m < lines.length && lines[m].startsWith('>')) {
      // And the previous non-blank line was a blockquote?
      let p = k - 1
      while (p >= 0 && lines[p].trim() === '') p--
      if (p >= 0 && lines[p].startsWith('>')) {
        v(k + 1, `blank line inside blockquote missing leading \`>\`; this splits the blockquote into two elements`)
      }
    }
  }
}

// ---------------------------------------------------------------------------
// 4. ASCII / Unicode box-drawing alignment
// ---------------------------------------------------------------------------
// For each fenced block, gather lines that contain right-border characters
// (`│` U+2502, `┃` U+2503, or ASCII `|` when box-drawing peers are present).
// Within a block, every line's rightmost border column should match the modal
// right-border column for that block. Mismatch => jagged border.

const BOX_RIGHT = new Set(['│', '┃', '┐', '┘', '┤', '╗', '╝', '╡', '╣', '╜', '╢'])
const BOX_ANY = /[┌┐└┘├┤┬┴┼─━│┃║═╔╗╚╝╠╣╦╩╬╞╟╚╔╗╣╢╡╕╖╗╘╛╜]/

function rightBorderCols(blockLines) {
  // Returns array of { line, col } for each line that has a right border.
  const out = []
  for (const { text, lineNo } of blockLines) {
    if (!BOX_ANY.test(text)) continue
    // Rightmost column of any box-drawing right-border character on this line.
    let rightmost = -1
    for (let c = 0; c < text.length; c++) {
      const ch = text[c]
      if (BOX_RIGHT.has(ch) || ch === '|') {
        rightmost = c
      }
    }
    if (rightmost >= 0) out.push({ line: lineNo, col: rightmost })
  }
  return out
}

// Walk fence blocks.
{
  let j = 0
  while (j < lines.length) {
    const m = lines[j].match(FENCE_RE)
    if (!m) {
      j++
      continue
    }
    const openChar = m[2][0]
    const openCount = m[2].length
    const startLine = j
    j++
    const blockLines = []
    while (j < lines.length) {
      const m2 = lines[j].match(FENCE_RE)
      if (
        m2 &&
        m2[2][0] === openChar &&
        m2[2].length >= openCount &&
        m2[3].trim() === ''
      ) {
        break
      }
      blockLines.push({ text: lines[j], lineNo: j + 1 })
      j++
    }
    // blockLines now holds inner content. Check alignment.
    const cols = rightBorderCols(blockLines)
    if (cols.length >= 2) {
      // Mode of columns.
      const freq = new Map()
      for (const c of cols) freq.set(c.col, (freq.get(c.col) || 0) + 1)
      let modeCol = -1
      let modeCount = 0
      for (const [col, n] of freq) {
        if (n > modeCount) {
          modeCount = n
          modeCol = col
        }
      }
      for (const c of cols) {
        if (c.col !== modeCol) {
          v(
            c.line,
            `ASCII/box right border at column ${c.col + 1}; expected column ${modeCol + 1} (jagged border — re-pad inner text)`
          )
        }
      }
    }
    j++
  }
}

// ---------------------------------------------------------------------------
// 5. Hard line break intent (warning)
// ---------------------------------------------------------------------------
// Heuristic: a line ending with no trailing whitespace, immediately followed
// by a non-empty line, where the two lines together look like a single
// sentence split across lines. We can't know author intent, so we only warn
// when a line ends with sentence-final punctuation and the next line starts
// with a lowercase letter — a common LLM pattern that fuses into one
// rendered paragraph.

for (let k = 0; k < lines.length - 1; k++) {
  if (isCode[k]) continue
  const a = lines[k]
  const b = lines[k + 1]
  if (!a || isCode[k + 1]) continue
  if (a.endsWith('  ') || a.endsWith('\t')) continue
  if (b.trim() === '') continue
  if (b.startsWith(' ') || b.startsWith('\t')) continue // continuation of list etc.
  if (/[:;]\s*$/.test(a) && /^[a-z]/.test(b.trim())) {
    w(k + 1, `line ends without trailing two spaces; if this is meant to be a hard line break, append two spaces`)
  }
}

// ---------------------------------------------------------------------------
// Report
// ---------------------------------------------------------------------------
let exit = 0
for (const { line, msg } of violations) {
  process.stderr.write(`${fileArg}:${line}: ERROR: ${msg}\n`)
}
for (const { line, msg } of warnings) {
  if (strictFlag) {
    process.stderr.write(`${fileArg}:${line}: ERROR (strict): ${msg}\n`)
  } else {
    process.stderr.write(`${fileArg}:${line}: WARN: ${msg}\n`)
  }
}

if (violations.length > 0) exit = 1
if (strictFlag && warnings.length > 0) exit = 1

if (exit === 0) {
  process.stderr.write(
    `validate_md.js: ${fileArg} OK (${lines.length} lines checked, ${warnings.length} warnings)\n`
  )
}
process.exit(exit)
