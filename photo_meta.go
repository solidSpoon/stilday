package main

import (
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

// photoMetadata merges what PhotoKit knows with the EXIF the system index
// exposes. It returns nil when there is nothing beyond the sheets.
func photoMetadata(path string, pk *photokitAsset) *photoMeta {
	m := &photoMeta{}
	if pk != nil {
		m.Filename = pk.Filename
		if pk.Capture > 0 {
			m.CapturedFrom = "exif"
		} else {
			m.CapturedFrom = "photos"
		}
		m.Favorite = pk.Favorite
		m.Hidden = pk.Hidden
		m.Albums = cleanAlbums(pk.Albums)
		if pk.Latitude != nil && pk.Longitude != nil {
			m.Location = &photoLocation{Latitude: *pk.Latitude, Longitude: *pk.Longitude}
		}
		m.Live = pk.Live
		m.Screenshot = pk.Screenshot
		m.Panorama = pk.Panorama
		m.HDR = pk.HDR
		m.Depth = pk.Depth
		m.Burst = pk.Burst
		m.Adjustments = pk.Adjustments
		m.Source = pk.Source
		if pk.Modified > 0 {
			m.ModifiedAt = time.Unix(int64(pk.Modified), 0).Local().Format(time.RFC3339)
		}
	}
	cam := cameraInfo(path)
	if m.Camera == "" {
		m.Camera = strings.TrimSpace(cam.Camera)
	}
	m.Lens = cam.Lens
	m.FNumber = cam.FNumber
	m.Exposure = cam.Exposure
	m.ISO = cam.ISO
	m.FocalLength = cam.FocalLength
	if m.empty() {
		return nil
	}
	return m
}

func cleanAlbums(in []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(in))
	for _, a := range in {
		a = strings.TrimSpace(a)
		if a == "" || seen[a] {
			continue
		}
		seen[a] = true
		out = append(out, a)
	}
	return out
}

// cameraInfo reads the EXIF fields mdls exposes for a HEIC or JPEG.

// cameraInfo reads the EXIF fields mdls exposes for a HEIC or JPEG.
func cameraInfo(path string) photoMeta {
	if _, err := exec.LookPath("mdls"); err != nil {
		return photoMeta{}
	}
	names := []string{
		"kMDItemAcquisitionMake",
		"kMDItemAcquisitionModel",
		"kMDItemLensModel",
		"kMDItemFNumber",
		"kMDItemExposureTimeSeconds",
		"kMDItemISOSpeed",
		"kMDItemFocalLength",
	}
	args := make([]string, 0, len(names)*2+1)
	for _, n := range names {
		args = append(args, "-name", n)
	}
	args = append(args, path)
	raw, err := exec.Command("mdls", args...).Output()
	if err != nil {
		return photoMeta{}
	}
	values := map[string]string{}
	for _, line := range strings.Split(string(raw), "\n") {
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		values[strings.TrimSpace(k)] = strings.Trim(strings.TrimSpace(v), "\"")
	}
	makeName := strings.TrimSpace(values["kMDItemAcquisitionMake"])
	model := strings.TrimSpace(values["kMDItemAcquisitionModel"])
	camera := strings.TrimSpace(makeName + " " + model)
	if camera == "" {
		camera = model
	}
	m := photoMeta{
		Camera: camera,
		Lens:   strings.TrimSpace(values["kMDItemLensModel"]),
	}
	if v := values["kMDItemFNumber"]; v != "" && v != "(null)" {
		if f, err := strconv.ParseFloat(v, 64); err == nil {
			m.FNumber = fmt.Sprintf("f/%.1f", f)
		}
	}
	if v := values["kMDItemExposureTimeSeconds"]; v != "" && v != "(null)" {
		if f, err := strconv.ParseFloat(v, 64); err == nil {
			if f > 0 && f < 1 {
				m.Exposure = fmt.Sprintf("1/%d", int(1/f+0.5))
			} else if f > 0 {
				m.Exposure = fmt.Sprintf("%.1fs", f)
			}
		}
	}
	if v := values["kMDItemISOSpeed"]; v != "" && v != "(null)" {
		if n, err := strconv.Atoi(v); err == nil {
			m.ISO = n
		}
	}
	if v := values["kMDItemFocalLength"]; v != "" && v != "(null)" {
		if f, err := strconv.ParseFloat(v, 64); err == nil {
			m.FocalLength = fmt.Sprintf("%.0fmm", f)
		}
	}
	return m
}

// cleanupLegacyPhotoOriginals removes the photos/ folders that older
// collectors copied into work days. The current layout keeps only
// previews/, thumbs/ and sheets/.
