package playoff

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"sort"
	"time"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
)

type top4PayloadDocument struct {
	TournamentID              uuid.UUID                 `json:"tournament_id"`
	RevisionNo                int                       `json:"revision_no"`
	PreviousRevisionID        *uuid.UUID                `json:"previous_revision_id,omitempty"`
	CreatedAt                 time.Time                 `json:"created_at"`
	FinalStandingsRevisionID  uuid.UUID                 `json:"final_standings_revision_id"`
	FinalStandingsDigest      string                    `json:"final_standings_digest"`
	Participants              []Top4Participant         `json:"participants"`
	QualificationRevisionIDs  []uuid.UUID               `json:"qualification_revision_ids"`
	QualificationDigests      []string                  `json:"qualification_digests"`
	QualificationDependencies []top4DependencyPayload   `json:"qualification_dependencies"`
	TerminalSeries            []finalSwissSeriesPayload `json:"terminal_series"`
}

type top4DependencyPayload struct {
	SourceRevisionID  uuid.UUID `json:"source_revision_id"`
	DerivedRevisionID uuid.UUID `json:"derived_revision_id"`
}

func top4Payload(
	authority top4Authority,
	participants []Top4Participant,
	qualifications []domain.ProjectionRevision,
	lineage []domain.RevisionDependency,
) ([]byte, error) {
	sourceRevision := authority.Source.Projection().Revision()
	sourceDigest := sourceRevision.PayloadDigest()
	document := top4PayloadDocument{
		TournamentID: authority.TournamentID, RevisionNo: authority.RevisionNo, CreatedAt: authority.CreatedAt,
		FinalStandingsRevisionID:  sourceRevision.ID().UUID(),
		FinalStandingsDigest:      hex.EncodeToString(sourceDigest[:]),
		Participants:              append([]Top4Participant(nil), participants...),
		QualificationRevisionIDs:  make([]uuid.UUID, len(qualifications)),
		QualificationDigests:      make([]string, len(qualifications)),
		QualificationDependencies: make([]top4DependencyPayload, len(lineage)),
		TerminalSeries:            make([]finalSwissSeriesPayload, len(authority.CurrentTerminalSeries)),
	}
	for index, dependency := range lineage {
		document.QualificationDependencies[index] = top4DependencyPayload{
			SourceRevisionID:  dependency.SourceRevisionID.UUID(),
			DerivedRevisionID: dependency.DerivedRevisionID.UUID(),
		}
	}
	if authority.Previous != nil {
		value := authority.Previous.Projection.Revision().ID().UUID()
		document.PreviousRevisionID = &value
	}
	for index, qualification := range qualifications {
		revision := qualification.Revision()
		digest := revision.PayloadDigest()
		document.QualificationRevisionIDs[index] = revision.ID().UUID()
		document.QualificationDigests[index] = hex.EncodeToString(digest[:])
	}
	for index, head := range authority.CurrentTerminalSeries {
		payload, err := finalSwissSeriesPayloadFromHead(head)
		if err != nil {
			return nil, err
		}
		document.TerminalSeries[index] = payload
	}
	sort.Slice(document.TerminalSeries, func(i, j int) bool {
		return bytes.Compare(document.TerminalSeries[i].SeriesID[:], document.TerminalSeries[j].SeriesID[:]) < 0
	})
	return json.Marshal(document)
}
