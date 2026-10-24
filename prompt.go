package main

import _ "embed"

// systemPromptBase is the stable, editable part of the analysis prompt.
// The code appends the day, folder, collectors and photo count after it.
//
//go:embed prompts/system-prompt.md
var systemPromptBase string
