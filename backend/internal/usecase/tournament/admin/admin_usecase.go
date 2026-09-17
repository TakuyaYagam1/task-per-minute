package admin

import application "github.com/TakuyaYagam1/task-per-minute/internal/usecase/tournament/admin/application"

type AdminDependencies = application.AdminDependencies
type AdminUseCase = application.AdminUseCase

func AdminNewUseCase(deps AdminDependencies) *AdminUseCase {
	return application.AdminNewUseCase(deps)
}

var _ AdminService = (*AdminUseCase)(nil)
