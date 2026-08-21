package main

import (
	"bytes"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
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

func newUploadRequest(fileData []byte, deadline string, addUnexpectedFile bool) (*http.Request, error) {
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)

	filePart, err := writer.CreateFormFile("file", "partage")
	if err != nil {
		return nil, err
	}
	if _, err := filePart.Write(fileData); err != nil {
		return nil, err
	}
	if err := writer.WriteField("deadline", deadline); err != nil {
		return nil, err
	}
	if addUnexpectedFile {
		unexpectedPart, err := writer.CreateFormFile("junk", "junk.bin")
		if err != nil {
			return nil, err
		}
		if _, err := unexpectedPart.Write([]byte("junk")); err != nil {
			return nil, err
		}
	}
	contentType := writer.FormDataContentType()
	if err := writer.Close(); err != nil {
		return nil, err
	}

	req := httptest.NewRequest(http.MethodPost, "/api/v1/parts", &body)
	req.Header.Set("Content-Type", contentType)
	req.Header.Set("X-Partage-Key", "test-key")
	return req, nil
}

func setupUploadHandlerTest(t *testing.T, maxFiles string) {
	t.Helper()
	oldStorageDirectory := storageDirectory
	oldPartageKey := partageKey
	oldMaxFileSize := maxFileSize
	storageDirectory = t.TempDir()
	partageKey = "test-key"
	maxFileSize = 1
	t.Setenv("MAX_TOTAL_FILES", maxFiles)
	t.Cleanup(func() {
		storageDirectory = oldStorageDirectory
		partageKey = oldPartageKey
		maxFileSize = oldMaxFileSize
	})
}

func TestPostHandlerRejectsOversizedStream(t *testing.T) {
	setupUploadHandlerTest(t, "24")

	payload := bytes.Repeat([]byte{'x'}, 1024*1024+1)
	req, err := newUploadRequest(payload, "1h", false)
	if err != nil {
		t.Fatal(err)
	}
	res := httptest.NewRecorder()
	postHandler(res, req)

	if res.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, want %d", res.Code, http.StatusRequestEntityTooLarge)
	}
	entries, err := os.ReadDir(storageDirectory)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("storage contains %d files after rejected upload, want 0", len(entries))
	}
}

func TestPostHandlerRejectsUnexpectedMultipartFile(t *testing.T) {
	setupUploadHandlerTest(t, "24")

	req, err := newUploadRequest([]byte("ok"), "1h", true)
	if err != nil {
		t.Fatal(err)
	}
	res := httptest.NewRecorder()
	postHandler(res, req)

	if res.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", res.Code, http.StatusBadRequest)
	}
	entries, err := os.ReadDir(storageDirectory)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("storage contains %d files after rejected upload, want 0", len(entries))
	}
}

func TestPostHandlerEnforcesFileLimitConcurrently(t *testing.T) {
	setupUploadHandlerTest(t, "1")

	const requestCount = 16
	requests := make([]*http.Request, 0, requestCount)
	for range requestCount {
		req, err := newUploadRequest([]byte("x"), "1h", false)
		if err != nil {
			t.Fatal(err)
		}
		requests = append(requests, req)
	}

	statuses := make(chan int, requestCount)
	var wg sync.WaitGroup
	for _, req := range requests {
		wg.Add(1)
		go func(req *http.Request) {
			defer wg.Done()
			res := httptest.NewRecorder()
			postHandler(res, req)
			statuses <- res.Code
		}(req)
	}
	wg.Wait()
	close(statuses)

	var successes int
	var storageFull int
	for status := range statuses {
		switch status {
		case http.StatusOK:
			successes++
		case http.StatusInsufficientStorage:
			storageFull++
		default:
			t.Errorf("unexpected status %d", status)
		}
	}
	if successes != 1 {
		t.Errorf("successful uploads = %d, want 1", successes)
	}
	if storageFull != requestCount-1 {
		t.Errorf("storage-full responses = %d, want %d", storageFull, requestCount-1)
	}

	entries, err := os.ReadDir(storageDirectory)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("storage contains %d files, want 1", len(entries))
	}
}
