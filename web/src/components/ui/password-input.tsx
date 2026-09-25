"use client"

import type {
  ButtonProps,
  GroupProps,
  InputProps,
  StackProps,
} from "@chakra-ui/react"
import {
  Box,
  HStack,
  IconButton,
  Input,
  InputGroup,
  Stack,
  mergeRefs,
  useControllableState,
} from "@chakra-ui/react"
import * as React from "react"
import { Eye, EyeOff } from "lucide-react"

export interface PasswordVisibilityProps {
  /**
   * The default visibility state of the password input.
   */
  defaultVisible?: boolean
  /**
   * The controlled visibility state of the password input.
   */
  visible?: boolean
  /**
   * Callback invoked when the visibility state changes.
   */
  onVisibleChange?: (visible: boolean) => void
  /**
   * Custom icons for the visibility toggle button.
   */
  visibilityIcon?: { on: React.ReactNode; off: React.ReactNode }
}

export interface PasswordInputProps
  extends Omit<InputProps, "value" | "onChange">, PasswordVisibilityProps {
  rootProps?: GroupProps
  value?: string
  onChange?: (e: React.ChangeEvent<HTMLInputElement>) => void
  /**
   * Render a browser-native input[type=password] instead of the asterisk
   * mask. The login screen opts in so password managers can save and fill
   * the credential; everywhere else stays masked.
   */
  native?: boolean
}

// applyMaskEdit recovers a masked field's real value after an edit: the
// browser shows asterisks, so the DOM value is the new mask plus whatever
// was typed at one spot. Diffing it against the previous mask locates that
// spot; applying the same insert/remove to the real value reconstructs it.
// Covers typing anywhere in the field, backspace/delete, selection
// replacement, paste, and cut.
function applyMaskEdit(
  prevValue: string,
  prevMask: string,
  next: string,
): { value: string; caret: number } {
  const min = Math.min(prevMask.length, next.length)
  let p = 0
  while (p < min && next[p] === prevMask[p]) p++
  let s = 0
  while (s < min - p && next[next.length - 1 - s] === prevMask[prevMask.length - 1 - s]) s++
  const inserted = next.slice(p, next.length - s)
  const removed = prevMask.length - p - s
  return {
    value: prevValue.slice(0, p) + inserted + prevValue.slice(p + removed),
    caret: p + inserted.length,
  }
}

// PasswordInput is a secret-entry field without input[type=password]: the
// browser sees a plain text input whose value is one asterisk per character,
// which keeps password managers and save-prompt autofill out of the way.
// The eye toggle reveals the real characters. Callers always get the real
// value in onChange (e.target.value).
export const PasswordInput = React.forwardRef<
  HTMLInputElement,
  PasswordInputProps
>(function PasswordInput(props, ref) {
  const {
    rootProps,
    defaultVisible,
    visible: visibleProp,
    onVisibleChange,
    visibilityIcon = { on: <Eye size="16" />, off: <EyeOff size="16" /> },
    native,
    value = "",
    onChange,
    ...rest
  } = props

  const [visible, setVisible] = useControllableState({
    value: visibleProp,
    defaultValue: defaultVisible || false,
    onChange: onVisibleChange,
  })

  const inputRef = React.useRef<HTMLInputElement>(null)
  const caretRef = React.useRef<number | null>(null)
  const display = visible || native ? value : "*".repeat(value.length)

  // a masked edit rewrites the displayed value, which moves the caret to the
  // end — put it back at the edit point (native fields need no correction)
  React.useEffect(() => {
    if (!native && caretRef.current !== null && inputRef.current) {
      inputRef.current.setSelectionRange(caretRef.current, caretRef.current)
      caretRef.current = null
    }
  }, [display, native])

  const handleChange = (e: React.ChangeEvent<HTMLInputElement>) => {
    const next = e.target.value
    let real: string
    if (visible || native) {
      real = next
    } else {
      const r = applyMaskEdit(value, "*".repeat(value.length), next)
      real = r.value
      caretRef.current = r.caret
    }
    onChange?.({ ...e, target: { ...e.target, value: real } } as React.ChangeEvent<HTMLInputElement>)
  }

  return (
    <InputGroup
      endElement={
        <VisibilityTrigger
          disabled={rest.disabled}
          onPointerDown={(e) => {
            if (rest.disabled) return
            if (e.button !== 0) return
            e.preventDefault()
            setVisible(!visible)
          }}
        >
          {visible ? visibilityIcon.off : visibilityIcon.on}
        </VisibilityTrigger>
      }
      {...rootProps}
    >
      <Input
        {...rest}
        ref={mergeRefs(ref, inputRef)}
        type={native ? (visible ? "text" : "password") : "text"}
        autoComplete={rest.autoComplete ?? (native ? "current-password" : "off")}
        value={display}
        onChange={handleChange}
      />
    </InputGroup>
  )
})

const VisibilityTrigger = React.forwardRef<HTMLButtonElement, ButtonProps>(
  function VisibilityTrigger(props, ref) {
    return (
      <IconButton
        tabIndex={-1}
        ref={ref}
        me="-2"
        aspectRatio="square"
        size="sm"
        variant="ghost"
        height="calc(100% - {spacing.2})"
        aria-label="Toggle password visibility"
        {...props}
      />
    )
  },
)

interface PasswordStrengthMeterProps extends StackProps {
  max?: number
  value: number
}

export const PasswordStrengthMeter = React.forwardRef<
  HTMLDivElement,
  PasswordStrengthMeterProps
>(function PasswordStrengthMeter(props, ref) {
  const { max = 4, value, ...rest } = props

  const percent = (value / max) * 100
  const { label, colorPalette } = getColorPalette(percent)

  return (
    <Stack align="flex-end" gap="1" ref={ref} {...rest}>
      <HStack width="full" {...rest}>
        {Array.from({ length: max }).map((_, index) => (
          <Box
            key={index}
            height="1"
            flex="1"
            rounded="sm"
            data-selected={index < value ? "" : undefined}
            layerStyle="fill.subtle"
            colorPalette="gray"
            _selected={{
              colorPalette,
              layerStyle: "fill.solid",
            }}
          />
        ))}
      </HStack>
      {label && <HStack textStyle="xs">{label}</HStack>}
    </Stack>
  )
})

function getColorPalette(percent: number) {
  switch (true) {
    case percent < 33:
      return { label: "Low", colorPalette: "red" }
    case percent < 66:
      return { label: "Medium", colorPalette: "orange" }
    default:
      return { label: "High", colorPalette: "green" }
  }
}
