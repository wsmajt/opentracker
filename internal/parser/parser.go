package parser

import "opentracker/internal/model"

// Parser defines the interface for parsing HTML into usage data.
type Parser interface {
	Parse(html string) (model.Usage, error)
}
