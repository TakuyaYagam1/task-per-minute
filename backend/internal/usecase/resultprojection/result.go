package resultprojection

import resultusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/result"

func ProjectOfficialResult(input OfficialResultProjectionInput) (OfficialResultProjectionPlan, error) {
	if !validTerminalResultSource(input) {
		return OfficialResultProjectionPlan{}, invalidOfficialResultProjection("terminal result source does not match evidence")
	}
	if input.NoGame != nil && !ordinaryOfficialResultInputIsZero(input) {
		return OfficialResultProjectionPlan{}, invalidOfficialResultProjection("mixed projection evidence")
	}
	cloned, err := cloneOfficialResultProjectionInput(input)
	if err != nil {
		return OfficialResultProjectionPlan{}, invalidOfficialResultProjection("clone evidence: %v", err)
	}
	public, operator, err := deriveOfficialResultProjection(cloned)
	if err != nil {
		return OfficialResultProjectionPlan{}, err
	}
	plan := OfficialResultProjectionPlan{input: cloned, public: public, operator: operator}
	if err := plan.Validate(); err != nil {
		return OfficialResultProjectionPlan{}, err
	}
	return plan, nil
}

func validTerminalResultSource(input OfficialResultProjectionInput) bool {
	if input.NoGame != nil {
		return input.TerminalSource == TerminalResultSourceNormalNoShow
	}
	if input.Score != nil && input.Score.TerminalEvidence != nil {
		return input.TerminalSource == TerminalResultSourcePreStartForfeit &&
			input.Score.TerminalEvidence.Source == resultusecase.SeriesScoreTerminalSourcePreStartForfeit
	}
	return input.TerminalSource == TerminalResultSourcePlayed
}

func (p OfficialResultProjectionPlan) Validate() error {
	public, operator, err := deriveOfficialResultProjection(p.input)
	if err != nil {
		return err
	}
	if !publicOfficialResultsEqual(public, p.public) || !operatorOfficialResultsEqual(operator, p.operator) {
		return invalidOfficialResultProjection("projection plan was spliced")
	}
	return nil
}

func (p OfficialResultProjectionPlan) Public() PublicOfficialResult {
	return clonePublicOfficialResult(p.public)
}

func (p OfficialResultProjectionPlan) Operator() OperatorOfficialResult {
	return cloneOperatorOfficialResult(p.operator)
}

func deriveOfficialResultProjection(
	input OfficialResultProjectionInput,
) (PublicOfficialResult, OperatorOfficialResult, error) {
	if input.NoGame != nil {
		if !ordinaryOfficialResultInputIsZero(input) {
			return PublicOfficialResult{}, OperatorOfficialResult{}, invalidOfficialResultProjection("mixed projection evidence")
		}
		return deriveRecordedNoGameProjection(*input.NoGame)
	}
	return deriveRevisionHeadProjection(input)
}
