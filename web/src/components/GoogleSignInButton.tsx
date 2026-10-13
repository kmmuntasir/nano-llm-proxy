import { useEffect, useRef, useState } from "react"
import { Box, Skeleton, Text } from "@chakra-ui/react"
import { useColorMode } from "./ui/color-mode"

// Google Identity Services (GIS) "Sign in with Google" button. The script is
// injected once per page load and shared; initialize() runs once per client
// id (GIS warns on repeated calls), and its callback dispatches to whichever
// button is currently mounted.

const GIS_SRC = "https://accounts.google.com/gsi/client"
const MIN_BUTTON_WIDTH = 200
const MAX_BUTTON_WIDTH = 400

interface GoogleCredentialResponse {
  credential: string
}

interface GoogleIdConfiguration {
  client_id: string
  callback: (response: GoogleCredentialResponse) => void
  ux_mode?: "popup" | "redirect"
}

interface GoogleButtonConfiguration {
  theme?: "outline" | "filled_blue" | "filled_black"
  size?: "large" | "medium" | "small"
  text?: "signin_with" | "signup_with" | "continue_with" | "signin"
  shape?: "rectangular" | "pill" | "circle" | "square"
  width?: number
}

interface GoogleAccountsId {
  initialize: (config: GoogleIdConfiguration) => void
  renderButton: (parent: HTMLElement, options: GoogleButtonConfiguration) => void
}

declare global {
  interface Window {
    google?: { accounts: { id: GoogleAccountsId } }
  }
}

let gisLoad: Promise<GoogleAccountsId> | null = null
let initializedClientId: string | null = null
let credentialHandler: ((credential: string) => void) | null = null

function loadGis(): Promise<GoogleAccountsId> {
  const ready = window.google?.accounts?.id
  if (ready) return Promise.resolve(ready)
  if (gisLoad) return gisLoad

  gisLoad = new Promise<GoogleAccountsId>((resolve, reject) => {
    const existing = document.querySelector<HTMLScriptElement>(`script[src="${GIS_SRC}"]`)
    const script = existing ?? document.createElement("script")
    const fail = () => {
      gisLoad = null
      script.remove()
      reject(new Error("failed to load Google Identity Services"))
    }
    script.addEventListener("load", () => {
      const id = window.google?.accounts?.id
      if (id) resolve(id)
      else fail()
    })
    script.addEventListener("error", fail)
    if (!existing) {
      script.src = GIS_SRC
      script.async = true
      script.defer = true
      document.head.appendChild(script)
    }
  })
  return gisLoad
}

function initializeOnce(id: GoogleAccountsId, clientId: string) {
  if (initializedClientId === clientId) return
  id.initialize({
    client_id: clientId,
    callback: (response) => credentialHandler?.(response.credential),
    ux_mode: "popup",
  })
  initializedClientId = clientId
}

interface GoogleSignInButtonProps {
  clientId: string
  onCredential: (credential: string) => void
}

export default function GoogleSignInButton({ clientId, onCredential }: GoogleSignInButtonProps) {
  const { colorMode } = useColorMode()
  const outerRef = useRef<HTMLDivElement>(null)
  const buttonRef = useRef<HTMLDivElement>(null)
  const onCredentialRef = useRef(onCredential)
  const [gis, setGis] = useState<GoogleAccountsId | null>(null)
  const [failed, setFailed] = useState(false)
  const [width, setWidth] = useState(0)

  useEffect(() => {
    onCredentialRef.current = onCredential
  }, [onCredential])

  // route GIS callbacks to this instance while it's mounted
  useEffect(() => {
    const handler = (credential: string) => onCredentialRef.current(credential)
    credentialHandler = handler
    return () => {
      if (credentialHandler === handler) credentialHandler = null
    }
  }, [])

  useEffect(() => {
    let cancelled = false
    loadGis()
      .then((id) => {
        if (cancelled) return
        initializeOnce(id, clientId)
        setGis(id)
      })
      .catch(() => {
        if (!cancelled) setFailed(true)
      })
    return () => {
      cancelled = true
    }
  }, [clientId])

  // GIS takes a fixed pixel width — track the container so it fits narrow screens
  useEffect(() => {
    const el = outerRef.current
    if (!el) return
    const measure = () => {
      const w = Math.floor(el.getBoundingClientRect().width)
      setWidth(Math.max(MIN_BUTTON_WIDTH, Math.min(MAX_BUTTON_WIDTH, w)))
    }
    measure()
    const observer = new ResizeObserver(measure)
    observer.observe(el)
    return () => observer.disconnect()
  }, [])

  useEffect(() => {
    const el = buttonRef.current
    if (!gis || !el || width === 0) return
    gis.renderButton(el, {
      theme: colorMode === "dark" ? "filled_black" : "outline",
      size: "large",
      text: "signin_with",
      shape: "rectangular",
      width,
    })
  }, [gis, width, colorMode])

  if (failed) {
    return (
      <Text color="red.fg" fontSize="sm" role="alert">
        Couldn't load Google sign-in. Check your connection or content blocker, then reload.
      </Text>
    )
  }

  return (
    <Box ref={outerRef} w="full" display="flex" justifyContent="center">
      {!gis && <Skeleton h="10" w="full" maxW={`${MAX_BUTTON_WIDTH}px`} />}
      <Box ref={buttonRef} display={gis ? "block" : "none"} />
    </Box>
  )
}
