import { createSystem, defaultConfig, defineConfig, defineSlotRecipe } from "@chakra-ui/react"
import { dialogAnatomy, drawerAnatomy } from "@chakra-ui/react/anatomy"

// The app's Chakra system: the defaults plus a blurred backdrop behind every
// modal surface (dialogs and the mobile nav drawer), set once here so no
// individual dialog has to remember it. createSystem deep-merges these
// partial recipes into Chakra's own, so only the backdrop changes.
const MODAL_BACKDROP = {
  backdropFilter: "blur(6px)",
  WebkitBackdropFilter: "blur(6px)",
}

const config = defineConfig({
  theme: {
    slotRecipes: {
      dialog: defineSlotRecipe({
        slots: dialogAnatomy.keys(),
        base: { backdrop: MODAL_BACKDROP },
      }),
      drawer: defineSlotRecipe({
        slots: drawerAnatomy.keys(),
        base: { backdrop: MODAL_BACKDROP },
      }),
    },
  },
})

export const system = createSystem(defaultConfig, config)
