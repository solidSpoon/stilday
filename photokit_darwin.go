//go:build darwin

package main

import (
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"time"

	"github.com/egoist/mygo"
)

//go:embed photokit.swift
var photokitSource string

type photokitAsset struct {
	ID          string   `json:"id"`
	File        string   `json:"file"`
	Filename    string   `json:"filename"`
	Time        float64  `json:"time"`
	Capture     float64  `json:"capture"`
	Width       int      `json:"width"`
	Height      int      `json:"height"`
	Favorite    bool     `json:"favorite"`
	Hidden      bool     `json:"hidden"`
	Albums      []string `json:"albums"`
	Latitude    *float64 `json:"latitude"`
	Longitude   *float64 `json:"longitude"`
	Modified    float64  `json:"modified"`
	Source      string   `json:"source"`
	Live        bool     `json:"live"`
	Screenshot  bool     `json:"screenshot"`
	Panorama    bool     `json:"panorama"`
	HDR         bool     `json:"hdr"`
	Depth       bool     `json:"depth"`
	Burst       bool     `json:"burst"`
	Adjustments bool     `json:"adjustments"`
}

type photokitResult struct {
	OK     bool            `json:"ok"`
	Error  string          `json:"error"`
	Assets []photokitAsset `json:"assets"`
}

// photokitBundle installs and compiles the small official PhotoKit helper
// on first use. It is a separate .app bundle so macOS gives it its own
// NSPhotoLibraryUsageDescription permission prompt.
func photokitBundle() (string, error) {
	base, err := mygo.App.Path(mygo.PathAppData)
	if err != nil {
		return "", err
	}
	root := filepath.Join(base, "photokit")
	bundle := filepath.Join(root, "Stilday Photos.app")
	exe := filepath.Join(bundle, "Contents", "MacOS", "StildayPhotos")
	info := filepath.Join(bundle, "Contents", "Info.plist")
	source := filepath.Join(root, "photokit.swift")
	sum := sha256.Sum256([]byte(photokitSource))
	version := hex.EncodeToString(sum[:8])[:12]
	marker := filepath.Join(root, "version")
	if old, err := os.ReadFile(marker); err == nil && string(old) == version {
		if _, err := os.Stat(exe); err == nil {
			return bundle, nil
		}
	}
	if _, err := exec.LookPath("swiftc"); err != nil {
		return "", fmt.Errorf("需要 Xcode Command Line Tools 才能使用照片图库（swiftc）")
	}
	if err := os.MkdirAll(filepath.Join(bundle, "Contents", "MacOS"), 0o755); err != nil {
		return "", err
	}
	if err := os.WriteFile(source, []byte(photokitSource), 0o644); err != nil {
		return "", err
	}
	plist := `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0"><dict>
<key>CFBundleIdentifier</key><string>com.stilday.photokit</string>
<key>CFBundleName</key><string>Stilday Photos</string>
<key>CFBundleDisplayName</key><string>Stilday Photos</string>
<key>CFBundleExecutable</key><string>StildayPhotos</string>
<key>CFBundlePackageType</key><string>APPL</string>
<key>CFBundleShortVersionString</key><string>1.0</string>
<key>LSUIElement</key><true/>
<key>NSPhotoLibraryUsageDescription</key><string>Stilday 需要读取照片图库中当天的照片，生成当天的时间流。</string>
</dict></plist>`
	if err := os.WriteFile(info, []byte(plist), 0o644); err != nil {
		return "", err
	}
	cmd := exec.Command("swiftc", "-O", "-parse-as-library",
		"-framework", "Photos", "-framework", "CoreGraphics", "-framework", "ImageIO",
		"-o", exe, source)
	if out, err := cmd.CombinedOutput(); err != nil {
		return "", fmt.Errorf("编译 PhotoKit 助手失败：%v：%s", err, string(out))
	}
	_ = os.WriteFile(marker, []byte(version), 0o644)
	return bundle, nil
}

// photokitExport runs the helper app and returns the exported originals.
func photokitExport(start, end time.Time, outDir string, max int) ([]photokitAsset, error) {
	bundle, err := photokitBundle()
	if err != nil {
		return nil, err
	}
	args := []string{"-W", "-n", "-a", bundle, "--args",
		strconv.FormatInt(start.Unix(), 10),
		strconv.FormatInt(end.Unix(), 10),
		outDir,
		strconv.Itoa(max)}
	cmd := exec.Command("open", args...)
	if out, err := cmd.CombinedOutput(); err != nil {
		return nil, fmt.Errorf("打开照片库助手失败：%v：%s", err, string(out))
	}
	data, err := os.ReadFile(filepath.Join(outDir, "assets.json"))
	if err != nil {
		return nil, fmt.Errorf("照片库助手没有返回结果：%w", err)
	}
	var res photokitResult
	if err := json.Unmarshal(data, &res); err != nil {
		return nil, err
	}
	if !res.OK {
		if res.Error == "" {
			res.Error = "unknown error"
		}
		return nil, fmt.Errorf("照片图库：%s", res.Error)
	}
	return res.Assets, nil
}
