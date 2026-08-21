/**
 * Partage: share files securely
 * © 2025 - Nicolas CARPi, Deltablot
 * License: MIT
 */
package main

import (
	"crypto/rand"
	"embed"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"mime"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"text/template"
	"time"
)

//go:generate bash build.sh

//go:embed dist/index.js* dist/main.css* index.html dist/favicon.ico dist/robots.txt
var staticFiles embed.FS

var (
	infoLogger             = log.New(os.Stdout, "[info] ", log.LstdFlags)
	errorLogger            = log.New(os.Stderr, "[error] ", log.LstdFlags|log.Lshortfile)
	storageMutex           sync.Mutex
	errStorageLimitReached = errors.New("storage limit reached")
)

var tmpl = template.Must(template.ParseFS(staticFiles, "index.html"))

type Part struct {
	Id        string    `json:"id"`
	CreatedAt time.Time `json:"created_at"`
	Deadline  string    `json:"deadline"`
	ExpiresAt int64     `json:"expires_at"`
}

// this will be overwritten during docker build
var partageVersion string = "dev"

var svgLogo string = ""

var storageDirectory string

var maxFileSizeStr = "1024"

var maxFileSize int64

var defaultMaxTotalFiles int64 = 24

const (
	shareIDBytes            = 9
	maxDeadline             = 504 * time.Hour
	maxMultipartOverhead    = int64(1 << 20)
	maxDeadlineFieldLength  = int64(64)
	serverReadHeaderTimeout = 10 * time.Second
	serverReadTimeout       = 1 * time.Hour
	serverWriteTimeout      = 1 * time.Hour
	serverIdleTimeout       = 2 * time.Minute
)

var defaultCleanupTimerMin int64 = 10

var siteUrl = "http://localhost"

// partageKey holds the randomly generated key
var partageKey string

func newShareID() (string, error) {
	rawID := make([]byte, shareIDBytes)
	if _, err := rand.Read(rawID); err != nil {
		return "", fmt.Errorf("failed to generate share id: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(rawID), nil
}

func storageFilename(id string, expiresAt int64) string {
	return id + "." + strconv.FormatInt(expiresAt, 36)
}

func parseStorageFilename(filename string) (int64, error) {
	id, expiresAtPart, found := strings.Cut(filename, ".")
	if !found || strings.Contains(expiresAtPart, ".") {
		return 0, fmt.Errorf("invalid storage filename")
	}

	rawID, err := base64.RawURLEncoding.DecodeString(id)
	if err != nil || len(rawID) != shareIDBytes || base64.RawURLEncoding.EncodeToString(rawID) != id {
		return 0, fmt.Errorf("invalid share id")
	}

	expiresAt, err := strconv.ParseInt(expiresAtPart, 36, 64)
	if err != nil || expiresAt <= 0 || strconv.FormatInt(expiresAt, 36) != expiresAtPart {
		return 0, fmt.Errorf("invalid base36 expiration")
	}
	return expiresAt, nil
}

func initMaxFileSize() int64 {
	maxFileSizeStr = "1024"
	if os.Getenv("MAX_FILE_SIZE_MB") != "" {
		maxFileSizeStr = os.Getenv("MAX_FILE_SIZE_MB")
	}
	maxFileSize, err := strconv.ParseInt(maxFileSizeStr, 10, 64)
	if err != nil {
		errorLogger.Fatalf("Server misconfiguration: invalid MAX_FILE_SIZE_MB %v", err)
	}
	return maxFileSize
}

func initPartageKey() {
	b := make([]byte, 16)
	_, err := rand.Read(b)
	if err != nil {
		log.Fatalf("Failed to generate random key: %v", err)
	}
	partageKey = hex.EncodeToString(b)
}

func expireTimestamp(period string) (int64, error) {
	duration, err := time.ParseDuration(period)
	if err != nil {
		return 0, err
	}
	if duration > maxDeadline {
		return 0, fmt.Errorf("deadline exceeds maximum allowed duration of 504h")
	}
	return time.Now().Add(duration).Unix(), nil
}

func getMaxTotalFiles() int64 {
	maxFiles, err := strconv.ParseInt(os.Getenv("MAX_TOTAL_FILES"), 10, 64)
	if err != nil || maxFiles <= 0 {
		return defaultMaxTotalFiles
	}
	return maxFiles
}

func countStoredFiles() (int64, error) {
	entries, err := os.ReadDir(storageDirectory)
	if err != nil {
		return 0, err
	}

	var fileCount int64
	for _, entry := range entries {
		if !entry.IsDir() && !strings.HasPrefix(entry.Name(), ".upload-") {
			fileCount++
		}
	}
	return fileCount, nil
}

func checkStorageCapacity(maxFiles int64) (int64, error) {
	storageMutex.Lock()
	defer storageMutex.Unlock()

	fileCount, err := countStoredFiles()
	if err != nil {
		return 0, err
	}
	if fileCount >= maxFiles {
		return fileCount, errStorageLimitReached
	}
	return fileCount, nil
}

func commitUpload(tempPath string, destFilePath string, maxFiles int64) (int64, error) {
	storageMutex.Lock()
	defer storageMutex.Unlock()

	fileCount, err := countStoredFiles()
	if err != nil {
		return 0, err
	}
	if fileCount >= maxFiles {
		return fileCount, errStorageLimitReached
	}
	if err := os.Rename(tempPath, destFilePath); err != nil {
		return fileCount, err
	}
	return fileCount + 1, nil
}

func writeMultipartError(w http.ResponseWriter, err error) {
	var maxBytesErr *http.MaxBytesError
	if errors.As(err, &maxBytesErr) {
		http.Error(w, "Request body too large", http.StatusRequestEntityTooLarge)
		return
	}
	http.Error(w, "Error parsing multipart form: "+err.Error(), http.StatusBadRequest)
}

// POST Handler
func postHandler(w http.ResponseWriter, r *http.Request) {
	// Retrieve the key from the request header.
	headerKey := r.Header.Get("X-Partage-Key")
	if headerKey != partageKey {
		http.Error(w, "Unauthorized: invalid key", http.StatusUnauthorized)
		return
	}

	w.Header().Set("Content-Type", "application/json")

	now := time.Now()
	part := Part{
		CreatedAt: now,
	}

	maxBytes := maxFileSize * 1024 * 1024
	// Bound the complete request before parsing multipart data. MultipartReader streams
	// parts directly and never spills attacker-controlled data to the process temp dir.
	r.Body = http.MaxBytesReader(w, r.Body, maxBytes+maxMultipartOverhead)
	reader, err := r.MultipartReader()
	if err != nil {
		http.Error(w, "Error parsing multipart form: "+err.Error(), http.StatusBadRequest)
		return
	}

	maxFiles := getMaxTotalFiles()
	fileCount, err := checkStorageCapacity(maxFiles)
	if err != nil {
		if errors.Is(err, errStorageLimitReached) {
			http.Error(w, fmt.Sprintf("Storage limit exceeded: %d files (max %d)", fileCount, maxFiles), http.StatusInsufficientStorage)
			return
		}
		http.Error(w, "Error reading storage directory: "+err.Error(), http.StatusInternalServerError)
		return
	}

	var (
		deadline    string
		gotDeadline bool
		gotFile     bool
		tempFile    *os.File
		tempPath    string
	)
	defer func() {
		if tempFile != nil {
			_ = tempFile.Close()
		}
		if tempPath != "" {
			_ = os.Remove(tempPath)
		}
	}()

	for {
		multipartPart, err := reader.NextPart()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			writeMultipartError(w, err)
			return
		}

		switch multipartPart.FormName() {
		case "file":
			if gotFile || multipartPart.FileName() == "" {
				http.Error(w, "Invalid file part", http.StatusBadRequest)
				return
			}

			tempFile, err = os.CreateTemp(storageDirectory, ".upload-*")
			if err != nil {
				http.Error(w, "Error creating file: "+err.Error(), http.StatusInternalServerError)
				return
			}
			tempPath = tempFile.Name()

			written, err := io.Copy(tempFile, io.LimitReader(multipartPart, maxBytes+1))
			if err != nil {
				var maxBytesErr *http.MaxBytesError
				if errors.As(err, &maxBytesErr) {
					http.Error(w, "Request body too large", http.StatusRequestEntityTooLarge)
				} else if errors.Is(err, io.ErrUnexpectedEOF) {
					http.Error(w, "Error parsing multipart form: "+err.Error(), http.StatusBadRequest)
				} else {
					http.Error(w, "Error saving file: "+err.Error(), http.StatusInternalServerError)
				}
				return
			}
			if err := tempFile.Close(); err != nil {
				http.Error(w, "Error saving file: "+err.Error(), http.StatusInternalServerError)
				return
			}
			tempFile = nil
			if written > maxBytes {
				http.Error(w, fmt.Sprintf("File too large. Maximum allowed is %d MB", maxFileSize), http.StatusRequestEntityTooLarge)
				return
			}
			if err := multipartPart.Close(); err != nil {
				writeMultipartError(w, err)
				return
			}
			gotFile = true

		case "deadline":
			if gotDeadline || multipartPart.FileName() != "" {
				http.Error(w, "Invalid deadline part", http.StatusBadRequest)
				return
			}
			deadlineBytes, err := io.ReadAll(io.LimitReader(multipartPart, maxDeadlineFieldLength+1))
			if err != nil {
				writeMultipartError(w, err)
				return
			}
			if int64(len(deadlineBytes)) > maxDeadlineFieldLength {
				http.Error(w, "Deadline value too large", http.StatusBadRequest)
				return
			}
			deadline = string(deadlineBytes)
			gotDeadline = true
			if err := multipartPart.Close(); err != nil {
				writeMultipartError(w, err)
				return
			}

		default:
			http.Error(w, "Unexpected multipart field", http.StatusBadRequest)
			return
		}
	}

	if !gotFile {
		http.Error(w, "Missing file part", http.StatusBadRequest)
		return
	}
	if !gotDeadline {
		http.Error(w, "Missing deadline part", http.StatusBadRequest)
		return
	}

	ts, err := expireTimestamp(deadline)
	if err != nil {
		http.Error(w, "Error parsing deadline: "+err.Error(), http.StatusBadRequest)
		return
	}

	// Assign a compact, unguessable 72-bit public identifier.
	id, err := newShareID()
	if err != nil {
		http.Error(w, "Error: "+err.Error(), http.StatusInternalServerError)
		return
	}
	part.Id = id
	part.ExpiresAt = ts
	part.Deadline = deadline

	destFilePath := filepath.Join(storageDirectory, storageFilename(part.Id, ts))
	fileCount, err = commitUpload(tempPath, destFilePath, maxFiles)
	if err != nil {
		if errors.Is(err, errStorageLimitReached) {
			http.Error(w, fmt.Sprintf("Storage limit exceeded: %d files (max %d)", fileCount, maxFiles), http.StatusInsufficientStorage)
			return
		}
		http.Error(w, "Error saving file: "+err.Error(), http.StatusInternalServerError)
		return
	}
	// The temp file has been atomically promoted to its final name.
	tempPath = ""

	infoLogger.Printf("received new file: %+v", part)

	// Send a confirmation response back as JSON.
	response := part
	if err := json.NewEncoder(w).Encode(response); err != nil {
		errorLogger.Printf("failed to write response: %v", err)
	}
}

// GET /part
func getFileHandler(w http.ResponseWriter, r *http.Request) {
	filename := r.URL.Path[len("/api/v1/part/"):]
	if _, err := parseStorageFilename(filename); err != nil {
		http.Error(w, "Invalid id format", http.StatusBadRequest)
		return
	}

	filePath := filepath.Join(storageDirectory, filepath.Base(filename))

	// Check if the file exists.
	if _, err := os.Stat(filePath); os.IsNotExist(err) {
		http.NotFound(w, r)
		return
	}

	// Force opaque downloads so http.ServeFile cannot content-sniff attacker-controlled
	// bytes into an active browser content type.
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", "attachment")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Security-Policy", "default-src 'none';")
	http.ServeFile(w, r, filePath)
}

func serveIndexTemplate(w http.ResponseWriter, r *http.Request) {
	// Determine which page based on the request URL.
	// Use true if the URL path is "/get"; false otherwise.
	getPage := false
	if r.URL.Path == "/get" {
		getPage = true
	}

	// Build the data object for the template.
	data := struct {
		GetPage     bool
		PartageKey  string
		SvgLogo     string
		MaxFileSize int64
		Version     string
	}{
		GetPage:     getPage,
		PartageKey:  partageKey,
		SvgLogo:     svgLogo,
		MaxFileSize: maxFileSize,
		Version:     partageVersion,
	}
	w.Header().Set("Content-Security-Policy",
		"default-src 'self'; "+
			"script-src 'self'; "+
			"style-src 'self' 'unsafe-inline'; "+
			"img-src 'self'; "+
			"connect-src 'self'; "+
			"frame-ancestors 'none';"+
			"upgrade-insecure-requests;",
	)
	w.Header().Set("Referrer-Policy", "same-origin")
	w.Header().Set("Strict-Transport-Security", "max-age=63072000")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("X-Frame-Options", "DENY")

	// Execute the template with the data.
	if err := tmpl.Execute(w, data); err != nil {
		errorLogger.Printf("Template error: %v", err)
		http.Error(w, "Error executing template", http.StatusInternalServerError)
	}
}

// cleanExpiredFiles scans the specified folder, parses each filename for an expiration Unix timestamp,
// and deletes the file if the expiration time is before the current time.
func cleanExpiredFiles(folder string) error {
	// Read directory entries.
	entries, err := os.ReadDir(folder)
	if err != nil {
		return fmt.Errorf("error reading directory %q: %w", folder, err)
	}

	now := time.Now()

	for _, entry := range entries {
		// Skip directories.
		if entry.IsDir() {
			continue
		}
		fileName := entry.Name()
		if strings.HasPrefix(fileName, ".upload-") {
			continue
		}

		timestamp, err := parseStorageFilename(fileName)
		if err != nil {
			errorLogger.Printf("skipping file %q: error parsing timestamp: %v\n", fileName, err)
			continue
		}

		expirationTime := time.Unix(timestamp, 0)
		// If the file's expiration time is before now, delete the file.
		if expirationTime.Before(now) {
			fullPath := filepath.Join(folder, fileName)
			if err := os.Remove(fullPath); err != nil {
				errorLogger.Printf("failed to remove file %q: %v\n", fullPath, err)
			} else {
				infoLogger.Printf("removed expired file: %q (expired at %s)\n", fullPath, expirationTime.Format(time.RFC3339))
			}
		}
	}

	return nil
}

// scheduleCleanup sets up a ticker that runs the cleanExpiredFiles function every 10 minutes in a separate goroutine.
func scheduleCleanup(folder string) {
	// Run the cleanup immediately on startup.
	infoLogger.Println("running initial cleanup...")
	if err := cleanExpiredFiles(folder); err != nil {
		errorLogger.Printf("error cleaning expired files: %v\n", err)
	}

	// Create a ticker that triggers every 10 minutes.
	cleanupTimerMinStr := os.Getenv("CLEANUP_TIMER_MIN")
	cleanupTimerMin, err := strconv.ParseInt(cleanupTimerMinStr, 10, 64)
	if err != nil {
		cleanupTimerMin = defaultCleanupTimerMin
	}
	ticker := time.NewTicker(time.Duration(cleanupTimerMin) * time.Minute)
	infoLogger.Printf("cleanup timer: every %d minutes", cleanupTimerMin)

	// Run the cleanup in a separate goroutine.
	go func() {
		for range ticker.C {
			if err := cleanExpiredFiles(folder); err != nil {
				errorLogger.Printf("error cleaning expired files: %v\n", err)
			}
		}
	}()
}

// serveAsset will pick the .br version if the client accepts it.
func serveAsset(w http.ResponseWriter, r *http.Request) {
	// strip leading slash
	reqPath := strings.TrimPrefix(r.URL.Path, "/")
	// detect mime type
	ext := path.Ext(reqPath)
	w.Header().Set("Content-Type", mime.TypeByExtension(ext))
	w.Header().Set("Cache-Control", "public, max-age=31536000")

	// if client supports brotli, try .br
	if strings.Contains(r.Header.Get("Accept-Encoding"), "br") {
		if f, err := staticFiles.Open("dist/" + reqPath + ".br"); err == nil {
			defer f.Close()
			w.Header().Set("Content-Encoding", "br")
			io.Copy(w, f)
			return
		}
	}
	// fallback to uncompressed
	f, err := staticFiles.Open("dist/" + reqPath)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	defer f.Close()
	io.Copy(w, f)
}

func newHTTPServer(addr string) *http.Server {
	return &http.Server{
		Addr:              addr,
		Handler:           http.DefaultServeMux,
		ReadHeaderTimeout: serverReadHeaderTimeout,
		ReadTimeout:       serverReadTimeout,
		WriteTimeout:      serverWriteTimeout,
		IdleTimeout:       serverIdleTimeout,
	}
}

func main() {
	infoLogger.Printf("starting partage version: %s", partageVersion)
	// Define and parse command-line flags.
	port := flag.String("port", "8080", "Port to listen on")
	dir := flag.String("dir", "/var/partage", "Directory to store uploaded files")
	flag.Parse()

	maxFileSize = initMaxFileSize()

	initPartageKey()

	// Store the chosen directory into a global variable for use in other functions.
	storageDirectory = *dir
	if _, err := os.Stat(storageDirectory); os.IsNotExist(err) {
		if err := os.MkdirAll(storageDirectory, os.ModePerm); err != nil {
			log.Fatalf("Failed to create storage directory: %v", err)
		}
	}

	siteUrlEnv := os.Getenv("SITE_URL")
	if len(siteUrlEnv) > 10 {
		siteUrl = siteUrlEnv
	}

	scheduleCleanup(storageDirectory)

	addr := ":" + *port
	infoLogger.Printf("server running on port: %s", *port)

	http.HandleFunc("GET /", serveIndexTemplate)
	http.HandleFunc("GET /get", serveIndexTemplate)
	http.HandleFunc("POST /api/v1/parts", postHandler)
	http.HandleFunc("GET /api/v1/part/", getFileHandler)
	http.HandleFunc("GET /favicon.ico", serveAsset)
	http.HandleFunc("GET /healthcheck", func(w http.ResponseWriter, r *http.Request) {
		// 204 No Content
		w.WriteHeader(http.StatusNoContent)
	})

	// in prod we embed the files, but in dev we serve them directly to avoid having to recompile binary after a change
	if os.Getenv("DEV") == "1" {
		http.HandleFunc("GET /index.js", func(w http.ResponseWriter, r *http.Request) {
			http.ServeFile(w, r, "src/index.js")
		})
		http.HandleFunc("GET /partage.js", func(w http.ResponseWriter, r *http.Request) {
			http.ServeFile(w, r, "src/partage.js")
		})
		http.HandleFunc("GET /utils.js", func(w http.ResponseWriter, r *http.Request) {
			http.ServeFile(w, r, "src/utils.js")
		})
		http.HandleFunc("GET /main.css", func(w http.ResponseWriter, r *http.Request) {
			http.ServeFile(w, r, "src/main.css")
		})
		infoLogger.Printf("dev service running at: http://localhost:%s", *port)
	} else { // PROD
		http.HandleFunc("GET /index.js", serveAsset)
		http.HandleFunc("GET /robots.txt", serveAsset)
		http.HandleFunc("GET /partage.js", serveAsset)
		http.HandleFunc("GET /utils.js", serveAsset)
		http.HandleFunc("GET /index.css", serveAsset)
		http.HandleFunc("GET /main.css", serveAsset)
		infoLogger.Printf("service running at: http://localhost:%s", *port)
	}

	server := newHTTPServer(addr)
	if err := server.ListenAndServe(); err != nil {
		errorLogger.Fatalf("failed to start server: %v", err)
	}
}
