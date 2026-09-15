// Package docs exists so the documentation can have tests.
//
// Silt's prose is checked the same way its code is, because this project has
// four recorded instances of a feature that was documented and not built. The
// guards live here and in internal/config: this one covers the wiki's shape —
// that every page is reachable and every link resolves — and the config one
// covers the settings reference against the struct the process actually reads.
//
// There is nothing to import. A package with only tests still needs a package
// clause, and a file saying why is better than an empty one.
package docs
