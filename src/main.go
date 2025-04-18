/**
 * Partage: share files securely
 * © 2025 - Nicolas CARPi, Deltablot
 * License: MIT
 */
package main

import (
	"crypto/rand"
	"embed"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"mime"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"text/template"
	"time"

	"github.com/google/uuid"
)

//go:generate bash build.sh

//go:embed dist/* index.html favicon.ico
var staticFiles embed.FS

var (
	infoLogger  = log.New(os.Stdout, "[info] ", log.LstdFlags)
	errorLogger = log.New(os.Stderr, "[error] ", log.LstdFlags|log.Lshortfile)
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

var storageDirectory string

var defaultMaxFileSizeMb = "1024"

var defaultMaxTotalFiles int64 = 24

var defaultCleanupTimerMin int64 = 10

var siteUrl = "http://localhost"

// uuidv7TimestampRegex ensures that the filename follows the format:
// UUID with version 7 (third group starts with '7') and then a hyphen and a Unix timestamp.
// For example: "123e4567-e89b-7d89-a456-426614174000-1678932930"
var uuidv7TimestampRegex = regexp.MustCompile(`^[a-fA-F0-9]{8}-[a-fA-F0-9]{4}-7[a-fA-F0-9]{3}-[a-fA-F0-9]{4}-[a-fA-F0-9]{12}-\d+$`)

// partageKey holds the randomly generated key
var partageKey string

func getUuidv7() (string, error) {
	id, err := uuid.NewV7()
	if err != nil {
		return "", fmt.Errorf("failed to generate UUID: %w", err)
	}
	return id.String(), nil
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
	return time.Now().Add(duration).Unix(), nil
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

	// Parse the multipart form with a maximum memory of 10 MB for file parts.
	// Files larger than this size will be stored in temporary files.
	err := r.ParseMultipartForm(10 << 20) // 10MB
	if err != nil {
		http.Error(w, "Error parsing multipart form: "+err.Error(), http.StatusBadRequest)
		return
	}

	// Retrieve the file part.
	file, header, err := r.FormFile("file")
	if err != nil {
		http.Error(w, "Error retrieving file: "+err.Error(), http.StatusBadRequest)
		return
	}
	defer file.Close()

	// enforce max file size from env (in MB)
	maxFileSizeStr := os.Getenv("MAX_FILE_SIZE_MB")
	if maxFileSizeStr == "" {
		maxFileSizeStr = defaultMaxFileSizeMb
	}
	maxFileSize, err := strconv.ParseInt(maxFileSizeStr, 10, 64)
	if err != nil {
		http.Error(w, "Server misconfiguration: invalid MAX_FILE_SIZE_MB", http.StatusInternalServerError)
		return
	}
	maxBytes := maxFileSize * 1024 * 1024
	if header.Size > maxBytes {
		http.Error(w, fmt.Sprintf("File too large. Maximum allowed is %d MB", maxFileSize), http.StatusRequestEntityTooLarge)
		return
	}

	// make sure the file number limit hasn't been reached
	// enforce max total files in storageDirectory
	maxFilesStr := os.Getenv("MAX_TOTAL_FILES")
	maxFiles, err := strconv.ParseInt(maxFilesStr, 10, 64)
	if err != nil || maxFiles <= 0 {
		maxFiles = defaultMaxTotalFiles
	}

	entries, err := os.ReadDir(storageDirectory)
	if err != nil {
		http.Error(w, "Error reading storage directory: "+err.Error(), http.StatusInternalServerError)
		return
	}

	var fileCount int64
	for _, e := range entries {
		if !e.IsDir() {
			fileCount++
		}
	}

	if fileCount >= maxFiles {
		http.Error(w, fmt.Sprintf("Storage limit exceeded: %d files (max %d)", fileCount, maxFiles), http.StatusInsufficientStorage)
		return
	}

	// Retrieve the deadline part.
	deadline := r.FormValue("deadline")
	ts, err := expireTimestamp(deadline)
	if err != nil {
		http.Error(w, "Error parsing deadline: "+err.Error(), http.StatusBadRequest)
		return
	}

	// assign id
	id, err := getUuidv7()
	if err != nil {
		http.Error(w, "Error: "+err.Error(), http.StatusInternalServerError)
		return
	}
	part.Id = id
	part.ExpiresAt = ts
	part.Deadline = deadline

	// For demonstration, write the uploaded file to disk.
	// In production, this could be stored in a database or object storage.
	destFilePath := storageDirectory + "/" + part.Id + "-" + strconv.FormatInt(ts, 10)
	dst, err := os.Create(destFilePath)
	if err != nil {
		http.Error(w, "Error creating file: "+err.Error(), http.StatusInternalServerError)
		return
	}
	defer dst.Close()

	// Copy the file content.
	if _, err := io.Copy(dst, file); err != nil {
		http.Error(w, "Error saving file: "+err.Error(), http.StatusInternalServerError)
		return
	}

	// For now, simply log the record. In a real application, you might save this record in a database.
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
	if !uuidv7TimestampRegex.MatchString(filename) {
		http.Error(w, "Invalid id format", http.StatusBadRequest)
		return
	}

	filePath := filepath.Join(storageDirectory, filepath.Base(filename))

	// Check if the file exists.
	if _, err := os.Stat(filePath); os.IsNotExist(err) {
		http.NotFound(w, r)
		return
	}

	// Serve the file.
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
		GetPage    bool
		PartageKey string
		SvgLogo    string
	}{
		GetPage:    getPage,
		PartageKey: partageKey,
		SvgLogo:    os.Getenv("SVG_LOGO"),
	}

	// Execute the template with the data.
	if err := tmpl.Execute(w, data); err != nil {
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

		// Split the filename on "-" and get the last part.
		parts := strings.Split(fileName, "-")
		if len(parts) < 2 {
			// Skip files that don't match the expected naming pattern.
			continue
		}
		lastPart := parts[len(parts)-1]

		// Parse the resulting string as a Unix timestamp.
		timestamp, err := strconv.ParseInt(lastPart, 10, 64)
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

func main() {
	infoLogger.Printf("starting partage version: %s", partageVersion)
	// Define and parse command-line flags.
	port := flag.String("port", "8080", "Port to listen on")
	dir := flag.String("dir", "/var/partage", "Directory to store uploaded files")
	flag.Parse()

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
		infoLogger.Printf("service running at: %s", siteUrl)
	}

	if err := http.ListenAndServe(addr, nil); err != nil {
		errorLogger.Fatalf("failed to start server: %v", err)
	}
}
