package questionnaire_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hazyhaar/askhuman/pkg/questionnaire"
)

func sampleSpec() questionnaire.QuestionnaireSpec {
	return questionnaire.QuestionnaireSpec{
		Project:        "acme-service",
		WorkflowID:     "wf-requirements",
		WorkstepID:     "ws-1.2-questions",
		Title:          "Requirement Elicitation — acme-service",
		Subtitle:       "Structured design arbitration",
		ContextSummary: "Core service architecture and validation gates.",
		Prerequisites:  []string{"Go 1.24+", "SQLite WAL"},
		Sections: []questionnaire.QuestionSection{
			{ID: "S1", Title: "Section 1 — Core Architecture"},
		},
		Questions: []questionnaire.QuestionItem{
			{
				ID:        "Q-01",
				SectionID: "S1",
				Axis:      "Architecture",
				Kind:      questionnaire.KindQCMWithFreeText,
				Title:     "Model family diversity policy",
				Context:   "Separation between synthesis and review stages.",
				Required:  true,
				Options: []questionnaire.ChoiceOption{
					{
						ID:          "OPT-A",
						Label:       "Three distinct model families required",
						Rationale:   "Prevents single-family bias.",
						Recommended: true,
					},
					{
						ID:        "OPT-B",
						Label:     "Two distinct model families",
						Rationale: "More resilient during provider outages.",
					},
				},
			},
			{
				ID:        "Q-02",
				SectionID: "S1",
				Axis:      "Validation Gates",
				Kind:      questionnaire.KindBooleanChecklist,
				Title:     "Blocking mechanical checks",
				Required:  true,
				Options: []questionnaire.ChoiceOption{
					{ID: "CHK-1", Label: "Reject any unverified reference", Recommended: true, DefaultChecked: true},
					{ID: "CHK-2", Label: "Reject any truncated excerpt", Recommended: true, DefaultChecked: true},
				},
			},
			{
				ID:        "Q-03",
				SectionID: "S1",
				Axis:      "Scope",
				Kind:      questionnaire.KindFreeText,
				Title:     "Additional domain constraints",
				Required:  true,
			},
		},
	}
}

func TestParseAndValidateSpec(t *testing.T) {
	spec := sampleSpec()
	raw, err := json.Marshal(spec)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	parsed, err := questionnaire.ParseSpec(bytes.NewReader(raw))
	if err != nil {
		t.Fatalf("ParseSpec failed: %v", err)
	}
	if parsed.Project != "acme-service" || len(parsed.Questions) != 3 {
		t.Fatalf("unexpected result: %+v", parsed)
	}
}

func TestRenderHTMLTempl(t *testing.T) {
	spec := sampleSpec()
	var buf bytes.Buffer
	if err := questionnaire.RenderHTML(context.Background(), spec, "/submit", &buf); err != nil {
		t.Fatalf("RenderHTML: %v", err)
	}
	html := buf.String()
	for _, needle := range []string{
		"<!doctype html>",
		"Requirement Elicitation — acme-service",
		"Q-01",
		"Q-02",
		"Q-03",
		"Three distinct model families required",
		"accent-color: var(--brand-color)",
		"data-recommended=\"true\"",
		"ask-btn-clear-card",
		"Clear selection",
		"inp.dataset.wasChecked === \"true\"",
	} {
		if !strings.Contains(html, needle) {
			t.Errorf("generated HTML missing %q", needle)
		}
	}
}

func TestExplicitNoneDeselection(t *testing.T) {
	spec := sampleSpec()
	sub := questionnaire.QuestionnaireSubmission{
		Project:    "acme-service",
		WorkflowID: "wf-requirements",
		WorkstepID: "ws-1.4-interview",
		Answers: []questionnaire.QuestionAnswer{
			{QuestionID: "Q-01", ExplicitNone: true},
			{QuestionID: "Q-02", BooleanChecks: map[string]bool{"CHK-1": false, "CHK-2": false}},
			{QuestionID: "Q-03", FreeText: "No additional constraints."},
		},
	}
	if err := questionnaire.ValidateSubmission(spec, sub); err != nil {
		t.Fatalf("ValidateSubmission with ExplicitNone=true failed: %v", err)
	}
	md := questionnaire.FormatMarkdownReport(spec, sub, "")
	if !strings.Contains(md, "*(none — explicit none)*") {
		t.Fatalf("expected *(none — explicit none)* in Markdown:\n%s", md)
	}
}

func TestValidateAndServerSubmit(t *testing.T) {
	spec := sampleSpec()
	tmp := t.TempDir()
	outJSON := filepath.Join(tmp, "answers.json")
	outMD := filepath.Join(tmp, "decisions.md")

	var notified bool
	h, err := questionnaire.NewHandler(questionnaire.ServerOptions{
		Spec:        spec,
		OutJSONPath: outJSON,
		OutMDPath:   outMD,
		OnSubmitted: func(_ questionnaire.QuestionnaireSubmission) {
			notified = true
		},
	})
	if err != nil {
		t.Fatalf("NewHandler: %v", err)
	}

	sub := questionnaire.QuestionnaireSubmission{
		Project:     "acme-service",
		WorkflowID:  "wf-requirements",
		WorkstepID:  "ws-1.4-interview",
		GeneralNote: "All good.",
		Answers: []questionnaire.QuestionAnswer{
			{QuestionID: "Q-01", SelectedOptions: []string{"OPT-A"}, FreeText: "Strict validation."},
			{QuestionID: "Q-02", BooleanChecks: map[string]bool{"CHK-1": true, "CHK-2": true}},
			{QuestionID: "Q-03", FreeText: "Zero external runtime dependencies."},
		},
	}
	body, err := json.Marshal(sub)
	if err != nil {
		t.Fatalf("marshal sub: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/submit", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected HTTP 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if !notified {
		t.Fatal("OnSubmitted was not called")
	}

	mdBytes, err := os.ReadFile(outMD)
	if err != nil {
		t.Fatalf("read outMD: %v", err)
	}
	md := string(mdBytes)
	if !strings.Contains(md, "**[YES]** `CHK-1`") || !strings.Contains(md, "`OPT-A`") {
		t.Fatalf("incomplete Markdown content:\n%s", md)
	}
}

func TestHostileRejections(t *testing.T) {
	// Unknown JSON field
	_, err := questionnaire.ParseSpec(strings.NewReader(`{"project":"x","title":"t","unknown_field":1,"questions":[]}`))
	if err == nil {
		t.Fatal("expected error on unknown JSON field")
	}

	// Single-choice QCM with multiple options selected
	spec := sampleSpec()
	sub := questionnaire.QuestionnaireSubmission{
		Project: "acme-service",
		Answers: []questionnaire.QuestionAnswer{
			{QuestionID: "Q-01", SelectedOptions: []string{"OPT-A", "OPT-B"}},
			{QuestionID: "Q-02", BooleanChecks: map[string]bool{"CHK-1": true}},
			{QuestionID: "Q-03", FreeText: "ok"},
		},
	}
	if err := questionnaire.ValidateSubmission(spec, sub); err == nil {
		t.Fatal("expected error when multiple options selected on single-choice QCM")
	}

	// Missing required question
	subMissing := questionnaire.QuestionnaireSubmission{
		Project: "acme-service",
		Answers: []questionnaire.QuestionAnswer{
			{QuestionID: "Q-01", SelectedOptions: []string{"OPT-A"}},
		},
	}
	if err := questionnaire.ValidateSubmission(spec, subMissing); err == nil {
		t.Fatal("expected error when required questions are missing")
	}
}
