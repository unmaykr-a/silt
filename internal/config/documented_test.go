package config_test

import (
	"os"
	"reflect"
	"regexp"
	"strings"
	"testing"

	"github.com/unmaykr-a/silt/internal/config"
)

// Every setting has to be written down somewhere someone will find it.
//
// The config reference in PROJECT.md and the commented .env.example are the
// two places people look, and both had gone quietly stale: sixteen variables
// were readable only by reading the struct — the whole OIDC block among them,
// which is exactly what someone is hunting for when a login will not work.
//
// Nothing else notices that, because a variable that is read but undocumented
// works perfectly. This is the thing that notices.

// envTags reads the SILT_* names straight off the struct, so the list can
// never disagree with what the process actually reads.
func envTags(t *testing.T) []string {
	t.Helper()
	var out []string
	typ := reflect.TypeOf(config.Config{})
	for i := 0; i < typ.NumField(); i++ {
		if name := typ.Field(i).Tag.Get("env"); strings.HasPrefix(name, "SILT_") {
			out = append(out, name)
		}
	}
	if len(out) < 20 {
		t.Fatalf("only found %d settings on the struct; the reflection is wrong", len(out))
	}
	return out
}

func read(t *testing.T, path string) string {
	t.Helper()
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(body)
}

func TestEverySettingIsInTheProjectReference(t *testing.T) {
	doc := read(t, "../../PROJECT.md")
	var missing []string
	for _, name := range envTags(t) {
		// Backticked, which is how the reference table writes them.
		if !strings.Contains(doc, "`"+name+"`") {
			missing = append(missing, name)
		}
	}
	if len(missing) > 0 {
		t.Errorf("PROJECT.md does not mention %d settings: %s",
			len(missing), strings.Join(missing, ", "))
	}
}

func TestEverySettingIsInTheWiki(t *testing.T) {
	// The third copy of the reference, and the one readers actually find. It is
	// a copy on purpose — PROJECT.md is the design brief and the wiki is the
	// manual, and they say different things around the same table — but a copy
	// with no guard is how the OIDC block came to be documented nowhere.
	doc := read(t, "../../docs/wiki/Configuration.md")
	var missing []string
	for _, name := range envTags(t) {
		if !strings.Contains(doc, "`"+name+"`") {
			missing = append(missing, name)
		}
	}
	if len(missing) > 0 {
		t.Errorf("docs/wiki/Configuration.md does not mention %d settings: %s",
			len(missing), strings.Join(missing, ", "))
	}
}

func TestTheWikiDocumentsNothingSiltDoesNotRead(t *testing.T) {
	// And the other direction, so a setting removed from the code does not go on
	// being documented as if it worked.
	known := map[string]bool{}
	for _, name := range envTags(t) {
		known[name] = true
	}
	// docker-compose.yml reads a couple itself, SILT_PORT among them; those
	// belong in the manual even though the process never sees them.
	compose := read(t, "../../docker-compose.yml")

	found := regexp.MustCompile("`(SILT_[A-Z_]+)`").FindAllStringSubmatch(
		read(t, "../../docs/wiki/Configuration.md"), -1)
	seen := map[string]bool{}
	var unknown []string
	for _, m := range found {
		if known[m[1]] || seen[m[1]] || strings.Contains(compose, "${"+m[1]) {
			continue
		}
		seen[m[1]] = true
		unknown = append(unknown, m[1])
	}
	if len(unknown) > 0 {
		t.Errorf("docs/wiki/Configuration.md documents settings nothing reads: %s",
			strings.Join(unknown, ", "))
	}
}

func TestEverySettingIsInTheExampleEnvironment(t *testing.T) {
	// .env.example is what people copy, so a setting absent from it is a
	// setting most operators will never learn exists.
	example := read(t, "../../.env.example")
	var missing []string
	for _, name := range envTags(t) {
		if !regexp.MustCompile(`(?m)^#?\s*` + name + `=`).MatchString(example) {
			missing = append(missing, name)
		}
	}
	if len(missing) > 0 {
		t.Errorf(".env.example does not offer %d settings: %s",
			len(missing), strings.Join(missing, ", "))
	}
}

func TestTheExampleEnvironmentOffersNothingNobodyReads(t *testing.T) {
	// The other direction: a variable someone sets from the example and nothing
	// ever reads is worse than one that is missing, because it looks like it
	// worked.
	//
	// "Nobody" rather than "Silt" — docker-compose.yml reads a couple of these
	// itself, SILT_PORT among them, and those belong in the example even though
	// the process never sees them.
	known := map[string]bool{}
	for _, name := range envTags(t) {
		known[name] = true
	}
	compose := read(t, "../../docker-compose.yml")

	found := regexp.MustCompile(`(?m)^#?\s*(SILT_[A-Z_]+)=`).FindAllStringSubmatch(read(t, "../../.env.example"), -1)
	var unknown []string
	for _, m := range found {
		if known[m[1]] || strings.Contains(compose, "${"+m[1]) {
			continue
		}
		unknown = append(unknown, m[1])
	}
	if len(unknown) > 0 {
		t.Errorf(".env.example offers settings nothing reads: %s", strings.Join(unknown, ", "))
	}
}
