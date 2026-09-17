package resultprojection

import publication "github.com/TakuyaYagam1/task-per-minute/internal/usecase/resultprojection/publication"

const (
	DependencyArtifact       = publication.DependencyArtifact
	DependencyOfficialResult = publication.DependencyOfficialResult
	DependencyGoldenPosition = publication.DependencyGoldenPosition
)

type (
	FinalScope                 = publication.FinalScope
	FinalHeadExpectation       = publication.FinalHeadExpectation
	PublicationIDs             = publication.PublicationIDs
	PublicationMember          = publication.PublicationMember
	PublicationDependency      = publication.PublicationDependency
	PublicationArtifact        = publication.PublicationArtifact
	FinalPublication           = publication.FinalPublication
	FinalPublicationReceipt    = publication.FinalPublicationReceipt
	FinalRevisionConflictError = publication.FinalRevisionConflictError
	FinalPublicationRepository = publication.FinalPublicationRepository
)
