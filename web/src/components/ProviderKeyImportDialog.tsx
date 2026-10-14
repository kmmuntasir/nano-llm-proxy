import { useRef, useState } from "react"
import type { ChangeEvent } from "react"
import { useMutation, useQueryClient } from "@tanstack/react-query"
import { Badge, Button, Code, Field, HStack, Input, Text, Textarea, VStack } from "@chakra-ui/react"
import { FileUp, Upload } from "lucide-react"
import { ApiError, post } from "../api/client"
import type {
  ProviderKeyImportRequest,
  ProviderKeyImportResponse,
  ProviderKeyImportResult,
  ProviderKeyImportStatus,
  ProviderView,
} from "../api/types"
import { DataTable } from "./DataTable"
import type { Column } from "./DataTable"
import JsonBlock from "./JsonBlock"
import {
  DialogBody,
  DialogCloseTrigger,
  DialogContent,
  DialogFooter,
  DialogHeader,
  DialogRoot,
  DialogTitle,
} from "./ui/dialog"
import { toaster } from "./ui/toaster"

// ProviderKeyImportDialog bulk-adds upstream keys from a JSON file (or
// pasted text). The text goes to the backend verbatim — it parses the object
// in order and reports duplicate aliases, which JSON.parse here would lose.
// The pasted keys only live in component state and are cleared on close.

const EXAMPLE = `{
  "work-1": "sk-aaa…",
  "work-2": "sk-bbb…"
}`

// the backend bounds request bodies at 1 MiB; refuse bigger files before
// loading them into the textarea
const MAX_FILE_BYTES = 1 << 20

const STATUS_PALETTE: Record<ProviderKeyImportStatus, string> = {
  added: "green",
  duplicate: "gray",
  invalid: "red",
  rejected: "red",
  refused: "orange",
  unverifiable: "yellow",
}

const COLUMNS: Column<ProviderKeyImportResult>[] = [
  {
    header: "Alias",
    render: (r) => (
      <Code fontFamily="mono" fontSize="xs" overflowWrap="break-word" whiteSpace="pre-wrap">
        {r.label || "(no alias)"}
      </Code>
    ),
  },
  {
    header: "Status",
    render: (r) => (
      <Badge variant="subtle" colorPalette={STATUS_PALETTE[r.status] ?? "gray"} rounded="full" px={2}>
        {r.status}
      </Badge>
    ),
  },
  {
    header: "Message",
    render: (r) => (
      <Text fontSize="xs" color="fg.muted" wordBreak="break-word">
        {r.message || "—"}
      </Text>
    ),
  },
]

export default function ProviderKeyImportDialog({
  provider,
  open,
  onOpenChange,
}: {
  provider: ProviderView
  open: boolean
  onOpenChange: (o: boolean) => void
}) {
  const qc = useQueryClient()
  const fileRef = useRef<HTMLInputElement>(null)
  const [content, setContent] = useState("")
  const [fileName, setFileName] = useState("")
  const [error, setError] = useState("")

  const importKeys = useMutation({
    mutationFn: () =>
      post<ProviderKeyImportResponse>(`/api/providers/${provider.id}/keys/import`, {
        content,
      } satisfies ProviderKeyImportRequest),
    onMutate: () => setError(""),
    onSuccess: async (res) => {
      await qc.invalidateQueries({ queryKey: ["providers"] })
      if (res.added > 0) {
        toaster.create({
          title: `${res.added} ${res.added === 1 ? "key" : "keys"} added`,
          type: "success",
        })
        return
      }
      toaster.create({ title: "No keys added", type: "warning" })
    },
    onError: (e) => {
      const msg = e instanceof ApiError ? e.message : "Failed to import the keys"
      setError(msg)
      // 404: the provider was deleted elsewhere — refresh the page's list
      if (e instanceof ApiError && e.status === 404) {
        void qc.invalidateQueries({ queryKey: ["providers"] })
      }
    },
  })
  const busy = importKeys.isPending
  const result = importKeys.data

  const close = () => {
    if (busy) return
    setContent("")
    setFileName("")
    setError("")
    importKeys.reset()
    onOpenChange(false)
  }

  const loadFile = async (e: ChangeEvent<HTMLInputElement>) => {
    const file = e.target.files?.[0]
    // reset so picking the same file again still fires onChange
    e.target.value = ""
    if (!file) return
    if (file.size > MAX_FILE_BYTES) {
      setError("The file is larger than 1 MiB")
      return
    }
    try {
      setContent(await file.text())
      setFileName(file.name)
      setError("")
    } catch {
      setError("Could not read the file")
    }
  }

  return (
    <DialogRoot
      open={open}
      onOpenChange={(e) => {
        if (!e.open) close()
      }}
      size="lg"
      closeOnInteractOutside={false}
      closeOnEscape={!busy}
    >
      <DialogContent>
        <DialogHeader>
          <DialogTitle wordBreak="break-word">Import keys — {provider.name}</DialogTitle>
        </DialogHeader>
        <DialogBody>
          <VStack align="stretch" gap={4}>
            <Text fontSize="sm" color="fg.muted">
              One JSON object: aliases as keys, API keys as values, in rotation order. Up to 200
              keys. Each key is verified against the provider first; keys that pass are added even
              if others fail.
            </Text>
            <JsonBlock code={EXAMPLE} title="Format" />
            <Input
              ref={fileRef}
              type="file"
              accept=".json,application/json,text/plain"
              display="none"
              onChange={(e) => void loadFile(e)}
            />
            <Field.Root invalid={error !== ""}>
              <HStack justify="space-between" width="full" flexWrap="wrap" gap={2}>
                <Field.Label>Keys JSON</Field.Label>
                <HStack gap={2} minW={0}>
                  {fileName && (
                    <Text fontSize="xs" color="fg.muted" truncate>
                      {fileName}
                    </Text>
                  )}
                  <Button
                    size="xs"
                    variant="outline"
                    type="button"
                    disabled={busy}
                    onClick={() => fileRef.current?.click()}
                  >
                    <FileUp /> Choose file
                  </Button>
                </HStack>
              </HStack>
              <Textarea
                fontFamily="mono"
                fontSize="xs"
                rows={8}
                autoComplete="off"
                spellCheck={false}
                placeholder="Paste the JSON here or choose a file"
                value={content}
                disabled={busy}
                onChange={(e) => setContent(e.target.value)}
              />
              {error && <Field.ErrorText wordBreak="break-word">{error}</Field.ErrorText>}
            </Field.Root>
            {busy && (
              <Text fontSize="sm" color="fg.muted">
                Verifying every key against {provider.name} — this can take a while.
              </Text>
            )}
            {result && (
              <VStack align="stretch" gap={2}>
                <Text fontSize="sm" fontWeight="medium">
                  {result.added} of {result.results.length}{" "}
                  {result.results.length === 1 ? "key" : "keys"} added
                </Text>
                <DataTable
                  columns={COLUMNS}
                  rows={result.results}
                  rowKey={(_, i) => String(i)}
                  empty="No entries"
                />
              </VStack>
            )}
          </VStack>
        </DialogBody>
        <DialogFooter>
          <Button type="button" variant="ghost" disabled={busy} onClick={close}>
            Close
          </Button>
          <Button
            type="button"
            colorPalette="blue"
            disabled={content.trim() === ""}
            loading={busy}
            loadingText="Verifying keys…"
            onClick={() => importKeys.mutate()}
          >
            <Upload /> Import
          </Button>
        </DialogFooter>
        <DialogCloseTrigger disabled={busy} />
      </DialogContent>
    </DialogRoot>
  )
}
