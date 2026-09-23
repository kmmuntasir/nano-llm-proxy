import { useState } from "react"
import CodeMirror from "@uiw/react-codemirror"
import { json } from "@codemirror/lang-json"
import { Button, Dialog } from "@chakra-ui/react"
import { Maximize2 } from "lucide-react"
import { useColorMode } from "./ui/color-mode"

interface Props {
  value: string
  onChange: (v: string) => void
  label?: string
  height?: string
}

// JsonEditor is a CodeMirror 6 editor with JSON syntax highlighting and a
// maximize button that pops the same buffer into a fullscreen dialog —
// long modelMeta documents get unreadable in a fixed box fast. The two
// instances share the value string, so edits in one show in the other.
export default function JsonEditor({ value, onChange, label, height = "220px" }: Props) {
  const { colorMode } = useColorMode()
  const [maxed, setMaxed] = useState(false)
  const extensions = [json()]

  const editor = (h: string) => (
    <CodeMirror
      value={value}
      height={h}
      theme={colorMode === "dark" ? "dark" : "light"}
      extensions={extensions}
      onChange={onChange}
      basicSetup={{ foldGutter: true, autocompletion: true }}
    />
  )

  return (
    <>
      <Button
        size="2xs"
        variant="outline"
        onClick={() => setMaxed(true)}
        aria-label={`maximize ${label ?? "editor"}`}
        mb={1}
      >
        <Maximize2 /> Maximize
      </Button>
      {editor(height)}

      <Dialog.Root open={maxed} onOpenChange={(e) => setMaxed(e.open)} size="full">
        <Dialog.Backdrop />
        <Dialog.Positioner>
          <Dialog.Content overflow="auto">
            <Dialog.Header>
              <Dialog.Title>{label ?? "JSON editor"}</Dialog.Title>
            </Dialog.Header>
            <Dialog.Body>{editor("70vh")}</Dialog.Body>
            <Dialog.CloseTrigger />
          </Dialog.Content>
        </Dialog.Positioner>
      </Dialog.Root>
    </>
  )
}
