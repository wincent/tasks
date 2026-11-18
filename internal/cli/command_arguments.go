package cli

import (
	"fmt"

	"github.com/wincent/tasks/internal/store"
)

func validateStatus(status string) error {
	if status != "" && status != "all" && !store.ValidStatus(status) {
		return fmt.Errorf("invalid status: %s", status)
	}
	return nil
}
