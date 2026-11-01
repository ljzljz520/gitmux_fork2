//go:build !windows
// +build !windows

package main

import (
	"flag"
	"os"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/rogpeppe/go-internal/gotooltest"
	"github.com/rogpeppe/go-internal/testscript"
)

var updateGolden = flag.Bool("update", false, "update golden files")

func TestCurrentDirScopeConfig(t *testing.T) {
	// The embedded default configuration opts out of directory scoping but
	// defines the local-clean symbol/style.
	if defaultCfg.Tmux.Options.CurrentDirScope {
		t.Errorf("current_dir_scope defaults to true, want false")
	}
	if defaultCfg.Tmux.Symbols.CleanLocal == "" {
		t.Errorf("default clean_local symbol is empty")
	}
	if defaultCfg.Tmux.Styles.CleanLocal == "" {
		t.Errorf("default clean_local style is empty")
	}

	// Enabling the option and overriding the local-clean symbol through YAML
	// must be decoded properly.
	var cfg Config
	in := []byte(`
tmux:
  symbols: {clean_local: "LC"}
  options: {current_dir_scope: true}
`)
	if err := yaml.Unmarshal(in, &cfg); err != nil {
		t.Fatalf("yaml.Unmarshal: %v", err)
	}
	if !cfg.Tmux.Options.CurrentDirScope {
		t.Errorf("current_dir_scope not decoded from YAML")
	}
	if cfg.Tmux.Symbols.CleanLocal != "LC" {
		t.Errorf("clean_local = %q, want LC", cfg.Tmux.Symbols.CleanLocal)
	}
}

func TestScripts(t *testing.T) {
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("GetWd error: %v", err)
	}
	params := testscript.Params{
		Dir: "testdata",
		Setup: func(env *testscript.Env) error {
			env.Setenv("GITMUX_DIR", wd)
			return nil
		},
		UpdateScripts: *updateGolden,
		TestWork:      true,
	}
	if err := gotooltest.Setup(&params); err != nil {
		t.Errorf("gotooltest.Setup error: %v", err)
	}
	testscript.Run(t, params)
}
