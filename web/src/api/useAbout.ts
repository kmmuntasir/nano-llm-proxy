import { useQuery } from "@tanstack/react-query"
import { api } from "./client"
import type { AboutView } from "./types"

// useAbout reads the running build and changelog (GET /api/about). Shared by
// the sidebar version label and the About page; the build never changes
// while the page is open, so it is fetched once.
export function useAbout() {
  return useQuery({
    queryKey: ["about"],
    queryFn: () => api<AboutView>("/api/about"),
    staleTime: Infinity,
  })
}
