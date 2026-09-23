import { useState } from "react"
import { Button, Code, Text, VStack } from "@chakra-ui/react"
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

interface Props {
  plaintext: string | null
  onClose: () => void
}

// KeyRevealDialog shows a freshly generated client key exactly once — the
// backend stores only its sha256, so there is no second look.
export default function KeyRevealDialog({ plaintext, onClose }: Props) {
  const [copied, setCopied] = useState(false)

  const copy = async () => {
    if (!plaintext) return
    try {
      await navigator.clipboard.writeText(plaintext)
      setCopied(true)
      toaster.create({ title: "Copied to clipboard", type: "success" })
    } catch {
      toaster.create({ title: "Copy failed — select it manually", type: "warning" })
    }
  }

  return (
    <DialogRoot
      open={plaintext !== null}
      onOpenChange={(e) => {
        if (!e.open) onClose()
      }}
      size="md"
      closeOnInteractOutside={false}
      closeOnEscape={false}
    >
      <DialogContent>
        <DialogHeader>
          <DialogTitle>Your new API key</DialogTitle>
        </DialogHeader>
        <DialogBody>
          <VStack align="stretch" gap={3}>
            <Text fontSize="sm" color="fg.muted">
              This is the only time the full key is shown. The gateway stores a
              hash — copy it now and put it in your client.
            </Text>
            <Code p={3} wordBreak="break-all" userSelect="all">
              {plaintext}
            </Code>
          </VStack>
        </DialogBody>
        <DialogFooter>
          <Button variant="outline" onClick={copy}>
            {copied ? "Copied" : "Copy"}
          </Button>
          <Button colorPalette="blue" onClick={onClose}>
            Done
          </Button>
        </DialogFooter>
        <DialogCloseTrigger />
      </DialogContent>
    </DialogRoot>
  )
}
