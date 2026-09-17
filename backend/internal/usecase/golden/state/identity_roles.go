package state

import "github.com/google/uuid"

// IdentityRole describes a retained Golden identity and its authority role.
type IdentityRole struct {
	Value uuid.UUID
	Role  string
}

func CoreIdentityRoles(state GoldenState) []IdentityRole {
	return exportIdentityRoles(goldenCoreIdentityRoles(state))
}

func PlanIdentityRoles(state GoldenState) []IdentityRole {
	return exportIdentityRoles(goldenPlanIdentityRoles(state))
}

func WindowIdentityRoles(state GoldenState) []IdentityRole {
	return exportIdentityRoles(goldenWindowIdentityRoles(state))
}

func TransitionIdentityRoles(state GoldenState) []IdentityRole {
	return exportIdentityRoles(goldenTransitionIdentityRoles(state))
}

func exportIdentityRoles(input []stateGoldenIdentityRole) []IdentityRole {
	result := make([]IdentityRole, len(input))
	for index, identity := range input {
		result[index] = IdentityRole{Value: identity.value, Role: identity.role}
	}
	return result
}
