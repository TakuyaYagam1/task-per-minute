package postgres

import "github.com/TakuyaYagam1/task-per-minute/internal/domain"

// categoryJSON remains a root-package bridge for the golden runtime mapper.
// Draft persistence itself is implemented by assignment/draft.
func categoryJSON(categories []domain.Category) ([]byte, error) {
	values := make([]string, len(categories))
	for index, category := range categories {
		values[index] = string(category)
	}
	return marshalJSON("category sequence", values)
}
