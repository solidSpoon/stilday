package main

import "testing"

func TestConfigWithDefaults(t *testing.T) {
	c := config{}.withDefaults()
	if c.PhotoStart != "00:00" || c.PhotoEnd != "23:59" {
		t.Errorf("photo window = %q–%q", c.PhotoStart, c.PhotoEnd)
	}
	if c.MaxPhotos != defaultMaxPhotos {
		t.Errorf("max photos = %d", c.MaxPhotos)
	}
	if c.Provider == "" || c.Model == "" {
		t.Errorf("provider = %q model = %q", c.Provider, c.Model)
	}
	if c.CollectTime != "22:00" || c.AutoTime != "10:00" {
		t.Errorf("clocks = %q, %q", c.CollectTime, c.AutoTime)
	}

	// The old one-time pipeline stored its collection time in auto_time.
	migrated := config{AutoTime: "21:30"}.withDefaults()
	if migrated.CollectTime != "21:30" || migrated.AutoTime != "10:00" {
		t.Errorf("migration = %q, %q", migrated.CollectTime, migrated.AutoTime)
	}

	// H:MM is accepted and normalized.
	shorthand := config{CollectTime: "2:00", AutoTime: "9:05"}.withDefaults()
	if shorthand.CollectTime != "02:00" || shorthand.AutoTime != "09:05" {
		t.Errorf("normalize = %q, %q", shorthand.CollectTime, shorthand.AutoTime)
	}
}

func TestConfigValidate(t *testing.T) {
	c := config{StorageDir: t.TempDir()}.withDefaults()
	if err := c.validate(); err != nil {
		t.Fatalf("valid config rejected: %v", err)
	}
	if err := (config{}).withDefaults().validate(); err == nil {
		t.Error("a config without storage validated")
	}
	bad := c
	bad.PhotoStart = "9"
	if err := bad.validate(); err == nil {
		t.Error("a bad photo window validated")
	}
	bad = c
	bad.AutoTime = "25:00"
	if err := bad.validate(); err == nil {
		t.Error("a bad analysis time validated")
	}
}

func TestNormalizeClock(t *testing.T) {
	for in, want := range map[string]string{
		"2:00": "02:00", "09:05": "09:05", " 23:59 ": "23:59", "": "", "25:00": "25:00", "abc": "abc",
	} {
		if got := normalizeClock(in); got != want {
			t.Errorf("normalizeClock(%q) = %q, want %q", in, got, want)
		}
	}
}
