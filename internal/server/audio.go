package server

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"time"
)

// TranscribeAudioAPI handles POST /api/v1/audio/transcribe
// Accepts multipart audio file upload, transcribes using configured method.
func (s *Server) TranscribeAudioAPI(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseMultipartForm(25 << 20); err != nil { // 25MB max
		http.Error(w, fmt.Sprintf("failed to parse form: %v", err), http.StatusBadRequest)
		return
	}

	file, header, err := r.FormFile("file")
	if err != nil {
		http.Error(w, "file is required", http.StatusBadRequest)
		return
	}
	defer file.Close()

	// Save to temp file
	dir := fmt.Sprintf("/tmp/at-audio/%d", time.Now().UnixNano())
	if err := os.MkdirAll(dir, 0o755); err != nil {
		http.Error(w, "failed to create temp dir", http.StatusInternalServerError)
		return
	}

	ext := filepath.Ext(header.Filename)
	if ext == "" {
		ext = ".webm" // browser default
	}
	tempPath := filepath.Join(dir, "audio"+ext)

	out, err := os.Create(tempPath)
	if err != nil {
		http.Error(w, "failed to create temp file", http.StatusInternalServerError)
		return
	}
	if _, err := io.Copy(out, file); err != nil {
		out.Close()
		http.Error(w, "failed to save file", http.StatusInternalServerError)
		return
	}
	out.Close()

	// Get transcription method: query param > variable > default "openai"
	method := r.URL.Query().Get("method")
	model := r.URL.Query().Get("model")

	// Check system variables for defaults
	if method == "" && s.variableStore != nil {
		if v, err := s.variableStore.GetVariableByKey(r.Context(), "speech_to_text"); err == nil && v != nil && v.Value != "" {
			method = v.Value
		}
	}
	if model == "" && s.variableStore != nil {
		if v, err := s.variableStore.GetVariableByKey(r.Context(), "whisper_model"); err == nil && v != nil && v.Value != "" {
			model = v.Value
		}
	}
	if method == "" {
		method = "openai"
	}
	if model == "" {
		model = "base"
	}

	// Transcribe
	text := s.transcribeAudioWithConfig(r.Context(), tempPath, method, model)

	// Clean up temp file
	defer os.RemoveAll(dir)

	if text == "" {
		http.Error(w, "transcription failed", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{
		"text": text,
	})
}
