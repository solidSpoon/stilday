package main

import (
	"encoding/json"
	"strings"
	"testing"
)

// theKinds is the preset set the prompt and the tool must both show.
var theKinds = []string{"observation", "activity", "food", "health", "plan", "interest"}

// The prompt's 记录 section is generated, so it must be there, name every
// field, and list the kinds so the system prompt modal shows them.
func TestPromptRendersDayLog(t *testing.T) {
	base := promptBase()
	if strings.Contains(base, dayLogPlaceholder) {
		t.Fatal("the day-log placeholder was not replaced")
	}
	for _, want := range []string{
		"type StildaySaveDayArgs = {",
		"entries: Array<{",
		"summary: string;",
		"occurred_at: string;",
		"事件类型，只能取下面这些值",
		"plan：",
		"2026-10-24T18:30:00+08:00",
		`kind: "` + strings.Join(theKinds, `" | "`) + `";`,
	} {
		if !strings.Contains(base, want) {
			t.Errorf("the prompt is missing %q", want)
		}
	}
}

// Other assistants read the entries, so the prompt says to write them
// completely, without spelling out examples the photos cannot support.
func TestPromptSaysEntriesAreRead(t *testing.T) {
	if !strings.Contains(promptBase(), "后续可能会有 AI 读取这些条目") {
		t.Error("the prompt does not say that other AIs read the entries")
	}
}

// Every kind the model may send needs a label, or the card would show the
// raw English value.
func TestEveryKindHasLabel(t *testing.T) {
	for _, kind := range theKinds {
		if got := kindLabel(kind); got == kind {
			t.Errorf("kind %q has no label", kind)
		}
	}
}

// The extension builds its TypeBox parameters from this JSON, so the shape
// the model sees must match the prompt.
func TestDayLogSchemaJSON(t *testing.T) {
	var spec struct {
		Type     string   `json:"type"`
		Required []string `json:"required"`
		Props    map[string]struct {
			Type     string   `json:"type"`
			Required []string `json:"required"`
			Items    struct {
				Required   []string `json:"required"`
				Properties map[string]struct {
					Type        string   `json:"type"`
					Description string   `json:"description"`
					Enum        []string `json:"enum"`
				} `json:"properties"`
			} `json:"items"`
		} `json:"properties"`
	}
	if err := json.Unmarshal([]byte(dayLogSchemaJSON()), &spec); err != nil {
		t.Fatalf("the day-log schema is not JSON: %v", err)
	}
	if spec.Type != "object" || strings.Join(spec.Required, ",") != "entries" {
		t.Fatalf("top level is %q, required %v", spec.Type, spec.Required)
	}
	entries, ok := spec.Props["entries"]
	if !ok || entries.Type != "array" {
		t.Fatalf("entries is %+v", entries)
	}
	if got := strings.Join(entries.Items.Required, ","); got != "kind,summary,occurred_at" {
		t.Errorf("entry required is %q", got)
	}
	if got := strings.Join(entries.Items.Properties["kind"].Enum, ","); got != strings.Join(theKinds, ",") {
		t.Errorf("kind enum is %q", got)
	}
	for _, name := range []string{"kind", "summary", "occurred_at"} {
		if entries.Items.Properties[name].Description == "" {
			t.Errorf("entry field %q has no description", name)
		}
	}
}
