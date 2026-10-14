package config

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// useConfigDir points the config directory at a temporary one, which is what
// keeps these tests off the developer's real ~/.config.
func useConfigDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	if runtimeIsWindows() {
		t.Setenv("AppData", dir)
	}
	return dir
}

func runtimeIsWindows() bool { return os.PathSeparator == '\\' }

func write(t *testing.T, dir, body string) string {
	t.Helper()
	p := filepath.Join(dir, "fth", "config.json")
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	return p
}

// Nothing configured means nothing surprising: the browser stays shut.
func TestLoadWithoutFileIsTheDefault(t *testing.T) {
	useConfigDir(t)
	got, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got != Default() {
		t.Errorf("Load with no file = %+v, want the defaults", got)
	}
	if got.Open {
		t.Error("open defaulted to true; it must be opt-in")
	}
}

func TestLoadRoundTrip(t *testing.T) {
	dir := useConfigDir(t)
	if err := Save(Config{Open: true}); err != nil {
		t.Fatalf("Save: %v", err)
	}
	// The file is meant to be hand-editable, so it has to be JSON and it has to
	// be where Save said it was.
	p := filepath.Join(dir, "fth", "config.json")
	raw, err := os.ReadFile(p)
	if err != nil {
		t.Fatalf("read %s: %v", p, err)
	}
	if !strings.Contains(string(raw), `"open": true`) {
		t.Errorf("config file = %s, want an open setting of true", raw)
	}

	got, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !got.Open {
		t.Error("Load after Save did not report open = true")
	}
}

// A setting the user believes they turned on must not be quietly dropped: a
// misspelled key is reported, naming the file, rather than ignored.
func TestLoadRejectsMisspelledSetting(t *testing.T) {
	dir := useConfigDir(t)
	write(t, dir, `{"oepn": true}`)
	_, err := Load()
	if err == nil {
		t.Fatal("Load accepted a misspelled setting")
	}
	if !strings.Contains(err.Error(), "config.json") {
		t.Errorf("error = %v; want it to name the file", err)
	}
}

// Decode stops at the end of the first value, so a file that is valid JSON
// followed by rubbish would otherwise load cleanly — and a broken file is
// precisely what this function promises to complain about.
func TestLoadRejectsContentAfterTheObject(t *testing.T) {
	dir := useConfigDir(t)
	for _, body := range []string{
		`{"open": true}{"open": false}`,
		`{"open": true} and then some prose`,
		`{"open": true}}`,
	} {
		write(t, dir, body)
		if got, err := Load(); err == nil {
			t.Errorf("Load(%q) = %+v, want an error", body, got)
		}
	}
	// Trailing whitespace is still fine: a file with a final newline must load.
	write(t, dir, "{\"open\": true}\n\n")
	if _, err := Load(); err != nil {
		t.Errorf("Load with a trailing newline: %v", err)
	}
}

func TestLoadRejectsBrokenJSON(t *testing.T) {
	dir := useConfigDir(t)
	write(t, dir, `{"open": true`)
	if _, err := Load(); err == nil {
		t.Fatal("Load accepted a truncated file")
	}
}

// An older fth can still read a file it predates: the settings it knows keep
// their defaults and the ones it does not know are refused only by a newer one.
func TestLoadAcceptsOlderSchema(t *testing.T) {
	dir := useConfigDir(t)
	write(t, dir, `{"version": 0, "open": true}`)
	got, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !got.Open {
		t.Error("an older schema should still carry the settings it had")
	}
}

func TestLoadRejectsNewerSchema(t *testing.T) {
	dir := useConfigDir(t)
	write(t, dir, `{"version": 99, "open": true}`)
	_, err := Load()
	if err == nil {
		t.Fatal("Load accepted a file from a newer fth")
	}
	if !strings.Contains(err.Error(), "newer") {
		t.Errorf("error = %v; want it to mention the newer fth", err)
	}
}

// Save writes back exactly the configuration it was handed, over whatever was
// on disk. That is what makes `fth config no-open` reliable when the stored
// value is true: the whole file is rewritten rather than patched, so no stale
// copy of a setting can survive the write.
func TestSaveWritesTheConfigItWasGiven(t *testing.T) {
	dir := useConfigDir(t)
	write(t, dir, `{"version": 1, "open": true}`)
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	cfg.Open = false
	if err := Save(cfg); err != nil {
		t.Fatalf("Save: %v", err)
	}
	got, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got.Open {
		t.Error("open = false was written but still reads back as true")
	}
}

// The whole point of writing atomically is that a file is never half-written,
// which is the state a user would have to hand-edit their way out of.
func TestSaveLeavesNoTemporaryFilesBehind(t *testing.T) {
	dir := useConfigDir(t)
	if err := Save(Config{Open: true}); err != nil {
		t.Fatalf("Save: %v", err)
	}
	entries, err := os.ReadDir(filepath.Join(dir, "fth"))
	if err != nil {
		t.Fatalf("readdir: %v", err)
	}
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".tmp") {
			t.Errorf("Save left %s behind", e.Name())
		}
	}
}

func TestPathIsUnderTheConfigDir(t *testing.T) {
	dir := useConfigDir(t)
	got, err := Path()
	if err != nil {
		t.Fatalf("Path: %v", err)
	}
	if want := filepath.Join(dir, "fth", "config.json"); got != want {
		t.Errorf("Path = %q, want %q", got, want)
	}
}

// No HOME and no XDG_CONFIG_HOME means there is nowhere to put a file, which is
// worth saying rather than silently writing to the working directory.
func TestPathWithoutHome(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", "")
	t.Setenv("HOME", "")
	t.Setenv("AppData", "")
	if _, err := Path(); err == nil {
		t.Skip("this platform resolves a configuration directory without HOME")
	} else if !errors.Is(err, ErrNoHome) {
		t.Errorf("Path error = %v, want ErrNoHome", err)
	}
}
