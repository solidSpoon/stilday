package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// piModelEntry is one model in Pi's local catalog.
type piModelEntry struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// fetchModels asks the provider for the models it serves, falling back to
// Pi's local catalog when the key or the network is unavailable.
func fetchModels(spec provider) ([]modelInfo, error) {
	catalog := func(err error) ([]modelInfo, error) {
		if models := piModelCatalog(spec); len(models) > 0 {
			return models, nil
		}
		return nil, err
	}
	key, err := appSecrets().Get(providerSecretKey(spec.ID))
	if err != nil || len(key) == 0 {
		return catalog(errors.New("先保存 " + spec.Name + " API key"))
	}
	if spec.ModelsURL == "" {
		return catalog(errors.New(spec.Name + " 没有模型列表接口"))
	}
	req, err := http.NewRequest(http.MethodGet, spec.ModelsURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("x-api-key", string(key))
	resp, err := (&http.Client{Timeout: 15 * time.Second}).Do(req)
	if err != nil {
		return catalog(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return catalog(fmt.Errorf("%s 返回 HTTP %d", spec.Name, resp.StatusCode))
	}
	var payload struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return catalog(err)
	}
	names := piModelNames(spec)
	out := make([]modelInfo, 0, len(payload.Data))
	seen := map[string]bool{}
	for _, m := range payload.Data {
		if m.ID == "" || seen[m.ID] {
			continue
		}
		seen[m.ID] = true
		name := names[m.ID]
		if name == "" {
			name = m.ID
		}
		out = append(out, modelInfo{ID: m.ID, Name: name})
	}
	sortModels(out)
	return out, nil
}

// piModelCatalog is the same catalog the API feeds, read from Pi's local
// copy. It is the fallback when the key or the network is unavailable.
func piModelCatalog(spec provider) []modelInfo {
	entries := readPiModels(spec)
	out := make([]modelInfo, 0, len(entries))
	for _, m := range entries {
		if m.ID == "" {
			continue
		}
		name := m.Name
		if name == "" {
			name = m.ID
		}
		out = append(out, modelInfo{ID: m.ID, Name: name})
	}
	sortModels(out)
	return out
}

// piModelNames reads the display names from Pi's local catalog, if it is
// there; the API only carries the model IDs.
func piModelNames(spec provider) map[string]string {
	out := map[string]string{}
	for _, m := range readPiModels(spec) {
		if m.ID != "" && m.Name != "" {
			out[m.ID] = m.Name
		}
	}
	return out
}

// readPiModels reads the provider's models from Pi's one catalog file.
func readPiModels(spec provider) []piModelEntry {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil
	}
	b, err := os.ReadFile(filepath.Join(home, ".pi", "agent", "models-store.json"))
	if err != nil {
		return nil
	}
	var catalog map[string]struct {
		Models []piModelEntry `json:"models"`
	}
	if json.Unmarshal(b, &catalog) != nil {
		return nil
	}
	return catalog[spec.catalogID()].Models
}

func sortModels(models []modelInfo) {
	sort.SliceStable(models, func(i, j int) bool {
		return strings.ToLower(models[i].Name) < strings.ToLower(models[j].Name)
	})
}

// loadModels fetches the model list off the UI thread and reports through
// the window.
func (a *app) loadModels() {
	if a.modelsLoading {
		return
	}
	spec := providerFor(a.settingsDraft.Provider)
	a.modelsLoading = true
	a.modelsErr = ""
	go func() {
		models, err := fetchModels(spec)
		update := func() {
			a.modelsLoading = false
			if err != nil {
				a.models = nil
				a.modelsErr = err.Error()
				return
			}
			a.models = models
			a.modelsErr = ""
		}
		if a.win == nil {
			update()
			return
		}
		a.win.Update(update)
	}()
}

// modelDisplay names a model, or returns its ID until the list arrives.
func (a *app) modelDisplay(id string) string {
	id = strings.TrimSpace(id)
	if id == "" {
		id = providerFor(a.settingsDraft.Provider).DefaultModel
	}
	for _, m := range a.models {
		if m.ID == id {
			return m.Name
		}
	}
	return id
}
