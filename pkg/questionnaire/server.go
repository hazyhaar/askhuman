package questionnaire

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"sync"
	"time"
)

// RenderHTML renders the interactive HTML5 page into w.
func RenderHTML(ctx context.Context, spec QuestionnaireSpec, submitEndpoint string, w io.Writer) error {
	if err := ValidateSpec(spec); err != nil {
		return err
	}
	return RenderPage(spec, submitEndpoint).Render(ctx, w)
}

// ServerOptions configures the local askhuman HTTP handler.
type ServerOptions struct {
	Spec        QuestionnaireSpec
	OutJSONPath string
	OutMDPath   string
	OnSubmitted func(sub QuestionnaireSubmission)
}

// NewHandler builds an http.Handler serving GET / (interactive templ page)
// and POST /submit (validation + atomic JSON/Markdown persistence).
func NewHandler(opts ServerOptions) (http.Handler, error) {
	if err := ValidateSpec(opts.Spec); err != nil {
		return nil, err
	}
	var mu sync.Mutex
	mux := http.NewServeMux()

	mux.HandleFunc("GET /", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_ = RenderHTML(r.Context(), opts.Spec, "/submit", w)
	})

	mux.HandleFunc("OPTIONS /submit", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "POST, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
		w.WriteHeader(http.StatusNoContent)
	})

	mux.HandleFunc("POST /submit", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		r.Body = http.MaxBytesReader(w, r.Body, MaxSubmissionBytes)
		defer r.Body.Close()

		sub, err := ParseSubmission(r.Body)
		if err != nil {
			w.WriteHeader(http.StatusBadRequest)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
			return
		}
		if sub.SubmittedAt == "" {
			sub.SubmittedAt = time.Now().UTC().Format(time.RFC3339)
		}
		if err := ValidateSubmission(opts.Spec, sub); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
			return
		}

		mu.Lock()
		defer mu.Unlock()

		if opts.OutJSONPath != "" {
			rawJSON, mErr := json.MarshalIndent(sub, "", "  ")
			if mErr != nil {
				w.WriteHeader(http.StatusInternalServerError)
				_ = json.NewEncoder(w).Encode(map[string]string{"error": mErr.Error()})
				return
			}
			rawJSON = append(rawJSON, '\n')
			if wErr := WriteAtomicFile(opts.OutJSONPath, rawJSON, 0o600); wErr != nil {
				w.WriteHeader(http.StatusInternalServerError)
				_ = json.NewEncoder(w).Encode(map[string]string{"error": fmt.Sprintf("write JSON: %v", wErr)})
				return
			}
		}

		if opts.OutMDPath != "" {
			absMD, _ := filepath.Abs(opts.OutMDPath)
			md := FormatMarkdownReport(opts.Spec, sub, absMD)
			if wErr := WriteAtomicFile(opts.OutMDPath, []byte(md), 0o644); wErr != nil {
				w.WriteHeader(http.StatusInternalServerError)
				_ = json.NewEncoder(w).Encode(map[string]string{"error": fmt.Sprintf("write Markdown: %v", wErr)})
				return
			}
		}

		if opts.OnSubmitted != nil {
			opts.OnSubmitted(sub)
		}

		_ = json.NewEncoder(w).Encode(map[string]string{
			"status":        "ok",
			"json_path":     opts.OutJSONPath,
			"markdown_path": opts.OutMDPath,
		})
	})

	return mux, nil
}
