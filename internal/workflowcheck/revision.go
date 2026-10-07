package workflowcheck

import (
	"fmt"

	"github.com/1260124186-cc/solo-0016-cellar-run-control/internal/domain"
)

func formulaRevision(api *client) error {
	original, err := ensureApprovedFormula(api, "Revisioned Pale")
	if err != nil {
		return err
	}
	if original.Revision != 1 || original.LineageID != original.ID ||
		original.SourceFormulaID != nil {
		return errInvalidWorkflow("first approved formula is not revision 1 of its lineage")
	}

	firstRun, err := createCheckedRun(api, "RUN-REV-1", original.ID, 201)
	if err != nil {
		return err
	}
	if firstRun.FormulaID != original.ID || firstRun.FormulaRevision != 1 {
		return errInvalidWorkflow("run did not record the adopted formula revision")
	}

	if err := rejectRevisionSources(api, original); err != nil {
		return err
	}

	candidate, err := startRevisionCandidate(api, original)
	if err != nil {
		return err
	}
	if err := confirmCandidatePending(api, original, candidate); err != nil {
		return err
	}

	approved, err := approveRevisionCandidate(api, candidate)
	if err != nil {
		return err
	}
	if err := confirmSupersededSource(api, original, approved); err != nil {
		return err
	}
	return confirmStaleFork(api, approved)
}

func rejectRevisionSources(api *client, original formulaView) error {
	draftResponse, err := api.call("POST", "/v1/formulas",
		createFormulaPayload("Revision Draft Source"), nil)
	if err != nil {
		return err
	}
	if err := requireStatus(draftResponse, 201, "create draft source"); err != nil {
		return err
	}
	var draft formulaView
	if err := draftResponse.decode(&draft); err != nil {
		return err
	}
	fromDraft, err := api.call("POST", "/v1/formulas/"+draft.ID+"/revisions", nil, nil)
	if err != nil {
		return err
	}
	if err := requireCode(fromDraft, 409, string(domain.CodeFormulaRevisionSource),
		"revise a draft formula"); err != nil {
		return err
	}
	missing, err := api.call("POST", "/v1/formulas/formula_missing/revisions", nil, nil)
	if err != nil {
		return err
	}
	if err := requireCode(missing, 404, string(domain.CodeFormulaNotFound),
		"revise a missing formula"); err != nil {
		return err
	}

	retirable, err := ensureApprovedFormula(api, "Retirable Source")
	if err != nil {
		return err
	}
	retireResponse, err := api.call("POST", "/v1/formulas/"+retirable.ID+"/retire",
		nil, ptrInt64(retirable.Version))
	if err != nil {
		return err
	}
	if err := requireStatus(retireResponse, 200, "retire source formula"); err != nil {
		return err
	}
	fromRetired, err := api.call("POST", "/v1/formulas/"+retirable.ID+"/revisions", nil, nil)
	if err != nil {
		return err
	}
	if err := requireCode(fromRetired, 409, string(domain.CodeFormulaRevisionSource),
		"revise a retired formula"); err != nil {
		return err
	}

	invalidOverride, err := api.call("POST", "/v1/formulas/"+original.ID+"/revisions",
		map[string]any{"target_original_gravity": 1.5}, nil)
	if err != nil {
		return err
	}
	if err := requireCode(invalidOverride, 400, string(domain.CodeInvalidInput),
		"revise with an out-of-range override"); err != nil {
		return err
	}
	nameConflict, err := api.call("POST", "/v1/formulas/"+original.ID+"/revisions",
		map[string]any{"name": "revision   DRAFT source"}, nil)
	if err != nil {
		return err
	}
	if err := requireCode(nameConflict, 409, string(domain.CodeFormulaNameConflict),
		"revise with another lineage name"); err != nil {
		return err
	}
	staleWrite, err := api.call("POST", "/v1/formulas/"+original.ID+"/revisions",
		nil, ptrInt64(1))
	if err != nil {
		return err
	}
	if err := requireCode(staleWrite, 412, string(domain.CodeRevisionConflict),
		"revise with a stale expected version"); err != nil {
		return err
	}
	return nil
}

func startRevisionCandidate(api *client, original formulaView) (formulaView, error) {
	response, err := api.call("POST", "/v1/formulas/"+original.ID+"/revisions",
		map[string]any{"max_fermentation_temp_c": 24.0}, nil)
	if err != nil {
		return formulaView{}, err
	}
	if err := requireStatus(response, 201, "create revision candidate"); err != nil {
		return formulaView{}, err
	}
	var candidate formulaView
	if err := response.decode(&candidate); err != nil {
		return formulaView{}, err
	}
	if candidate.State != domain.FormulaDraft || candidate.Revision != 2 ||
		candidate.LineageID != original.LineageID {
		return formulaView{}, errInvalidWorkflow("revision candidate is not a draft of the next revision")
	}
	if candidate.SourceFormulaID == nil || *candidate.SourceFormulaID != original.ID ||
		candidate.SourceVersion == nil || *candidate.SourceVersion != original.Version {
		return formulaView{}, errInvalidWorkflow("revision candidate did not record its source version")
	}
	if candidate.MaxFermentationTempC != 24.0 || len(candidate.Steps) != len(original.Steps) {
		return formulaView{}, errInvalidWorkflow("revision candidate did not apply overrides and copy steps")
	}
	return candidate, nil
}

func confirmCandidatePending(api *client, original, candidate formulaView) error {
	againstCandidate, err := api.call("POST", "/v1/runs",
		createRunPayload("RUN-REV-2", candidate.ID), nil)
	if err != nil {
		return err
	}
	if err := requireCode(againstCandidate, 409, string(domain.CodeFormulaState),
		"run against a pending candidate"); err != nil {
		return err
	}
	stillCurrent, err := createCheckedRun(api, "RUN-REV-3", original.ID, 201)
	if err != nil {
		return err
	}
	if stillCurrent.FormulaRevision != 1 {
		return errInvalidWorkflow("pending candidate replaced the current revision too early")
	}

	stepsResponse, err := api.call("PUT", "/v1/formulas/"+candidate.ID+"/steps",
		formulaStepPayload(), ptrInt64(candidate.Version))
	if err != nil {
		return err
	}
	if err := requireStatus(stepsResponse, 200, "edit candidate steps"); err != nil {
		return err
	}
	staleApprove, err := api.call("POST", "/v1/formulas/"+candidate.ID+"/approve",
		nil, ptrInt64(candidate.Version))
	if err != nil {
		return err
	}
	if err := requireCode(staleApprove, 412, string(domain.CodeRevisionConflict),
		"approve candidate with a stale version"); err != nil {
		return err
	}
	return nil
}

func approveRevisionCandidate(api *client, candidate formulaView) (formulaView, error) {
	response, err := api.call("POST", "/v1/formulas/"+candidate.ID+"/approve",
		nil, ptrInt64(candidate.Version+1))
	if err != nil {
		return formulaView{}, err
	}
	if err := requireStatus(response, 200, "approve revision candidate"); err != nil {
		return formulaView{}, err
	}
	var approved formulaView
	if err := response.decode(&approved); err != nil {
		return formulaView{}, err
	}
	if approved.State != domain.FormulaApproved || approved.Revision != 2 {
		return formulaView{}, errInvalidWorkflow("approved candidate has unexpected state or revision")
	}
	return approved, nil
}

func confirmSupersededSource(api *client, original, approved formulaView) error {
	superseded, err := getFormula(api, original.ID)
	if err != nil {
		return err
	}
	if superseded.State != domain.FormulaApproved || superseded.SupersededByID == nil ||
		*superseded.SupersededByID != approved.ID {
		return errInvalidWorkflow("superseded formula is not queryable with its replacement")
	}
	editOld, err := api.call("PUT", "/v1/formulas/"+original.ID+"/steps",
		formulaStepPayload(), ptrInt64(superseded.Version))
	if err != nil {
		return err
	}
	if err := requireCode(editOld, 409, string(domain.CodeFormulaState),
		"edit a superseded formula"); err != nil {
		return err
	}
	runOld, err := api.call("POST", "/v1/runs",
		createRunPayload("RUN-REV-4", original.ID), nil)
	if err != nil {
		return err
	}
	if err := requireCode(runOld, 409, string(domain.CodeFormulaState),
		"run against a superseded formula"); err != nil {
		return err
	}
	newRun, err := createCheckedRun(api, "RUN-REV-5", approved.ID, 201)
	if err != nil {
		return err
	}
	if newRun.FormulaRevision != 2 {
		return errInvalidWorkflow("new run did not adopt the approved revision")
	}
	repeat, err := api.call("POST", "/v1/formulas/"+approved.ID+"/approve",
		nil, ptrInt64(approved.Version))
	if err != nil {
		return err
	}
	if err := requireCode(repeat, 409, string(domain.CodeFormulaState),
		"approve a revision twice"); err != nil {
		return err
	}
	return nil
}

func confirmStaleFork(api *client, approved formulaView) error {
	firstFork, err := createFork(api, approved)
	if err != nil {
		return err
	}
	secondFork, err := createFork(api, approved)
	if err != nil {
		return err
	}
	if firstFork.Revision != 3 || secondFork.Revision != 3 {
		return errInvalidWorkflow("forked candidates did not record the next revision")
	}
	approveFirst, err := api.call("POST", "/v1/formulas/"+firstFork.ID+"/approve",
		nil, ptrInt64(firstFork.Version))
	if err != nil {
		return err
	}
	if err := requireStatus(approveFirst, 200, "approve first fork"); err != nil {
		return err
	}
	approveSecond, err := api.call("POST", "/v1/formulas/"+secondFork.ID+"/approve",
		nil, ptrInt64(secondFork.Version))
	if err != nil {
		return err
	}
	if err := requireCode(approveSecond, 409, string(domain.CodeFormulaRevisionStale),
		"approve a candidate from a stale source"); err != nil {
		return err
	}

	stranded, err := getFormula(api, secondFork.ID)
	if err != nil {
		return err
	}
	if stranded.State != domain.FormulaDraft || stranded.Version != secondFork.Version {
		return errInvalidWorkflow("rejected stale approval left a partial mutation")
	}
	head, err := getFormula(api, approved.ID)
	if err != nil {
		return err
	}
	if head.SupersededByID == nil || *head.SupersededByID != firstFork.ID {
		return errInvalidWorkflow("stale approval clobbered the lineage head")
	}
	fromSuperseded, err := api.call("POST", "/v1/formulas/"+approved.ID+"/revisions", nil, nil)
	if err != nil {
		return err
	}
	if err := requireCode(fromSuperseded, 409, string(domain.CodeFormulaRevisionSource),
		"revise a superseded formula"); err != nil {
		return err
	}
	newest, err := createCheckedRun(api, "RUN-REV-6", firstFork.ID, 201)
	if err != nil {
		return err
	}
	if newest.FormulaRevision != 3 {
		return errInvalidWorkflow("newest run did not adopt the third revision")
	}
	return nil
}

func createFork(api *client, source formulaView) (formulaView, error) {
	response, err := api.call("POST", "/v1/formulas/"+source.ID+"/revisions", nil, nil)
	if err != nil {
		return formulaView{}, err
	}
	if err := requireStatus(response, 201, "create fork candidate"); err != nil {
		return formulaView{}, err
	}
	var candidate formulaView
	if err := response.decode(&candidate); err != nil {
		return formulaView{}, err
	}
	if candidate.SourceFormulaID == nil || *candidate.SourceFormulaID != source.ID ||
		candidate.SourceVersion == nil || *candidate.SourceVersion != source.Version {
		return formulaView{}, fmt.Errorf("fork candidate recorded the wrong source")
	}
	return candidate, nil
}

func createCheckedRun(
	api *client,
	code string,
	formulaID string,
	status int,
) (runView, error) {
	response, err := api.call("POST", "/v1/runs", createRunPayload(code, formulaID), nil)
	if err != nil {
		return runView{}, err
	}
	if err := requireStatus(response, status, "create run "+code); err != nil {
		return runView{}, err
	}
	var run runView
	if err := response.decode(&run); err != nil {
		return runView{}, err
	}
	return run, nil
}

func getFormula(api *client, id string) (formulaView, error) {
	response, err := api.call("GET", "/v1/formulas/"+id, nil, nil)
	if err != nil {
		return formulaView{}, err
	}
	if err := requireStatus(response, 200, "read formula"); err != nil {
		return formulaView{}, err
	}
	var formula formulaView
	if err := response.decode(&formula); err != nil {
		return formulaView{}, err
	}
	return formula, nil
}
