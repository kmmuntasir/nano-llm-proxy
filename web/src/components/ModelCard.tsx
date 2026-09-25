import type { ReactNode } from "react"
import { Badge, Card, Code, HStack, Text } from "@chakra-ui/react"
import CopyButton from "./CopyButton"

export interface ModelCardData {
  name: string
  /** the exact catalog string ("provider/model-1M-txt") — shown with a copy button when set */
  modelId?: string
  description?: string
  contextWindow?: number
  maxOutputTokens?: number
  inputModalities?: string[]
  reasoning?: boolean
  responsesApi?: boolean
  free?: boolean
  provider?: string
  /** extra per-page row (e.g. the responses toggle) */
  footer?: ReactNode
}

export function fmtTokens(n: number) {
  if (n >= 1_000_000) return `${(n / 1_000_000).toFixed(1)}M`
  if (n >= 1_000) return `${(n / 1_000).toFixed(1)}k`
  return String(n)
}

// cosmetic context/modality suffix the gateway strips anyway
const SUFFIX_RE = /-\d+(?:\.\d+)?[KMG](?:-txt|-img|-vid|-aud|-pdf)*$/

// cleanModelId strips the cosmetic suffix from a catalog id, turning
// "glm/glm-5.3-1M-txt" into "glm/glm-5.3".
export function cleanModelId(id: string): string {
  const [prov, ...rest] = id.split("/")
  return rest.length > 0 ? `${prov}/${rest.join("/").replace(SUFFIX_RE, "")}` : id
}

// ModelCard is the shared rendering for one model: name, capability badges,
// token limits, and modalities. Used by the Providers page model list and the
// Models page.
export default function ModelCard({
  name,
  modelId,
  description,
  contextWindow,
  maxOutputTokens,
  inputModalities,
  reasoning,
  responsesApi,
  free,
  provider,
  footer,
}: ModelCardData) {
  return (
    <Card.Root size="sm" height="100%">
      <Card.Body gap={2} px={4} py={3}>
        <HStack justify="space-between" flexWrap="wrap" gap={2}>
          <Text fontFamily="mono" fontSize="sm" fontWeight="medium" truncate maxW="70%">
            {name}
          </Text>
          <HStack gap={1} flexWrap="wrap" justify="end">
            {provider && (
              <Badge variant="outline" colorPalette="gray">
                {provider}
              </Badge>
            )}
            {free && (
              <Badge colorPalette="green" variant="subtle">
                free
              </Badge>
            )}
            {responsesApi && (
              <Badge colorPalette="blue" variant="subtle">
                responses API
              </Badge>
            )}
            {reasoning && (
              <Badge colorPalette="purple" variant="subtle">
                reasoning
              </Badge>
            )}
          </HStack>
        </HStack>
        {modelId && (
          <HStack gap={1} align="center" justify="space-between">
            <Code fontFamily="mono" fontSize="xs" px={2} py={1} truncate title={modelId}>
              {modelId}
            </Code>
            <CopyButton text={modelId} label={`copy ${modelId}`} />
          </HStack>
        )}
        {description && (
          <Text fontSize="xs" color="fg.muted" lineClamp={2}>
            {description}
          </Text>
        )}
        <HStack gap={2} flexWrap="wrap" align="center">
          {contextWindow ? (
            <Badge variant="outline" colorPalette="blue">
              {fmtTokens(contextWindow)} context
            </Badge>
          ) : (
            <Badge variant="outline" colorPalette="gray">
              context unknown
            </Badge>
          )}
          {maxOutputTokens ? (
            <Badge variant="outline" colorPalette="orange">
              {fmtTokens(maxOutputTokens)} output
            </Badge>
          ) : null}
          {(inputModalities ?? []).map((mod) => (
            <Badge key={mod} variant="subtle">
              {mod}
            </Badge>
          ))}
        </HStack>
        {footer}
      </Card.Body>
    </Card.Root>
  )
}
