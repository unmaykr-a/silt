package api_test

import (
	"net/http"
	"testing"
)

// A backup is a read, and the write guard lets every read through — so
// "administrator only" had to be said here rather than assumed from the method.
//
// The file is every project, every captured compose file, the audit trail and
// the session table in one download. Redaction means the values are keyed
// digests rather than secrets, which is the point of it, but "may read the
// screens" and "may walk off with the database" are not the same permission.
func TestAViewerCannotDownloadTheDatabase(t *testing.T) {
	url := viewerServer(t)
	client := &http.Client{}
	viewer := map[string]string{"X-Remote-User": "reader", "X-Remote-Groups": "users"}

	code, body := status(t, client, http.MethodGet, url+"/api/maintenance/backup", viewer, "")
	if code == http.StatusOK {
		t.Fatalf("a viewer downloaded the database: %.60s", body)
	}
	if code != http.StatusForbidden {
		t.Errorf("viewer backup = %d, want 403", code)
	}
}

func TestAnAdministratorCanDownloadTheDatabase(t *testing.T) {
	url := viewerServer(t)
	client := &http.Client{}
	admin := map[string]string{"X-Remote-User": "andri", "X-Remote-Groups": "silt-admins"}

	code, body := status(t, client, http.MethodGet, url+"/api/maintenance/backup", admin, "")
	if code != http.StatusOK {
		t.Fatalf("administrator backup = %d %.60s", code, body)
	}
	if len(body) < 15 || body[:15] != "SQLite format 3" {
		t.Errorf("the download is not a database: %.40q", body)
	}
}
