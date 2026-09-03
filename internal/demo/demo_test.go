package demo_test

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/unmaykr-a/silt/internal/demo"
	"github.com/unmaykr-a/silt/internal/store"
	"github.com/unmaykr-a/silt/internal/store/sqlcgen"
)

// The seed is not shipped code, but it is what the published demo shows and
// what the end-to-end suite runs against — so a seed that quietly stops
// producing history is a demo that quietly stops demonstrating anything, and a
// suite asserting against data that no longer has the shape it assumed.
// These are the properties both depend on.

func TestEveryStackHasAHistory(t *testing.T) {
	db, projects := seeded(t)

	for _, p := range projects {
		if got := len(snapshots(t, db, p.ID)); got < 2 {
			// Identical observations are touched rather than inserted, so a
			// stack whose configuration never moves collapses to one row. That
			// is correct storage behaviour and a broken demo: the density
			// strip, the diff picker and the image history all need somewhere
			// to go.
			t.Errorf("%s has %d snapshots, want at least 2 — its history never changes", p.Name, got)
		}
	}
}

func TestTheNewestChangeCarriesSeveralKinds(t *testing.T) {
	db, projects := seeded(t)
	ctx := context.Background()

	media := project(t, projects, "media")
	rows := snapshots(t, db, media.ID)
	if len(rows) < 2 {
		t.Fatalf("media has %d snapshots, want at least 2", len(rows))
	}

	// One snapshot carrying more than one change is the whole reason the diff
	// screen groups by service and kind. A demo where every diff is a single
	// version string moving demonstrates none of it.
	from, err := db.LoadSnapshotModel(ctx, rows[1].ID)
	if err != nil {
		t.Fatalf("load from: %v", err)
	}
	to, err := db.LoadSnapshotModel(ctx, rows[0].ID)
	if err != nil {
		t.Fatalf("load to: %v", err)
	}

	before, after := from.Project.Services["radarr"], to.Project.Services["radarr"]
	if before.Image == after.Image {
		t.Error("the newest media change does not move the image")
	}
	if before.ImageID == after.ImageID {
		// Image identity used to be derived from the service name, so it never
		// moved and the image history had one row however many upgrades the
		// history contained.
		t.Error("the newest media change does not move the image id")
	}
	if len(after.Environment) <= len(before.Environment) {
		t.Error("the newest media change does not add an environment key")
	}
	if before.Environment["RADARR__API_KEY"] == after.Environment["RADARR__API_KEY"] {
		t.Error("the newest media change does not rotate the API key")
	}
}

func TestTheDemoDoesNotStoreASecretInCleartext(t *testing.T) {
	db, projects := seeded(t)
	ctx := context.Background()

	// The seed contains literal API keys so redaction has something to do.
	// Written into the observation directly rather than through the capture
	// path, they reached the database and the published demo displayed one —
	// on the one screen whose entire point is that it never stores a value
	// like this.
	planted := []string{"8f14e45fceea167a", "c4ca4238a0b92382", "demo-secret-"}

	seenAFile := false
	for _, p := range projects {
		for _, row := range snapshots(t, db, p.ID) {
			files, err := db.RQ.ListComposeFiles(ctx, row.ID)
			if err != nil {
				t.Fatalf("list files for %s: %v", p.Name, err)
			}
			for _, f := range files {
				body, err := db.ComposeFileContent(ctx, row.ID, f.Path)
				if err != nil {
					t.Fatalf("read %s: %v", f.Path, err)
				}
				seenAFile = true
				for _, secret := range planted {
					if strings.Contains(body, secret) {
						t.Fatalf("%s/%s holds %q in cleartext", p.Name, f.Path, secret)
					}
				}
				// The keep-listed values beside it must survive, or the demo
				// would show a file of nothing but digests and teach the
				// opposite lesson.
				if strings.Contains(body, "environment:") && !strings.Contains(body, "TZ=Europe/Tallinn") {
					t.Errorf("%s/%s redacted a keep-listed value", p.Name, f.Path)
				}
			}
		}
	}
	if !seenAFile {
		t.Fatal("no compose files in the seed, so this proved nothing")
	}
}

func TestOneStackCarriesAnUnappliedEdit(t *testing.T) {
	db, projects := seeded(t)

	// Drift is the state that cannot be produced by changing a container, so
	// it is the one the seed has to construct deliberately — and the one the
	// attention filters and the config.drift event both depend on.
	rows := snapshots(t, db, project(t, projects, "gitea").ID)
	found := false
	for _, r := range rows {
		if r.FilesChanged == 1 && r.ConfigChanged == 0 {
			found = true
		}
	}
	if !found {
		t.Error("gitea has no snapshot where only the file changed")
	}
}

func project(t *testing.T, projects []sqlcgen.Project, name string) sqlcgen.Project {
	t.Helper()
	for _, p := range projects {
		if p.Name == name {
			return p
		}
	}
	t.Fatalf("no %s project in the seed", name)
	return sqlcgen.Project{}
}

func snapshots(t *testing.T, db *store.Store, projectID int64) []sqlcgen.Snapshot {
	t.Helper()
	rows, err := db.RQ.ListSnapshots(context.Background(), sqlcgen.ListSnapshotsParams{
		ProjectID: projectID,
		// Comfortably past the newest, including the drift snapshot the seed
		// writes a second into the future.
		Before:  store.Now() + 3_600_000,
		MaxRows: 100,
	})
	if err != nil {
		t.Fatalf("list snapshots: %v", err)
	}
	return rows
}

func seeded(t *testing.T) (*store.Store, []sqlcgen.Project) {
	t.Helper()
	ctx := context.Background()

	db, err := store.Open(ctx, filepath.Join(t.TempDir(), "silt.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { db.Close() })

	if err := demo.Seed(ctx, db); err != nil {
		t.Fatalf("seed: %v", err)
	}
	projects, err := db.RQ.ListProjects(ctx, 1)
	if err != nil {
		t.Fatalf("list projects: %v", err)
	}
	if len(projects) != len(demo.Stacks()) {
		t.Fatalf("seeded %d projects, want %d", len(projects), len(demo.Stacks()))
	}
	return db, projects
}
