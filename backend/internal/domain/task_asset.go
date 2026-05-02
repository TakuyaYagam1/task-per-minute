package domain

import (
	"fmt"

	"github.com/google/uuid"
)

func TaskSourceFileKey(taskID uuid.UUID) string {
	return fmt.Sprintf("tasks/%s/source.zip", taskID)
}
