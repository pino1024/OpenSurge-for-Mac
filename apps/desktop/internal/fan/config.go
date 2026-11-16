// Package fan stores the menu-bar fan curve where the signed-in user can edit it.
// The root fan process reads the same file.
package fan

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

type Point struct {
	C float64 `json:"c"`
	P float64 `json:"p"`
}

type Config struct {
	Visible bool    `json:"visible"`
	Enabled bool    `json:"enabled"`
	Points  []Point `json:"points"`
}

type Status struct {
	Temp    float64 `json:"temp"`
	RPM     float64 `json:"rpm"`
	Percent float64 `json:"percent"`
	Active  bool    `json:"active"`
}

func Default() Config {
	return Config{Visible: true, Enabled: true, Points: pointsBetween(60, 85)}
}

func pointsBetween(start, full float64) []Point {
	return []Point{{C: start, P: 0}, {C: start + (full-start)*12/25, P: 26}, {C: full, P: 100}}
}

func Path() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "OpenSurge", "fan.json"), nil
}

func StatusPath() (string, error) {
	path, err := Path()
	if err != nil {
		return "", err
	}
	return filepath.Join(filepath.Dir(path), "fan-status.json"), nil
}

func Load(path string) (Config, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return Default(), nil
	}
	if err != nil {
		return Config{}, err
	}
	cfg := Default()
	if err := json.Unmarshal(data, &cfg); err != nil {
		return Config{}, err
	}
	migrated := false
	if !bytes.Contains(data, []byte(`"visible"`)) {
		var old struct {
			Enabled bool `json:"enabled"`
		}
		_ = json.Unmarshal(data, &old)
		cfg.Visible = old.Enabled
		cfg.Enabled = true
		migrated = true
	}
	if !bytes.Contains(data, []byte(`"points"`)) {
		var old struct {
			Start float64 `json:"start"`
			Full  float64 `json:"full"`
		}
		_ = json.Unmarshal(data, &old)
		if old.Full >= old.Start+5 && old.Start >= 20 && old.Full <= 110 {
			cfg.Points = pointsBetween(old.Start, old.Full)
		} else {
			cfg.Points = Default().Points
		}
		migrated = true
	}
	if err := Validate(cfg); err != nil {
		return Config{}, err
	}
	if migrated {
		_ = Save(path, cfg)
	}
	return cfg, nil
}

func Save(path string, cfg Config) error {
	if err := Validate(cfg); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	data, err := json.Marshal(cfg)
	if err != nil {
		return err
	}
	data = append(data, '\n')
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func ReadStatus(path string) (Status, bool) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Status{}, false
	}
	var status Status
	if json.Unmarshal(data, &status) != nil {
		return Status{}, false
	}
	return status, true
}

func Validate(cfg Config) error {
	if len(cfg.Points) < 2 || len(cfg.Points) > 6 {
		return fmt.Errorf("fan curve needs 2 to 6 points")
	}
	for i, point := range cfg.Points {
		if point.C < 20 || point.C > 110 || point.P < 0 || point.P > 100 {
			return fmt.Errorf("fan curve point is out of range")
		}
		if i > 0 && (point.C < cfg.Points[i-1].C+2 || point.P < cfg.Points[i-1].P) {
			return fmt.Errorf("fan curve points must rise in temperature and speed")
		}
	}
	return nil
}
