package output

import (
	"encoding/json"
	"fmt"
	"os"

	"opentracker/internal/model"
)

func Print(results []model.ProviderResult) error {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false)
	if err := enc.Encode(results); err != nil {
		return fmt.Errorf("cannot encode output: %w", err)
	}
	return nil
}
