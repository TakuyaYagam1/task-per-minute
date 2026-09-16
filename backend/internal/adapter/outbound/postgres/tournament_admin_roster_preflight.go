package postgres

// Preflight loading lives in tournament/admin/roster. The child keeps the
// published-content authority reads together with the roster workflow, while
// this root file remains for the source-level SQL contract test.
//
// The child implementation reads GetCurrentTournamentContentConfiguration,
// ListTournamentContentCategoryPoolRevisions,
// ListTournamentContentCategoryPoolMemberships,
// ListTournamentContentStageDefaults, and ListTaskPoolVersionHealth before it
// calls domain.CreateContentConfiguration and assigns
// TaskHealth: loadedContent.taskHealth.
