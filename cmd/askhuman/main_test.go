package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hazyhaar/askhuman/pkg/questionnaire"
)

func TestCLIRenderAndFormatMD(t *testing.T) {
	tmp := t.TempDir()
	specPath := filepath.Join(tmp, "spec.json")
	htmlPath := filepath.Join(tmp, "askhuman.html")
	ansPath := filepath.Join(tmp, "answers.json")
	mdPath := filepath.Join(tmp, "decisions.md")

	spec := questionnaire.QuestionnaireSpec{
		Project:    "acme-service",
		WorkflowID: "wf-requirements",
		WorkstepID: "ws-1.2-questions",
		Title:      "Requirements Questionnaire",
		Questions: []questionnaire.QuestionItem{
			{
				ID:       "Q-01",
				Axis:     "Scope",
				Kind:     questionnaire.KindQCMWithFreeText,
				Title:    "Architecture choice",
				Required: true,
				Options: []questionnaire.ChoiceOption{
					{ID: "A", Label: "Option A", Recommended: true},
					{ID: "B", Label: "Option B"},
				},
			},
		},
	}
	specBytes, _ := json.MarshalIndent(spec, "", "  ")
	if err := os.WriteFile(specPath, specBytes, 0o644); err != nil {
		t.Fatal(err)
	}

	var out, errBuf bytes.Buffer
	code := Run([]string{"validate-spec", "-in", specPath}, &out, &errBuf)
	if code != 0 {
		t.Fatalf("validate-spec code=%d err=%s", code, errBuf.String())
	}

	out.Reset()
	errBuf.Reset()
	code = Run([]string{"render", "-in", specPath, "-out", htmlPath}, &out, &errBuf)
	if code != 0 {
		t.Fatalf("render code=%d err=%s", code, errBuf.String())
	}
	htmlContent, err := os.ReadFile(htmlPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(htmlContent), "Requirements Questionnaire") {
		t.Fatalf("unexpected HTML: %s", string(htmlContent))
	}

	sub := questionnaire.QuestionnaireSubmission{
		Project:    "acme-service",
		WorkflowID: "wf-requirements",
		WorkstepID: "ws-1.4-interview",
		Answers: []questionnaire.QuestionAnswer{
			{QuestionID: "Q-01", SelectedOptions: []string{"A"}, FreeText: "With strict validation."},
		},
	}
	subBytes, _ := json.MarshalIndent(sub, "", "  ")
	if err := os.WriteFile(ansPath, subBytes, 0o644); err != nil {
		t.Fatal(err)
	}

	out.Reset()
	errBuf.Reset()
	code = Run([]string{"format-md", "-spec", specPath, "-answers", ansPath, "-out", mdPath}, &out, &errBuf)
	if code != 0 {
		t.Fatalf("format-md code=%d err=%s", code, errBuf.String())
	}
	mdContent, err := os.ReadFile(mdPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(mdContent), "Option A") {
		t.Fatalf("unexpected Markdown: %s", string(mdContent))
	}
}
