package golden

import goldenstate "github.com/TakuyaYagam1/task-per-minute/internal/usecase/golden/state"

type IdentityRole = goldenstate.IdentityRole

func CoreIdentityRoles(state GoldenState) []IdentityRole {
	return goldenstate.CoreIdentityRoles(state)
}

func PlanIdentityRoles(state GoldenState) []IdentityRole {
	return goldenstate.PlanIdentityRoles(state)
}

func WindowIdentityRoles(state GoldenState) []IdentityRole {
	return goldenstate.WindowIdentityRoles(state)
}

func TransitionIdentityRoles(state GoldenState) []IdentityRole {
	return goldenstate.TransitionIdentityRoles(state)
}
