package main

import (
	_ "embed"
	"strings"
)

// systemPromptBase is the stable, editable part of the analysis prompt.
// The code appends the day, folder, collectors and photo count after it,
// and fills the generated day-log section.
//
//go:embed prompts/system-prompt.md
var systemPromptBase string

// promptBase is the embedded prompt with its generated sections filled in.
// The arguments come from day_log_schema.go, so the prompt and the tool
// cannot disagree.
func promptBase() string {
	return strings.ReplaceAll(systemPromptBase, dayLogPlaceholder, renderDayLog())
}
