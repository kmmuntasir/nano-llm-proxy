import type { ReactNode } from "react"
import { Button } from "@chakra-ui/react"
import {
  DialogActionTrigger,
  DialogBody,
  DialogCloseTrigger,
  DialogContent,
  DialogFooter,
  DialogHeader,
  DialogRoot,
  DialogTitle,
  DialogDescription,
} from "./ui/dialog"

interface Props {
  open: boolean
  onOpenChange: (e: { open: boolean }) => void
  title: string
  body: ReactNode
  confirmText?: string
  destructive?: boolean
  onConfirm: () => void
  busy?: boolean
}

// ConfirmDialog wraps the Chakra v3 dialog snippet for destructive/important
// confirmations (delete user, disable key, …).
export default function ConfirmDialog({
  open,
  onOpenChange,
  title,
  body,
  confirmText = "Confirm",
  destructive = false,
  onConfirm,
  busy = false,
}: Props) {
  return (
    <DialogRoot open={open} onOpenChange={onOpenChange} role="alertdialog" size="sm">
      <DialogContent>
        <DialogHeader>
          <DialogTitle>{title}</DialogTitle>
        </DialogHeader>
        <DialogBody>
          <DialogDescription>{body}</DialogDescription>
        </DialogBody>
        <DialogFooter>
          <DialogActionTrigger asChild>
            <Button variant="outline">Cancel</Button>
          </DialogActionTrigger>
          <Button
            colorPalette={destructive ? "red" : "blue"}
            onClick={onConfirm}
            loading={busy}
          >
            {confirmText}
          </Button>
        </DialogFooter>
        <DialogCloseTrigger />
      </DialogContent>
    </DialogRoot>
  )
}
