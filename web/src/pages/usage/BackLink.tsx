import { Button } from "@chakra-ui/react"
import { ArrowLeft } from "lucide-react"
import { Link as RouterLink } from "react-router-dom"

export default function BackLink({ to, label }: { to: string; label: string }) {
  return (
    <Button asChild variant="ghost" size="xs" alignSelf="start" px={1}>
      <RouterLink to={to}>
        <ArrowLeft /> {label}
      </RouterLink>
    </Button>
  )
}
