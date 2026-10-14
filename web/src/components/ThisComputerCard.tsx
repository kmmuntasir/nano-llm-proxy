import { useState } from "react"
import { Link as RouterLink } from "react-router-dom"
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query"
import {
  Alert,
  Badge,
  Button,
  Card,
  Code,
  Dialog,
  Field,
  Heading,
  HStack,
  Input,
  Link,
  Switch,
  Text,
  VStack,
} from "@chakra-ui/react"
import { api, put, ApiError } from "../api/client"
import type {
  LocalAutostartRequest,
  LocalInfoView,
  LocalPortRequest,
  LocalPortResponse,
} from "../api/types"
import { toaster } from "./ui/toaster"
import ConfirmDialog from "./ConfirmDialog"
import CopyButton from "./CopyButton"

// ThisComputerCard: the local-mode section of Settings. "Require an API key"
// is part of the settings document (saved with Save settings); autostart and
// the port are machine state behind /api/local and apply immediately.

const MIN_PORT = 1024
const MAX_PORT = 65535
// time for the restarted gateway to bind the new port before we navigate
const RESTART_WAIT_MS = 3000

function errorMessage(e: unknown, fallback: string): string {
  return e instanceof ApiError ? e.message : fallback
}

function useLocalInfo() {
  return useQuery({
    queryKey: ["local"],
    queryFn: () => api<LocalInfoView>("/api/local"),
  })
}

function SwitchRow({
  checked,
  disabled,
  label,
  onChange,
}: {
  checked: boolean
  disabled?: boolean
  label: string
  onChange: (checked: boolean) => void
}) {
  return (
    <Switch.Root
      checked={checked}
      disabled={disabled}
      onCheckedChange={(e) => onChange(e.checked)}
    >
      <Switch.HiddenInput />
      <Switch.Control>
        <Switch.Thumb />
      </Switch.Control>
      <Switch.Label>{label}</Switch.Label>
    </Switch.Root>
  )
}

function RequireKeyField({
  value,
  onChange,
}: {
  value: boolean
  onChange: (v: boolean) => void
}) {
  return (
    <Field.Root>
      <SwitchRow checked={value} label="Require an API key" onChange={onChange} />
      <Field.HelperText>
        {value ? (
          <>
            Agents need a key from{" "}
            <Link asChild colorPalette="blue" variant="underline">
              <RouterLink to="/profile">Profile → My API Keys</RouterLink>
            </Link>
          </>
        ) : (
          <>
            Agents can use any placeholder key, for example{" "}
            <Code fontFamily="mono">local</Code>
          </>
        )}
      </Field.HelperText>
    </Field.Root>
  )
}

function AutostartField({ info }: { info: LocalInfoView }) {
  const qc = useQueryClient()
  // the value the user asked for, while the confirmation is open
  const [pending, setPending] = useState<boolean | null>(null)
  const { supported, enabled, running, error } = info.autostart

  const toggle = useMutation({
    mutationFn: (on: boolean) => {
      const body: LocalAutostartRequest = { enabled: on }
      return put<LocalInfoView>("/api/local/autostart", body)
    },
    onSuccess: async (next) => {
      await qc.invalidateQueries({ queryKey: ["local"] })
      if (next.autostart.error) return // shown inline
      toaster.create({
        title: next.autostart.enabled ? "Starts automatically" : "Automatic start turned off",
        type: "success",
      })
    },
    onError: async (e) => {
      await qc.invalidateQueries({ queryKey: ["local"] })
      toaster.create({ title: errorMessage(e, "Failed to change automatic start"), type: "error" })
    },
    onSettled: () => setPending(null),
  })

  return (
    <>
      <Field.Root disabled={!supported}>
        <SwitchRow
          checked={enabled}
          disabled={!supported || toggle.isPending}
          label="Start automatically when I sign in"
          onChange={setPending}
        />
        {supported ? (
          <Field.HelperText>
            {running ? "Running as a background service." : "Not running as a background service."}
          </Field.HelperText>
        ) : (
          <Field.HelperText>
            Not available here — this system has no background-service mechanism
            nano-llm-proxy can use. Start it with{" "}
            <Code fontFamily="mono">nano-llm-proxy local</Code>.
          </Field.HelperText>
        )}
        {error && (
          <Text fontSize="sm" color="red.fg" wordBreak="break-word">
            {error}
          </Text>
        )}
      </Field.Root>
      <ConfirmDialog
        open={pending !== null}
        onOpenChange={(e) => {
          if (!e.open && !toggle.isPending) setPending(null)
        }}
        title={pending ? "Start automatically?" : "Stop starting automatically?"}
        body={
          pending
            ? "nano-llm-proxy will start in the background each time you sign in to this computer."
            : "nano-llm-proxy won't start when you sign in. This running copy keeps going until you sign out or restart."
        }
        confirmText={pending ? "Turn on" : "Turn off"}
        busy={toggle.isPending}
        onConfirm={() => {
          if (pending !== null) toggle.mutate(pending)
        }}
      />
    </>
  )
}

function ChangePortDialog({
  open,
  onOpenChange,
  currentPort,
  onSaved,
}: {
  open: boolean
  onOpenChange: (o: boolean) => void
  currentPort: number
  onSaved: (res: LocalPortResponse, port: number) => void
}) {
  const [value, setValue] = useState(String(currentPort))
  const [error, setError] = useState("")

  const change = useMutation({
    mutationFn: (port: number) => {
      const body: LocalPortRequest = { port }
      return put<LocalPortResponse>("/api/local/port", body)
    },
    onSuccess: (res, port) => {
      close()
      onSaved(res, port)
    },
    onError: (e) => {
      const msg = errorMessage(e, "Failed to change the port")
      setError(msg)
      toaster.create({ title: msg, type: "error" })
    },
  })

  const close = () => {
    setValue(String(currentPort))
    setError("")
    onOpenChange(false)
  }

  const port = Number(value)
  const valid = Number.isInteger(port) && port >= MIN_PORT && port <= MAX_PORT
  const unchanged = port === currentPort

  const submit = () => {
    setError("")
    if (!valid) {
      setError(`Enter a port from ${MIN_PORT} to ${MAX_PORT}`)
      return
    }
    change.mutate(port)
  }

  return (
    <Dialog.Root
      open={open}
      onOpenChange={(e) => (e.open ? onOpenChange(true) : close())}
      role="alertdialog"
    >
      <Dialog.Backdrop />
      <Dialog.Positioner>
        <Dialog.Content
          as="form"
          maxW="420px"
          onSubmit={(e) => {
            e.preventDefault()
            submit()
          }}
        >
          <Dialog.Header>
            <Dialog.Title>Change port</Dialog.Title>
          </Dialog.Header>
          <Dialog.Body>
            <VStack align="stretch" gap={3}>
              <Field.Root required invalid={value !== "" && !valid}>
                <Field.Label>
                  New port <Field.RequiredIndicator />
                </Field.Label>
                <Input
                  autoComplete="off"
                  type="number"
                  min={MIN_PORT}
                  max={MAX_PORT}
                  value={value}
                  onChange={(e) => setValue(e.target.value)}
                />
                <Field.HelperText>
                  {MIN_PORT}–{MAX_PORT}. Currently {currentPort}.
                </Field.HelperText>
              </Field.Root>
              <Alert.Root status="warning" size="sm">
                <Alert.Indicator />
                <Alert.Content>
                  <Alert.Description>
                    Agents and tools set to{" "}
                    <Code fontFamily="mono">http://localhost:{currentPort}</Code> stop working
                    until you update them to the new port. nano-llm-proxy restarts by itself when
                    it runs as a background service; otherwise restart it yourself.
                  </Alert.Description>
                </Alert.Content>
              </Alert.Root>
              {error && (
                <Text color="red.fg" fontSize="sm">
                  {error}
                </Text>
              )}
            </VStack>
          </Dialog.Body>
          <Dialog.Footer>
            <Dialog.ActionTrigger asChild>
              <Button type="button" variant="outline">
                Cancel
              </Button>
            </Dialog.ActionTrigger>
            <Button
              type="submit"
              colorPalette="orange"
              loading={change.isPending}
              disabled={!valid || unchanged}
            >
              Change port
            </Button>
          </Dialog.Footer>
          <Dialog.CloseTrigger />
        </Dialog.Content>
      </Dialog.Positioner>
    </Dialog.Root>
  )
}

function PortField({ port }: { port: number }) {
  const qc = useQueryClient()
  const [open, setOpen] = useState(false)
  const [message, setMessage] = useState("")

  const onSaved = (res: LocalPortResponse, next: number) => {
    // only follow a plain http(s) URL from the server
    if (res.restarting && /^https?:\/\//.test(res.url)) {
      toaster.create({ title: `Restarting on port ${next}…`, type: "info" })
      setTimeout(() => {
        window.location.href = res.url
      }, RESTART_WAIT_MS)
      return
    }
    const msg = res.message || `Saved — restart nano-llm-proxy to use port ${next}`
    setMessage(msg)
    toaster.create({ title: msg, type: "success" })
    void qc.invalidateQueries({ queryKey: ["local"] })
  }

  return (
    <VStack align="stretch" gap={1}>
      <HStack gap={3} flexWrap="wrap">
        <Text fontSize="sm">
          Port <Code fontFamily="mono">{port}</Code>
        </Text>
        <Button size="xs" variant="outline" onClick={() => setOpen(true)}>
          Change port
        </Button>
      </HStack>
      {message && (
        <Text fontSize="sm" color="fg.muted">
          {message}
        </Text>
      )}
      <ChangePortDialog
        key={port}
        open={open}
        onOpenChange={setOpen}
        currentPort={port}
        onSaved={onSaved}
      />
    </VStack>
  )
}

function PathRow({ label, path }: { label: string; path: string }) {
  return (
    <VStack align="stretch" gap={0.5} minW={0}>
      <Text fontSize="xs" color="fg.muted">
        {label}
      </Text>
      <HStack gap={1} align="start" minW={0}>
        <Code fontFamily="mono" fontSize="xs" wordBreak="break-all" minW={0}>
          {path}
        </Code>
        {path && <CopyButton text={path} label={`copy ${label.toLowerCase()} path`} />}
      </HStack>
    </VStack>
  )
}

function SearxngStatus({ info }: { info: LocalInfoView }) {
  const { installed, running, url } = info.searxng
  if (!installed && info.platform === "windows") {
    return (
      <Text fontSize="sm" color="fg.muted">
        Web search needs a SearXNG server — set its URL under Web tools below.
      </Text>
    )
  }
  if (!installed) {
    return (
      <Text fontSize="sm" color="fg.muted">
        Web search needs SearXNG — re-run the installer without{" "}
        <Code fontFamily="mono">--no-search</Code>, or set a SearXNG URL under Web tools below.
      </Text>
    )
  }
  return (
    <HStack gap={2} flexWrap="wrap" fontSize="sm">
      <Text>SearXNG</Text>
      <Badge variant="subtle" colorPalette={running ? "green" : "orange"}>
        {running ? "Running" : "Not running"}
      </Badge>
      {url && (
        <Code fontFamily="mono" fontSize="xs" wordBreak="break-all">
          {url}
        </Code>
      )}
    </HStack>
  )
}

export default function ThisComputerCard({
  requireApiKey,
  onRequireApiKeyChange,
}: {
  requireApiKey: boolean
  onRequireApiKeyChange: (v: boolean) => void
}) {
  const { data, isLoading, error } = useLocalInfo()

  return (
    <Card.Root>
      <Card.Header>
        <Heading size="sm">This computer</Heading>
        <Text fontSize="xs" color="fg.muted">
          nano-llm-proxy is running in local mode and only accepts connections from this
          computer.
        </Text>
      </Card.Header>
      <Card.Body>
        <VStack align="stretch" gap={5}>
          <RequireKeyField value={requireApiKey} onChange={onRequireApiKeyChange} />
          {isLoading ? (
            <Text fontSize="sm" color="fg.muted">
              Loading…
            </Text>
          ) : error || !data ? (
            <Text fontSize="sm" color="red.fg">
              {errorMessage(error, "Failed to load this computer's details")}
            </Text>
          ) : (
            <>
              <AutostartField info={data} />
              <PortField port={data.port} />
              <VStack align="stretch" gap={3}>
                <PathRow label="Data folder" path={data.dataDir} />
                <PathRow label="Log file" path={data.logFile} />
                <PathRow label="Program" path={data.binary} />
              </VStack>
              <SearxngStatus info={data} />
            </>
          )}
        </VStack>
      </Card.Body>
    </Card.Root>
  )
}
