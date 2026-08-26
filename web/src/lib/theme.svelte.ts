/**
 * The light/dark choice, owned in one place.
 *
 * It lives outside App because more than the header needs it: the density
 * strip draws its own axes on a canvas and has to know which palette to use,
 * and a canvas cannot inherit a CSS variable.
 */

export type Theme = "dark" | "light";

const STORAGE_KEY = "silt.theme";

function stored(): Theme | null {
  try {
    const saved = localStorage.getItem(STORAGE_KEY);
    return saved === "light" || saved === "dark" ? saved : null;
  } catch {
    // Private windows and blocked site data both throw here.
    return null;
  }
}

function createTheme() {
  // Dark is Silt's default, but someone whose system says otherwise should not
  // have to click on first visit.
  const initial: Theme =
    stored() ??
    (typeof matchMedia === "function" && matchMedia("(prefers-color-scheme: light)").matches
      ? "light"
      : "dark");

  // Applied before the state exists, so the first paint is already correct
  // rather than flashing dark and correcting itself.
  if (typeof document !== "undefined") {
    document.documentElement.classList.toggle("dark", initial === "dark");
  }

  let value = $state<Theme>(initial);

  function apply(next: Theme) {
    value = next;
    document.documentElement.classList.toggle("dark", next === "dark");
    try {
      localStorage.setItem(STORAGE_KEY, next);
    } catch {
      // Non-fatal: the choice just will not persist.
    }
  }

  return {
    get value() {
      return value;
    },
    get dark() {
      return value === "dark";
    },
    set(next: Theme) {
      apply(next);
    },
    toggle() {
      apply(value === "dark" ? "light" : "dark");
    },
  };
}

export const theme = createTheme();
