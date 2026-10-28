// Package questionnaire defines the declarative model for interactive
// questionnaires (free text, QCM + free text, boolean checklists),
// HTML5 rendering via templ, strict submission validation, and atomic
// serialization to JSON and Markdown.
package questionnaire

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"
)

// MaxSpecBytes bounds the maximum size of a JSON specification file (2 MiB).
const MaxSpecBytes = 2 * 1024 * 1024

// MaxSubmissionBytes bounds the maximum size of a JSON answers submission (1 MiB).
const MaxSubmissionBytes = 1 * 1024 * 1024

// QuestionKind distinguishes the three supported question types.
type QuestionKind string

const (
	// KindFreeText represents an open-ended question (textarea).
	KindFreeText QuestionKind = "free_text"
	// KindQCMWithFreeText represents a single or multiple choice question paired with a free-text amendment field.
	KindQCMWithFreeText QuestionKind = "qcm_free_text"
	// KindBooleanChecklist represents a list of boolean assertions (Yes/No) with an optional free-text field.
	KindBooleanChecklist QuestionKind = "boolean_checklist"
)

// ChoiceOption describes a choice option in a QCM or boolean checklist.
type ChoiceOption struct {
	ID             string `json:"id"`
	Label          string `json:"label"`
	Rationale      string `json:"rationale,omitempty"`
	Recommended    bool   `json:"recommended,omitempty"`
	DefaultChecked bool   `json:"default_checked,omitempty"`
}

// QuestionSection visually groups several questions around a theme.
type QuestionSection struct {
	ID          string `json:"id"`
	Title       string `json:"title"`
	Description string `json:"description,omitempty"`
}

// QuestionItem defines an atomic question presented to the user.
type QuestionItem struct {
	ID                  string         `json:"id"`
	SectionID           string         `json:"section_id,omitempty"`
	Axis                string         `json:"axis"`
	Kind                QuestionKind   `json:"kind"`
	Title               string         `json:"title"`
	Context             string         `json:"context,omitempty"`
	Required            bool           `json:"required"`
	MultiSelect         bool           `json:"multi_select,omitempty"`
	Options             []ChoiceOption `json:"options,omitempty"`
	FreeTextLabel       string         `json:"free_text_label,omitempty"`
	FreeTextPlaceholder string         `json:"free_text_placeholder,omitempty"`
	MaxFreeTextBytes    int            `json:"max_free_text_bytes,omitempty"`
}

// QuestionnaireSpec describes an entire requirement or arbitration questionnaire.
type QuestionnaireSpec struct {
	Project        string            `json:"project"`
	WorkflowID     string            `json:"workflow_id"`
	WorkstepID     string            `json:"workstep_id"`
	Title          string            `json:"title"`
	Subtitle       string            `json:"subtitle,omitempty"`
	ContextSummary string            `json:"context_summary,omitempty"`
	Prerequisites  []string          `json:"prerequisites,omitempty"`
	Sections       []QuestionSection `json:"sections,omitempty"`
	Questions      []QuestionItem    `json:"questions"`
}

// QuestionAnswer holds the user's answer to a specific question.
type QuestionAnswer struct {
	QuestionID      string          `json:"question_id"`
	SelectedOptions []string        `json:"selected_options,omitempty"`
	BooleanChecks   map[string]bool `json:"boolean_checks,omitempty"`
	ExplicitNone    bool            `json:"explicit_none,omitempty"`
	FreeText        string          `json:"free_text,omitempty"`
}

// QuestionnaireSubmission groups all answers submitted by the user.
type QuestionnaireSubmission struct {
	Project     string           `json:"project"`
	WorkflowID  string           `json:"workflow_id"`
	WorkstepID  string           `json:"workstep_id"`
	SubmittedAt string           `json:"submitted_at,omitempty"`
	GeneralNote string           `json:"general_note,omitempty"`
	Answers     []QuestionAnswer `json:"answers"`
}

// LoadSpecFile reads and validates a questionnaire specification file bounded to MaxSpecBytes.
func LoadSpecFile(path string) (QuestionnaireSpec, error) {
	f, err := os.Open(path)
	if err != nil {
		return QuestionnaireSpec{}, fmt.Errorf("questionnaire: open %s: %w", path, err)
	}
	defer f.Close()
	return ParseSpec(f)
}

// ParseSpec decodes and validates a QuestionnaireSpec from r with DisallowUnknownFields.
func ParseSpec(r io.Reader) (QuestionnaireSpec, error) {
	limited := io.LimitReader(r, MaxSpecBytes+1)
	raw, err := io.ReadAll(limited)
	if err != nil {
		return QuestionnaireSpec{}, fmt.Errorf("questionnaire: read spec: %w", err)
	}
	if len(raw) > MaxSpecBytes {
		return QuestionnaireSpec{}, fmt.Errorf("questionnaire: spec exceeds limit of %d bytes", MaxSpecBytes)
	}
	var spec QuestionnaireSpec
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&spec); err != nil {
		return QuestionnaireSpec{}, fmt.Errorf("questionnaire: invalid JSON: %w", err)
	}
	if err := ValidateSpec(spec); err != nil {
		return QuestionnaireSpec{}, err
	}
	return spec, nil
}

// ValidateSpec checks the structural consistency of a QuestionnaireSpec.
func ValidateSpec(spec QuestionnaireSpec) error {
	if strings.TrimSpace(spec.Project) == "" {
		return errors.New("questionnaire: empty project field")
	}
	if strings.TrimSpace(spec.Title) == "" {
		return errors.New("questionnaire: empty title field")
	}
	if len(spec.Questions) == 0 {
		return errors.New("questionnaire: no questions declared")
	}

	sectionIDs := make(map[string]bool, len(spec.Sections))
	for _, sec := range spec.Sections {
		id := strings.TrimSpace(sec.ID)
		if id == "" {
			return errors.New("questionnaire: section with empty id")
		}
		if sectionIDs[id] {
			return fmt.Errorf("questionnaire: duplicate section %q", id)
		}
		sectionIDs[id] = true
	}

	seenQ := make(map[string]bool, len(spec.Questions))
	for _, q := range spec.Questions {
		qid := strings.TrimSpace(q.ID)
		if qid == "" {
			return errors.New("questionnaire: question with empty id")
		}
		if seenQ[qid] {
			return fmt.Errorf("questionnaire: duplicate question %q", qid)
		}
		seenQ[qid] = true

		if q.SectionID != "" && len(sectionIDs) > 0 && !sectionIDs[q.SectionID] {
			return fmt.Errorf("questionnaire: question %s references unknown section %q", qid, q.SectionID)
		}
		if strings.TrimSpace(q.Title) == "" {
			return fmt.Errorf("questionnaire: question %s has empty title", qid)
		}
		if strings.TrimSpace(q.Axis) == "" {
			return fmt.Errorf("questionnaire: question %s has empty axis", qid)
		}

		switch q.Kind {
		case KindFreeText:
			// Options not required for free_text
		case KindQCMWithFreeText, KindBooleanChecklist:
			if len(q.Options) == 0 {
				return fmt.Errorf("questionnaire: question %s (%s) requires at least one option", qid, q.Kind)
			}
			if q.Kind == KindQCMWithFreeText && len(q.Options) < 2 {
				return fmt.Errorf("questionnaire: QCM question %s requires at least 2 options (got %d)", qid, len(q.Options))
			}
			seenOpt := make(map[string]bool, len(q.Options))
			for _, opt := range q.Options {
				oid := strings.TrimSpace(opt.ID)
				if oid == "" {
					return fmt.Errorf("questionnaire: question %s has an option with empty id", qid)
				}
				if seenOpt[oid] {
					return fmt.Errorf("questionnaire: question %s has duplicate option %q", qid, oid)
				}
				if strings.TrimSpace(opt.Label) == "" {
					return fmt.Errorf("questionnaire: question %s option %s has empty label", qid, oid)
				}
				seenOpt[oid] = true
			}
		default:
			return fmt.Errorf("questionnaire: question %s has unknown kind %q", qid, q.Kind)
		}
	}
	return nil
}

// ParseSubmission decodes a JSON submission bounded to MaxSubmissionBytes.
func ParseSubmission(r io.Reader) (QuestionnaireSubmission, error) {
	limited := io.LimitReader(r, MaxSubmissionBytes+1)
	raw, err := io.ReadAll(limited)
	if err != nil {
		return QuestionnaireSubmission{}, fmt.Errorf("questionnaire: read submission: %w", err)
	}
	if len(raw) > MaxSubmissionBytes {
		return QuestionnaireSubmission{}, fmt.Errorf("questionnaire: submission exceeds limit of %d bytes", MaxSubmissionBytes)
	}
	var sub QuestionnaireSubmission
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&sub); err != nil {
		return QuestionnaireSubmission{}, fmt.Errorf("questionnaire: invalid submission JSON: %w", err)
	}
	return sub, nil
}

// ValidateSubmission checks that a submission satisfies all constraints of the QuestionnaireSpec.
func ValidateSubmission(spec QuestionnaireSpec, sub QuestionnaireSubmission) error {
	qByID := make(map[string]QuestionItem, len(spec.Questions))
	for _, q := range spec.Questions {
		qByID[q.ID] = q
	}

	ansByID := make(map[string]QuestionAnswer, len(sub.Answers))
	for _, ans := range sub.Answers {
		q, ok := qByID[ans.QuestionID]
		if !ok {
			return fmt.Errorf("questionnaire: answer for unknown question %q", ans.QuestionID)
		}
		if _, dup := ansByID[ans.QuestionID]; dup {
			return fmt.Errorf("questionnaire: duplicate answer for question %q", ans.QuestionID)
		}

		maxBytes := q.MaxFreeTextBytes
		if maxBytes <= 0 {
			maxBytes = 32768
		}
		if len(ans.FreeText) > maxBytes {
			return fmt.Errorf("questionnaire: free text of %s exceeds %d bytes", q.ID, maxBytes)
		}

		validOpts := make(map[string]bool, len(q.Options))
		for _, opt := range q.Options {
			validOpts[opt.ID] = true
		}

		switch q.Kind {
		case KindFreeText:
			if q.Required && strings.TrimSpace(ans.FreeText) == "" {
				return fmt.Errorf("questionnaire: required free-text question %s is empty", q.ID)
			}
		case KindQCMWithFreeText:
			for _, sel := range ans.SelectedOptions {
				if !validOpts[sel] {
					return fmt.Errorf("questionnaire: question %s has unknown selected option %q", q.ID, sel)
				}
			}
			if !q.MultiSelect && len(ans.SelectedOptions) > 1 {
				return fmt.Errorf("questionnaire: single-choice question %s received %d selections", q.ID, len(ans.SelectedOptions))
			}
			if q.Required && len(ans.SelectedOptions) == 0 && strings.TrimSpace(ans.FreeText) == "" && !ans.ExplicitNone {
				return fmt.Errorf("questionnaire: required question %s has no selected option, no free text, and no explicit none", q.ID)
			}
		case KindBooleanChecklist:
			for k := range ans.BooleanChecks {
				if !validOpts[k] {
					return fmt.Errorf("questionnaire: question %s has unknown boolean option %q", q.ID, k)
				}
			}
			if q.Required && len(ans.BooleanChecks) == 0 && len(ans.SelectedOptions) == 0 && strings.TrimSpace(ans.FreeText) == "" {
				return fmt.Errorf("questionnaire: required checklist %s is empty", q.ID)
			}
		}

		ansByID[ans.QuestionID] = ans
	}

	for _, q := range spec.Questions {
		if !q.Required {
			continue
		}
		if _, ok := ansByID[q.ID]; !ok {
			return fmt.Errorf("questionnaire: required question %s is missing from submission", q.ID)
		}
	}
	return nil
}

// FormatMarkdownReport generates the Markdown report for a user submission.
func FormatMarkdownReport(spec QuestionnaireSpec, sub QuestionnaireSubmission, targetFilepath string) string {
	var b strings.Builder
	_ = targetFilepath

	ts := sub.SubmittedAt
	if strings.TrimSpace(ts) == "" {
		ts = time.Now().UTC().Format(time.RFC3339)
	}

	fmt.Fprintf(&b, "# User Arbitration (`%s` / `%s`) — %s\n\n", spec.WorkflowID, spec.WorkstepID, spec.Project)
	fmt.Fprintf(&b, "- **Title**: %s\n", spec.Title)
	fmt.Fprintf(&b, "- **Timestamp**: `%s`\n\n", ts)

	ansMap := make(map[string]QuestionAnswer, len(sub.Answers))
	for _, a := range sub.Answers {
		ansMap[a.QuestionID] = a
	}

	for _, q := range spec.Questions {
		fmt.Fprintf(&b, "## `%s` [%s] — %s\n\n", q.ID, q.Axis, q.Title)
		ans, ok := ansMap[q.ID]
		if !ok {
			b.WriteString("- *No answer recorded.*\n\n")
			continue
		}

		optLabels := make(map[string]string, len(q.Options))
		for _, opt := range q.Options {
			optLabels[opt.ID] = opt.Label
		}

		switch q.Kind {
		case KindFreeText:
			if strings.TrimSpace(ans.FreeText) != "" {
				fmt.Fprintf(&b, "- **Decision (free text)**:\n  > %s\n", strings.ReplaceAll(strings.TrimSpace(ans.FreeText), "\n", "\n  > "))
			} else {
				b.WriteString("- **Decision**: *(empty)*\n")
			}
		case KindQCMWithFreeText:
			if len(ans.SelectedOptions) > 0 {
				b.WriteString("- **Selected option(s)**:\n")
				for _, oid := range ans.SelectedOptions {
					fmt.Fprintf(&b, "  - `%s`: %s\n", oid, optLabels[oid])
				}
			} else {
				b.WriteString("- **Selected option(s)**: *(none — explicit none)*\n")
			}
			if strings.TrimSpace(ans.FreeText) != "" {
				fmt.Fprintf(&b, "- **Free-text amendment**:\n  > %s\n", strings.ReplaceAll(strings.TrimSpace(ans.FreeText), "\n", "\n  > "))
			}
		case KindBooleanChecklist:
			b.WriteString("- **Boolean assertions (Yes / No)**:\n")
			for _, opt := range q.Options {
				checked := ans.BooleanChecks[opt.ID] || slices.Contains(ans.SelectedOptions, opt.ID)
				status := "NO"
				if checked {
					status = "YES"
				}
				fmt.Fprintf(&b, "  - **[%s]** `%s` — %s\n", status, opt.ID, opt.Label)
			}
			if strings.TrimSpace(ans.FreeText) != "" {
				fmt.Fprintf(&b, "- **Additional notes**:\n  > %s\n", strings.ReplaceAll(strings.TrimSpace(ans.FreeText), "\n", "\n  > "))
			}
		}
		b.WriteString("\n")
	}

	if strings.TrimSpace(sub.GeneralNote) != "" {
		b.WriteString("## General Note\n\n")
		b.WriteString(strings.TrimSpace(sub.GeneralNote))
		b.WriteString("\n")
	}

	return b.String()
}

// WriteAtomicFile atomically writes data to targetPath with permission perm.
func WriteAtomicFile(targetPath string, data []byte, perm os.FileMode) error {
	dir := filepath.Dir(targetPath)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".tmp-askhuman-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer func() {
		_ = os.Remove(tmpName)
	}()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Chmod(perm); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpName, targetPath)
}

// QuestionsForSection returns the questions belonging to sectionID.
func QuestionsForSection(spec QuestionnaireSpec, sectionID string) []QuestionItem {
	var out []QuestionItem
	for _, q := range spec.Questions {
		if q.SectionID == sectionID {
			out = append(out, q)
		}
	}
	return out
}
