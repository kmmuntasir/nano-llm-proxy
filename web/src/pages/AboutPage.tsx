import type { ComponentType, ReactNode } from "react"
import {
  Accordion,
  Badge,
  Box,
  Button,
  Card,
  Code,
  Grid,
  Heading,
  HStack,
  Image,
  Link,
  List,
  Spinner,
  Text,
  VStack,
} from "@chakra-ui/react"
import { Bug, ExternalLink, GitBranch, GitPullRequest, Star } from "lucide-react"
import { useAbout } from "../api/useAbout"
import type { AboutView, ReleaseNotesView } from "../api/types"
import InlineMarkdown, { isHttpURL } from "../components/InlineMarkdown"
import logoUrl from "../assets/logo.png"

// AboutPage — the running build, how to contribute, and the changelog the
// binary was built with. Reached from the version label in the sidebar.

const UNRELEASED = "Unreleased"
const TAGLINE =
  "One tiny binary that pools LLM providers behind a single OpenAI- and Anthropic-compatible endpoint."

function formatBuildDate(value: string): string {
  const d = new Date(value)
  return Number.isNaN(d.getTime()) ? value : d.toLocaleString()
}

// isThisBuild marks the release the running binary corresponds to: the
// Unreleased entry when it is ahead of a tag, otherwise the tagged release.
// A pre-release (0.3.0-rc.1) usually has no changelog section of its own —
// its notes sit under Unreleased until the final release — so it marks
// Unreleased unless the changelog does name it.
function isThisBuild(about: AboutView, release: ReleaseNotesView): boolean {
  if (about.commitsAhead > 0) return release.version === UNRELEASED
  if (about.baseVersion === "") return false
  if (release.version === about.baseVersion) return true
  return (
    about.prerelease &&
    release.version === UNRELEASED &&
    !about.releases.some((r) => r.version === about.baseVersion)
  )
}

function InfoRow({ label, children }: { label: string; children: ReactNode }) {
  return (
    <>
      <Text fontSize="sm" color="fg.muted">
        {label}
      </Text>
      <Box fontSize="sm" minW={0} wordBreak="break-word">
        {children}
      </Box>
    </>
  )
}

function HeroCard({ about }: { about: AboutView }) {
  const ahead = about.commitsAhead
  return (
    <Card.Root>
      <Card.Body gap={4}>
        <HStack gap={3} align="center" minW={0}>
          <Image src={logoUrl} alt="Nano LLM Proxy logo" boxSize="48px" flexShrink={0} />
          <VStack align="start" gap={1} minW={0}>
            <Heading size="lg">{about.name || "Nano LLM Proxy"}</Heading>
            <Text fontSize="sm" color="fg.muted">
              {TAGLINE}
            </Text>
          </VStack>
        </HStack>
        <Grid templateColumns="auto minmax(0, 1fr)" columnGap={4} rowGap={1.5}>
          <InfoRow label="Version">
            <Code fontFamily="mono">{about.version}</Code>
            {ahead > 0 && about.baseVersion && (
              <Text as="span" color="fg.muted" ml={2}>
                based on v{about.baseVersion} + {ahead} {ahead === 1 ? "commit" : "commits"}
              </Text>
            )}
            {ahead === 0 && about.prerelease && (
              <Text as="span" color="fg.muted" ml={2}>
                pre-release
              </Text>
            )}
          </InfoRow>
          {about.commit && (
            <InfoRow label="Commit">
              <Code fontFamily="mono">{about.commit}</Code>
            </InfoRow>
          )}
          {about.buildDate && (
            <InfoRow label="Built">{formatBuildDate(about.buildDate)}</InfoRow>
          )}
          {about.license && <InfoRow label="License">{about.license}</InfoRow>}
        </Grid>
      </Card.Body>
    </Card.Root>
  )
}

function ExternalButton({
  href,
  icon: Icon,
  children,
}: {
  href: string
  icon: ComponentType
  children: ReactNode
}) {
  return (
    <Button asChild size="sm" variant="outline">
      <a href={href} target="_blank" rel="noopener noreferrer">
        <Icon />
        {children}
      </a>
    </Button>
  )
}

function OpenSourceCard({ about }: { about: AboutView }) {
  const repo = about.repository.replace(/\/+$/, "")
  const license = about.license || "GPL-3.0"
  return (
    <Card.Root>
      <Card.Header>
        <Heading size="sm">Open source</Heading>
      </Card.Header>
      <Card.Body gap={3}>
        <Text fontSize="sm">
          Nano LLM Proxy is free software under {license}, built in the open.
          Issues, ideas and pull requests are welcome — the contributing guide
          explains how to get started.
        </Text>
        <Text fontSize="sm" color="fg.muted">
          If it is useful to you, star the repository to help others find it.
        </Text>
        {isHttpURL(repo) && (
          <HStack gap={2} flexWrap="wrap">
            <ExternalButton href={repo} icon={GitBranch}>
              View on GitHub
            </ExternalButton>
            <ExternalButton href={repo} icon={Star}>
              Star on GitHub
            </ExternalButton>
            <ExternalButton href={`${repo}/issues/new`} icon={Bug}>
              Report an issue
            </ExternalButton>
            <ExternalButton href={`${repo}/blob/main/CONTRIBUTING.md`} icon={GitPullRequest}>
              Contributing guide
            </ExternalButton>
          </HStack>
        )}
      </Card.Body>
    </Card.Root>
  )
}

function ReleaseTrigger({ about, release }: { about: AboutView; release: ReleaseNotesView }) {
  const unreleased = release.version === UNRELEASED
  return (
    <Accordion.ItemTrigger>
      <HStack flex={1} gap={2} flexWrap="wrap" minW={0}>
        <Text fontWeight="semibold">{unreleased ? UNRELEASED : `v${release.version}`}</Text>
        {unreleased && (
          <Text fontSize="xs" color="fg.muted">
            in development
          </Text>
        )}
        {release.date && (
          <Text fontSize="sm" color="fg.muted">
            {release.date}
          </Text>
        )}
        {isThisBuild(about, release) && (
          <Badge size="sm" colorPalette="green" variant="subtle">
            This build
          </Badge>
        )}
      </HStack>
      <Accordion.ItemIndicator />
    </Accordion.ItemTrigger>
  )
}

function ReleaseBody({ release }: { release: ReleaseNotesView }) {
  const paragraphs = release.intro
    .split(/\n\s*\n/)
    .map((p) => p.replace(/\s*\n\s*/g, " ").trim())
    .filter(Boolean)
  // Go encodes a nil slice as null; treat it as empty.
  const sections = release.sections ?? []
  const empty = paragraphs.length === 0 && sections.length === 0

  return (
    <VStack align="stretch" gap={4} minW={0}>
      {empty && (
        <Text fontSize="sm" color="fg.muted">
          No notes for this release.
        </Text>
      )}
      {paragraphs.map((p, i) => (
        <Text key={i} fontSize="sm" wordBreak="break-word">
          <InlineMarkdown text={p} />
        </Text>
      ))}
      {sections.map((section) => (
        <VStack key={section.title} align="stretch" gap={2} minW={0}>
          <Heading as="h3" size="xs">
            {section.title}
          </Heading>
          <List.Root fontSize="sm" pl={4} gap={1}>
            {(section.items ?? []).map((item, i) => (
              <List.Item key={i} wordBreak="break-word">
                <InlineMarkdown text={item} />
              </List.Item>
            ))}
          </List.Root>
        </VStack>
      ))}
      {isHttpURL(release.url) && (
        <Link
          href={release.url}
          target="_blank"
          rel="noopener noreferrer"
          colorPalette="blue"
          variant="underline"
          fontSize="sm"
          alignSelf="start"
        >
          Compare on GitHub <ExternalLink size={14} />
        </Link>
      )}
    </VStack>
  )
}

function Changelog({ about }: { about: AboutView }) {
  const releases = about.releases ?? []
  return (
    <VStack align="stretch" gap={3}>
      <Heading size="md">Changelog</Heading>
      {releases.length === 0 ? (
        <Text fontSize="sm" color="fg.muted">
          No changelog in this build.
        </Text>
      ) : (
        <Accordion.Root
          multiple
          collapsible
          variant="enclosed"
          defaultValue={[releases[0].version]}
        >
          {releases.map((release) => (
            <Accordion.Item key={release.version} value={release.version}>
              <ReleaseTrigger about={about} release={release} />
              <Accordion.ItemContent>
                <Accordion.ItemBody>
                  <ReleaseBody release={release} />
                </Accordion.ItemBody>
              </Accordion.ItemContent>
            </Accordion.Item>
          ))}
        </Accordion.Root>
      )}
    </VStack>
  )
}

export default function AboutPage() {
  const about = useAbout()

  if (about.isLoading) {
    return (
      <Box p={10} display="grid" placeItems="center">
        <Spinner />
      </Box>
    )
  }

  if (!about.data) {
    return (
      <VStack align="stretch" gap={6}>
        <Heading size="lg">About</Heading>
        <Text fontSize="sm" color="fg.error">
          {about.error?.message ?? "Failed to load build information"}
        </Text>
      </VStack>
    )
  }

  return (
    <VStack align="stretch" gap={6}>
      <Heading size="lg">About</Heading>
      <HeroCard about={about.data} />
      <OpenSourceCard about={about.data} />
      <Changelog about={about.data} />
    </VStack>
  )
}
