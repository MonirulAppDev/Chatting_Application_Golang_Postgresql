// Package handlers defines the HTTP request handlers and controller logic for the application.
package handlers

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"backend/internal/config"
)

/*
MediaHandler manages media file uploads, storage validation, and file path generation.
Files are categorized into images (./uploads/images/) and audio (./uploads/audio/).
*/
type MediaHandler struct {
	// Storage directory path for uploaded files (e.g. ./uploads)
	UploadDir string
	// Storage directory path for stickers (e.g. ./static/stickers)
	StickersDir string
	// Maximum allowed file size in bytes (e.g. 20MB)
	MaxFileSizeBytes int64
	// Host IP for generating accessible mobile URLs
	HostIP string
	// Server Port
	ServerPort string
}

// NewMediaHandler creates and returns a new MediaHandler instance with configured storage options.
func NewMediaHandler(cfg *config.Config) *MediaHandler {
	maxSize, err := strconv.ParseInt(cfg.MaxFileSizeBytes, 10, 64)
	if err != nil || maxSize <= 0 {
		maxSize = 20 * 1024 * 1024 // 20 MB fallback default
	}

	stickersDir := cfg.StickersDir
	if stickersDir == "" {
		stickersDir = "./static/stickers"
	}

	// Ensure root uploads directory, subdirectories, and stickers directory exist on disk
	imageDir := filepath.Join(cfg.UploadDir, "images")
	audioDir := filepath.Join(cfg.UploadDir, "audio")

	for _, dir := range []string{cfg.UploadDir, imageDir, audioDir, stickersDir} {
		if err := os.MkdirAll(dir, os.ModePerm); err != nil {
			log.Printf("Warning: failed to create storage directory %s: %v", dir, err)
		}
	}

	return &MediaHandler{
		UploadDir:        cfg.UploadDir,
		StickersDir:      stickersDir,
		MaxFileSizeBytes: maxSize,
		HostIP:           cfg.HostIP,
		ServerPort:       cfg.ServerPort,
	}
}

/*
StickerItem represents a static sticker asset returned by the stickers listing endpoint.
*/
type StickerItem struct {
	Name    string `json:"name"`
	Pack    string `json:"pack"`
	URL     string `json:"url"`
	FullURL string `json:"full_url,omitempty"`
}

// imageExtensions defines supported image formats
var imageExtensions = map[string]bool{
	".jpg":  true,
	".jpeg": true,
	".png":  true,
	".gif":  true,
	".webp": true,
}

// audioExtensions defines supported audio formats
var audioExtensions = map[string]bool{
	".mp3":  true,
	".wav":  true,
	".m4a":  true,
	".aac":  true,
	".ogg":  true,
	".weba": true,
}

// UploadFile handles multipart/form-data media file uploads.
// It categorizes images into ./uploads/images/ and audio into ./uploads/audio/
// with unique names: timestamp + random/UUID + extension.
// Invalid file types or oversized files return 400 Bad Request.
//
// @Summary Upload media file (Image or Audio)
// @Description Upload image (to ./uploads/images/) or audio (to ./uploads/audio/) up to 20MB. Returns the stored relative and full file URLs.
// @Tags Media
// @Security BearerAuth
// @Accept multipart/form-data
// @Produce json
// @Param file formData file true "Media file to upload (image or audio)"
// @Success 201 {object} map[string]interface{} "File uploaded successfully"
// @Failure 400 {object} map[string]string "Bad request, file too large, or unsupported type"
// @Failure 401 {object} map[string]string "Unauthorized"
// @Failure 500 {object} map[string]string "Internal server error"
// @Router /api/upload [post]
func (h *MediaHandler) UploadFile(w http.ResponseWriter, r *http.Request) {
	/*
		Step 1: Extract Authenticated User ID
	*/
	_, ok := getUserIDFromContext(r)
	if !ok {
		writeJSON(w, http.StatusUnauthorized, map[string]string{
			"error": "Unauthorized: invalid or missing authentication credentials.",
		})
		return
	}

	/*
		Step 2: Restrict Request Body Size to Prevent Memory Exhaustion
	*/
	r.Body = http.MaxBytesReader(w, r.Body, h.MaxFileSizeBytes)
	if err := r.ParseMultipartForm(h.MaxFileSizeBytes); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{
			"error": fmt.Sprintf("File size exceeds maximum allowed limit (%d MB).", h.MaxFileSizeBytes/(1024*1024)),
		})
		return
	}

	/*
		Step 3: Retrieve Uploaded File from Form ('file' or fallback 'media')
	*/
	file, header, err := r.FormFile("file")
	if err != nil {
		file, header, err = r.FormFile("media")
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{
				"error": "No file uploaded. Please provide a file using 'file' form key.",
			})
			return
		}
	}
	defer file.Close()

	// Verify individual file size within limit
	if header.Size > h.MaxFileSizeBytes {
		writeJSON(w, http.StatusBadRequest, map[string]string{
			"error": fmt.Sprintf("File size (%d bytes) exceeds maximum allowed limit (%d MB).", header.Size, h.MaxFileSizeBytes/(1024*1024)),
		})
		return
	}

	/*
		Step 4: Check Extension & Content-Type for Image vs Audio Segregation
	*/
	ext := strings.ToLower(filepath.Ext(header.Filename))
	contentType := strings.ToLower(header.Header.Get("Content-Type"))

	var subDir string
	if imageExtensions[ext] || strings.HasPrefix(contentType, "image/") {
		subDir = "images"
		if ext == "" {
			ext = ".jpg"
		}
	} else if audioExtensions[ext] || strings.HasPrefix(contentType, "audio/") {
		subDir = "audio"
		if ext == "" {
			ext = ".mp3"
		}
	} else {
		// Invalid / unsupported file type
		writeJSON(w, http.StatusBadRequest, map[string]string{
			"error": fmt.Sprintf("Invalid file type '%s'. Only image (jpg, jpeg, png, gif, webp) and audio (mp3, wav, m4a, aac, ogg) are supported.", ext),
		})
		return
	}

	/*
		Step 5: Generate Unique Filename (timestamp + random/UUID + extension)
	*/
	randBytes := make([]byte, 8)
	if _, err := rand.Read(randBytes); err != nil {
		randBytes = []byte(fmt.Sprintf("%08d", time.Now().Nanosecond()))
	}
	randomHex := hex.EncodeToString(randBytes)
	filename := fmt.Sprintf("%d_%s%s", time.Now().Unix(), randomHex, ext)

	/*
		Step 6: Ensure Destination Directory and Save File
	*/
	targetDir := filepath.Join(h.UploadDir, subDir)
	if err := os.MkdirAll(targetDir, os.ModePerm); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{
			"error": "Failed to create destination storage directory.",
		})
		return
	}

	destPath := filepath.Join(targetDir, filename)
	dst, err := os.Create(destPath)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{
			"error": "Failed to create file on storage.",
		})
		return
	}
	defer dst.Close()

	if _, err := io.Copy(dst, file); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{
			"error": "Failed to write file contents to disk.",
		})
		return
	}

	/*
		Step 7: Construct Accessible URLs and Response Payload
	*/
	relativeURL := fmt.Sprintf("/uploads/%s/%s", subDir, filename)
	fullURL := fmt.Sprintf("http://%s:%s%s", h.HostIP, h.ServerPort, relativeURL)

	mediaType := "image"
	if subDir == "audio" {
		mediaType = "audio"
	}

	writeJSON(w, http.StatusCreated, map[string]any{
		"url":          relativeURL,
		"type":         mediaType,
		"filename":     filename,
		"full_url":     fullURL,
		"size_bytes":   header.Size,
		"content_type": header.Header.Get("Content-Type"),
		"message":      "File uploaded successfully.",
	})
}

// GetStickers scans the static stickers directory and returns all available stickers grouped by pack.
//
// @Summary List available stickers
// @Description Scan ./static/stickers and return sticker assets with name, pack name, and access URL.
// @Tags Media
// @Produce json
// @Success 200 {object} map[string]interface{} "List of available stickers"
// @Router /api/stickers [get]
func (h *MediaHandler) GetStickers(w http.ResponseWriter, r *http.Request) {
	stickers := make([]StickerItem, 0)

	// Read entries in stickers root folder
	entries, err := os.ReadDir(h.StickersDir)
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{
			"stickers": stickers,
			"count":    0,
		})
		return
	}

	for _, entry := range entries {
		if entry.IsDir() {
			packName := entry.Name()
			packPath := filepath.Join(h.StickersDir, packName)

			files, err := os.ReadDir(packPath)
			if err != nil {
				continue
			}

			for _, file := range files {
				if file.IsDir() {
					continue
				}

				ext := strings.ToLower(filepath.Ext(file.Name()))
				if ext == ".png" || ext == ".webp" || ext == ".jpg" || ext == ".jpeg" {
					name := strings.TrimSuffix(file.Name(), filepath.Ext(file.Name()))
					relURL := fmt.Sprintf("/static/stickers/%s/%s", packName, file.Name())
					fullURL := fmt.Sprintf("http://%s:%s%s", h.HostIP, h.ServerPort, relURL)

					stickers = append(stickers, StickerItem{
						Name:    name,
						Pack:    packName,
						URL:     relURL,
						FullURL: fullURL,
					})
				}
			}
		} else {
			// Sticker directly in stickers root directory
			ext := strings.ToLower(filepath.Ext(entry.Name()))
			if ext == ".png" || ext == ".webp" || ext == ".jpg" || ext == ".jpeg" {
				name := strings.TrimSuffix(entry.Name(), filepath.Ext(entry.Name()))
				relURL := fmt.Sprintf("/static/stickers/%s", entry.Name())
				fullURL := fmt.Sprintf("http://%s:%s%s", h.HostIP, h.ServerPort, relURL)

				stickers = append(stickers, StickerItem{
					Name:    name,
					Pack:    "default",
					URL:     relURL,
					FullURL: fullURL,
				})
			}
		}
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"stickers": stickers,
		"count":    len(stickers),
	})
}
