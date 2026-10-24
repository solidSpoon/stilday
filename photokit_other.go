//go:build !darwin

package main

import (
	"fmt"
	"time"
)

type photokitAsset struct {
	File        string
	Filename    string
	Time        float64
	Capture     float64
	Width       int
	Height      int
	Favorite    bool
	Hidden      bool
	Albums      []string
	Latitude    *float64
	Longitude   *float64
	Modified    float64
	Source      string
	Live        bool
	Screenshot  bool
	Panorama    bool
	HDR         bool
	Depth       bool
	Burst       bool
	Adjustments bool
}

func photokitExport(start, end time.Time, outDir string, max int) ([]photokitAsset, error) {
	return nil, fmt.Errorf("PhotoKit is only available on macOS")
}
