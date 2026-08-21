package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestGetFileHandlerForcesOpaqueDownload(t *testing.T) {
	oldStorageDirectory := storageDirectory
	storageDirectory = t.TempDir()
	t.Cleanup(func() {
		storageDirectory = oldStorageDirectory
	})

	const filename = "019f329d-b729-7b14-9e40-7a24b9849531.1783264224"
	const payload = `<!doctype html><script>document.title="xss"</script>`
	if err := os.WriteFile(filepath.Join(storageDirectory, filename), []byte(payload), 0600); err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/part/"+filename, nil)
	res := httptest.NewRecorder()
	getFileHandler(res, req)

	if res.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", res.Code, http.StatusOK)
	}
	if got := res.Header().Get("Content-Type"); got != "application/octet-stream" {
		t.Errorf("Content-Type = %q, want application/octet-stream", got)
	}
	if got := res.Header().Get("Content-Disposition"); got != "attachment" {
		t.Errorf("Content-Disposition = %q, want attachment", got)
	}
	if got := res.Header().Get("X-Content-Type-Options"); got != "nosniff" {
		t.Errorf("X-Content-Type-Options = %q, want nosniff", got)
	}
	if got := res.Header().Get("Content-Security-Policy"); got != "default-src 'none';" {
		t.Errorf("Content-Security-Policy = %q, want default-src 'none';", got)
	}
	if got := res.Body.String(); got != payload {
		t.Errorf("body = %q, want %q", got, payload)
	}
}
