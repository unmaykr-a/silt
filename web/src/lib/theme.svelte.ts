/**
 * The light/dark choice, owned in one place.
 *
 * It lives outside App because more than the header needs it: the density
 * strip draws its own axes on a canvas and has to know which palette to use,
 * and a canvas cannot inherit a CSS variable.
 *
 * Three settings, not two. The old version read the system preference once on
 * first load and then stored a concrete light-or-dark forever, so someone
 * whose desktop switches at sunset had to switch Silt by hand every day —
 * and there was no way back to "whatever the system says" once you had
 * touched the toggle. "system" is now a stored value of its own that keeps
 * listening.
 */

export type Theme = "dark" | "light" | "system";

const STORAGE_KEY = "silt.theme";

function stored(): Theme | null {
  try {
    const saved = localStorage.getItem(STORAGE_KEY);
    return saved === "light" || saved === "dark" || saved === "system" ? saved : null;
  } catch {
    // Private windows and blocked site data both throw here.
    return null;
  }
}

function systemPrefersDark(): boolean {
  // Dark is Silt's default, so anything that cannot answer resolves to dark.
  if (typeof matchMedia !== "function") return true;
  return !matchMedia("(prefers-color-scheme: light)").matches;
}

function resolve(choice: Theme): boolean {
  return choice === "system" ? systemPrefersDark() : choice === "dark";
}

function createTheme() {
  // An install with no stored choice follows the system rather than pinning
  // whatever it happened to be on the first visit.
  const initial: Theme = stored() ?? "system";

  // Applied before the state exists, so the first paint is already correct
  // rather than flashing dark and correcting itself.
  if (typeof document !== "undefined") {
    document.documentElement.classList.toggle("dark", resolve(initial));
  }

  let choice = $state<Theme>(initial);
  let dark = $state<boolean>(resolve(initial));

  function paint() {
    dark = resolve(choice);
    document.documentElement.classList.toggle("dark", dark);
  }

  // Follow the system while "system" is selected. The listener is registered
  // once for the life of the page rather than added and removed as the choice
  // changes: it is one callback, and a subscription that comes and goes is a
  // subscription that can be missing when it matters.
  if (typeof matchMedia === "function") {
    matchMedia("(prefers-color-scheme: light)").addEventListener("change", () => {
      if (choice === "system") paint();
    });
  }

  function apply(next: Theme) {
    choice = next;
    paint();
    try {
      localStorage.setItem(STORAGE_KEY, next);
    } catch {
      // Non-fatal: the choice just will not persist.
    }
  }

  return {
    /** What the reader chose: light, dark, or follow the system. */
    get value() {
      return choice;
    },
    /** What that resolves to right now. Canvas code wants this one. */
    get dark() {
      return dark;
    },
    set(next: Theme) {
      apply(next);
    },
    /**
     * Flip between light and dark, leaving "system" behind.
     *
     * Kept for the keyboard shortcut. Choosing "system" again is a deliberate
     * act, so a toggle never lands back on it by accident.
     */
    toggle() {
      apply(dark ? "light" : "dark");
    },
  };
}

export const theme = createTheme();
