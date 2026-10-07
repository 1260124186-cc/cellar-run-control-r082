package storage

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/1260124186-cc/solo-0016-cellar-run-control/internal/domain"
)

const stateFileName = "state.json"

type Store struct {
	mu       sync.RWMutex
	path     string
	snapshot domain.Snapshot
	now      func() time.Time
}

func Open(dataDir string) (*Store, error) {
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		return nil, fmt.Errorf("create data directory: %w", err)
	}
	store := &Store{
		path:     filepath.Join(dataDir, stateFileName),
		snapshot: domain.EmptySnapshot(),
		now:      func() time.Time { return time.Now().UTC() },
	}
	if err := store.load(); err != nil {
		return nil, err
	}
	return store, nil
}

func (s *Store) load() error {
	raw, err := os.ReadFile(s.path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("read state: %w", err)
	}
	var loaded domain.Snapshot
	if err := json.Unmarshal(raw, &loaded); err != nil {
		return fmt.Errorf("decode state: %w", err)
	}
	normalized, err := normalizeSnapshot(loaded)
	if err != nil {
		return err
	}
	s.snapshot = normalized
	return nil
}

func normalizeSnapshot(value domain.Snapshot) (domain.Snapshot, error) {
	if value.Revision < 0 {
		return domain.Snapshot{}, fmt.Errorf("state revision cannot be negative")
	}
	if value.FormulaFamilies == nil {
		value.FormulaFamilies = map[string]domain.FormulaFamily{}
	}
	if value.Formulas == nil {
		value.Formulas = map[string]domain.Formula{}
	}
	if value.Vessels == nil {
		value.Vessels = map[string]domain.Vessel{}
	}
	if value.Runs == nil {
		value.Runs = map[string]domain.FermentationRun{}
	}
	if value.NameIndex == nil {
		value.NameIndex = map[string]string{}
	}
	if value.VesselIndex == nil {
		value.VesselIndex = map[string]string{}
	}
	if value.RunIndex == nil {
		value.RunIndex = map[string]string{}
	}
	if err := migrateFormulaLineage(&value); err != nil {
		return domain.Snapshot{}, err
	}
	if err := validateIndexes(value); err != nil {
		return domain.Snapshot{}, err
	}
	return value, nil
}

// migrateFormulaLineage upgrades snapshots written before formula families
// existed: every standalone formula becomes revision 1 of its own family and
// the name index is rebuilt to point at families. Snapshots that already carry
// family data are only validated, never rewritten.
func migrateFormulaLineage(value *domain.Snapshot) error {
	if len(value.FormulaFamilies) > 0 {
		return nil
	}
	families := make(map[string]domain.FormulaFamily, len(value.Formulas))
	nameIndex := make(map[string]string, len(value.Formulas))
	for id, formula := range value.Formulas {
		familyID := "fam_" + strings.TrimPrefix(id, "formula_")
		family := domain.FormulaFamily{
			ID:           familyID,
			Name:         formula.Name,
			NextRevision: 2,
			CreatedAt:    formula.CreatedAt,
			UpdatedAt:    formula.UpdatedAt,
		}
		switch formula.State {
		case domain.FormulaApproved:
			if formula.SupersededAt == nil {
				current := id
				family.CurrentID = &current
			}
		case domain.FormulaDraft:
			draft := id
			family.DraftID = &draft
		}
		families[familyID] = family
		formula.FamilyID = familyID
		if formula.RevisionNumber == 0 {
			formula.RevisionNumber = 1
		}
		value.Formulas[id] = formula
		key := domain.NameKey(formula.Name)
		if key == "" {
			return fmt.Errorf("formula %s has an empty normalized name", id)
		}
		if existing, found := nameIndex[key]; found && existing != familyID {
			return fmt.Errorf("formula name index contains a duplicate key")
		}
		nameIndex[key] = familyID
	}
	value.FormulaFamilies = families
	value.NameIndex = nameIndex
	return nil
}

func validateIndexes(value domain.Snapshot) error {
	expectedFamilies := make(map[string]struct{}, len(value.FormulaFamilies))
	expectedNames := make(map[string]string, len(value.FormulaFamilies))
	familyMembers := make(map[string][]string)
	for id, family := range value.FormulaFamilies {
		expectedFamilies[id] = struct{}{}
		if family.ID != id {
			return fmt.Errorf("formula family %s has a mismatched identity", id)
		}
		key := domain.NameKey(family.Name)
		if key == "" {
			return fmt.Errorf("formula family %s has an empty normalized name", id)
		}
		if existing, found := expectedNames[key]; found && existing != id {
			return fmt.Errorf("formula name index contains a duplicate key")
		}
		expectedNames[key] = id
		if family.CurrentID != nil {
			if _, ok := value.Formulas[*family.CurrentID]; !ok {
				return fmt.Errorf("formula family %s points at a missing current revision", id)
			}
		}
		if family.DraftID != nil {
			if _, ok := value.Formulas[*family.DraftID]; !ok {
				return fmt.Errorf("formula family %s points at a missing draft revision", id)
			}
		}
		if family.NextRevision < 1 {
			return fmt.Errorf("formula family %s has an invalid next revision", id)
		}
	}
	if err := compareIndexes("formula name", value.NameIndex, expectedNames); err != nil {
		return err
	}

	revisionNumbers := make(map[string]map[int]string, len(value.FormulaFamilies))
	for id := range value.FormulaFamilies {
		revisionNumbers[id] = map[int]string{}
	}
	for id, formula := range value.Formulas {
		if formula.FamilyID == "" {
			return fmt.Errorf("formula %s has no formula family", id)
		}
		if _, ok := value.FormulaFamilies[formula.FamilyID]; !ok {
			return fmt.Errorf("formula %s points at a missing formula family", id)
		}
		if formula.RevisionNumber < 1 {
			return fmt.Errorf("formula %s has an invalid revision number", id)
		}
		if existing, found := revisionNumbers[formula.FamilyID][formula.RevisionNumber]; found && existing != id {
			return fmt.Errorf("formula family %s contains duplicate revision numbers", formula.FamilyID)
		}
		revisionNumbers[formula.FamilyID][formula.RevisionNumber] = id
		if formula.RevisionOfID != nil {
			source, ok := value.Formulas[*formula.RevisionOfID]
			if !ok || source.FamilyID != formula.FamilyID {
				return fmt.Errorf("formula %s has an invalid revision source", id)
			}
			if formula.SourceRevisionNumber == nil ||
				*formula.SourceRevisionNumber != source.RevisionNumber {
				return fmt.Errorf("formula %s records a mismatched source revision", id)
			}
		}
		familyMembers[formula.FamilyID] = append(familyMembers[formula.FamilyID], id)
	}
	for id, family := range value.FormulaFamilies {
		members := familyMembers[id]
		if len(members) == 0 {
			return fmt.Errorf("formula family %s has no revisions", id)
		}
		if family.CurrentID != nil {
			current := value.Formulas[*family.CurrentID]
			if current.State != domain.FormulaApproved || current.SupersededAt != nil {
				return fmt.Errorf("formula family %s current revision is not runnable", id)
			}
		}
		if family.DraftID != nil {
			draft := value.Formulas[*family.DraftID]
			if draft.State != domain.FormulaDraft {
				return fmt.Errorf("formula family %s draft marker is stale", id)
			}
		}
		maxRevision := 0
		hasOpenDraft := false
		for _, memberID := range members {
			member := value.Formulas[memberID]
			if member.RevisionNumber > maxRevision {
				maxRevision = member.RevisionNumber
			}
			if member.State == domain.FormulaDraft {
				hasOpenDraft = true
			}
		}
		if family.NextRevision != maxRevision+1 {
			return fmt.Errorf("formula family %s has an inconsistent next revision", id)
		}
		if family.HasDraft() != hasOpenDraft {
			return fmt.Errorf("formula family %s draft marker does not match its revisions", id)
		}
	}

	expectedVessels := make(map[string]string, len(value.Vessels))
	for id, vessel := range value.Vessels {
		if existing, found := expectedVessels[vessel.Code]; found && existing != id {
			return fmt.Errorf("vessel index contains a duplicate code")
		}
		expectedVessels[vessel.Code] = id
	}
	if err := compareIndexes("vessel code", value.VesselIndex, expectedVessels); err != nil {
		return err
	}

	expectedRuns := make(map[string]string, len(value.Runs))
	for id, run := range value.Runs {
		if existing, found := expectedRuns[run.Code]; found && existing != id {
			return fmt.Errorf("run index contains a duplicate code")
		}
		expectedRuns[run.Code] = id
		if run.FormulaID != "" {
			if _, ok := value.Formulas[run.FormulaID]; !ok {
				return fmt.Errorf("run %s points at a missing formula revision", id)
			}
		}
	}
	return compareIndexes("run code", value.RunIndex, expectedRuns)
}

func compareIndexes(label string, actual, expected map[string]string) error {
	if len(actual) != len(expected) {
		return fmt.Errorf("%s index does not match its collection", label)
	}
	for key, expectedID := range expected {
		actualID, found := actual[key]
		if !found || actualID != expectedID {
			return fmt.Errorf("%s index does not match its collection", label)
		}
	}
	return nil
}

func (s *Store) Revision() int64 {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.snapshot.Revision
}

func (s *Store) View(visitor func(domain.Snapshot) error) error {
	s.mu.RLock()
	copied := s.snapshot.Clone()
	s.mu.RUnlock()
	return visitor(copied)
}

func (s *Store) Update(expected *int64, mutate func(*domain.Snapshot) error) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if expected != nil && *expected != s.snapshot.Revision {
		return domain.NewError(domain.CodeRevisionConflict,
			"the supplied revision does not match the current snapshot")
	}
	candidate := s.snapshot.Clone()
	if err := mutate(&candidate); err != nil {
		return err
	}
	candidate.Revision = s.snapshot.Revision + 1
	candidate.UpdatedAt = s.now()
	if err := writeSnapshot(s.path, candidate); err != nil {
		return domain.NewError(domain.CodeStorage, err.Error())
	}
	s.snapshot = candidate
	return nil
}

func (s *Store) Execute(mutator func(*domain.Snapshot) error) error {
	return s.Update(nil, mutator)
}
