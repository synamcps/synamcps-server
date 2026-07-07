package httpapi

import (
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"net"
	"net/http"
	"net/url"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/synamcps/synamcps-server/internal/knowledge"
	"github.com/synamcps/synamcps-server/internal/models"
)

// isDisallowedIP reports whether an IP must not be reached by server-side
// fetches (SSRF guard): loopback, link-local (incl. cloud metadata
// 169.254.169.254), multicast, unspecified and private/ULA ranges.
func isDisallowedIP(ip net.IP) bool {
	if ip == nil {
		return true
	}
	return ip.IsLoopback() ||
		ip.IsLinkLocalUnicast() ||
		ip.IsLinkLocalMulticast() ||
		ip.IsMulticast() ||
		ip.IsUnspecified() ||
		ip.IsPrivate()
}

// ssrfSafeClient returns an HTTP client whose dialer refuses connections to
// internal addresses. The Control hook runs after DNS resolution with the
// concrete IP, so it also covers redirects and DNS-rebinding.
func ssrfSafeClient(timeout time.Duration) *http.Client {
	dialer := &net.Dialer{
		Timeout: timeout,
		Control: func(_, address string, _ syscall.RawConn) error {
			host, _, err := net.SplitHostPort(address)
			if err != nil {
				return err
			}
			ip := net.ParseIP(host)
			if ip == nil {
				return fmt.Errorf("cannot resolve address %q", host)
			}
			if isDisallowedIP(ip) {
				return fmt.Errorf("address %s is not allowed", ip)
			}
			return nil
		},
	}
	return &http.Client{
		Timeout:   timeout,
		Transport: &http.Transport{DialContext: dialer.DialContext},
	}
}

type IngestHandler struct {
	service *knowledge.Service
}

func NewIngestHandler(service *knowledge.Service) *IngestHandler {
	return &IngestHandler{service: service}
}

// POST /api/knowledge/ingest/file (multipart/form-data)
// Fields: storageId, title, visibility, source, sourceUrl; file=<upload>
func (h *IngestHandler) IngestFile(w http.ResponseWriter, r *http.Request) {
	p, ok := principalFromRequest(r)
	if !ok {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	if err := r.ParseMultipartForm(32 << 20); err != nil {
		http.Error(w, "invalid multipart form", http.StatusBadRequest)
		return
	}

	storageID := strings.TrimSpace(r.FormValue("storageId"))
	title := strings.TrimSpace(r.FormValue("title"))
	visibility := models.Visibility(strings.TrimSpace(r.FormValue("visibility")))
	source := strings.TrimSpace(r.FormValue("source"))
	sourceURL := strings.TrimSpace(r.FormValue("sourceUrl"))
	if sourceURL != "" {
		if _, err := url.ParseRequestURI(sourceURL); err != nil {
			http.Error(w, "invalid sourceUrl", http.StatusUnprocessableEntity)
			return
		}
	}

	f, fh, err := r.FormFile("file")
	if err != nil {
		http.Error(w, "missing file", http.StatusBadRequest)
		return
	}
	defer f.Close()
	payload, err := io.ReadAll(f)
	if err != nil {
		http.Error(w, "failed to read file", http.StatusBadRequest)
		return
	}

	filename := "upload.bin"
	if fh != nil && fh.Filename != "" {
		filename = fh.Filename
	}
	mimeType := strings.TrimSpace(r.FormValue("mimeType"))
	if mimeType == "" {
		mimeType = mime.TypeByExtension(strings.ToLower(filepath.Ext(filename)))
	}
	if mimeType == "" {
		mimeType = "application/octet-stream"
	}
	if title == "" {
		title = filename
	}

	doc, err := h.service.IngestBinary(r.Context(), p, accessContextFromRequest(r), knowledge.BinaryInput{
		StorageID:  storageID,
		Title:      title,
		Filename:   filename,
		Payload:    payload,
		MimeType:   mimeType,
		Visibility: visibility,
		GroupIDs:   []string{},
		Source:     source,
		SourceURL:  sourceURL,
		Channel:    "api",
	})
	if err != nil {
		http.Error(w, err.Error(), statusFromErr(err))
		return
	}
	writeJSON(w, doc, http.StatusCreated)
}

type ingestLinkRequest struct {
	StorageID  string            `json:"storageId"`
	Title      string            `json:"title"`
	URL        string            `json:"url"`
	Visibility models.Visibility `json:"visibility"`
	Source     string            `json:"source"`
}

// POST /api/knowledge/ingest/link (json)
func (h *IngestHandler) IngestLink(w http.ResponseWriter, r *http.Request) {
	p, ok := principalFromRequest(r)
	if !ok {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	var req ingestLinkRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid json", http.StatusBadRequest)
		return
	}
	req.URL = strings.TrimSpace(req.URL)
	if req.URL == "" {
		http.Error(w, "url is empty", http.StatusUnprocessableEntity)
		return
	}
	u, err := url.ParseRequestURI(req.URL)
	if err != nil {
		http.Error(w, "invalid url", http.StatusUnprocessableEntity)
		return
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		http.Error(w, "unsupported url scheme", http.StatusUnprocessableEntity)
		return
	}

	client := ssrfSafeClient(30 * time.Second)
	resp, err := client.Get(u.String())
	if err != nil {
		http.Error(w, "failed to download", http.StatusBadRequest)
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		http.Error(w, "download failed: "+resp.Status, http.StatusBadRequest)
		return
	}
	payload, err := io.ReadAll(io.LimitReader(resp.Body, 32<<20))
	if err != nil {
		http.Error(w, "failed to read body", http.StatusBadRequest)
		return
	}

	filename := "download.bin"
	if u.Path != "" {
		base := filepath.Base(u.Path)
		if base != "." && base != "/" && base != "" {
			filename = base
		}
	}
	mimeType := resp.Header.Get("Content-Type")
	if mimeType != "" {
		if mt, _, err := mime.ParseMediaType(mimeType); err == nil {
			mimeType = mt
		}
	}
	if mimeType == "" {
		mimeType = mime.TypeByExtension(strings.ToLower(filepath.Ext(filename)))
	}
	if mimeType == "" {
		mimeType = "application/octet-stream"
	}
	title := strings.TrimSpace(req.Title)
	if title == "" {
		title = filename
	}

	source := strings.TrimSpace(req.Source)
	if source == "" {
		source = "link"
	}
	doc, err := h.service.IngestBinary(r.Context(), p, accessContextFromRequest(r), knowledge.BinaryInput{
		StorageID:  strings.TrimSpace(req.StorageID),
		Title:      title,
		Filename:   filename,
		Payload:    payload,
		MimeType:   mimeType,
		Visibility: req.Visibility,
		GroupIDs:   []string{},
		Source:     source,
		SourceURL:  u.String(),
		Channel:    "api",
	})
	if err != nil {
		http.Error(w, err.Error(), statusFromErr(err))
		return
	}
	writeJSON(w, doc, http.StatusCreated)
}

