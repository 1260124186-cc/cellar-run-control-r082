package domain

import (
	"math"
	"strings"
	"time"
)

type FormulaState string

const (
	FormulaDraft    FormulaState = "draft"
	FormulaApproved FormulaState = "approved"
	FormulaRetired  FormulaState = "retired"
)

type FormulaStep struct {
	Sequence     int     `json:"sequence"`
	Label        string  `json:"label"`
	TemperatureC float64 `json:"temperature_c"`
	HoldMinutes  int     `json:"hold_minutes"`
}

type Formula struct {
	ID                    string        `json:"id"`
	LineageID             string        `json:"lineage_id"`
	Revision              int           `json:"revision"`
	Name                  string        `json:"name"`
	Style                 string        `json:"style"`
	TargetOriginalGravity float64       `json:"target_original_gravity"`
	TargetFinalGravity    float64       `json:"target_final_gravity"`
	MaxFermentationTempC  float64       `json:"max_fermentation_temp_c"`
	MinimumDays           int           `json:"minimum_days"`
	MaximumDays           int           `json:"maximum_days"`
	Steps                 []FormulaStep `json:"steps"`
	State                 FormulaState  `json:"state"`
	Version               int64         `json:"version"`
	SourceFormulaID       *string       `json:"source_formula_id,omitempty"`
	SourceVersion         *int64        `json:"source_version,omitempty"`
	SupersededByID        *string       `json:"superseded_by_id,omitempty"`
	SupersededAt          *time.Time    `json:"superseded_at,omitempty"`
	CreatedAt             time.Time     `json:"created_at"`
	UpdatedAt             time.Time     `json:"updated_at"`
	ApprovedAt            *time.Time    `json:"approved_at,omitempty"`
	RetiredAt             *time.Time    `json:"retired_at,omitempty"`
}

func (f Formula) Clone() Formula {
	cloned := f
	cloned.Steps = append([]FormulaStep(nil), f.Steps...)
	if f.SourceFormulaID != nil {
		value := *f.SourceFormulaID
		cloned.SourceFormulaID = &value
	}
	if f.SourceVersion != nil {
		value := *f.SourceVersion
		cloned.SourceVersion = &value
	}
	if f.SupersededByID != nil {
		value := *f.SupersededByID
		cloned.SupersededByID = &value
	}
	if f.SupersededAt != nil {
		value := *f.SupersededAt
		cloned.SupersededAt = &value
	}
	return cloned
}

func (f *Formula) Normalize() {
	f.Name = NormalizeName(f.Name)
	f.Style = NormalizeName(f.Style)
}

func (f Formula) Validate() error {
	if f.Name == "" {
		return FieldError(CodeInvalidInput, "name", "is required")
	}
	if len([]rune(f.Name)) > 120 {
		return FieldError(CodeInvalidInput, "name", "must not exceed 120 characters")
	}
	if f.Style == "" {
		return FieldError(CodeInvalidInput, "style", "is required")
	}
	if len([]rune(f.Style)) > 80 {
		return FieldError(CodeInvalidInput, "style", "must not exceed 80 characters")
	}
	if f.TargetOriginalGravity < 1.030 || f.TargetOriginalGravity > 1.120 {
		return FieldError(CodeInvalidInput, "target_original_gravity",
			"must be between 1.030 and 1.120")
	}
	if f.TargetFinalGravity <= 0 || f.TargetOriginalGravity-f.TargetFinalGravity < 0.005 {
		return FieldError(CodeInvalidInput, "target_final_gravity",
			"must be positive and at least 0.005 below target original gravity")
	}
	if f.TargetFinalGravity > f.TargetOriginalGravity {
		return FieldError(CodeInvalidInput, "target_final_gravity",
			"must not exceed target original gravity")
	}
	if f.MaxFermentationTempC < 5 || f.MaxFermentationTempC > 40 {
		return FieldError(CodeInvalidInput, "max_fermentation_temp_c",
			"must be between 5 and 40 Celsius")
	}
	if f.MinimumDays < 1 || f.MinimumDays > 120 {
		return FieldError(CodeInvalidInput, "minimum_days",
			"must be between 1 and 120")
	}
	if f.MaximumDays < f.MinimumDays || f.MaximumDays > 120 {
		return FieldError(CodeInvalidInput, "maximum_days",
			"must be between minimum days and 120")
	}
	if len(f.Steps) > 24 {
		return FieldError(CodeInvalidInput, "steps", "must contain at most 24 entries")
	}
	if f.State == FormulaApproved || f.State == FormulaRetired {
		if err := ValidateFormulaSteps(f.Steps, true); err != nil {
			return err
		}
	}
	return nil
}

func ValidateFormulaSteps(steps []FormulaStep, required bool) error {
	if required && len(steps) == 0 {
		return FieldError(CodeInvalidInput, "steps", "must contain at least one entry")
	}
	if len(steps) > 24 {
		return FieldError(CodeInvalidInput, "steps", "must contain at most 24 entries")
	}
	for index, step := range steps {
		expected := index + 1
		if step.Sequence != expected {
			return FieldError(CodeInvalidInput, "steps",
				"sequence values must be contiguous and begin at 1")
		}
		if strings.TrimSpace(step.Label) == "" {
			return FieldError(CodeInvalidInput, "steps.label", "is required")
		}
		if len([]rune(step.Label)) > 120 {
			return FieldError(CodeInvalidInput, "steps.label",
				"must not exceed 120 characters")
		}
		if step.TemperatureC < 5 || step.TemperatureC > 90 {
			return FieldError(CodeInvalidInput, "steps.temperature_c",
				"must be between 5 and 90 Celsius")
		}
		if step.HoldMinutes < 1 || step.HoldMinutes > 1440 {
			return FieldError(CodeInvalidInput, "steps.hold_minutes",
				"must be between 1 and 1440")
		}
	}
	return nil
}

func (f *Formula) SetSteps(steps []FormulaStep) error {
	if f.State != FormulaDraft {
		return NewError(CodeFormulaState, "only a draft formula can change process steps")
	}
	if err := ValidateFormulaSteps(steps, false); err != nil {
		return err
	}
	f.Steps = append([]FormulaStep(nil), steps...)
	f.UpdatedAt = time.Now().UTC()
	f.Version++
	return nil
}

func (f *Formula) Approve(now time.Time) error {
	if f.State != FormulaDraft {
		return NewError(CodeFormulaState, "only a draft formula can be approved")
	}
	if err := ValidateFormulaSteps(f.Steps, true); err != nil {
		return err
	}
	stamp := now.UTC()
	f.State = FormulaApproved
	f.ApprovedAt = &stamp
	f.UpdatedAt = stamp
	f.Version++
	return nil
}

func (f *Formula) Retire(now time.Time) error {
	if f.State != FormulaApproved {
		return NewError(CodeFormulaState, "only an approved formula can be retired")
	}
	stamp := now.UTC()
	f.State = FormulaRetired
	f.RetiredAt = &stamp
	f.UpdatedAt = stamp
	f.Version++
	return nil
}

// IsCurrent reports whether the formula is the approved head of its lineage.
// Only the current revision accepts new runs or new revision candidates.
func (f Formula) IsCurrent() bool {
	return f.State == FormulaApproved && f.SupersededByID == nil
}

// BeginRevision builds a draft revision candidate from the current approved
// formula. The candidate copies the source content, records the source
// identifier and version, and stays a draft until it is approved.
func (f Formula) BeginRevision(now time.Time) (Formula, error) {
	if f.State != FormulaApproved {
		return Formula{}, NewError(CodeFormulaRevisionSource,
			"only an approved formula can be a revision source")
	}
	if f.SupersededByID != nil {
		return Formula{}, NewError(CodeFormulaRevisionSource,
			"a superseded formula cannot be a revision source")
	}
	stamp := now.UTC()
	sourceID := f.ID
	sourceVersion := f.Version
	candidate := Formula{
		LineageID:             f.LineageID,
		Revision:              f.Revision + 1,
		Name:                  f.Name,
		Style:                 f.Style,
		TargetOriginalGravity: f.TargetOriginalGravity,
		TargetFinalGravity:    f.TargetFinalGravity,
		MaxFermentationTempC:  f.MaxFermentationTempC,
		MinimumDays:           f.MinimumDays,
		MaximumDays:           f.MaximumDays,
		Steps:                 append([]FormulaStep(nil), f.Steps...),
		State:                 FormulaDraft,
		Version:               1,
		SourceFormulaID:       &sourceID,
		SourceVersion:         &sourceVersion,
		CreatedAt:             stamp,
		UpdatedAt:             stamp,
	}
	return candidate, nil
}

// MarkSuperseded records that a newer approved revision replaced this formula
// as the current head of its lineage. The formula itself stays approved and
// queryable, but it no longer accepts new runs or revision candidates.
func (f *Formula) MarkSuperseded(byID string, now time.Time) error {
	if f.State != FormulaApproved || f.SupersededByID != nil {
		return NewError(CodeFormulaState,
			"only the current approved formula can be superseded")
	}
	stamp := now.UTC()
	replacement := byID
	f.SupersededByID = &replacement
	f.SupersededAt = &stamp
	f.UpdatedAt = stamp
	f.Version++
	return nil
}

func GravityDifference(original, final float64) float64 {
	return math.Abs(original - final)
}
