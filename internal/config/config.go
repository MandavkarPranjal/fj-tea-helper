// Package config reads and writes fth's global configuration file.
//
// The file is optional. Everything in it has a default that fth works with
// perfectly well without, so a missing file is not an error and a missing
// directory is not one either — fth simply behaves as if the file were empty.
//
// This is a different bargain from the detection cache in internal/forge, which
// is free to treat a corrupt file as an empty one because everything it holds
// is a measurement fth can take again. A config file is the user's stated
// intent, and a typo in it would silently flip a setting they believe they have
// enabled. So a malformed or unrecognised file is reported rather than ignored.
package config

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// Version is the schema of the file on disk. It is written with every save and
// checked on load, so a file from a future fth is reported instead of being
// half-understood.
const Version = 1

// Config is the global configuration.
//
// Every field is a default-off behaviour: fth does nothing on your behalf that
// you did not ask for. Adding a setting here means documenting it in the
// README as well, since this file is meant to be hand-editable.
type Config struct {
	// Open opens the newly created repository in a browser once the run
	// finishes successfully. It is off by default, because a window that
	// appears by itself is the kind of thing people learn to flinch at.
	Open bool `json:"open"`
}

// Default returns the configuration fth uses when there is no file: every
// behaviour opt-in.
func Default() Config { return Config{} }

// file is the on-disk shape: the schema version, then the settings themselves,
// which sit at the top level alongside it.
type file struct {
	Version int `json:"version"`
	Config
}

// ErrNoHome reports that there is nowhere to keep a configuration file at all,
// which is rare but real: an unset HOME on Unix, or no profile directory on
// Windows. Callers that only want to read settings can treat it as "no config".
var ErrNoHome = errors.New("no user configuration directory; set HOME or XDG_CONFIG_HOME")

// Path returns the location of the configuration file, or ErrNoHome when the
// platform cannot say where a per-user file belongs.
//
// XDG_CONFIG_HOME wins when it is set, which is os.UserConfigDir's rule on
// Unix; elsewhere it falls back to the platform's own answer.
func Path() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil || dir == "" {
		return "", ErrNoHome
	}
	return filepath.Join(dir, "fth", "config.json"), nil
}

// Load reads the configuration, returning the defaults when there is no file.
//
// A file that cannot be read or understood is an error, and the error names
// the path: a setting the user believes they have enabled must not be dropped
// on the floor without saying which file was at fault.
func Load() (Config, error) {
	path, err := Path()
	if err != nil {
		return Default(), err
	}
	raw, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return Default(), nil
	}
	if err != nil {
		return Default(), fmt.Errorf("read %s: %w", path, err)
	}

	var f file
	dec := json.NewDecoder(bytes.NewReader(raw))
	// Unknown fields are refused rather than ignored. This file is written by
	// hand often enough that "oepn" is a typo waiting to happen, and silently
	// skipping it means the toggle never opens anything and nothing says why.
	dec.DisallowUnknownFields()
	if err := dec.Decode(&f); err != nil {
		return Default(), fmt.Errorf("read %s: %w", path, err)
	}
	// A newer fth may know settings this one would drop on the floor, which is
	// the same silent surprise as a typo. An older one is fine: settings it does
	// know keep their defaults.
	if f.Version > Version {
		return Default(), fmt.Errorf("read %s: written by a newer fth (schema %d, this build understands %d)", path, f.Version, Version)
	}
	return f.Config, nil
}

// Save writes the configuration, creating the directory if it needs to.
//
// The write is atomic — a temporary file in the same directory, renamed into
// place — so a run interrupted halfway cannot leave a file that is not JSON.
// Every failure is returned: unlike a cache, a config that failed to save is a
// setting the user believes they changed and did not.
func Save(c Config) error {
	path, err := Path()
	if err != nil {
		return err
	}
	raw, err := json.MarshalIndent(file{Version: Version, Config: c}, "", "  ")
	if err != nil {
		return err
	}
	raw = append(raw, '\n')

	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create %s: %w", dir, err)
	}
	tmp, err := os.CreateTemp(dir, "config-*.tmp")
	if err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	name := tmp.Name()
	_ = tmp.Chmod(0o600)
	if _, err := tmp.Write(raw); err != nil {
		_ = tmp.Close()
		_ = os.Remove(name)
		return fmt.Errorf("write %s: %w", path, err)
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(name)
		return fmt.Errorf("write %s: %w", path, err)
	}
	if err := os.Rename(name, path); err != nil {
		_ = os.Remove(name)
		return fmt.Errorf("write %s: %w", path, err)
	}
	return nil
}
