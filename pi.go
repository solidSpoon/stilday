package main

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"strings"
	"time"
)

// piState is the first-run download of the Pi runtime.
type piState struct {
	version   string
	path      string
	installed bool
	busy      bool
	stage     string
	done      int64
	total     int64
	err       string
}

// piTarget is everything needed to start one Pi child: the binary, the
// provider and model, and the credentials in its environment. The UI
// thread captures it, so a worker never reads the app's config.
type piTarget struct {
	bin   string
	spec  provider
	model string
	env   []string
}

// piTarget resolves the configured Pi binary and model. A missing Pi is an
// error here, once, instead of three copies of the same checks.
func (a *app) piTarget() (piTarget, error) {
	bin := a.piPath
	if bin == "" {
		bin, a.piErr = resolvePi()
	}
	if bin == "" {
		if a.piErr == "" {
			a.piErr = "还没有 Pi 运行环境"
		}
		return piTarget{}, errors.New(a.piErr)
	}
	spec := providerFor(a.cfg.Provider)
	model := strings.TrimSpace(a.cfg.Model)
	if model == "" {
		model = spec.DefaultModel
	}
	// A model chosen from the API list is stored bare; Pi's own catalog may
	// carry the provider prefix.
	model = strings.TrimPrefix(model, spec.ID+"/")
	t := piTarget{bin: bin, spec: spec, model: model}
	// The keychain belongs to the signed app; tests have no window and
	// must never prompt for it.
	if a.win != nil {
		if key, err := appSecrets().Get(providerSecretKey(spec.ID)); err == nil {
			if key := strings.TrimSpace(string(key)); key != "" {
				t.env = append(t.env, spec.Env+"="+key)
			}
		}
	}
	return t, nil
}

// resolvePi finds the Pi binary: the STILDAY_PI override, the runtime the
// app downloaded, or pi on PATH.
func resolvePi() (string, string) {
	// STILDAY_PI is an explicit override, for tests and for a Pi elsewhere.
	if bin := os.Getenv("STILDAY_PI"); bin != "" {
		if p, err := exec.LookPath(bin); err == nil {
			return p, ""
		}
		if st, err := os.Stat(bin); err == nil && !st.IsDir() {
			return bin, ""
		}
		return "", "STILDAY_PI 指向的 pi 不存在"
	}
	// The runtime Stilday downloads on first use.
	if p := installedPiBinary(); p != "" {
		return p, ""
	}
	if p, err := exec.LookPath("pi"); err == nil {
		return p, ""
	}
	return "", "还没有 Pi 运行环境。在设置里下载，或安装 Pi 后运行 pi /login。"
}

// downloadPi fetches the standalone Pi runtime into the app's private data
// directory, verifies the release checksum, and switches the chat to it.
func (a *app) downloadPi() {
	if a.pi.busy {
		return
	}
	dest, err := piInstallDir()
	if err != nil {
		a.pi.err = err.Error()
		return
	}
	a.pi.busy = true
	a.pi.err = ""
	a.pi.stage = "正在连接…"
	a.pi.done, a.pi.total = 0, 0
	rel := defaultPiRelease()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	go func() {
		defer cancel()
		bin, err := installPi(ctx, rel, dest, func(stage string, done, total int64) {
			if a.win == nil {
				return
			}
			a.win.Update(func() {
				if !a.pi.busy {
					return
				}
				a.pi.stage, a.pi.done, a.pi.total = stage, done, total
			})
		})
		if a.win == nil {
			return
		}
		a.win.Update(func() {
			a.pi.busy = false
			if err != nil {
				a.pi.err = err.Error()
				return
			}
			a.pi.installed = true
			a.pi.path = bin
			a.pi.stage = "Pi 已安装"
			a.piPath = bin
			a.piErr = ""
		})
	}()
}
